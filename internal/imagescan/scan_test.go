package imagescan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/imagescan"
)

func TestIsImageFile(t *testing.T) {
	valid := []string{"foo.png", "bar.JPG", "baz.jpeg", "test.gif", "test.webp", "vector.svg", "icon.ico", "pic.bmp", "scan.tiff", "photo.avif"}
	for _, f := range valid {
		if !imagescan.IsImageFile(f) {
			t.Errorf("IsImageFile(%q) = false, want true", f)
		}
	}

	invalid := []string{"foo.txt", "script.sh", "code.go", "archive.zip", "noext", "image.png.bak"}
	for _, f := range invalid {
		if imagescan.IsImageFile(f) {
			t.Errorf("IsImageFile(%q) = true, want false", f)
		}
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()

	// Create valid images in root and subdirectories
	if err := os.WriteFile(filepath.Join(dir, "root.png"), []byte("png1"), 0644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "assets", "icons")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "app.svg"), []byte("<svg></svg>"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create ignored files/directories
	nodeModules := filepath.Join(dir, "node_modules", "somepkg")
	if err := os.MkdirAll(nodeModules, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeModules, "pkg_logo.png"), []byte("logo"), 0644); err != nil {
		t.Fatal(err)
	}

	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "git.png"), []byte("git"), 0644); err != nil {
		t.Fatal(err)
	}

	docFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(docFile, []byte("text"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err := imagescan.Scan(dir)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if len(found) != 2 {
		t.Fatalf("Scan found %d files, want 2 (root.png and app.svg)", len(found))
	}

	names := map[string]bool{}
	for _, f := range found {
		names[f.Filename] = true
	}
	if !names["root.png"] || !names["app.svg"] {
		t.Errorf("Unexpected filenames found: %+v", names)
	}
}

func TestInScannedTree(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"p", "root")
	for _, c := range []struct {
		path string
		want bool
	}{
		{filepath.Join(root, "logo.png"), true},
		{filepath.Join(root, "images", "a", "logo.png"), true},
		{filepath.Join(root, ".hidden.png"), true},
		{filepath.Join(root, ".monoagent", "vault", "img-001.png"), false},
		{filepath.Join(root, "site", "node_modules", "x.png"), false},
		{filepath.Join(root, "a", ".git", "x.png"), false},
		{root, false},
		{filepath.Join(root, "..", "other", "x.png"), false},
		{filepath.Join(root+"-sibling", "x.png"), false},
	} {
		if got := imagescan.InScannedTree(root, c.path); got != c.want {
			t.Errorf("InScannedTree(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
