package openaiapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// turnFolder is a working folder that holds the output folder of a turn, "out-x".
func turnFolder(t *testing.T) (dir, out string) {
	t.Helper()
	dir = slotFolder(t)
	out = filepath.Join(dir, "out-x")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, out
}

// sparseFile makes a file of size bytes that begins with head and holds no data
// blocks after it: a runtime, or a process that outlived its turn, can make as many as
// it likes in no time and without the disk.
func sparseFile(t *testing.T, path string, head []byte, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(head); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// What is no image by its first bytes is not read any further: a folder of big files that
// are no image costs a few bytes of each, not 20 MiB.
func TestCollectImagesReadsOnlyTheFirstBytesOfAFileThatIsNoImage(t *testing.T) {
	dir := slotFolder(t)
	const files = 8
	for i := range files {
		sparseFile(t, filepath.Join(dir, fmt.Sprintf("f%d.dat", i)), []byte("not a picture"), maxImageBytes)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := collectIn(t, dir, 4)
	runtime.ReadMemStats(&after)

	if len(got.images) != 0 || got.skipped["not an image"] != files {
		t.Fatalf("%d images, skipped %v, want the %d files left out as no image", len(got.images), got.skipped, files)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 4<<20 {
		t.Errorf("looking at %d files of 20 MiB that are no image allocated %d MiB: only their first bytes are to be read", files, alloc>>20)
	}
}

// A file that begins like an image and is one is read whole, whatever else it is.
func TestCollectImagesReadsWholeAFileThatBeginsLikeAnImage(t *testing.T) {
	dir := slotFolder(t)
	body := append(append([]byte{}, pngBytes...), make([]byte, 3<<20)...)
	body[len(body)-1] = 7
	writeFiles(t, dir, map[string][]byte{"a.png": body, "b.png": []byte("\x89PNG\r\n\x1a\n")}) // the second is no more than its signature
	got := collectIn(t, dir, 4)
	if !equalImages(got.images, body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("%d images: the whole of a file that begins like an image is returned, a signature alone included", len(got.images))
	}
}

// A folder is looked at up to a number of entries, whatever it holds: a process that
// outlived an earlier turn makes as many as it likes.
func TestCollectImagesLooksAtNoMoreThanTheFirstEntriesOfAFolder(t *testing.T) {
	dir := slotFolder(t)
	var looked atomic.Int32
	afterImageLstatHook.set(func() { looked.Add(1) })
	t.Cleanup(func() { afterImageLstatHook.set(nil) })

	for i := range maxCollectEntries {
		writeFiles(t, dir, map[string][]byte{fmt.Sprintf("f%03d.txt", i): []byte("a note")})
	}
	got := collectIn(t, dir, 4)
	if int(looked.Load()) != maxCollectEntries || got.skipped["too many files"] != 0 {
		t.Fatalf("a folder of exactly %d entries: %d looked at, skipped %v, want all looked at and none too many", maxCollectEntries, looked.Load(), got.skipped)
	}

	looked.Store(0)
	for i := maxCollectEntries; i < maxCollectEntries+36; i++ {
		writeFiles(t, dir, map[string][]byte{fmt.Sprintf("f%03d.txt", i): []byte("a note")})
	}
	got = collectIn(t, dir, 4)
	if int(looked.Load()) != maxCollectEntries || got.skipped["too many files"] != 1 {
		t.Errorf("a folder of %d entries: %d looked at, skipped %v, want the first %d looked at and the folder said to hold too many", maxCollectEntries+36, looked.Load(), got.skipped, maxCollectEntries)
	}
}

// The caller leaving, or the time being up, ends the collection between two files.
func TestReadImagesStopsWhenTheCallerLeavesOrTheTimeIsUp(t *testing.T) {
	dir := slotFolder(t)
	writeFiles(t, dir, map[string][]byte{"a.png": pngBytes})
	root := openRoot(t, dir)

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if got := readImages(gone, root, 4, time.Now().Add(time.Minute)); len(got.images) != 0 || got.skipped["the caller left"] != 1 {
		t.Errorf("the caller left: %d images, skipped %v", len(got.images), got.skipped)
	}
	if got := readImages(context.Background(), root, 4, time.Now().Add(-time.Second)); len(got.images) != 0 || got.skipped["it took too long"] != 1 {
		t.Errorf("the time was up: %d images, skipped %v", len(got.images), got.skipped)
	}
	if got := readImages(context.Background(), root, 4, time.Now().Add(time.Minute)); !equalImages(got.images, pngBytes) {
		t.Errorf("with time left and the caller there: %d images", len(got.images))
	}
}

// A step that does not return (a read that waits on a disk, a share that stopped
// answering) must not hold the request: the caller leaving, or the budget running out,
// ends the collection, and what it was doing is not waited for.
func TestCollectImagesIsNotHeldUpByAStepThatDoesNotReturn(t *testing.T) {
	for name, c := range map[string]struct {
		leave bool // the caller leaves; otherwise the budget runs out
		why   string
	}{
		"the caller leaves":   {true, "the caller left"},
		"the budget runs out": {false, "it took too long"},
	} {
		dir, out := turnFolder(t)
		writeFiles(t, out, map[string][]byte{"a.png": pngBytes})
		entered, release := make(chan struct{}, 1), make(chan struct{})
		afterImageLstatHook.set(func() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		})
		oldBudget := collectBudget
		collectBudget = 300 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan collected, 1)
		go func() { done <- collectImages(ctx, dir, "out-x", 4) }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the collection never reached the step", name)
		}
		if c.leave {
			cancel()
		}
		select {
		case got := <-done:
			if len(got.images) != 0 || got.skipped[c.why] != 1 {
				t.Errorf("%s: %d images, skipped %v, want nothing and %q", name, len(got.images), got.skipped, c.why)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: the collection is held up by a step that does not return", name)
		}
		cancel()
		afterImageLstatHook.set(nil)
		close(release)
		collectBudget = oldBudget
	}
}

// Only the folder a turn was given is read. Files at the top of its working folder, in
// another folder of it, or in the runtime's temp folder are not: whoever wrote them, it
// was not this turn. They are counted for the operator, by reason.
func TestCollectImagesReadsOnlyTheTurnsOwnFolder(t *testing.T) {
	dir, out := turnFolder(t)
	for _, d := range []string{"out-y", turnTmpName} { // another turn's folder, and the runtime's temp folder
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFiles(t, out, map[string][]byte{"mine.png": jpegBytes})
	writeFiles(t, filepath.Join(dir, "out-y"), map[string][]byte{"theirs.png": webpBytes})
	writeFiles(t, filepath.Join(dir, turnTmpName), map[string][]byte{"tmp.png": gifBytes})
	writeFiles(t, dir, map[string][]byte{"top.png": pngBytes, "top-2.png": pngBytes, "notes.txt": []byte("x")})

	got := collectImages(context.Background(), dir, "out-x", 4)
	if !equalImages(got.images, jpegBytes) {
		t.Fatalf("collected %d images, want only the one in the turn's own folder", len(got.images))
	}
	if got.skipped["outside the turn's folder"] != 3 { // the three files at the top; a folder is not a file
		t.Errorf("skipped = %v, want the three files at the top counted as outside the turn's folder", got.skipped)
	}

	// With nothing in the turn's folder the files beside it are not a fallback, and are said to be there.
	if err := os.Remove(filepath.Join(out, "mine.png")); err != nil {
		t.Fatal(err)
	}
	got = collectImages(context.Background(), dir, "out-x", 4)
	if len(got.images) != 0 || got.skipped["outside the turn's folder"] != 3 {
		t.Errorf("an empty output folder: %d images, skipped %v", len(got.images), got.skipped)
	}
}
