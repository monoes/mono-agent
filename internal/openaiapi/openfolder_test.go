package openaiapi

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// openFolder is how the emptying and the collection reach a turn's folder: a link
// or a file where the folder belongs is not a folder, whatever it points to.
func TestOpenFolderOpensAPlainFolderAndRefusesWhatIsNotOne(t *testing.T) {
	base := t.TempDir()
	plain, file, link := filepath.Join(base, "plain"), filepath.Join(base, "file"), filepath.Join(base, "link")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := openFolder(plain)
	if err != nil {
		t.Fatalf("a plain folder: %v", err)
	}
	root.Close()
	if _, err := openFolder(file); err == nil {
		t.Error("a file was opened as a folder")
	}
	if _, err := openFolder(filepath.Join(base, "missing")); err == nil {
		t.Error("a folder that is not there was opened")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// A relative link that stays inside the parent: a root handle follows it, so
	// only the look from the parent says it is a link.
	if err := os.Symlink("plain", link); err != nil {
		t.Fatal(err)
	}
	if root, err := openFolder(link); err == nil {
		root.Close()
		t.Error("a link to a folder was opened as the folder")
	}
}

// A link planted where the folder was, between the look from the parent and the
// open, that points to a folder inside the parent is followed by a root handle: what
// was opened must be the folder that was looked at.
func TestOpenFolderRefusesAFolderSwappedForALinkWhileItIsOpened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	base := t.TempDir()
	dir, sibling := filepath.Join(base, "slot"), filepath.Join(base, "sibling")
	for _, d := range []string{dir, sibling} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	afterLstatHook.set(func() {
		_ = os.RemoveAll(dir)
		if err := os.Symlink("sibling", dir); err != nil { // relative: stays inside the parent
			t.Error(err)
		}
	})
	t.Cleanup(func() { afterLstatHook.set(nil) })

	if root, err := openFolder(dir); err == nil {
		root.Close()
		t.Fatal("the sibling folder was opened through a link planted after the look")
	}
}
