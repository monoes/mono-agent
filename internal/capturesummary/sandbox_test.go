package capturesummary

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Capture summaries are sandboxed in the summary workspace through
// whichever path monomind offers, and not at all on an older one.
func TestExecRunnerSandbox(t *testing.T) {
	for _, m := range sandboxtest.Kinds {
		argsLog := sandboxtest.Install(t, m, "a summary")
		a, err := ExecRunner(0)(context.Background(), Target{Runtime: "codex"}, "summarize")
		if err != nil || a.Text != "a summary" {
			t.Fatalf("%s monomind: %+v, %v", m, a, err)
		}
		sandboxtest.Check(t, argsLog, m, sandboxtest.Workspace(monomind.WorkspaceSummary))
	}
}
