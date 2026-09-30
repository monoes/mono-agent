//go:build !unix

package orgchat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Without flock the lock is a claim file created with O_EXCL; a claim
// older than staleLockAge belongs to a crashed holder and is broken.
const staleLockAge = 5 * time.Minute

func lockFile(ctx context.Context, f *os.File) error {
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
			_ = os.Remove(claim)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another process has held %s for %s", filepath.Base(claim), lockWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

func unlockFile(f *os.File) error {
	return os.Remove(f.Name() + ".claim")
}
