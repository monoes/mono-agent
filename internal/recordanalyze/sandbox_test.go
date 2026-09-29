package recordanalyze

import (
	"context"
	"io"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
)

// Recording analysis turns are sandboxed in their own workspace only when
// monomind advertises it.
func TestExecRunnerSandbox(t *testing.T) {
	for _, advertise := range []bool{true, false} {
		argsLog := sandboxtest.Install(t, advertise, "{}")
		if _, err := (ExecRunner{Runtime: "codex"}).run(context.Background(), "analyze", io.Discard); err != nil {
			t.Fatalf("advertise=%v: %v", advertise, err)
		}
		sandboxtest.Check(t, argsLog, advertise, sandboxtest.Workspace(monomind.WorkspaceRecordReview))
	}
}
