package data

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/workflow"
)

// confinedDirs returns a confined context rooted at a fresh workdir, the
// workdir, and a directory outside it holding secret.csv (C-46).
func confinedDirs(t *testing.T) (context.Context, string, string) {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(outside, "secret.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	return fsconfine.WithRoot(context.Background(), root), root, outside
}

func TestWriteBinaryFile_ConfinedToOrgWorkdir(t *testing.T) {
	ctx, root, outside := confinedDirs(t)
	in := workflow.NodeInput{Items: []workflow.Item{workflow.NewItem(map[string]interface{}{"data": "hi"})}}
	for _, p := range []string{filepath.Join(outside, "out.txt"), "../outside/out.txt", "escape/out.txt", "/etc/cron.d/x"} {
		_, err := (&WriteBinaryFileNode{}).Execute(ctx, in, map[string]interface{}{"file_path": p, "field": "data", "encoding": "utf8"})
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("file_path %q: err = %v, want ErrOutsideWorkdir", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("a refused write still created a file outside the workdir (stat err %v)", err)
	}
	out, err := (&WriteBinaryFileNode{}).Execute(ctx, in, map[string]interface{}{"file_path": "reports/out.txt", "field": "data", "encoding": "utf8"})
	if err != nil {
		t.Fatalf("write inside the workdir: %v", err)
	}
	want := filepath.Join(root, "reports", "out.txt")
	if got := out[0].Items[0].JSON["file_path"]; got != want {
		t.Errorf("file_path = %v, want %s (relative to the workdir)", got, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "hi" {
		t.Errorf("content = %q", b)
	}
}

func TestSpreadsheet_ConfinedToOrgWorkdir(t *testing.T) {
	ctx, root, outside := confinedDirs(t)
	for _, op := range []string{"read_csv", "read_xlsx"} {
		for _, p := range []string{filepath.Join(outside, "secret.csv"), "../outside/secret.csv", "escape/secret.csv"} {
			_, err := (&SpreadsheetNode{}).Execute(ctx, workflow.NodeInput{}, map[string]interface{}{"operation": op, "file_path": p})
			if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
				t.Errorf("%s %q: err = %v, want ErrOutsideWorkdir", op, p, err)
			}
		}
	}
	items := []workflow.Item{workflow.NewItem(map[string]interface{}{"a": "1"})}
	for _, op := range []string{"write_csv", "write_xlsx"} {
		_, err := (&SpreadsheetNode{}).Execute(ctx, workflow.NodeInput{Items: items}, map[string]interface{}{"operation": op, "file_path": filepath.Join(outside, "new."+op)})
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("%s outside: err = %v, want ErrOutsideWorkdir", op, err)
		}
	}
	if _, err := (&SpreadsheetNode{}).Execute(ctx, workflow.NodeInput{Items: items}, map[string]interface{}{"operation": "write_csv", "file_path": "t.csv"}); err != nil {
		t.Fatalf("write_csv inside: %v", err)
	}
	out, err := (&SpreadsheetNode{}).Execute(ctx, workflow.NodeInput{}, map[string]interface{}{"operation": "read_csv", "file_path": filepath.Join(root, "t.csv")})
	if err != nil || len(out[0].Items) != 1 {
		t.Fatalf("read_csv inside: %v, %v", out, err)
	}
}

func TestSpreadsheet_UnconfinedKeepsAbsolutePaths(t *testing.T) {
	_, _, outside := confinedDirs(t)
	out, err := (&SpreadsheetNode{}).Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{"operation": "read_csv", "file_path": filepath.Join(outside, "secret.csv")})
	if err != nil || len(out[0].Items) != 1 {
		t.Fatalf("an unconfined run must read any path as before: %v, %v", out, err)
	}
}
