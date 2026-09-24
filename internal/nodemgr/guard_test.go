package nodemgr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// countArchiveGets wraps a fakeDist handler, counting archive downloads.
func countArchiveGets(srv *httptest.Server, n *atomic.Int32) {
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") || strings.HasSuffix(r.URL.Path, ".zip") {
			n.Add(1)
		}
		inner.ServeHTTP(w, r)
	})
}

// Two installs at once (a GUI click and `doctor fix`) run one after the
// other: the second finds the first's result instead of deleting it.
func TestConcurrentInstallsSerialise(t *testing.T) {
	m := testManager(t)
	srv := fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false)
	var gets atomic.Int32
	countArchiveGets(srv, &gets)
	m.BaseURL = srv.URL

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := *m // separate Managers, as separate processes would have
			_, errs[i] = other.Install(context.Background(), "24.1.0", nil)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := gets.Load(); n != 1 {
		t.Fatalf("archive downloaded %d times, want 1", n)
	}
	if got, err := NodeVersion(context.Background(), m.NodePath("24.1.0")); err != nil || got != "24.1.0" {
		t.Fatalf("installed node: %q, %v", got, err)
	}
}

// An install waits for the lock another process holds, says so, and gives
// up when its context ends.
func TestInstallWaitsForTheLock(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false).URL
	unlock, err := m.lock(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var lines []string
	if _, err := m.Install(ctx, "24.1.0", func(l string) { lines = append(lines, l) }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Install with the lock held: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "waiting for another monoagent process") {
		t.Fatalf("no waiting message: %q", lines)
	}
	unlock()
	if _, err := m.Install(context.Background(), "24.1.0", nil); err != nil {
		t.Fatal(err)
	}
}

// Remove("") deletes the lock file while holding it; a process that was
// waiting on the old file must not then hold a lock nobody else sees.
func TestLockSurvivesItsFileBeingDeleted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file can't be deleted on Windows")
	}
	m := testManager(t)
	first, err := m.lock(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func())
	go func() {
		second, err := m.lock(context.Background(), nil)
		if err != nil {
			t.Error(err)
		}
		got <- second
	}()
	time.Sleep(300 * time.Millisecond) // the goroutine now waits on the old file
	if err := os.Remove(filepath.Join(m.Root, lockFile)); err != nil {
		t.Fatal(err)
	}
	first()
	second := <-got
	defer second()
	f, err := os.OpenFile(filepath.Join(m.Root, lockFile), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("the waiter holds a lock on a deleted file: %v", err)
	}
	defer f.Close()
	if err := tryLock(f); !errors.Is(err, errLocked) {
		t.Fatalf("the current lock file is free while the waiter thinks it holds the lock: %v", err)
	}
}

// Leftovers of an interrupted install are swept at the next one.
func TestInstallSweepsLeftovers(t *testing.T) {
	m := testManager(t)
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false).URL
	os.MkdirAll(filepath.Join(m.Root, ".unpack-123", "node-v24.1.0-linux-x64"), 0o755)
	os.MkdirAll(filepath.Join(m.Root, ".trash-9", "22.12.0"), 0o755)
	os.WriteFile(filepath.Join(m.Root, ".download-456"), []byte("half"), 0o644)
	if _, err := m.Install(context.Background(), "24.1.0", nil); err != nil {
		t.Fatal(err)
	}
	for _, left := range []string{".unpack-123", ".trash-9", ".download-456"} {
		if _, err := os.Lstat(filepath.Join(m.Root, left)); err == nil {
			t.Errorf("%s survived the install", left)
		}
	}
}

