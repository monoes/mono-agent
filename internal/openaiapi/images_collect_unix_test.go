//go:build unix

package openaiapi

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// collectWithin reads the images at the top of dir and fails the test if that does
// not return: a FIFO that something opens for reading waits for a writer for ever.
func collectWithin(t *testing.T, dir string, n int) collected {
	t.Helper()
	root := openRoot(t, dir)
	done := make(chan collected, 1)
	go func() { done <- readImages(context.Background(), root, n, time.Now().Add(time.Minute)) }()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the collection hung: it opened something that waits for a writer")
		return collected{}
	}
}

// A runtime, or a prompt that steers it, can plant a link to an image anywhere on
// the disk. The gateway runs as the OS user, outside the sandbox: it never follows
// one, whatever it points to.
func TestCollectImagesNeverFollowsALink(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(secret, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := slotFolder(t)
	if err := os.WriteFile(filepath.Join(dir, "real.png"), jpegBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"a-absolute.png": secret,                               // an image elsewhere on the disk
		"b-relative.png": filepath.Join("..", "..", "nowhere"), // out of the folder, and not there
		"c-inside.png":   "real.png",                           // an image of the turn's own folder
		"d-dangling.png": "not-there.png",
		"e-folder.png":   outside, // a folder with an image in it
	} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	got := collectWithin(t, dir, 4)
	if !equalImages(got.images, jpegBytes) {
		t.Fatalf("collected %d images, want only the real file: a link must not be followed, inside the folder or out of it", len(got.images))
	}
	if got.skipped["a link"] != 5 { // a link to a folder is a link too, not a folder of the turn's
		t.Errorf("skipped = %v, want 5 links left out", got.skipped)
	}
}

// A FIFO is not a file to read: opening it for reading waits for a writer.
func TestCollectImagesLeavesAFifoAlone(t *testing.T) {
	dir := slotFolder(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "a.png"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string][]byte{"b.png": pngBytes})

	got := collectWithin(t, dir, 4)
	if !equalImages(got.images, pngBytes) || got.skipped["not a regular file"] != 1 {
		t.Fatalf("a FIFO next to an image: %d images, skipped %v", len(got.images), got.skipped)
	}
}

// What stands at a name when it is opened is what was looked at: a process that
// outlived its turn can swap the file for a FIFO, or for a link, between the two.
func TestCollectImagesCannotBeSwungBetweenTheLookAndTheOpen(t *testing.T) {
	for name, swap := range map[string]func(path string, dir string){
		"a FIFO": func(path, _ string) {
			_ = os.Remove(path)
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Error(err)
			}
		},
		"a link to another image of the folder": func(path, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "other.png"), jpegBytes, 0o600)
			_ = os.Remove(path)
			if err := os.Symlink("other.png", path); err != nil {
				t.Error(err)
			}
		},
	} {
		dir := slotFolder(t)
		path := filepath.Join(dir, "a.png")
		if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		var swapped atomic.Bool
		afterImageLstatHook.set(func() {
			if swapped.CompareAndSwap(false, true) {
				swap(path, dir)
			}
		})

		got := collectWithin(t, dir, 1)
		afterImageLstatHook.set(nil)
		if !swapped.Load() {
			t.Fatalf("%s: the hook never ran: the test does not exercise the race", name)
		}
		if len(got.images) != 0 || got.skipped["changed while it was read"] != 1 {
			t.Errorf("%s: %d images, skipped %v: a file swapped after the look must be left out", name, len(got.images), got.skipped)
		}
	}
}

// A turn that replaced a folder of its own with a link (a sandbox that confines writes
// below the working folder lets it) must not have the collection read what is behind
// it: neither the working folder nor the output folder in it.
func TestCollectImagesDoesNotReadThroughAFolderReplacedWithALink(t *testing.T) {
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "out-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(outside, "out-x"), map[string][]byte{"secret.png": pngBytes})
	ctx := context.Background()

	// The working folder is a link to a folder that holds an output folder with an image.
	dir := slotFolder(t)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if got := collectImages(ctx, dir, "out-x", 4); len(got.images) != 0 || got.note() == "" {
		t.Errorf("read %d images through a link standing where the working folder was (note %q)", len(got.images), got.note())
	}

	// The output folder is a link: to a folder elsewhere, and to another folder of the working folder.
	dir = slotFolder(t)
	if err := os.Mkdir(filepath.Join(dir, "out-y"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(dir, "out-y"), map[string][]byte{"other.png": jpegBytes})
	for name, target := range map[string]string{"elsewhere": filepath.Join(outside, "out-x"), "a sibling": "out-y"} {
		link := filepath.Join(dir, "out-x")
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if got := collectImages(ctx, dir, "out-x", 4); len(got.images) != 0 || got.note() == "" {
			t.Errorf("%s: read %d images through a link standing where the output folder was (note %q)", name, len(got.images), got.note())
		}
	}
}

// A FIFO standing where the output folder was is not opened for reading either: the
// collection ends.
func TestCollectImagesDoesNotHangOnAFifoWhereTheOutputFolderWas(t *testing.T) {
	dir := slotFolder(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "out-x"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan collected, 1)
	go func() { done <- collectImages(context.Background(), dir, "out-x", 4) }()
	select {
	case got := <-done:
		if len(got.images) != 0 || got.note() == "" {
			t.Errorf("%d images, note %q", len(got.images), got.note())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the collection hung on a FIFO standing where the output folder was")
	}
}

// A file the owner cannot read is left out, not fatal for the others.
func TestCollectImagesLeavesOutAFileItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file: nothing about an unreadable one is tested")
	}
	dir := slotFolder(t)
	writeFiles(t, dir, map[string][]byte{"a.png": pngBytes, "b.png": jpegBytes})
	if err := os.Chmod(filepath.Join(dir, "a.png"), 0); err != nil {
		t.Fatal(err)
	}
	got := collectWithin(t, dir, 4)
	if !equalImages(got.images, jpegBytes) || got.skipped["unreadable"] != 1 {
		t.Fatalf("%d images, skipped %v", len(got.images), got.skipped)
	}
}
