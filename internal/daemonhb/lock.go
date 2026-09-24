package daemonhb

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLocked is returned by Lock when another daemon holds the lock.
var ErrLocked = errors.New("another monoagentcli daemon is already running for this home")

// LockPath is the daemon's single-instance lock, next to its heartbeat.
func LockPath() string { return filepath.Join(filepath.Dir(Path()), "daemon.lock") }

// Lock takes the daemon's single-instance lock, held until release is
// called or the process exits (the OS drops it then, so a crash leaves no
// stale lock). A second daemon gets ErrLocked. Taken before anything else
// starts: two daemons would both fire the same schedules.
func Lock() (release func(), err error) {
	path := LockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unlock(f)
		f.Close()
	}, nil
}

// Locked reports whether a daemon holds the lock right now, including one
// still starting up (its first heartbeat comes only after startup).
func Locked() bool {
	release, err := Lock()
	if err != nil {
		return errors.Is(err, ErrLocked)
	}
	release()
	return false
}
