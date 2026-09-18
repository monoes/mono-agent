package fsconfine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workdir builds a confinement root with a file inside and a sibling
// directory outside it, both behind their real (symlink-free) paths.
func workdir(t *testing.T) (root, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "work")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(root, "sub", "in.txt"), filepath.Join(outside, "secret.txt")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

func TestPath_UnconfinedIsUnchanged(t *testing.T) {
	for _, p := range []string{"/etc/passwd", "../x", "rel/file.csv", ""} {
		got, err := Path(context.Background(), p)
		if err != nil || got != p {
			t.Errorf("Path(%q) = %q, %v; want it unchanged", p, got, err)
		}
	}
}

func TestPath_AllowsPathsInsideTheRoot(t *testing.T) {
	root, _ := workdir(t)
	ctx := WithRoot(context.Background(), root)
	cases := map[string]string{
		"sub/in.txt":                          filepath.Join(root, "sub", "in.txt"),
		filepath.Join(root, "sub", "in.txt"):  filepath.Join(root, "sub", "in.txt"),
		"sub/../sub/in.txt":                   filepath.Join(root, "sub", "in.txt"),
		"new/dir/out.csv":                     filepath.Join(root, "new", "dir", "out.csv"),
		".":                                   root,
		filepath.Join(root, "sub", "..", "z"): filepath.Join(root, "z"),
	}
	for in, want := range cases {
		got, err := Path(ctx, in)
		if err != nil {
			t.Errorf("Path(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPath_RefusesEscapes(t *testing.T) {
	root, outside := workdir(t)
	ctx := WithRoot(context.Background(), root)
	for _, p := range []string{
		"../outside/secret.txt",
		"sub/../../outside/secret.txt",
		filepath.Join(outside, "secret.txt"),
		"/etc/passwd",
		"..",
		filepath.Dir(root),
		root + "-sibling/file", // a prefix match is not containment
	} {
		_, err := Path(ctx, p)
		if !errors.Is(err, ErrOutsideWorkdir) {
			t.Errorf("Path(%q) err = %v, want ErrOutsideWorkdir", p, err)
		}
		if err != nil && !strings.Contains(err.Error(), root) {
			t.Errorf("Path(%q) error %q should name the workdir", p, err)
		}
	}
}

func TestPath_RefusesSymlinkEscapes(t *testing.T) {
	root, outside := workdir(t)
	// A symlinked file, a symlinked directory, and a dangling link whose
	// target does not exist yet — a write through it would create a file
	// outside the root.
	must(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "file-link")))
	must(t, os.Symlink(outside, filepath.Join(root, "dir-link")))
	must(t, os.Symlink(filepath.Join(outside, "new.txt"), filepath.Join(root, "dangling")))
	must(t, os.Symlink(filepath.Join(outside, "missing-dir"), filepath.Join(root, "dangling-dir")))
	ctx := WithRoot(context.Background(), root)
	for _, p := range []string{"file-link", "dir-link/secret.txt", "dir-link/new.txt", "dangling", "dangling-dir/x/y"} {
		if _, err := Path(ctx, p); !errors.Is(err, ErrOutsideWorkdir) {
			t.Errorf("Path(%q) err = %v, want ErrOutsideWorkdir", p, err)
		}
	}
}

func TestPath_FollowsSymlinksThatStayInside(t *testing.T) {
	root, _ := workdir(t)
	must(t, os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "alias")))
	ctx := WithRoot(context.Background(), root)
	got, err := Path(ctx, "alias/in.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sub", "in.txt"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPath_RootBehindSymlink(t *testing.T) {
	root, _ := workdir(t)
	link := filepath.Join(filepath.Dir(root), "root-link")
	must(t, os.Symlink(root, link))
	ctx := WithRoot(context.Background(), link)
	got, err := Path(ctx, filepath.Join(link, "sub", "in.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sub", "in.txt"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := Path(ctx, filepath.Join(link, "..", "outside", "secret.txt")); !errors.Is(err, ErrOutsideWorkdir) {
		t.Errorf("escape through a symlinked root: err = %v", err)
	}
}

func TestPath_FailsClosedOnABadRoot(t *testing.T) {
	root, _ := workdir(t)
	for name, ctx := range map[string]context.Context{
		"relative": WithRoot(context.Background(), "work"),
		"missing":  WithRoot(context.Background(), filepath.Join(root, "does-not-exist")),
		"invalid":  withInvalidRoot(context.Background(), "org.workdir is not a string"),
	} {
		if _, err := Path(ctx, filepath.Join(root, "sub", "in.txt")); err == nil {
			t.Errorf("%s root: Path succeeded, want an error", name)
		}
	}
}

func TestPaths(t *testing.T) {
	root, outside := workdir(t)
	ctx := WithRoot(context.Background(), root)
	got, err := Paths(ctx, []string{"sub/in.txt", "a.txt"})
	if err != nil || len(got) != 2 || got[1] != filepath.Join(root, "a.txt") {
		t.Fatalf("Paths = %v, %v", got, err)
	}
	if _, err := Paths(ctx, []string{"sub/in.txt", filepath.Join(outside, "secret.txt")}); !errors.Is(err, ErrOutsideWorkdir) {
		t.Errorf("one escaping path must refuse the batch, got %v", err)
	}
	if got, err := Paths(context.Background(), nil); err != nil || got != nil {
		t.Errorf("unconfined nil = %v, %v", got, err)
	}
}

func TestFromTriggerData(t *testing.T) {
	root, _ := workdir(t)
	cases := []struct {
		name     string
		td       map[string]interface{}
		confined bool
		valid    bool
	}{
		{"absent", map[string]interface{}{"input": map[string]interface{}{"path": "/etc"}}, false, true},
		{"nil", nil, false, true},
		{"org without workdir", map[string]interface{}{"org": map[string]interface{}{"name": "growth"}}, false, true},
		{"workdir", map[string]interface{}{"org": map[string]interface{}{"workdir": root}}, true, true},
		{"empty workdir", map[string]interface{}{"org": map[string]interface{}{"workdir": ""}}, true, false},
		{"non-string workdir", map[string]interface{}{"org": map[string]interface{}{"workdir": 42}}, true, false},
	}
	for _, c := range cases {
		ctx := FromTriggerData(context.Background(), c.td)
		_, confined := Root(ctx)
		if confined != c.confined {
			t.Errorf("%s: confined = %v, want %v", c.name, confined, c.confined)
		}
		_, err := Path(ctx, filepath.Join(root, "sub", "in.txt"))
		if (err == nil) != c.valid {
			t.Errorf("%s: Path err = %v, want valid=%v", c.name, err, c.valid)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
