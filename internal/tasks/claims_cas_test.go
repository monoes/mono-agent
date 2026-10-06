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

// meddler is the transaction's own connection with a change made on each side of the UPDATE of a claim
// (or of the statement that begins with prefix): before runs just before it reaches the database, after
// just after it returns. The write lock keeps every other connection from changing a task between the
// read of a claim and its UPDATE, so the change is made through the connection of the transaction. What
// is under test is that the UPDATE alone decides what a claim takes, and that the claim says so when what
// it read does not agree with what it did.
type meddler struct {
	dbx
	prefix        string // the statements to meddle with begin with this: the UPDATE of a claim when empty
	before, after func(x dbx)
}

func (m *meddler) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	prefix := m.prefix
	if prefix == "" {
		prefix = "UPDATE tasks SET status = 'in_progress'"
	}
	if !strings.HasPrefix(strings.TrimSpace(q), prefix) {
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
// with the id of the task as their one argument. It returns the row (profile, status, holder and end of
// the lease, separated by bars) and the number of events of the task as they are inside the transaction
// when the claim is done, and what the claim says. The transaction is undone afterwards.
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
		_, err = s.claimTx(bg, m, "default", id, bot(by), s.now().UTC(), 0)
		if e := x.QueryRowContext(bg, `SELECT profile_id || '|' || status || '|' || claimed_by || '|' || claim_until FROM tasks WHERE id = ?`, id).Scan(&row); e != nil {
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
// and what the UPDATE did do not agree on whether the task is taken, it says so, and the transaction
// keeps nothing. Where they agree on that and the task is the holder's own, the UPDATE alone decides the
// lease too: a renewal never shortens it, whatever the claim read.
func TestTheUpdateOfAClaimDecidesWhatItTakes(t *testing.T) {
	notASentinel := func(err error) bool {
		return err != nil && !errors.Is(err, ErrNotReady) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrClaimed) && !errors.Is(err, ErrLimit)
	}
	for _, c := range []struct {
		name          string
		held          string // when the claim reads it, this agent holds the task for another hour (none: it is Ready)
		before, after string // changes made to the task, each with its id as the one argument
		want          func(err error) bool
		row           string // the row, in the transaction, afterwards
		events        int
	}{
		{"moved to another profile after it was read", "", `UPDATE tasks SET profile_id = 'p2' WHERE id = ?`, "",
			func(err error) bool { return errors.Is(err, ErrNotFound) }, "p2|ready||", 1},
		{"moved to Review after it was read", "", `UPDATE tasks SET status = 'review' WHERE id = ?`, "",
			func(err error) bool { return errors.Is(err, ErrNotReady) }, "default|review||", 1},
		{"taken by another agent after it was read", "", `UPDATE tasks SET status = 'in_progress', claimed_by = 'other', claim_until = '2026-10-05T13:00:00Z' WHERE id = ?`, "",
			func(err error) bool {
				var ce *ClaimedError
				return errors.Is(err, ErrClaimed) && errors.As(err, &ce) && ce.By == "other"
			}, "default|in_progress|other|2026-10-05T13:00:00Z", 1},
		{"changed and changed back, so that there is nothing to refuse", "", `UPDATE tasks SET status = 'review' WHERE id = ?`, `UPDATE tasks SET status = 'ready' WHERE id = ?`,
			func(err error) bool { return notASentinel(err) && strings.Contains(err.Error(), "not taken") }, "default|ready||", 1},
		{"a lease that runs out after it was read", "other", `UPDATE tasks SET claim_until = '2026-10-05T11:00:00Z' WHERE id = ?`, "",
			func(err error) bool { return notASentinel(err) && strings.Contains(err.Error(), "refuse") }, "default|in_progress|one|2026-10-05T12:30:00Z", 2},
		{"a renewal whose lease was made longer after it was read keeps the longer end", "one", `UPDATE tasks SET claim_until = '2026-10-05T17:00:00Z' WHERE id = ?`, "",
			func(err error) bool { return err == nil }, "default|in_progress|one|2026-10-05T17:00:00Z", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			addProfile(t, db, "p2")
			id := mustAdd(t, s, "default", "contested", true).ID
			if c.held != "" {
				claimsClaim(t, s, id, c.held, time.Hour)
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

// Only a renewal by the holder keeps the later of two lease ends. Any other claim, a takeover of a stale
// claim or a claim of a Ready task, ends exactly one lease after now, whatever lease the row still holds:
// the one the claim it takes over had, or the stray one that a Ready row may carry, ending far off, under
// another name or under the claimant's own.
func TestAClaimThatIsNoRenewalEndsExactlyOneLeaseAfterNow(t *testing.T) {
	const lease = 2 * time.Hour
	for _, c := range []struct {
		name string
		prep func(t *testing.T, s *Store, db *sql.DB, clk *clock) int64 // makes the task and returns its id
	}{
		{"a takeover of a stale claim", func(t *testing.T, s *Store, db *sql.DB, clk *clock) int64 {
			id := mustAdd(t, s, "default", "abandoned", true).ID
			claimsClaim(t, s, id, "one", 10*time.Minute)
			clk.advance(20 * time.Minute)
			return id
		}},
		{"a Ready task with nothing left on it", func(t *testing.T, s *Store, db *sql.DB, clk *clock) int64 {
			return mustAdd(t, s, "default", "plain", true).ID
		}},
		{"a Ready task with a claim of another name left on it, ending far off", func(t *testing.T, s *Store, db *sql.DB, clk *clock) int64 {
			id := mustAdd(t, s, "default", "stray", true).ID
			claimsStray(t, db, id, "ghost", clk.t.Add(10*time.Hour))
			return id
		}},
		{"a Ready task with a claim of the claimant's own name left on it, ending far off", func(t *testing.T, s *Store, db *sql.DB, clk *clock) int64 {
			id := mustAdd(t, s, "default", "stray", true).ID
			claimsStray(t, db, id, "two", clk.t.Add(10*time.Hour))
			return id
		}},
	} {
		for _, via := range []string{"claim", "next --claim"} {
			t.Run(c.name+", by "+via, func(t *testing.T) {
				s, db, clk := newTestStore(t)
				id := c.prep(t, s, db, clk)
				var got *Task
				var err error
				if via == "claim" {
					var task Task
					task, err = s.Claim(bg, "default", id, bot("two"), lease)
					got = &task
				} else {
					got, err = s.Next(bg, "default", bot("two"), true, lease)
				}
				if err != nil || got == nil || got.ID != id || got.Claim == nil || got.Claim.By != "two" {
					t.Fatalf("%s: %+v, %v, want the task taken by two", via, got, err)
				}
				if want := clk.t.Add(lease).Format(timeFmt); got.Claim.Until.UTC().Format(timeFmt) != want {
					t.Errorf("the task says the lease ends %v, want %s", got.Claim.Until, want)
				}
				if _, until := opsClaim(t, db, id); until != clk.t.Add(lease).Format(timeFmt) {
					t.Errorf("the lease is stored as ending %q, want one lease after now, %q", until, clk.t.Add(lease).Format(timeFmt))
				}
			})
		}
	}
}

// The UPDATE of an agent's comment renews the lease by itself, with the care of the UPDATE of a claim: it
// keeps the later of the end the row holds and half an hour from now, and only on a row that the agent
// holds, whatever the comment read. The task is changed through the transaction's connection between the
// comment's read of it and its UPDATE, and the row the UPDATE leaves is what is looked at.
func TestTheUpdateOfACommentNeverShortensALeaseNorTouchesOneItDoesNotHold(t *testing.T) {
	for _, c := range []struct {
		name   string
		before string // the change made to the task after it was read, with its id as the one argument
		row    string // the row afterwards, in the transaction
	}{
		{"a lease made longer after the comment read the task", `UPDATE tasks SET claim_until = '2026-10-05T17:00:00Z' WHERE id = ?`,
			"default|in_progress|one|2026-10-05T17:00:00Z"},
		{"a task that another agent took over after it was read", `UPDATE tasks SET claimed_by = 'other', claim_until = '2026-10-05T11:00:00Z' WHERE id = ?`,
			"default|in_progress|other|2026-10-05T11:00:00Z"},
		{"a card moved to Review after it was read, with a claim left on it", `UPDATE tasks SET status = 'review', claim_until = '2026-10-05T12:10:00Z' WHERE id = ?`,
			"default|review|one|2026-10-05T12:10:00Z"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, _ := newTestStore(t)
			id := mustAdd(t, s, "default", "held", true).ID
			claimsClaim(t, s, id, "one", time.Hour) // until 13:00, longer than the half hour that a comment renews for
			var row string
			err := s.tx(bg, func(x dbx) error {
				m := &meddler{dbx: x, prefix: "UPDATE tasks SET claim_until", before: func(x dbx) {
					if _, e := x.ExecContext(bg, c.before, id); e != nil {
						t.Error(e)
					}
				}}
				if _, err := s.commentTx(bg, m, "default", id, "going on", bot("one")); err != nil {
					t.Errorf("the comment: %v", err)
				}
				if e := x.QueryRowContext(bg, `SELECT profile_id || '|' || status || '|' || claimed_by || '|' || claim_until FROM tasks WHERE id = ?`, id).Scan(&row); e != nil {
					t.Error(e)
				}
				return errors.New("undo")
			})
			if err == nil || err.Error() != "undo" {
				t.Fatalf("the transaction: %v", err)
			}
			if row != c.row {
				t.Errorf("the row is %q, want %q", row, c.row)
			}
		})
	}
}
