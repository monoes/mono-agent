package account

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// A rename or a remove is durable only once the directory that holds the entry
// has been flushed: after a power cut the old refresh.enc, a token that was
// rotated away, can come back, and the next refresh would present it, which
// monoes.me takes for theft. What a flush does cannot be seen from a test, so
// these tests see that and when the store asks for one, through the syncDirFn
// seam. They are in package account because the seam is not exported.

// syncCall is one call of syncDirFn: the directory it was given, and the names
// that directory held at that moment.
type syncCall struct {
	dir     string
	entries []string
}

// spyOnSyncDir replaces syncDirFn for the rest of the test and returns what it
// has been called with so far. A test that uses it must not call t.Parallel().
func spyOnSyncDir(t *testing.T) func() []syncCall {
	t.Helper()
	var mu sync.Mutex
	var calls []syncCall
	prev := syncDirFn
	syncDirFn = func(dir string) {
		var names []string
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				names = append(names, e.Name())
			}
		}
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, syncCall{dir: dir, entries: names})
	}
	t.Cleanup(func() { syncDirFn = prev })
	return func() []syncCall {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func hasTempFile(names []string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, ".tmp-") })
}

func TestAWriteSyncsItsDirectoryOnceTheRenameIsDone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	st := OpenStore(dir, NewMemorySealer())
	calls := spyOnSyncDir(t)
	for _, c := range []struct {
		name  string
		file  string
		write func() error
	}{
		{"Save", sessionFile, func() error { return st.Save(&Session{Host: "h"}) }},
		{"SaveRefresh", refreshFile, func() error { return st.SaveRefresh("rt-1") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := len(calls())
			if err := c.write(); err != nil {
				t.Fatal(err)
			}
			got := calls()[before:]
			if len(got) != 1 || got[0].dir != dir {
				t.Fatalf("directories synced: %q, want exactly one: %q", dirsOf(got), dir)
			}
			if !slices.Contains(got[0].entries, c.file) || hasTempFile(got[0].entries) {
				t.Fatalf("when the directory was synced it held %v: the rename was not done yet, and it is what the sync is for", got[0].entries)
			}
		})
	}
}

func dirsOf(calls []syncCall) []string {
	dirs := []string{}
	for _, c := range calls {
		dirs = append(dirs, c.dir)
	}
	return dirs
}

func TestARemovalSyncsItsDirectoryOnceTheFileIsGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	st := OpenStore(dir, NewMemorySealer())
	if err := st.SaveRefresh("rt-1"); err != nil {
		t.Fatal(err)
	}
	calls := spyOnSyncDir(t)
	if err := st.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 || got[0].dir != dir {
		t.Fatalf("directories synced: %q, want exactly one: %q", dirsOf(got), dir)
	}
	if slices.Contains(got[0].entries, refreshFile) {
		t.Fatalf("when the directory was synced it still held %v: the remove was not done yet", got[0].entries)
	}
}

// Nothing was removed, so there is nothing to make durable.
func TestARemovalOfNothingSyncsNothing(t *testing.T) {
	t.Run("in a directory that exists", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "account")
		st := OpenStore(dir, NewMemorySealer())
		if err := st.Save(&Session{Host: "h"}); err != nil { // the directory exists, refresh.enc does not
			t.Fatal(err)
		}
		calls := spyOnSyncDir(t)
		if err := st.DeleteRefresh(); err != nil {
			t.Fatal(err)
		}
		if got := calls(); len(got) != 0 {
			t.Fatalf("%d syncs for a DeleteRefresh that removed nothing, want none", len(got))
		}
	})
	t.Run("with no directory", func(t *testing.T) {
		st := OpenStore(filepath.Join(t.TempDir(), "account"), NewMemorySealer())
		calls := spyOnSyncDir(t)
		if err := st.DeleteRefresh(); err != nil {
			t.Fatal(err)
		}
		if got := calls(); len(got) != 0 {
			t.Fatalf("%d syncs for a DeleteRefresh with no directory, want none", len(got))
		}
	})
}

// A rename that failed has made nothing durable, and the old file stands.
func TestAFailedRenameSyncsNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	if err := os.MkdirAll(filepath.Join(dir, sessionFile), 0o700); err != nil { // a directory where the file must go
		t.Fatal(err)
	}
	st := OpenStore(dir, NewMemorySealer())
	calls := spyOnSyncDir(t)
	if err := st.Save(&Session{Host: "h"}); err == nil {
		t.Fatal("Save over a directory succeeded: this test cannot tell")
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("%d syncs after a rename that failed, want none", len(got))
	}
}

// syncDir is best effort: whatever it is given, it neither panics nor creates
// anything.
func TestSyncDirNeverFailsAWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncDir(dir)                      // a directory
	syncDir(file)                     // not a directory
	syncDir(filepath.Join(dir, "no")) // not there at all
	syncDir("")
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "file" {
		t.Fatalf("syncDir changed the directory: %v (err %v)", entries, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "no")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("syncDir created a missing directory (stat err %v)", err)
	}
}
