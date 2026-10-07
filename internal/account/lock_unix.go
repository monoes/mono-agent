//go:build !windows

package account

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock without waiting, the way
// internal/daemonhb does; Store.Lock polls it so a context can end the wait.
func tryLock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return errLockHeld
		}
		return err
	}
	return nil
}

func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
