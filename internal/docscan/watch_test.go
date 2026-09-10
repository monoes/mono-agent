package docscan_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/docscan"
)

const testInterval = 30 * time.Millisecond

func waitForCall(t *testing.T, calls *int, mu *sync.Mutex, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := *calls
		mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d onChange call(s)", want)
}

func TestWatcherEmitsOnFirstScanForPreExistingFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "resume.md"), "hello")

	var mu sync.Mutex
	var calls int
	var lastFiles []docscan.FileInfo
	w := docscan.NewWatcher(root, testInterval, func(files []docscan.FileInfo) {
		mu.Lock()
		calls++
		lastFiles = files
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()

	waitForCall(t, &calls, &mu, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(lastFiles) != 1 || lastFiles[0].Filename != "resume.md" {
		t.Fatalf("expected the pre-existing file on the first scan, got %+v", lastFiles)
	}
}

func TestWatcherEmitsWhenFileAdded(t *testing.T) {
	root := t.TempDir()

	var mu sync.Mutex
	var calls int
	var lastFiles []docscan.FileInfo
	w := docscan.NewWatcher(root, testInterval, func(files []docscan.FileInfo) {
		mu.Lock()
		calls++
		lastFiles = files
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()

	// First scan: empty root, no files.
	time.Sleep(testInterval * 2)

	mustWrite(t, filepath.Join(root, "new.txt"), "just added")
	waitForCall(t, &calls, &mu, 1)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		found := len(lastFiles) == 1 && lastFiles[0].Filename == "new.txt"
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected new.txt to eventually appear in a snapshot, got %+v", lastFiles)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatcherEmitsWhenFileRemoved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "resume.md")
	mustWrite(t, path, "hello")

	var mu sync.Mutex
	var calls int
	var lastFiles []docscan.FileInfo
	w := docscan.NewWatcher(root, testInterval, func(files []docscan.FileInfo) {
		mu.Lock()
		calls++
		lastFiles = files
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()
	waitForCall(t, &calls, &mu, 1) // initial scan sees resume.md

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitForCall(t, &calls, &mu, 2)
	mu.Lock()
	defer mu.Unlock()
	if len(lastFiles) != 0 {
		t.Fatalf("expected an empty snapshot after removal, got %+v", lastFiles)
	}
}

func TestWatcherDoesNotEmitWhenNothingChanged(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "resume.md"), "hello")

	var mu sync.Mutex
	var calls int
	w := docscan.NewWatcher(root, testInterval, func(files []docscan.FileInfo) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	w.Start()
	defer w.Stop()
	waitForCall(t, &calls, &mu, 1)

	time.Sleep(testInterval * 5)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 call with no changes after the first scan, got %d", calls)
	}
}

func TestWatcherStopEndsPolling(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var calls int
	w := docscan.NewWatcher(root, testInterval, func(files []docscan.FileInfo) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	w.Start()
	waitForCall(t, &calls, &mu, 1)
	w.Stop()

	mu.Lock()
	after := calls
	mu.Unlock()
	time.Sleep(testInterval * 5)
	mu.Lock()
	defer mu.Unlock()
	if calls != after {
		t.Fatalf("expected no more calls after Stop, went from %d to %d", after, calls)
	}
}
