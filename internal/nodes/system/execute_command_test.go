package system

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func runCmd(t *testing.T, config map[string]interface{}) []workflow.NodeOutput {
	t.Helper()
	n := &ExecuteCommandNode{}
	out, err := n.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, o := range out {
		if o.Handle == "main" {
			return out
		}
	}
	t.Fatalf("no main handle in output: %+v", out)
	return nil
}

func mainJSON(t *testing.T, out []workflow.NodeOutput) map[string]interface{} {
	t.Helper()
	for _, o := range out {
		if o.Handle == "main" {
			if len(o.Items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(o.Items))
			}
			return o.Items[0].JSON
		}
	}
	t.Fatal("no main handle")
	return nil
}

func TestExecuteCommand_ZeroTimeoutUsesDefault(t *testing.T) {
	// A zero timeout previously created an already-expired context and
	// killed the command instantly; it must fall back to the 30s default.
	out := runCmd(t, map[string]interface{}{
		"command": "echo",
		"args":    []interface{}{"hi"},
		"timeout": 0.0,
	})
	j := mainJSON(t, out)
	if j["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0 (stdout=%q stderr=%q)", j["exit_code"], j["stdout"], j["stderr"])
	}
	if strings.TrimSpace(j["stdout"].(string)) != "hi" {
		t.Errorf("stdout = %q, want hi", j["stdout"])
	}
	if _, has := j["truncated"]; has {
		t.Errorf("small output must not be marked truncated: %+v", j)
	}
}

func TestExecuteCommand_ZeroTimeoutSecondsUsesDefault(t *testing.T) {
	out := runCmd(t, map[string]interface{}{
		"command":         "echo",
		"args":            []interface{}{"hi"},
		"timeout_seconds": 0.0,
	})
	if j := mainJSON(t, out); j["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", j["exit_code"])
	}
}

func TestExecuteCommand_NoTimeoutConfig(t *testing.T) {
	out := runCmd(t, map[string]interface{}{
		"command": "echo",
		"args":    []interface{}{"ok"},
	})
	if j := mainJSON(t, out); strings.TrimSpace(j["stdout"].(string)) != "ok" {
		t.Errorf("stdout = %q, want ok", j["stdout"])
	}
}

func TestExecuteCommand_LargeOutputTruncated(t *testing.T) {
	// ~20MB of output against the 10MB per-stream cap: the captured
	// stdout is capped, the flag is set, and the command still succeeds.
	out := runCmd(t, map[string]interface{}{
		"command": "head",
		"args":    []interface{}{"-c", "20971520", "/dev/zero"},
	})
	j := mainJSON(t, out)
	if j["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", j["exit_code"])
	}
	if got := len(j["stdout"].(string)); got != maxCommandOutputBytes {
		t.Errorf("stdout length = %d, want exactly the %d cap", got, maxCommandOutputBytes)
	}
	if truncated, _ := j["truncated"].(bool); !truncated {
		t.Errorf("truncated flag missing: %+v", j)
	}
}

func TestExecuteCommand_NonZeroExitRoutesToErrorHandle(t *testing.T) {
	n := &ExecuteCommandNode{}
	out, err := n.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"command": "false",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	handles := map[string]bool{}
	for _, o := range out {
		handles[o.Handle] = true
	}
	if !handles["main"] || !handles["error"] {
		t.Errorf("expected main+error handles, got %v", handles)
	}
	if j := mainJSON(t, out); j["exit_code"] == 0 {
		t.Errorf("exit_code = %v, want non-zero", j["exit_code"])
	}
}
