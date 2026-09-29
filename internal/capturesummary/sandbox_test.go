package capturesummary

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Capture summaries are sandboxed in the summary workspace only when
// monomind advertises it.
func TestExecRunnerSandbox(t *testing.T) {
	for _, advertise := range []bool{true, false} {
		argsLog := sandboxtest.Install(t, advertise, "a summary")
		a, err := ExecRunner(0)(context.Background(), Target{Runtime: "codex"}, "summarize")
		if err != nil || a.Text != "a summary" {
			t.Fatalf("advertise=%v: %+v, %v", advertise, a, err)
		}
		sandboxtest.Check(t, argsLog, advertise, sandboxtest.Workspace(monomind.WorkspaceSummary))
	}
}
