//go:build unix

package automation

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on the open lock file, waiting for
// whoever holds it (another CLI process seeding or installing).
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
