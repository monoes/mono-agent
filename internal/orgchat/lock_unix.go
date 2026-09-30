//go:build unix

package orgchat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockFile takes an exclusive flock on the open lock file without blocking
// in the kernel: it retries until the holder (another process resolving in
// the same org) lets go, ctx ends, or lockWaitTimeout passes.
func lockFile(ctx context.Context, f *os.File) error {
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another process has held %s for %s", filepath.Base(f.Name()), lockWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
