package tasks

import (
	"context"
	"testing"
)

// Once the work of a transaction is done, the context can no longer undo it or turn it into an
// error. A cancel that raced the COMMIT would otherwise report a failure for a task that was stored,
// and a retry without a client id would make a duplicate.
func TestACancelAfterTheWorkStillCommits(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	err := s.tx(ctx, func(x dbx) error {
		if _, err := x.ExecContext(ctx, `INSERT INTO tasks (profile_id, title, position, created_at, updated_at) VALUES ('default', 'kept', 1, ?, ?)`, rowTime, rowTime); err != nil {
			return err
		}
		cancel() // the last statement is done; the context ends before the COMMIT
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v, want the commit to go through", err)
	}
	if n := countWhere(t, db, "tasks", "title = 'kept'"); n != 1 {
		t.Errorf("%d rows kept, want 1", n)
	}
	mustAdd(t, s, "default", "after", false) // and the connection is free
}
