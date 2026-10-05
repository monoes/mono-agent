package openaiapi

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// maxCleanDepth is how deep emptyDir walks. A runtime builds nothing this deep
// by accident (a node_modules tree is a few dozen levels), and a walk holds a
// file descriptor per level: a chain of directories deeper than this is a way to
// exhaust the process's descriptors, so such a folder is set aside, not walked.
const maxCleanDepth = 100

// maxPurgePasses is how many times purge lists a directory that keeps filling
// after it was emptied, before it gives up on it.
const maxPurgePasses = 3

// emptyBudget is how long emptyDir may take, whatever the tree does. A process a
// runtime left behind can keep changing a folder while it is emptied, and the walk
// of a tree that changes has no end it can count on: an open that waits for a
// writer (Root.OpenRoot does not ask for a directory, so a FIFO standing where a
// directory was listed blocks it), a directory filled as fast as it is emptied. A
// folder that takes longer is set aside by the caller, which is one rename, not
// waited for. A variable so a test can shorten it.
var emptyBudget = 30 * time.Second

// testHook is a function a test installs to change the tree at the moment a
// process that outlived its turn would. It is read atomically: a walk that
// emptyDir gave up on keeps running, and keeps reading its hooks, after the test
// that installed them has moved on.
type testHook struct{ p atomic.Pointer[func()] }

func (h *testHook) set(f func()) {
	if f == nil {
		h.p.Store(nil)
		return
	}
	h.p.Store(&f)
}

func (h *testHook) run() {
	if f := h.p.Load(); f != nil {
		(*f)()
	}
}

// afterLstatHook runs after emptyDir has looked at the folder it was asked to
// empty and before it opens it.
var afterLstatHook testHook

// afterListHook runs after either walk has listed a directory and before it acts
// on what it found.
var afterListHook testHook

// emptyDir removes everything inside dir and keeps dir. It reports whether the
// folder is empty afterwards: false for a tree deeper than maxCleanDepth (nothing
// is removed from it), for one it could not empty, and for one it could not empty
// in time (emptyBudget). A walk that is given up on is not waited for and ends by
// itself at its next step, so the caller can set the folder aside at once.
func emptyDir(dir string) bool {
	deadline := time.Now().Add(emptyBudget)
	done := make(chan bool, 1) // buffered: a walk given up on must not wait for a reader of its answer
	go func() { done <- emptyDirBy(dir, deadline) }()
	timer := time.NewTimer(emptyBudget)
	defer timer.Stop()
	select {
	case ok := <-done:
		return ok
	case <-timer.C:
		return false
	}
}

// emptyDirBy is the walk emptyDir bounds: it looks at the clock between its steps
// and gives up once deadline has passed.
//
// A runtime can leave a read-only directory behind it (Go's module cache does),
// which a removal cannot empty, so the directories are opened up first: the
// gateway owns these folders.
//
// All of it is done through open handles, never by path: dir is opened by
// openFolder. Below dir a process can outlive its turn and keep changing the
// tree, and a directory it swaps for a link while this walks cannot send the
// chmod, the listing or the removal outside dir: the handle refuses a link that
// leaves it.
func emptyDirBy(dir string, deadline time.Time) bool {
	root, err := openFolder(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	return openUp(root, 0, deadline) && purge(root, 0, deadline)
}

// openFolder opens a turn's folder as a root, for everything the gateway does
// to it: emptying it, and reading what the turn left in it. dir is first looked at
// from its parent, which no turn can change (a sandbox that confines writes
// below the turn's folder still lets the turn remove that folder and put a link
// in its place): a link, or anything that is not a directory, is not a folder to
// open, whatever it points to. The handle opened on it must be the one that was
// looked at, since a link that stays inside the parent is followed by a root
// handle. The gateway owns the folder, so it is opened up first: a runtime may
// have left it unreadable.
func openFolder(dir string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openChild(parent, filepath.Base(dir))
}

// openChild is openFolder for a folder of one that is open already: a turn's own
// output folder, inside its working folder, which the turn can swap for a link as
// well as the working folder itself.
func openChild(parent *os.Root, name string) (*os.Root, error) {
	fi, err := parent.Lstat(name) // a link at name is not followed
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a plain directory", name)
	}
	afterLstatHook.run()
	_ = parent.Chmod(name, 0o700)
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if cur, err := root.Lstat("."); err != nil || !os.SameFile(fi, cur) {
		root.Close()
		return nil, fmt.Errorf("%s was replaced while it was opened", name)
	}
	return root, nil
}

// expired reports whether deadline has passed.
func expired(deadline time.Time) bool { return !time.Now().Before(deadline) }

// readDir lists the directory root is.
func readDir(root *os.Root) ([]os.DirEntry, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// openUp gives the owner access to every real directory under root, down to
// maxCleanDepth, and reports whether the tree is no deeper. A link is reported
// as a link by the listing, so it is neither opened nor entered; a directory
// that was swapped for one after the listing is refused by the handle, and the
// folder is then reported as one that cannot be emptied.
func openUp(root *os.Root, depth int, deadline time.Time) bool {
	entries, err := readDir(root)
	if err != nil {
		return false
	}
	afterListHook.run()
	for _, e := range entries {
		if expired(deadline) {
			return false
		}
		if !e.IsDir() {
			continue
		}
		if depth+1 > maxCleanDepth {
			return false
		}
		_ = root.Chmod(e.Name(), 0o700)
		sub, err := root.OpenRoot(e.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed meanwhile
		}
		if err != nil {
			return false
		}
		ok := openUp(sub, depth+1, deadline)
		sub.Close()
		if !ok {
			return false
		}
	}
	return true
}

// purge removes everything inside root, one directory at a time, and reports
// whether the directory it listed last was empty. It is os.Root.RemoveAll with
// the limits that does not have, which a tree that is still changing needs: it
// does not enter a directory below maxCleanDepth (each level holds a descriptor),
// it lists a directory that keeps filling only maxPurgePasses times, and it stops
// at the deadline. What stands where a directory was listed is removed as what it
// is now: Remove does not follow a link and does not open what it removes, and the
// handle refuses a link that leaves the root.
func purge(root *os.Root, depth int, deadline time.Time) bool {
	for pass := 0; pass < maxPurgePasses; pass++ {
		entries, err := readDir(root)
		if err != nil {
			return false
		}
		if len(entries) == 0 {
			return true
		}
		afterListHook.run()
		for _, e := range entries {
			if expired(deadline) {
				return false
			}
			name := e.Name()
			if e.IsDir() {
				if depth+1 > maxCleanDepth {
					return false
				}
				_ = root.Chmod(name, 0o700)
				switch sub, err := root.OpenRoot(name); {
				case errors.Is(err, fs.ErrNotExist):
					continue // removed meanwhile
				case err == nil:
					ok := purge(sub, depth+1, deadline)
					sub.Close()
					if !ok {
						return false
					}
				}
				// An open that failed means what is there is no longer a directory
				// of this tree (a link out of it, a file): Remove takes it away as it is.
			}
			if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false
			}
		}
	}
	return false // it kept filling
}
