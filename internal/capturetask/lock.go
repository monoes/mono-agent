package capturetask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Locking the board.
//
// <org>-issues.json is a shared document: monomind's dashboard writes it,
// the mastermind skills write it, and two `monoagentcli capture task` runs
// write it. Appending an issue is a read-modify-write of the whole file, so
// without a lock the slower writer publishes a document that never held the
// faster one's issue — an atomic rename makes that clobber clean, not safe.
//
// So every write takes an exclusive advisory lock on a sidecar file first,
// re-reads the board under it, and checks the board has not moved between
// that read and the rename (boardStamp). The lock stops writers that take
// it; the stamp catches the ones that do not — a foreign writer is a
// refusal to file rather than a silently discarded issue.
//
// The sidecar is dot-prefixed so it is not mistaken for an org artifact,
// and it is never deleted: removing a lock file is its own race (a second
// process can be holding the fd of a file this one just unlinked), and an
// empty file costs nothing.

// lockSuffix names the sidecar: .<org>-issues.json.lock
const lockSuffix = ".lock"

// lockPath is the sidecar lock file for a board.
func lockPath(board string) string {
	return filepath.Join(filepath.Dir(board), "."+filepath.Base(board)+lockSuffix)
}

// lockBoard takes the exclusive lock for one board, blocking until it is
// free, and returns the release function.
func lockBoard(board string) (func(), error) {
	dir := filepath.Dir(board)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.OpenFile(lockPath(board), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock board %s: %w", board, err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock board %s: %w", board, err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// errBoardChanged says the board was rewritten by someone who did not take
// the lock, between this process reading it and publishing its own copy.
// Writing anyway would drop whatever they wrote.
var errBoardChanged = errors.New("capturetask: the board changed while the task was being filed")

// boardStamp identifies a version of the board cheaply. It is not a
// content hash: it exists to catch another writer, not to prove two files
// are identical, and a stat is what can be taken again immediately before
// the rename.
type boardStamp struct {
	exists  bool
	size    int64
	modTime int64 // UnixNano
}

func stampOf(path string) (boardStamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return boardStamp{}, nil
	}
	if err != nil {
		return boardStamp{}, fmt.Errorf("stat board %s: %w", path, err)
	}
	return boardStamp{exists: true, size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

func (s boardStamp) same(other boardStamp) bool {
	return s == other
}
