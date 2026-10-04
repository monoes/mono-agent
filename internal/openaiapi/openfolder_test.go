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

// A folder inside a folder that is open is opened with the same care: a link or a file
// at its name is not a folder, and neither is one that was swapped in after the look.
func TestOpenChildRefusesALinkAFileAndASwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	slot := filepath.Join(t.TempDir(), "slot")
	for _, d := range []string{"plain", "swapped", "sibling"} {
		if err := os.MkdirAll(filepath.Join(slot, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(slot, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sibling", filepath.Join(slot, "link")); err != nil { // stays inside the folder
		t.Fatal(err)
	}
	root := openRoot(t, slot)

	child, err := openChild(root, "plain")
	if err != nil {
		t.Fatalf("a plain folder: %v", err)
	}
	child.Close()
	for _, name := range []string{"file", "link", "missing"} {
		if c, err := openChild(root, name); err == nil {
			c.Close()
			t.Errorf("%s was opened as a folder", name)
		}
	}
	afterLstatHook.set(func() {
		_ = os.RemoveAll(filepath.Join(slot, "swapped"))
		if err := os.Symlink("sibling", filepath.Join(slot, "swapped")); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { afterLstatHook.set(nil) })
	if c, err := openChild(root, "swapped"); err == nil {
		c.Close()
		t.Error("a folder swapped for a link after the look was opened")
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
