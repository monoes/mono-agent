//go:build unix

package openaiapi

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A directory swapped for a FIFO between the listing and the open makes the open
// wait for a writer that never comes: os.Root.OpenRoot does not ask for a
// directory. A process a runtime left behind can do that to a folder as often as
// it likes, so emptyDir does not wait for it: it says it could not, and the
// caller sets the folder aside.
func TestEmptyDirDoesNotHangOnAFifoStandingWhereADirectoryWas(t *testing.T) {
	withBudget(t, 300*time.Millisecond)
	dir := filepath.Join(t.TempDir(), "slot")
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	var swapped atomic.Bool
	afterListHook.set(func() {
		if !swapped.CompareAndSwap(false, true) {
			return
		}
		_ = os.RemoveAll(filepath.Join(dir, "d")) // listed as a directory a moment ago
		if err := syscall.Mkfifo(filepath.Join(dir, "d"), 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { afterListHook.set(nil) })

	done := make(chan bool, 1)
	go func() { done <- emptyDir(dir) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a folder whose walk was given up on must not be reported emptied")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("emptyDir hung on a FIFO standing where a directory was listed")
	}
	if !swapped.Load() {
		t.Fatal("the hook never ran: the test does not exercise the race")
	}
}
