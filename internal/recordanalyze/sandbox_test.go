package recordanalyze

import (
	"context"
	"io"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Recording analysis turns are sandboxed in their own workspace through
// whichever path monomind offers.
func TestExecRunnerSandbox(t *testing.T) {
	for _, m := range sandboxtest.Kinds {
		argsLog := sandboxtest.Install(t, m, "{}")
		if _, err := (ExecRunner{Runtime: "codex"}).run(context.Background(), "analyze", io.Discard); err != nil {
			t.Fatalf("%s monomind: %v", m, err)
		}
		sandboxtest.Check(t, argsLog, m, sandboxtest.Workspace(monomind.WorkspaceRecordReview))
	}
}
