//go:build !unix

package capturetask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Without flock, the lock is a file that can only be created once:
// O_EXCL succeeds for exactly one writer, and the others wait for it to go
// away. This is the platform where the sidecar IS deleted on release,
// because its existence is the lock.
//
// A holder that was killed would leave the door locked forever, so a lock
// older than staleLockAge is broken — long enough that no honest write is
// still in flight (appending one issue is milliseconds of work), short
// enough that a crash does not need a person with a shell.
const (
	lockPollInterval = 20 * time.Millisecond
	lockWaitTimeout  = 30 * time.Second
	staleLockAge     = 2 * time.Minute
)

func lockFile(f *os.File) error {
	// The caller opened the sidecar to hold the fd; the claim itself is a
	// second file beside it, so this build never depends on O_CREATE
	// having been exclusive.
	claim := f.Name() + ".claim"
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		h, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			h.Close()
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(claim); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(claim) // the holder is gone; take the door off its hinges
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another process has held %s for %s", filepath.Base(claim), lockWaitTimeout)
		}
		time.Sleep(lockPollInterval)
	}
}

func unlockFile(f *os.File) error {
	return os.Remove(f.Name() + ".claim")
}
