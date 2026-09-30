package orgchat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Where flock isn't available the lock is a claim file created with
// O_EXCL. Its holder touches it every claimRefresh while it holds it, so a
// claim older than claimStale belongs to a holder that crashed and is
// broken; a live one, however long its monomind calls take, never looks
// stale. Variables so tests can shorten them.
var (
	claimRefresh = 10 * time.Second
	claimStale   = 30 * time.Second
)

// claimLock takes the claim at path, waiting for its holder until ctx ends
// or lockWaitTimeout passes, and returns the release.
func claimLock(ctx context.Context, path string) (func(), error) {
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		h, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			h.Close()
			return holdClaim(path), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > claimStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another process has held %s for %s", filepath.Base(path), lockWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// holdClaim keeps the claim fresh until the returned release removes it.
func holdClaim(path string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(claimRefresh)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				_ = os.Chtimes(path, now, now)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			_ = os.Remove(path)
		})
	}
}
