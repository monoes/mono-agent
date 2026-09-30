package orgchat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func shortClaims(t *testing.T) {
	t.Helper()
	refresh, stale := claimRefresh, claimStale
	claimRefresh, claimStale = 20*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { claimRefresh, claimStale = refresh, stale })
}

// A holder whose monomind calls run long keeps its claim fresh, so a
// waiter never breaks it, however long past claimStale it waits.
func TestClaimOfALiveHolderIsNeverBroken(t *testing.T) {
	shortClaims(t)
	path := filepath.Join(t.TempDir(), "x.lock.claim")
	release, err := claimLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 4*claimStale)
	defer cancel()
	if _, err := claimLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a live claim was taken over: %v", err)
	}
}

// A claim nobody refreshes (its holder crashed) is broken once stale.
func TestClaimOfACrashedHolderIsBroken(t *testing.T) {
	shortClaims(t)
	path := filepath.Join(t.TempDir(), "x.lock.claim")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := claimLock(ctx, path)
	if err != nil {
		t.Fatalf("stale claim not broken: %v", err)
	}
	release()
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("release left the claim: %v", err)
	}
}
