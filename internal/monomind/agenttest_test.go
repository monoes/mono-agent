//go:build !windows

package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAgentTestMonomind answers `agent test` like monomind 2.18.5 (§13):
// JSON on stdout, exit 1 for a failed status. argv is written to a file so
// the test can check the flags.
func fakeAgentTestMonomind(t *testing.T, body string) (bin, argvFile string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "monomind")
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\necho \"$@\" > " + argvFile + "\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvFile
}

func TestAgentTestParsesOK(t *testing.T) {
	bin, argv := fakeAgentTestMonomind(t, `echo '{"v":1,"runtime":"codex","model":"gpt-5.5","status":"ok","reply":"ok","latency_first_ms":812,"latency_ms":1430,"input_tokens":12,"output_tokens":1,"cost_usd":0.0001,"cost_estimated":true,"runtime_version":"0.52.0","error":null}'`)
	res, err := AgentTest(context.Background(), bin, "codex", "gpt-5.5", 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || res.LatencyMs != 1430 || res.LatencyFirstMs == nil || *res.LatencyFirstMs != 812 ||
		res.CostUSD == nil || !res.CostEstimated || res.RuntimeVersion == nil || *res.RuntimeVersion != "0.52.0" {
		t.Errorf("result = %+v", res)
	}
	got, _ := os.ReadFile(argv)
	if want := "agent test codex --json --model=gpt-5.5 --timeout 45s"; strings.TrimSpace(string(got)) != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestAgentTestFailedStatusStillReturnsResult(t *testing.T) {
	bin, argv := fakeAgentTestMonomind(t, `echo '{"v":1,"runtime":"claude","model":null,"status":"auth","reply":null,"latency_first_ms":null,"latency_ms":900,"input_tokens":null,"output_tokens":null,"cost_usd":null,"cost_estimated":false,"runtime_version":null,"error":{"code":"auth","message":"Not logged in","login_hint":"claude /login"}}'; exit 1`)
	res, err := AgentTest(context.Background(), bin, "claude", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "auth" || res.Error == nil || res.Error.LoginHint != "claude /login" || res.CostUSD != nil || res.Model != nil {
		t.Errorf("result = %+v", res)
	}
	got, _ := os.ReadFile(argv)
	if strings.Contains(string(got), "--model") || strings.Contains(string(got), "--timeout") {
		t.Errorf("default model and no timeout must not pass flags: %q", got)
	}
}

func TestAgentTestNoJSONIsAnError(t *testing.T) {
	bin, _ := fakeAgentTestMonomind(t, `echo "unknown runtime zed" >&2; exit 2`)
	_, err := AgentTest(context.Background(), bin, "zed", "", 0)
	if err == nil || !strings.Contains(err.Error(), "unknown runtime zed") {
		t.Errorf("err = %v, want the stderr text", err)
	}
	if !errors.Is(err, ErrAgentTestUnsupported) {
		t.Errorf("a fast failure without JSON must be ErrAgentTestUnsupported, got %v", err)
	}
}

func TestAgentTestSlowNoJSONIsNotUnsupported(t *testing.T) {
	prev := agentTestFastFail
	agentTestFastFail = 100 * time.Millisecond
	t.Cleanup(func() { agentTestFastFail = prev })
	bin, _ := fakeAgentTestMonomind(t, `sleep 0.3; echo "runner crashed" >&2; exit 1`)
	_, err := AgentTest(context.Background(), bin, "claude", "", 0)
	if err == nil || errors.Is(err, ErrAgentTestUnsupported) {
		t.Errorf("a run that took its time may have called the model; err = %v, want a plain error", err)
	}
}

func TestAgentTestGoDeadline(t *testing.T) {
	prev := agentTestGrace
	agentTestGrace = 200 * time.Millisecond
	t.Cleanup(func() { agentTestGrace = prev })
	bin, argv := fakeAgentTestMonomind(t, `sleep 30`)
	start := time.Now()
	_, err := AgentTest(context.Background(), bin, "claude", "", 300*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrAgentTestUnsupported) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the Go-side deadline did not stop monomind")
	}
	got, _ := os.ReadFile(argv)
	if !strings.Contains(string(got), "--timeout 300ms") {
		t.Errorf("a sub-second timeout must not round to 0s: %q", got)
	}
}

func TestAgentTestRejectsDashArgs(t *testing.T) {
	bin, argv := fakeAgentTestMonomind(t, `echo '{"status":"ok","latency_ms":1}'`)
	for _, c := range []struct{ runtime, model string }{{"--help", ""}, {"claude", "--timeout"}, {"", ""}} {
		if _, err := AgentTest(context.Background(), bin, c.runtime, c.model, 0); err == nil {
			t.Errorf("runtime %q model %q: want an error", c.runtime, c.model)
		}
	}
	if _, err := os.Stat(argv); err == nil {
		t.Error("monomind must not run for a rejected argument")
	}
}

func TestAgentTestCancelKillsTheProcess(t *testing.T) {
	bin, _ := fakeAgentTestMonomind(t, `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := AgentTest(ctx, bin, "claude", "", 0); err == nil {
		t.Error("a cancelled test must return an error")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("cancel did not stop the process")
	}
}
