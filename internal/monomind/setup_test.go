package monomind

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestIsAgentNotSetup(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"monomind missing", fmt.Errorf("chat: %w", &ErrNotFound{Tried: []string{"/x"}}), true},
		{"monomind too old", unusable("monomind %s is too old", "2.1.0"), true},
		{"no runtime", fmt.Errorf("cache-only mode: %w", ErrNoRuntime), true},
		{"runtime not installed", fmt.Errorf("%q: %w", "codex", ErrRuntimeNotInstalled), true},
		{"auth", &ProtocolError{Code: ErrAuth, Message: "codex: auth_error (401) Run: codex login"}, true},
		{"missing binary", &ProtocolError{Code: ErrMissingBinary, Message: "codex CLI not found"}, true},
		{"no runner", &ProtocolError{Code: ErrNoRunner, Message: `unknown runtime "nosuch"`}, true},
		{"claude not logged in", &ProtocolError{Code: ErrRunnerError, Message: "Claude Code returned an error result: Not logged in · Please run /login"}, true},
		{"rev 27 missing key", &ProtocolError{Code: ErrAuth, Fatal: true, Message: "missing API key for provider openrouter"}, true},
		{"unclassified text", &ProtocolError{Code: ErrRunnerError, Message: "exit 1\n[output below is not classified]\nNot logged in · Please run /login"}, false},
		{"marker in plain text", errors.New("node ask failed: agent.ask (claude) turn failed: x " + AgentNotSetupMarker), true},
		{"quota", &ProtocolError{Code: ErrQuota, Message: "usage limit reached"}, false},
		{"runner error", &ProtocolError{Code: ErrRunnerError, Message: "done reported nonzero exit_code 1"}, false},
		{"timeout", &ProtocolError{Code: ErrTimeout, Message: "timed out"}, false},
		{"plain", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := IsAgentNotSetup(c.err); got != c.want {
			t.Errorf("%s: IsAgentNotSetup(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

func TestMarkNotSetup(t *testing.T) {
	pe := &ProtocolError{Code: ErrMissingBinary, Message: "codex CLI not found"}
	marked := MarkNotSetup(pe)
	if !strings.HasSuffix(marked.Error(), " "+AgentNotSetupMarker) {
		t.Errorf("marked text = %q", marked.Error())
	}
	var got *ProtocolError
	if !errors.As(marked, &got) || got != pe {
		t.Error("marked error no longer unwraps to the protocol error")
	}
	// Marking twice, or through a wrapper, adds the marker once.
	twice := MarkNotSetup(fmt.Errorf("agent.ask: %w", marked))
	if n := strings.Count(twice.Error(), AgentNotSetupMarker); n != 1 {
		t.Errorf("marker appears %d times: %q", n, twice.Error())
	}
	plain := errors.New("boom")
	if MarkNotSetup(plain) != plain {
		t.Error("an unrelated error was changed")
	}
	if MarkNotSetup(nil) != nil {
		t.Error("nil became an error")
	}
}

// The done after a non-fatal error reports that error, so Claude Code's
// "Not logged in" survives as the turn's failure.
func TestExecNotLoggedInIsAgentNotSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	t.Setenv("FAKE_MODE", "not_logged_in")
	res, err := Exec(context.Background(), ExecOptions{Bin: fakeBin(t, "fake-monomind.sh"), Runtime: "claude", Prompt: "hi"}, nil)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Message, "Not logged in") || res.Err.ExitCode != 1 {
		t.Fatalf("turn error = %+v, want the not-logged-in cause with exit 1", res.Err)
	}
	if !IsAgentNotSetup(res.Err) {
		t.Errorf("IsAgentNotSetup(%v) = false", res.Err)
	}
}

func TestExecMissingBinaryIsAgentNotSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	t.Setenv("FAKE_MODE", "missing_binary")
	res, err := Exec(context.Background(), ExecOptions{Bin: fakeBin(t, "fake-monomind.sh"), Runtime: "codex", Prompt: "hi"}, nil)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Err == nil || res.Err.Code != ErrMissingBinary || !IsAgentNotSetup(res.Err) {
		t.Fatalf("turn error = %+v, want an agent_not_setup missing-binary", res.Err)
	}
}

func TestHandshakeTooOldIsAgentNotSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	bin := writeInlineFakeBin(t, `echo '{"v":1,"version":"2.1.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
exit 0`)
	_, err := Handshake(context.Background(), bin)
	if err == nil || !strings.Contains(err.Error(), "too old") || !IsAgentNotSetup(err) {
		t.Fatalf("handshake error = %v, want an agent_not_setup too-old", err)
	}
}

// A runner that reports its missing CLI as a plain runner-error is
// reclassified once `agent scan` confirms the runtime is not installed.
func TestReclassifyMissingRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	t.Setenv(EnvOverride, fakeBin(t, "fake-monomind.sh"))
	t.Setenv("FAKE_MODE", "runner_missing_cli")
	res, err := Exec(context.Background(), ExecOptions{Runtime: "codex", Prompt: "hi"}, nil)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Err == nil || IsAgentNotSetup(res.Err) {
		t.Fatalf("turn error = %+v, want an unclassified runner-error first", res.Err)
	}
	ReclassifyMissingRuntime(context.Background(), "codex", res)
	if res.Err.Code != ErrMissingBinary || !IsAgentNotSetup(res.Err) || res.Err.ExitCode != 1 {
		t.Errorf("reclassified error = %+v, want missing-binary with exit 1", res.Err)
	}

	// An installed runtime's failure is left alone.
	other := &TurnResult{Err: &ProtocolError{Code: ErrRunnerError, Message: "boom"}}
	ReclassifyMissingRuntime(context.Background(), "claude", other)
	if other.Err.Code != ErrRunnerError {
		t.Errorf("installed runtime's error became %+v", other.Err)
	}
}
