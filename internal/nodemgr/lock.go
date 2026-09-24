package nodemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// lockFile serialises every change to Root across processes (a GUI click
// and `doctor fix` installing at once used to delete each other's tree
// around the final rename). Install, Use, Remove and Prune hold it; reads
// (Current, Installed, Activate) don't need it, since each change they
// could observe is a single rename or file write.
const lockFile = ".lock"

// errLocked is returned by tryLock when another process holds the lock.
var errLocked = errors.New("locked")

// lock takes the managed Node's lock, waiting (and saying so once) while
// another monoagent process holds it. The returned func releases it.
func (m *Manager) lock(ctx context.Context, progress func(string)) (func(), error) {
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(m.Root, lockFile)
	waiting := false
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, err
		}
		err = tryLock(f)
		if err == nil {
			// Remove("") deletes the lock file while holding it; a process
			// that opened it before then now holds a lock nobody else can
			// see, so it starts over on the new file.
			held, serr := f.Stat()
			cur, perr := os.Stat(path)
			if serr == nil && perr == nil && os.SameFile(held, cur) {
				return func() { unlock(f); f.Close() }, nil
			}
			unlock(f)
			f.Close()
			continue
		}
		f.Close()
		if !errors.Is(err, errLocked) {
			return nil, err
		}
		if !waiting && progress != nil {
			progress("waiting for another monoagent process to finish changing " + m.Root)
		}
		waiting = true
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
