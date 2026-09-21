//go:build unix

package capturetask

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on the open file, waiting for whoever
// holds it. flock is tied to the open file description, so two locks taken
// through two Open calls conflict even inside one process — which is what
// makes the in-process race test meaningful rather than a mutex in
// disguise.
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue // a signal interrupted the wait, not a failure to lock
		}
		return err
	}
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
