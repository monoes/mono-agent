package httpnodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/workflow"
)

// TestFTP_RefusesLocalPathsOutsideOrgWorkdir: an FTP upload reads and a
// download writes local_path, so a run a role's grant started is confined to
// the role's workdir (C-46). The refusal comes before the dial; the host is
// never contacted.
func TestFTP_RefusesLocalPathsOutsideOrgWorkdir(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, outside := filepath.Join(base, "work"), filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	ctx := fsconfine.WithRoot(context.Background(), root)
	for _, op := range []string{"upload", "download"} {
		for _, p := range []string{filepath.Join(outside, "f"), "../outside/f", "out/f", "/etc/passwd"} {
			_, err := (&FTPNode{}).Execute(ctx, workflow.NodeInput{}, map[string]interface{}{
				"host": "ftp.invalid", "operation": op, "remote_path": "/r", "local_path": p})
			if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
				t.Errorf("%s %q: err = %v, want ErrOutsideWorkdir", op, p, err)
			}
		}
	}
}
