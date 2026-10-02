package apikeys

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// holdWrite takes SQLite's write lock on another connection, as a transaction that
// has not committed yet: statements of other connections that write wait for it
// (busy_timeout) and reads go on. It is how a test makes two calls that overlap in
// production overlap every time: both have read the key by the time the lock is
// released, and then write one after the other. commit ends the transaction; run
// is called inside it first.
func holdWrite(t *testing.T, db *sql.DB, run func(tx *sql.Tx)) (commit func()) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	run(tx)
	return func() {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

// settle gives goroutines that were started time to read the key and to queue for
// the write lock. A call that is slower only lets the test pass, never fail: the
// old code fails this test whenever both have read before the lock is released.
const settle = 300 * time.Millisecond

// Two updates of one key that overlap must both land. Each used to read the key,
// then write both of its columns from what it had read, so the one that wrote
// second put the other's change back.
func TestTwoUpdatesOfOneKeyAtOnceBothLand(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	k, _, err := s.Create(ctx, "default", "app", true)
	if err != nil {
		t.Fatal(err)
	}
	commit := holdWrite(t, db.DB, func(tx *sql.Tx) {
		if _, err := tx.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, "2026-10-02T00:00:00.000Z", k.ID); err != nil {
			t.Fatal(err)
		}
	})

	off, renamed := false, "renamed"
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, u := range []Update{{Context: &off}, {Name: &renamed}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update(ctx, "default", k.ID, u)
			errs <- err
		}()
	}
	time.Sleep(settle)
	commit()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("an update of an active key must succeed: %v", err)
		}
	}

	got, err := s.Get(ctx, "default", k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" || got.Context {
		t.Errorf("both changes must land: name %q, context %v; want renamed and off", got.Name, got.Context)
	}
}

// An update that reads an active key and then loses the race to a revoke must not
// write the revoked row: it reports the key as not found, like an update that came
// after the revoke, and the row stays as the revoke left it.
func TestAnUpdateThatLosesTheRaceToARevokeChangesNothing(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	k, _, err := s.Create(ctx, "default", "app", true)
	if err != nil {
		t.Fatal(err)
	}
	commit := holdWrite(t, db.DB, func(tx *sql.Tx) {
		if _, err := tx.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ?`, "2026-10-02T00:00:00.000Z", k.ID); err != nil {
			t.Fatal(err)
		}
	})

	off, renamed := false, "renamed"
	done := make(chan error, 1)
	go func() {
		_, err := s.Update(ctx, "default", k.ID, Update{Name: &renamed, Context: &off})
		done <- err
	}()
	time.Sleep(settle) // it has read the key, which is still active, and waits to write
	commit()
	if err := <-done; !errors.Is(err, ErrNotFound) {
		t.Errorf("an update that lost the race to a revoke: err = %v, want ErrNotFound", err)
	}

	got, err := s.Get(ctx, "default", k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil || got.Name != "app" || !got.Context {
		t.Errorf("a revoked key was written: name %q, context %v, revoked %v", got.Name, got.Context, got.RevokedAt != nil)
	}
}

// An update that sets what a key already has changes no value, and is still an
// update of an active key: it must not be taken for a key that is not there.
func TestAnUpdateThatSetsWhatTheKeyHasSucceeds(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	k, _, err := s.Create(ctx, "default", "app", true)
	if err != nil {
		t.Fatal(err)
	}
	same, on := "app", true
	for _, u := range []Update{{Name: &same}, {Context: &on}, {Name: &same, Context: &on}, {}} {
		got, err := s.Update(ctx, "default", k.ID, u)
		if err != nil || got.Name != "app" || !got.Context {
			t.Errorf("Update(%+v) = %+v, %v; want the key as it is", u, got, err)
		}
	}
}
