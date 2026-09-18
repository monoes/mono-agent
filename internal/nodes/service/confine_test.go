package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/workflow"
)

// TestUploadNodes_RefuseFilesOutsideOrgWorkdir: YouTube and Drive uploads
// send a local file away, so a run a role's grant started may only upload
// from the role's workdir (C-46). The refusal comes before any request.
func TestUploadNodes_RefuseFilesOutsideOrgWorkdir(t *testing.T) {
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
	secret := filepath.Join(outside, "secret.mp4")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "media")); err != nil {
		t.Fatal(err)
	}
	ctx := fsconfine.WithRoot(context.Background(), root)
	for _, p := range []string{secret, "../outside/secret.mp4", "media/secret.mp4"} {
		_, err := (&YouTubeNode{}).Execute(ctx, workflow.NodeInput{}, map[string]interface{}{
			"access_token": "invalid", "operation": "upload_video", "title": "t", "video_file_path": p})
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("youtube %q: err = %v, want ErrOutsideWorkdir", p, err)
		}
		_, err = (&GoogleDriveNode{}).Execute(ctx, workflow.NodeInput{}, map[string]interface{}{
			"access_token": "invalid", "operation": "upload_file", "file_path": p})
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("google_drive %q: err = %v, want ErrOutsideWorkdir", p, err)
		}
	}
}
