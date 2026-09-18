package gemini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/fsconfine"
)

// TestUploadImage_RefusesFilesOutsideOrgWorkdir: the reference image is a
// local file handed to a web page, so in a run a role's grant started it may
// only come from the role's workdir (C-46). The refusal comes before the
// page is used (this page has no browser behind it).
func TestUploadImage_RefusesFilesOutsideOrgWorkdir(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "work")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := fsconfine.WithRoot(context.Background(), root)
	var page browser.PageInterface = browser.NewRodPage(nil)
	for _, p := range []string{outside, "../secret.png"} {
		_, err := (&GeminiBot{}).methodUploadImage(ctx, page, p)
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("%q: err = %v, want ErrOutsideWorkdir", p, err)
		}
	}
}