func TestSizeCaps(t *testing.T) {
	ctx := context.Background()
	t.Run("download", func(t *testing.T) {
		m := testManager(t)
		m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false).URL
		m.maxDownload = 100
		if _, err := m.Install(ctx, "24.1.0", nil); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("oversized download: %v", err)
		}
	})
	t.Run("download without a length", func(t *testing.T) {
		m := testManager(t)
		m.maxDownload = 100
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 10; i++ {
				w.Write(make([]byte, 64))
				w.(http.Flusher).Flush() // chunked: no Content-Length to reject up front
			}
		}))
		defer srv.Close()
		if _, err := m.download(ctx, srv.URL, new(strings.Builder), func(string) {}); err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("oversized stream: %v", err)
		}
	})
	t.Run("unpacked", func(t *testing.T) {
		m := testManager(t)
		top := strings.TrimSuffix(m.archiveName("24.1.0"), ".tar.gz") + "/"
		arch := makeTarGz(t, []tarEntry{{name: top + "bin/node", body: strings.Repeat("x", 4096)}})
		m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": arch}, false).URL
		m.maxUnpacked = 1024
		if _, err := m.Install(ctx, "24.1.0", nil); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("oversized archive: %v", err)
		}
		entries, _ := os.ReadDir(m.Root)
		for _, e := range entries {
			if e.Name() != lockFile {
				t.Errorf("left %s behind", e.Name())
			}
		}
	})
}

// Checksums signed by a key that isn't a pinned release key are refused
// before anything is downloaded.
func TestInstallRejectsUnsignedChecksums(t *testing.T) {
	m := testManager(t)
	srv := fakeDist(t, m, map[string][]byte{"24.1.0": nodeArchive(t, m, "24.1.0")}, false)
	var gets atomic.Int32
	countArchiveGets(srv, &gets)
	m.BaseURL = srv.URL
	m.keys = openpgp.EntityList{testSigner(t)} // not the key fakeDist signed with
	if _, err := m.Install(context.Background(), "24.1.0", nil); err == nil || !strings.Contains(err.Error(), "not signed by a Node.js release key") {
		t.Fatalf("want a signature error, got %v", err)
	}
	if gets.Load() != 0 {
		t.Fatal("the archive was downloaded before the checksums were trusted")
	}
}

// Remove and Prune keep a version a running process uses (Linux, where
// /proc makes that cheap to see).
func TestRemoveKeepsAVersionInUse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("in-use detection reads /proc")
	}
	m := testManager(t)
	top := strings.TrimSuffix(m.archiveName("24.1.0"), ".tar.gz") + "/"
	// Not `exec sleep`: the shell must stay, with the script in its args.
	node := "#!/bin/sh\nif [ \"$1\" = wait ]; then sleep 10; fi\necho v24.1.0\n"
	m.BaseURL = fakeDist(t, m, map[string][]byte{"24.1.0": makeTarGz(t, []tarEntry{{name: top + "bin/node", body: node}})}, false).URL
	if _, err := m.Install(context.Background(), "24.1.0", nil); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(m.NodePath("24.1.0"), "wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	waitFor(t, func() bool { _, ok := processUsing(filepath.Join(m.Root, "24.1.0")); return ok })

	var inUse *InUseError
	if err := m.Remove("24.1.0"); !errors.As(err, &inUse) || inUse.PID != cmd.Process.Pid {
		t.Fatalf("Remove of a running version: %v", err)
	}
	if err := m.Remove(""); !errors.As(err, &inUse) {
		t.Fatalf("Remove(\"\") while running: %v", err)
	}
	kept, err := m.PruneVersions("99.0.0")
	if err != nil || len(kept) != 1 || kept[0].Version != "24.1.0" {
		t.Fatalf("Prune: kept %v, %v", kept, err)
	}
	if _, ok := m.Current(); !ok {
		t.Fatal("the running version was removed")
	}
	cmd.Process.Kill()
	cmd.Wait()
	if err := m.Remove("24.1.0"); err != nil {
		t.Fatalf("Remove once stopped: %v", err)
	}
	if inst, _ := m.Installed(); len(inst) != 0 {
		t.Fatalf("still installed: %v", inst)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
