package action

import (
	"context"
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
)

// TestStepUpload_RefusesFilesOutsideOrgWorkdir: a browser upload step hands
// local files to a web page, so in a run a role's grant started it may only
// upload from the role's workdir (C-46). The refusal comes before the page is
// touched — this executor has no page at all.
func TestStepUpload_RefusesFilesOutsideOrgWorkdir(t *testing.T) {
	ctx := fsconfine.WithRoot(context.Background(), t.TempDir())
	ae := &ActionExecutor{}
	for _, text := range []string{"/etc/passwd", "../../etc/passwd", "ok.png, /etc/shadow"} {
		res, err := ae.stepUpload(ctx, StepDef{ID: "u", Type: "upload", Text: text})
		if err != nil {
			t.Fatalf("%q: unexpected error %v", text, err)
		}
		if res.Success || !errors.Is(res.Error, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("%q: result %+v, want a failure wrapping ErrOutsideWorkdir", text, res)
		}
	}
}
