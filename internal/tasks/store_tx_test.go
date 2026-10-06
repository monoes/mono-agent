package tasks

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// The event and the revision are part of the add: when either cannot be written, nothing is.
func TestAddWritesTheTaskTheEventAndTheRevisionOrNone(t *testing.T) {
	for _, table := range []string{"task_events", "task_board_rev"} {
		t.Run(table, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			if _, err := db.Exec(`CREATE TRIGGER no_` + table + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Add(bg, "default", AddInput{Title: "t"}, human); err == nil {
				t.Fatal("the add succeeded though its " + table + " row was refused")
			}
			for _, tb := range []string{"tasks", "task_events", "task_board_rev"} {
				if n := countWhere(t, db, tb, "1 = 1"); n != 0 {
					t.Errorf("%d rows left in %s", n, tb)
				}
			}
			if _, err := db.Exec(`DROP TRIGGER no_` + table); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Add(bg, "default", AddInput{Title: "t"}, human); err != nil {
				t.Errorf("the next add: %v", err)
			}
		})
	}
}

// A context that ends in the middle of the transaction rolls it back and leaves the connection
// and the write lock free.
func TestACancelledContextLeavesNothingBehind(t *testing.T) {
	s, db, c := newTestStore(t)
	ctx, cancel := context.WithCancel(bg)
	s.now = func() time.Time { cancel(); return c.t } // read inside the transaction
	if _, _, err := s.Add(ctx, "default", AddInput{Title: "t"}, human); err == nil {
		t.Fatal("the add succeeded in a cancelled context")
	}
	s.now = c.now
	if n := countWhere(t, db, "tasks", "1 = 1") + countWhere(t, db, "task_events", "1 = 1") + countWhere(t, db, "task_board_rev", "1 = 1"); n != 0 {
		t.Errorf("%d rows were written", n)
	}
	for i := 0; i < 3; i++ { // the pool hands out every connection it has
		if _, _, err := s.Add(bg, "default", AddInput{Title: "after"}, human); err != nil {
			t.Fatalf("add %d after the cancelled one: %v", i+1, err)
		}
	}
}

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

func TestSnapshotReadsOneStateAndEndsItsTransaction(t *testing.T) {
	s, db, _ := newTestStore(t)
	mustAdd(t, s, "default", "one", false)
	var first, second int
	err := s.snapshot(bg, func(x dbx) error {
		if err := x.QueryRowContext(bg, `SELECT COUNT(*) FROM tasks`).Scan(&first); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO tasks (profile_id, title, position, created_at, updated_at) VALUES ('default', 'sneaked in', 1, ?, ?)`, rowTime, rowTime); err != nil {
			return err
		}
		return x.QueryRowContext(bg, `SELECT COUNT(*) FROM tasks`).Scan(&second)
	})
	if err != nil || first != 1 || second != 1 {
		t.Errorf("a snapshot saw %d then %d tasks (err %v), want 1 and 1", first, second, err)
	}
	for i := 0; i < 3; i++ { // the connection came back to the pool outside a transaction
		if _, _, err := s.Add(bg, "default", AddInput{Title: "after"}, human); err != nil {
			t.Fatalf("add %d after the snapshot: %v", i+1, err)
		}
	}
}

// BEGIN IMMEDIATE: writers that read first still queue up instead of failing with "database is locked".
func TestConcurrentAddsAllSucceed(t *testing.T) {
	s, db, _ := newTestStore(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, _, err := s.Add(bg, "default", AddInput{Title: fmt.Sprintf("w%d-%d", w, i)}, human); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("%d of 80 adds failed, first: %v", len(failures), failures[0])
	}
	var rev, distinct int
	_ = db.QueryRow(`SELECT rev FROM task_board_rev`).Scan(&rev)
	_ = db.QueryRow(`SELECT COUNT(DISTINCT position) FROM tasks`).Scan(&distinct)
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 80 || rev != 80 || distinct != 80 {
		t.Errorf("%d tasks, revision %d, %d distinct positions, want 80 each", n, rev, distinct)
	}
}
