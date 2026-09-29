package browserjev

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// The TYPE_TEXT helper's turns are sandboxed in the text-helper workspace
// only when monomind advertises it.
func TestTextHelperSandbox(t *testing.T) {
	for _, advertise := range []bool{true, false} {
		argsLog := sandboxtest.Install(t, advertise, `{"text":"Ada"}`)
		got, err := monomindWriter("codex", "")(context.Background(), map[string]any{"goal": "name"})
		if err != nil || got != "Ada" {
			t.Fatalf("advertise=%v: %q, %v", advertise, got, err)
		}
		sandboxtest.Check(t, argsLog, advertise, sandboxtest.Workspace(monomind.WorkspaceTextHelper))
	}
}
