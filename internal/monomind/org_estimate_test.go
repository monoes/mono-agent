package monomind

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
		"if [ \"$1 $2\" = \"org validate\" ]; then echo ok; exit 0; fi\n" +
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

// monomind 2.24.x hands `org run` to a serve daemon whose heartbeat is up to
// 3 minutes old while its pid lives, before it prints the estimate, so the
// estimate must be refused for any age with a live pid.
func TestOrgCostEstimate_RefusesAnyLiveServeHeartbeatAge(t *testing.T) {
	dead := 0
	cmd := exec.Command("true")
	if err := cmd.Run(); err == nil {
		dead = cmd.Process.Pid
	}
	cases := []struct {
		name    string
		age     time.Duration
		pid     int
		refused bool
	}{
		{"30s", 30 * time.Second, os.Getpid(), true},
		{"90s", 90 * time.Second, os.Getpid(), true},
		{"170s", 170 * time.Second, os.Getpid(), true},
		{"10min", 10 * time.Minute, os.Getpid(), true},
		{"dead pid", 30 * time.Second, dead, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := estimateFake(t, "cat '"+goldenPath("run-estimate-abort.txt")+"'; exit 1\n")
			hb := `{"pid":` + strconv.Itoa(c.pid) + `,"updatedAt":"` + time.Now().Add(-c.age).UTC().Format(time.RFC3339) + `"}`
			if err := os.MkdirAll(filepath.Join(root, ".monomind"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".monomind", "serve-heartbeat.json"), []byte(hb), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := OrgCostEstimate(context.Background(), root, "sec")
			if c.refused != errors.Is(err, ErrEstimateUnavailable) {
				t.Fatalf("refused=%v want %v (err=%v)", errors.Is(err, ErrEstimateUnavailable), c.refused, err)
			}
			_, ran := os.Stat(filepath.Join(root, "argv"))
			if c.refused && ran == nil {
				t.Error("org run must not be called")
			}
		})
	}
}

// Below KnownGoodMonomindVersion, and when the org does not validate, `org run`
// is never called: either could start a real run instead of aborting.
func TestOrgCostEstimate_GatesVersionAndValidate(t *testing.T) {
	cases := []struct{ name, version, validate string }{
		{"old monomind", "2.20.0", "exit 0"},
		{"invalid org", "2.24.1", "echo 'invalid: bad role'; exit 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(t.TempDir(), "monomind")
			script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then sed 's/2\\.24\\.1/" + c.version + "/' '" + goldenPath("version.json") + "'; exit 0; fi\n" +
				"if [ \"$1 $2\" = \"org validate\" ]; then " + c.validate + "; fi\n" +
				"echo \"$@\" > '" + filepath.Join(root, "argv") + "'\ncat '" + goldenPath("run-estimate-abort.txt") + "'; exit 1\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvOverride, bin)
			if _, err := OrgCostEstimate(context.Background(), root, "sec"); !errors.Is(err, ErrEstimateUnavailable) {
				t.Fatalf("want unavailable, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "argv")); err == nil {
				t.Error("org run must not be called")
			}
		})
	}
}

func TestOrgRunStart_RejectsInvalidOrgName(t *testing.T) {
	for _, name := range []string{"../x", "a/b", "", ".hidden"} {
		if err := OrgRunStart(context.Background(), t.TempDir(), name, ""); err == nil || !strings.Contains(err.Error(), "invalid org name") {
			t.Errorf("name %q: err = %v, want invalid org name", name, err)
		}
	}
}
