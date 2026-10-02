package openaiapi

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// withBudget shortens emptyBudget for one test.
func withBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := emptyBudget
	emptyBudget = d
	t.Cleanup(func() { emptyBudget = old })
}

// A process a runtime leaves behind can keep changing a folder while the gateway
// empties it, and a walk of a tree that changes has no end it can count on (an
// open that waits, a directory refilled as fast as it is emptied). emptyDir is
// bounded in time: it gives up, and the caller sets the folder aside instead of
// waiting for it.
func TestEmptyDirGivesUpWhenItTakesTooLong(t *testing.T) {
	withBudget(t, 200*time.Millisecond)
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	afterLstatHook.set(func() { <-release })
	t.Cleanup(func() { afterLstatHook.set(nil) })
	t.Cleanup(func() { close(release) }) // runs first: lets the abandoned walk end

	start := time.Now()
	done := make(chan bool, 1)
	go func() { done <- emptyDir(dir) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a folder that could not be emptied in time must not be reported emptied")
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Errorf("emptyDir took %v with a budget of %v", took, emptyBudget)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("emptyDir waited for a walk that does not end")
	}
}

// A walk that was given up on must not go on in the background: it looks at the
// clock between its steps, so a folder that was set aside is not emptied behind
// the operator's back, and a walk the caller left holds nothing open.
func TestEmptyDirByStopsWhenItsTimeIsUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if emptyDirBy(dir, time.Now().Add(-time.Second)) {
		t.Error("a walk that is out of time reported the folder emptied")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 20 {
		t.Errorf("a walk that is out of time must not remove anything: %d entries left of 20", len(entries))
	}
}

// openRoot opens dir as a root, closed with the test.
func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

// purge removes everything below a root, what os.Root.RemoveAll does without its
// limits: a link is removed, never followed; a directory a runtime left
// unreadable is opened up first.
func TestPurgeRemovesEverythingAFolderCanHold(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open any directory: nothing about unreadable ones is tested")
	}
	base := t.TempDir()
	dir, outside := filepath.Join(base, "slot"), filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(dir, "a", "b"), filepath.Join(dir, "closed", "inner"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	precious := filepath.Join(outside, "precious.txt")
	for _, f := range []string{filepath.Join(dir, "top"), filepath.Join(dir, "a", "b", "f"), filepath.Join(dir, "closed", "inner", "g"), precious} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link-out")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	for _, d := range []string{filepath.Join(dir, "closed", "inner"), filepath.Join(dir, "closed")} { // deepest first
		if err := os.Chmod(d, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range []string{filepath.Join(dir, "closed"), filepath.Join(dir, "closed", "inner")} {
			_ = os.Chmod(d, 0o700)
		}
	})

	if !purge(openRoot(t, dir), 0, time.Now().Add(time.Minute)) {
		t.Fatal("purge must empty a tree with a closed directory in it")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d entries left", len(entries))
	}
	if _, err := os.Stat(precious); err != nil {
		t.Errorf("purge followed a link out of the folder: %v", err)
	}
}

// Every level of a walk holds a descriptor: a chain deeper than anything a runtime
// makes by accident is how a turn would exhaust the process's descriptors, and
// what built it can go on building while the gateway walks.
func TestPurgeDoesNotDescendBeyondTheDepthBound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	deepest := dir
	for i := 0; i < maxCleanDepth+20; i++ {
		deepest = filepath.Join(deepest, "d")
	}
	if err := os.MkdirAll(deepest, 0o700); err != nil {
		t.Fatal(err)
	}
	if purge(openRoot(t, dir), 0, time.Now().Add(time.Minute)) {
		t.Error("a tree deeper than maxCleanDepth must not be reported emptied")
	}
}

// A directory that is refilled as fast as it is emptied is given up on after a
// few passes, not chased for as long as the process keeps writing.
func TestPurgeGivesUpOnADirectoryThatKeepsFilling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "first"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var listings atomic.Int32
	afterListHook.set(func() {
		n := listings.Add(1)
		_ = os.WriteFile(filepath.Join(dir, "again-"+string(rune('a'+n))), []byte("x"), 0o600)
	})
	t.Cleanup(func() { afterListHook.set(nil) })

	if purge(openRoot(t, dir), 0, time.Now().Add(time.Minute)) {
		t.Error("a directory that is filled again after every pass must not be reported emptied")
	}
	if n := listings.Load(); n != maxPurgePasses {
		t.Errorf("purge listed the directory %d times, want the %d passes it is allowed", n, maxPurgePasses)
	}
}

// A directory swapped for a link between the listing and the open: the open is
// refused, and what is removed is the link, not what it points to.
func TestPurgeRemovesALinkPlantedAfterTheListingWithoutFollowingIt(t *testing.T) {
	base := t.TempDir()
	dir, outside := filepath.Join(base, "slot"), filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(dir, "z", "inner"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(outside, 0o755); err != nil { // the mode a followed link would change
		t.Fatal(err)
	}
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	afterListHook.set(func() {
		if swapped {
			return
		}
		swapped = true
		_ = os.RemoveAll(filepath.Join(dir, "z")) // listed as a directory a moment ago
		if err := os.Symlink(outside, filepath.Join(dir, "z")); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { afterListHook.set(nil) })

	if !purge(openRoot(t, dir), 0, time.Now().Add(time.Minute)) {
		t.Error("the folder must still be emptied: the link is removed")
	}
	if !swapped {
		t.Fatal("the hook never ran: the test does not exercise the race")
	}
	if fi, err := os.Stat(outside); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("a link planted while purge walked sent it outside the folder: %v %v", fi, err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Errorf("what is behind the link was removed: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d entries left", len(entries))
	}
}
