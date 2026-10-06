package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// meddler is the transaction's own connection with a change made on each side of the UPDATE of a claim:
// before runs just before it reaches the database, after just after it returns. The write lock keeps
// every other connection from changing a task between the read of a claim and its UPDATE, so the change
// is made through the connection of the transaction. What is under test is that the UPDATE alone decides
// what a claim takes, and that the claim says so when what it read does not agree with what it did.
type meddler struct {
	dbx
	before, after func(x dbx)
}

func (m *meddler) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if !strings.HasPrefix(strings.TrimSpace(q), "UPDATE tasks SET status = 'in_progress'") {
		return m.dbx.ExecContext(ctx, q, args...)
	}
	if m.before != nil {
		m.before(m.dbx)
	}
	res, err := m.dbx.ExecContext(ctx, q, args...)
	if m.after != nil {
		m.after(m.dbx)
	}
	return res, err
}

// claimMeddled runs a claim of the task by an agent through a meddler, whose changes are statements
// with the id of the task as their one argument. It returns the row and the number of events of the task
// as they are inside the transaction when the claim is done, and what the claim says. The transaction is
// undone afterwards.
func claimMeddled(t *testing.T, s *Store, id int64, by, before, after string) (row string, events int, err error) {
	t.Helper()
	run := func(q string) func(x dbx) {
		if q == "" {
			return nil
		}
		return func(x dbx) {
			if _, e := x.ExecContext(bg, q, id); e != nil {
				t.Error(e)
			}
		}
	}
	txErr := s.tx(bg, func(x dbx) error {
		m := &meddler{dbx: x, before: run(before), after: run(after)}
		_, err = s.claimTx(bg, m, "default", id, bot(by), 0)
		if e := x.QueryRowContext(bg, `SELECT profile_id || '|' || status || '|' || claimed_by FROM tasks WHERE id = ?`, id).Scan(&row); e != nil {
			t.Error(e)
		}
		if e := x.QueryRowContext(bg, `SELECT COUNT(*) FROM task_events WHERE task_id = ?`, id).Scan(&events); e != nil {
			t.Error(e)
		}
		return errors.New("undo")
	})
	if txErr == nil || txErr.Error() != "undo" {
		t.Fatalf("the transaction: %v", txErr)
	}
	return row, events, err
}

// The UPDATE of a claim takes a task only if it is Ready, or In progress with a lease that has run out
// or already held under the name, in the profile the claim is for; the UPDATE decides that (spec 5.2).
// The claim reads the task before, for what to write, and again when the UPDATE takes nothing, to say
// why, so a task that changed in between is refused for what it has become. Where what the claim read
// and what the UPDATE did do not agree, it says so, and the transaction keeps nothing.
func TestTheUpdateOfAClaimDecidesWhatItTakes(t *testing.T) {
	notASentinel := func(err error) bool {
		return err != nil && !errors.Is(err, ErrNotReady) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrClaimed) && !errors.Is(err, ErrLimit)
	}
	for _, c := range []struct {
		name          string
		heldByOther   bool   // when the claim reads it, "other" holds the task for another hour
		before, after string // changes made to the task, each with its id as the one argument
		want          func(err error) bool
		row           string // the row, in the transaction, afterwards
		events        int
	}{
		{"moved to another profile after it was read", false, `UPDATE tasks SET profile_id = 'p2' WHERE id = ?`, "",
			func(err error) bool { return errors.Is(err, ErrNotFound) }, "p2|ready|", 1},
		{"moved to Review after it was read", false, `UPDATE tasks SET status = 'review' WHERE id = ?`, "",
			func(err error) bool { return errors.Is(err, ErrNotReady) }, "default|review|", 1},
		{"taken by another agent after it was read", false, `UPDATE tasks SET status = 'in_progress', claimed_by = 'other', claim_until = '2026-10-05T13:00:00Z' WHERE id = ?`, "",
			func(err error) bool {
				var ce *ClaimedError
				return errors.Is(err, ErrClaimed) && errors.As(err, &ce) && ce.By == "other"
			}, "default|in_progress|other", 1},
		{"changed and changed back, so that there is nothing to refuse", false, `UPDATE tasks SET status = 'review' WHERE id = ?`, `UPDATE tasks SET status = 'ready' WHERE id = ?`,
			func(err error) bool { return notASentinel(err) && strings.Contains(err.Error(), "not taken") }, "default|ready|", 1},
		{"a lease that runs out after it was read", true, `UPDATE tasks SET claim_until = '2026-10-05T11:00:00Z' WHERE id = ?`, "",
			func(err error) bool { return notASentinel(err) && strings.Contains(err.Error(), "refuse") }, "default|in_progress|one", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			addProfile(t, db, "p2")
			id := mustAdd(t, s, "default", "contested", true).ID
			if c.heldByOther {
				claimsClaim(t, s, id, "other", time.Hour)
			}
			row, events, err := claimMeddled(t, s, id, "one", c.before, c.after)
			if !c.want(err) {
				t.Errorf("the claim says %v", err)
			}
			if row != c.row || events != c.events {
				t.Errorf("the row is %q with %d events, want %q with %d", row, events, c.row, c.events)
			}
		})
	}
}

// A claim takes the task it names and no other, whatever else the board holds, for a claim, a renewal
// and a takeover.
func TestAClaimTakesTheTaskNamedAndNoOther(t *testing.T) {
	s, db, clk := newTestStore(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true).ID)
	}
	held := func(want ...string) {
		t.Helper()
		for i, w := range want {
			if by, _ := opsClaim(t, db, ids[i]); by != w {
				t.Errorf("task #%d is held by %q, want %q (all: %q)", ids[i], by, w, want)
			}
		}
	}
	claimsClaim(t, s, ids[2], "one", 0)
	held("", "", "one", "", "")
	claimsClaim(t, s, ids[0], "two", 0)
	held("two", "", "one", "", "")
	claimsClaim(t, s, ids[2], "one", time.Hour) // a renewal
	held("two", "", "one", "", "")
	clk.advance(2 * time.Hour)
	claimsClaim(t, s, ids[0], "three", 0) // a takeover
	held("three", "", "one", "", "")
	if n := countWhere(t, db, "tasks", "status = 'ready'"); n != 3 {
		t.Errorf("%d tasks are Ready, want 3", n)
	}
}
