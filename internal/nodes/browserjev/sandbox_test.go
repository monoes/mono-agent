package browserjev

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// The TYPE_TEXT helper's turns are sandboxed in the text-helper workspace
// through whichever path monomind offers.
func TestTextHelperSandbox(t *testing.T) {
	for _, m := range sandboxtest.Kinds {
		argsLog := sandboxtest.Install(t, m, `{"text":"Ada"}`)
		got, err := monomindWriter("codex", "")(context.Background(), map[string]any{"goal": "name"})
		if err != nil || got != "Ada" {
			t.Fatalf("%s monomind: %q, %v", m, got, err)
		}
		sandboxtest.Check(t, argsLog, m, sandboxtest.Workspace(monomind.WorkspaceTextHelper))
	}
}
