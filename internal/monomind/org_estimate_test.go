package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The recorded 2.24.1 output of `org run sec --yes --budget-usd=-1` (real).
func TestParseCostEstimate_Recorded(t *testing.T) {
	est, ok := ParseCostEstimate(string(goldenBytes(t, "run-estimate-abort.txt")))
	if !ok {
		t.Fatal("no estimate parsed")
	}
	if !strings.HasPrefix(est.StaleRates, "⚠ stale rates: no live provider pricing lookup") {
		t.Errorf("stale line = %q", est.StaleRates)
	}
	if est.TotalUSD == nil || *est.TotalUSD != 1.80 {
		t.Errorf("total = %v, want 1.80", est.TotalUSD)
	}
	if !strings.Contains(est.Text, "checker") || strings.Contains(est.Text, "Aborting") {
		t.Errorf("text must be the estimate block only:\n%s", est.Text)
	}
	if _, ok := ParseCostEstimate("org sec is not signed (unsigned)"); ok {
		t.Error("a refusal has no estimate")
	}
}

func estimateFake(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake monomind is a shell script")
	}
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "monomind")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then cat '" + goldenPath("version.json") + "'; exit 0; fi\n" +
		"echo \"$@\" > '" + filepath.Join(root, "argv") + "'\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	return root
}

func TestOrgCostEstimate_ReadOnlyCall(t *testing.T) {
	root := estimateFake(t, "cat '"+goldenPath("run-estimate-abort.txt")+"'; exit 1\n")
	est, err := OrgCostEstimate(context.Background(), root, "sec")
	if err != nil || est.StaleRates == "" {
		t.Fatalf("est=%+v err=%v", est, err)
	}
	argv, _ := os.ReadFile(filepath.Join(root, "argv"))
	if got := strings.TrimSpace(string(argv)); got != "org run sec --yes --budget-usd=-1" {
		t.Errorf("argv = %q: the call must abort before any session starts", got)
	}
}

func TestOrgCostEstimate_Refusals(t *testing.T) {
	root := estimateFake(t, "echo 'org sec is not signed (unsigned)'; exit 1\n")
	if _, err := OrgCostEstimate(context.Background(), root, "sec"); !errors.Is(err, ErrEstimateUnavailable) {
		t.Errorf("an output with no estimate is unavailable, got %v", err)
	}
	if _, err := OrgCostEstimate(context.Background(), root, "../x"); err == nil {
		t.Error("bad org name must be refused")
	}
	// A live serve daemon would take `org run` as a real run: never called.
	hb := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"updatedAt":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.MkdirAll(filepath.Join(root, ".monomind"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".monomind", "serve-heartbeat.json"), []byte(hb), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(root, "argv"))
	if _, err := OrgCostEstimate(context.Background(), root, "sec"); !errors.Is(err, ErrEstimateUnavailable) {
		t.Fatalf("live serve daemon: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "argv")); err == nil {
		t.Error("monomind was invoked although org serve is live")
	}
}

// A real run's own stdout carries the same estimate block, before any session
// output: where a run's pre-run estimate is already captured it parses alike.
func TestParseCostEstimate_FromRealRunStdout(t *testing.T) {
	est, ok := ParseCostEstimate(string(goldenBytes(t, "budget-run-stdout.txt")))
	if !ok || !strings.HasPrefix(est.StaleRates, "⚠ stale rates") || est.TotalUSD == nil || *est.TotalUSD != 1.80 {
		t.Fatalf("est = %+v ok=%v", est, ok)
	}
	if strings.Contains(est.Text, "org bud running") {
		t.Errorf("the block ends at the total, got:\n%s", est.Text)
	}
}
