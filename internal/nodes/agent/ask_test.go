package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/workflow"
)

// writeFakeIncompatibleMonomind writes a script that mimics a pre-protocol
// monomind install: it responds to unknown subcommands (including
// `--version --json`) with human-readable help text instead of JSON, exit 0
// — reproducing the real installed-but-too-old case.
func writeFakeIncompatibleMonomind(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "monomind")
	script := "#!/bin/sh\necho 'Agent Management Commands'\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake monomind: %v", err)
	}
	return path
}

func TestAskNodeType(t *testing.T) {
	node := &AskNode{}
	if got := node.Type(); got != "agent.ask" {
		t.Errorf("Type() = %q, want %q", got, "agent.ask")
	}
}

func TestAskNodeRequiresRuntime(t *testing.T) {
	node := &AskNode{}
	_, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"prompt": "hello",
	})
	if err == nil {
		t.Fatal("Execute() = nil error, want a missing-runtime error")
	}
	if !errors.Is(err, workflow.ErrInvalidConfig) {
		t.Errorf("Execute() error = %v, want it to wrap workflow.ErrInvalidConfig", err)
	}
}

func TestAskNodeRequiresPrompt(t *testing.T) {
	node := &AskNode{}
	_, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"runtime": "claude",
	})
	if err == nil {
		t.Fatal("Execute() = nil error, want a missing-prompt error")
	}
	if !errors.Is(err, workflow.ErrInvalidConfig) {
		t.Errorf("Execute() error = %v, want it to wrap workflow.ErrInvalidConfig", err)
	}
}

func TestRegisterAllRegistersAgentAsk(t *testing.T) {
	r := workflow.NewNodeTypeRegistry()
	RegisterAll(r)
	factory, ok := r.Get("agent.ask")
	if !ok {
		t.Fatal("Get(\"agent.ask\"): not registered")
	}
	if got := factory().Type(); got != "agent.ask" {
		t.Errorf("factory().Type() = %q, want %q", got, "agent.ask")
	}
}

func TestExpandTemplate(t *testing.T) {
	item := workflow.Item{JSON: map[string]interface{}{"text": "hello world", "count": 3}}

	got := expandTemplate("say: {{$json.text}} ({{$json.count}} times)", item)
	want := "say: hello world (3 times)"
	if got != want {
		t.Errorf("expandTemplate() = %q, want %q", got, want)
	}
}

func TestExpandTemplateLeavesUnknownFieldsUntouched(t *testing.T) {
	item := workflow.Item{JSON: map[string]interface{}{"text": "hi"}}
	got := expandTemplate("{{$json.missing}}", item)
	if got != "{{$json.missing}}" {
		t.Errorf("expandTemplate() = %q, want the placeholder left as-is", got)
	}
}

// TestAskNodeFailsFastOnIncompatibleMonomind guards against a regression
// where Execute called monomind.Exec directly (skipping the Ensure
// handshake): against a too-old/incompatible monomind that answers unknown
// flags with exit-0 help text instead of JSON, the old code silently
// returned a "successful" empty answer instead of an actionable error.
func TestAskNodeFailsFastOnIncompatibleMonomind(t *testing.T) {
	t.Setenv(monomind.EnvOverride, writeFakeIncompatibleMonomind(t))

	node := &AskNode{}
	out, err := node.Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{}}},
	}, map[string]interface{}{
		"runtime": "claude",
		"prompt":  "say hi",
	})
	if err == nil {
		t.Fatalf("Execute() = %+v, nil error — want a handshake error against an incompatible monomind", out)
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Errorf("Execute() error = %q, want it to mention the handshake failure", err.Error())
	}
	// Stored as a run's error_message, the text alone still classifies.
	if !strings.HasSuffix(err.Error(), monomind.AgentNotSetupMarker) {
		t.Errorf("Execute() error = %q, want the %s marker", err.Error(), monomind.AgentNotSetupMarker)
	}
}

// A runtime that is not logged in fails the node with a marked error.
func TestAskNodeMarksNotLoggedIn(t *testing.T) {
	bin := writeFakeMonomindScript(t, `if [ "$1" = "--version" ]; then
  echo '{"v":1,"version":"2.16.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
echo '{"v":1,"type":"start","runtime":"claude","cwd":"/app","pid":1}'
echo '{"v":1,"type":"assistant","text":"Not logged in · Please run /login"}'
echo '{"v":1,"type":"error","code":"runner-error","fatal":false,"message":"Claude Code returned an error result: Not logged in · Please run /login"}'
echo '{"v":1,"type":"done","exit_code":1}'
exit 1
`)
	t.Setenv(monomind.EnvOverride, bin)

	_, err := (&AskNode{}).Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{}}},
	}, map[string]interface{}{"runtime": "claude", "prompt": "say hi"})
	if err == nil {
		t.Fatal("Execute() = nil error, want the not-logged-in failure")
	}
	if !strings.Contains(err.Error(), "Not logged in") || !strings.HasSuffix(err.Error(), monomind.AgentNotSetupMarker) {
		t.Errorf("Execute() error = %q, want the cause and the %s marker", err.Error(), monomind.AgentNotSetupMarker)
	}
	if !monomind.IsAgentNotSetup(err) {
		t.Error("IsAgentNotSetup = false")
	}
}

// An ordinary turn failure is not marked.
func TestAskNodeLeavesOtherFailuresUnmarked(t *testing.T) {
	bin := writeFakeMonomindScript(t, `if [ "$1" = "--version" ]; then
  echo '{"v":1,"version":"2.16.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
echo '{"v":1,"type":"start","runtime":"claude","cwd":"/app","pid":1}'
echo '{"v":1,"type":"error","code":"quota","fatal":true,"message":"usage limit reached"}'
echo '{"v":1,"type":"done","exit_code":1}'
exit 1
`)
	t.Setenv(monomind.EnvOverride, bin)

	_, err := (&AskNode{}).Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{}}},
	}, map[string]interface{}{"runtime": "claude", "prompt": "say hi"})
	if err == nil || strings.Contains(err.Error(), monomind.AgentNotSetupMarker) {
		t.Errorf("Execute() error = %v, want an unmarked quota failure", err)
	}
}

func writeFakeMonomindScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	path := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake monomind: %v", err)
	}
	return path
}
