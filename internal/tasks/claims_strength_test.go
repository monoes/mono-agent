package tasks

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// A card the operator moved to In progress by hand has no claim. An agent that comments on it, finishes it
// or releases it is refused as not its claimant, and nothing is written: heldBy must not read a claim that
// is not there (without the nil check the call dereferences it and the CLI dies with a stack trace).
func TestAnAgentIsNotTheClaimantOfACardTheOperatorWorksOn(t *testing.T) {
	s, db, _ := newTestStore(t)
	card := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", card.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	before := dumpBoard(t, db)
	if _, err := s.Comment(bg, "default", card.ID, "x", bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a comment by an agent: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Finish(bg, "default", card.ID, Outcome{Result: "x"}, bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a finish by an agent: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Release(bg, "default", card.ID, "x", bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a release by an agent: %v, want ErrNotClaimant", err)
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := s.Comment(bg, "default", card.ID, "from the operator", human); err != nil {
		t.Errorf("the operator's comment on its own card: %v", err)
	}
}

// A comment is refused for who asks and for which task before it is refused for the size of a history: a
// task of another profile is not found, and a task another agent holds is not the asker's, whatever number
// of events they hold. Only the task's own holder and the operator, who may comment on it, are told the
// limit. (Else the limit would say that a task of another profile exists and how long its history is.)
func TestACommentIsRefusedForItsAuthorAndItsTaskBeforeItsSize(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	theirs := mustAdd(t, s, other, "theirs", true)
	if _, err := s.Claim(bg, other, theirs.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	claimsSeedEvents(t, db, theirs.ID, MaxEventsPerTask+10)
	mine := mustAdd(t, s, "default", "mine", true)
	claimsClaim(t, s, mine.ID, "one", 0)
	claimsSeedEvents(t, db, mine.ID, MaxEventsPerTask)
	before := dumpBoard(t, db)

	for _, a := range []Actor{bot("one"), bot("two"), human} {
		if _, err := s.Comment(bg, "default", theirs.ID, "x", a); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrLimit) {
			t.Errorf("a comment by %+v on a task of another profile that holds %d events: %v, want ErrNotFound", a, MaxEventsPerTask+10, err)
		}
	}
	if _, err := s.Comment(bg, "default", mine.ID, "x", bot("two")); !errors.Is(err, ErrNotClaimant) || errors.Is(err, ErrLimit) {
		t.Errorf("a comment by an agent that does not hold a task of %d events: %v, want ErrNotClaimant", MaxEventsPerTask, err)
	}
	for _, a := range []Actor{bot("one"), human} {
		if _, err := s.Comment(bg, "default", mine.ID, "x", a); !errors.Is(err, ErrLimit) {
			t.Errorf("a comment by %+v on a task of %d events: %v, want ErrLimit", a, MaxEventsPerTask, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// claimsTexts are the five texts an agent or the operator writes, each as a call that writes one on a task
// that "one" holds.
func claimsTexts() []struct {
	name string
	call func(s *Store, id int64, text string) error
} {
	return []struct {
		name string
		call func(s *Store, id int64, text string) error
	}{
		{"a comment by an agent", func(s *Store, id int64, text string) error {
			_, err := s.Comment(bg, "default", id, text, bot("one"))
			return err
		}},
		{"a comment by the operator", func(s *Store, id int64, text string) error {
			_, err := s.Comment(bg, "default", id, text, human)
			return err
		}},
		{"a result", func(s *Store, id int64, text string) error {
			_, err := s.Finish(bg, "default", id, Outcome{Result: text}, bot("one"))
			return err
		}},
		{"a question", func(s *Store, id int64, text string) error {
			_, err := s.Finish(bg, "default", id, Outcome{Question: text}, bot("one"))
			return err
		}},
		{"a release note", func(s *Store, id int64, text string) error {
			_, err := s.Release(bg, "default", id, text, bot("one"))
			return err
		}},
	}
}

// What an agent writes keeps its lines (a result or a question is several lines as often as one), and is cut
// only past the size of a comment, to exactly that size: a text of 8192 bytes is stored whole, one of 8193
// is stored as 8192 bytes that end in the notice.
func TestAWrittenTextKeepsItsLinesAndIsCutOnlyPastTheLimit(t *testing.T) {
	multi := "first line\n\tindented second line\n\nthird, after a blank line\n- item\n- item"
	exact := strings.Repeat("x", MaxCommentBytes)
	over := strings.Repeat("x", MaxCommentBytes+1)
	for _, k := range claimsTexts() {
		t.Run(k.name, func(t *testing.T) {
			s, _, _ := newTestStore(t)
			noteOf := func(id int64) string {
				t.Helper()
				ev := opsEvents(t, s, id)
				return strings.SplitN(ev[len(ev)-1], "|", 4)[3]
			}
			held := func(title string) int64 {
				t.Helper()
				id := mustAdd(t, s, "default", title, true).ID
				claimsClaim(t, s, id, "one", 0)
				return id
			}
			id := held("several lines")
			if err := k.call(s, id, multi); err != nil {
				t.Fatal(err)
			}
			if got := noteOf(id); got != multi {
				t.Errorf("stored %q, want the lines kept: %q", got, multi)
			}
			id = held("exactly the limit")
			if err := k.call(s, id, exact); err != nil {
				t.Fatal(err)
			}
			if got := noteOf(id); got != exact {
				t.Errorf("a text of %d bytes was stored as %d bytes: %.40q...", len(exact), len(got), got)
			}
			id = held("one byte over")
			if err := k.call(s, id, over); err != nil {
				t.Fatal(err)
			}
			if got := noteOf(id); len(got) != MaxCommentBytes || !strings.Contains(got, "[truncated: 8193 characters in the original]") || !strings.HasPrefix(got, "xxxx") {
				t.Errorf("a text of %d bytes was stored as %d bytes, ending %q", len(over), len(got), got[max(0, len(got)-60):])
			}
		})
	}
}

// A card goes to the end of a column one gap past the card that is there. Where that card leaves no room (a
// row written by hand, at the end of the int64 range), a claim, a finish and a release refuse with the plain
// "out of range" error and write nothing: an error that was swallowed would put the card at place 0, in the
// middle of the column, and go on to write its event.
func TestAClaimAFinishAndAReleaseRefuseWhereTheColumnLeavesNoRoomForTheCard(t *testing.T) {
	for _, c := range []struct {
		name   string
		column string
		pos    int64
		run    func(s *Store, id int64) error
	}{
		{"a claim, into In progress", "in_progress", math.MaxInt64 - 100, func(s *Store, id int64) error {
			_, err := s.Claim(bg, "default", id, bot("one"), 0)
			return err
		}},
		{"a next --claim, into In progress", "in_progress", math.MaxInt64 - 100, func(s *Store, id int64) error {
			_, err := s.Next(bg, "default", bot("one"), true, 0)
			return err
		}},
		{"a finish, into the top of Review", "review", math.MinInt64 + 100, func(s *Store, id int64) error {
			_, err := s.Finish(bg, "default", id, Outcome{Result: "r"}, bot("one"))
			return err
		}},
		{"a release, into the bottom of Ready", "ready", math.MaxInt64 - 100, func(s *Store, id int64) error {
			_, err := s.Release(bg, "default", id, "n", bot("one"))
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			id := mustAdd(t, s, "default", "the card", true).ID
			if c.column != "in_progress" { // the card has to be held to be finished or released
				claimsClaim(t, s, id, "one", 0)
			}
			seedAt(t, db, "default", c.column, c.pos)
			before := dumpBoard(t, db)
			err := c.run(s, id)
			if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("the call: %v, want a plain error saying the positions are out of range", err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Errorf("a refused call changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// When the UPDATE of the task itself is the write that fails, the call fails with that error and nothing is
// kept: not an event for a change that was not made, nor a revision for it. (The other two writes of a verb,
// the event and the revision, are refused in TestEveryAgentVerbWritesTheChangeTheEventAndTheRevisionOrNone.)
func TestEveryAgentVerbWritesNothingWhenTheUpdateOfTheTaskFails(t *testing.T) {
	held := func(t *testing.T, s *Store) int64 {
		id := mustAdd(t, s, "default", "a", true).ID
		claimsClaim(t, s, id, "one", 0)
		return id
	}
	for _, sc := range []struct {
		name string
		prep func(t *testing.T, s *Store, clk *clock) func() error
	}{
		{"Claim of a Ready task", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			return func() error { _, err := s.Claim(bg, "default", id, bot("one"), 0); return err }
		}},
		{"Claim by the holder", func(t *testing.T, s *Store, clk *clock) func() error {
			id := held(t, s)
			return func() error { _, err := s.Claim(bg, "default", id, bot("one"), time.Hour); return err }
		}},
		{"Claim of a stale claim", func(t *testing.T, s *Store, clk *clock) func() error {
			id := held(t, s)
			clk.advance(DefaultLease)
			return func() error { _, err := s.Claim(bg, "default", id, bot("two"), 0); return err }
		}},
		{"Next with claim", func(t *testing.T, s *Store, clk *clock) func() error {
			mustAdd(t, s, "default", "a", true)
			return func() error { _, err := s.Next(bg, "default", bot("one"), true, 0); return err }
		}},
		{"Comment by an agent", func(t *testing.T, s *Store, clk *clock) func() error {
			id := held(t, s)
			return func() error { _, err := s.Comment(bg, "default", id, "text", bot("one")); return err }
		}},
		{"Comment by the operator", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			return func() error { _, err := s.Comment(bg, "default", id, "text", human); return err }
		}},
		{"Finish", func(t *testing.T, s *Store, clk *clock) func() error {
			id := held(t, s)
			return func() error { _, err := s.Finish(bg, "default", id, Outcome{Result: "r"}, bot("one")); return err }
		}},
		{"Release", func(t *testing.T, s *Store, clk *clock) func() error {
			id := held(t, s)
			return func() error { _, err := s.Release(bg, "default", id, "no", bot("one")); return err }
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			s, db, clk := newTestStore(t)
			call := sc.prep(t, s, clk)
			before := dumpBoard(t, db)
			if _, err := db.Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`); err != nil {
				t.Fatal(err)
			}
			if err := call(); err == nil || !strings.Contains(err.Error(), "refused for the test") {
				t.Fatalf("the call: %v, want the refusal of the trigger", err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Fatalf("a failed call left changes behind:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			dropRefusals(t, db)
			if err := call(); err != nil {
				t.Errorf("the same call once the obstacle is gone: %v", err)
			}
		})
	}
}

// A row the store cannot read (a time that is not a time, left there by something else) stops every verb
// that reads it with that error: never an empty task and no error, and never a revision or an event written
// for a task that was not read. The head of Ready is broken here, so a peek, a next --claim and a claim of
// it all meet it, and the claim of the holder is broken, so each verb of the holder meets that.
func TestARowThatCannotBeReadStopsTheVerbWithAnErrorAndNothingIsWritten(t *testing.T) {
	s, db, _ := newTestStore(t)
	head := mustAdd(t, s, "default", "broken head of Ready", true)
	held := mustAdd(t, s, "default", "broken claim", true)
	claimsClaim(t, s, held.ID, "one", 0)
	if _, err := db.Exec(`UPDATE tasks SET created_at = 'not a time' WHERE id = ?`, head.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET claim_until = 'not a time' WHERE id = ?`, held.ID); err != nil {
		t.Fatal(err)
	}
	before := dumpBoard(t, db)
	for _, c := range []struct {
		name string
		run  func() (any, error)
	}{
		{"a peek", func() (any, error) { got, err := s.Next(bg, "default", bot("two"), false, 0); return got, err }},
		{"next --claim", func() (any, error) { got, err := s.Next(bg, "default", bot("two"), true, 0); return got, err }},
		{"a claim of the head of Ready", func() (any, error) { got, err := s.Claim(bg, "default", head.ID, bot("two"), 0); return got, err }},
		{"a renewal by the holder", func() (any, error) { got, err := s.Claim(bg, "default", held.ID, bot("one"), 0); return got, err }},
		{"a comment by the holder", func() (any, error) { got, err := s.Comment(bg, "default", held.ID, "x", bot("one")); return got, err }},
		{"a comment by the operator", func() (any, error) { got, err := s.Comment(bg, "default", held.ID, "x", human); return got, err }},
		{"a finish by the holder", func() (any, error) {
			got, err := s.Finish(bg, "default", held.ID, Outcome{Result: "r"}, bot("one"))
			return got, err
		}},
		{"a release by the holder", func() (any, error) { got, err := s.Release(bg, "default", held.ID, "n", bot("one")); return got, err }},
	} {
		got, err := c.run()
		if err == nil || !strings.Contains(err.Error(), "not a time") || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %+v, %v, want the error that the stored time is not a time", c.name, got, err)
		}
		if after := dumpBoard(t, db); after != before {
			t.Errorf("%s changed the database:\nbefore:\n%s\nafter:\n%s", c.name, before, after)
		}
	}
}

// The refusal for the size of a history is for a history of MaxEventsToClaim events, and for no shorter one: a
// claim whose UPDATE took nothing though the rules allow it (the task is changed under it and changed back
// here) is a plain error for a task of 600 events, which is long for a comment and not yet too long to claim.
func TestAClaimTheUpdateRefusedIsTheCapOnlyFromTwoThousandEvents(t *testing.T) {
	s, db, _ := newTestStore(t)
	id := mustAdd(t, s, "default", "contested", true).ID
	claimsSeedEvents(t, db, id, MaxEventsPerTask+100)
	_, _, err := claimMeddled(t, s, id, "one", `UPDATE tasks SET status = 'review' WHERE id = ?`, `UPDATE tasks SET status = 'ready' WHERE id = ?`)
	if err == nil || errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "not taken") {
		t.Errorf("the claim says %v, want the plain error that a task the rules allow was not taken", err)
	}
}

// Every verb gives its connection back, whatever it ends in: a success, a refusal, an error. A pool run dry
// by connections that were not given back stops every other verb, and the database with it.
func TestEveryAgentVerbGivesItsConnectionBack(t *testing.T) {
	s, db, _ := newTestStore(t)
	ready := mustAdd(t, s, "default", "ready", true)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"a claim", func() error { _, err := s.Claim(bg, "default", ready.ID, bot("two"), 0); return err }},
		{"a refused claim", func() error { _, err := s.Claim(bg, "default", held.ID, bot("two"), 0); return err }},
		{"a claim of a task that is not there", func() error { _, err := s.Claim(bg, "default", 424242, bot("two"), 0); return err }},
		{"a claim by a name that is refused", func() error { _, err := s.Claim(bg, "default", held.ID, bot("you"), 0); return err }},
		{"next --claim with nothing to take", func() error { _, err := s.Next(bg, "default", bot("two"), true, 0); return err }},
		{"a peek", func() error { _, err := s.Next(bg, "default", bot("two"), false, 0); return err }},
		{"a peek of a profile that is not there", func() error { _, err := s.Next(bg, "nowhere", bot("two"), false, 0); return err }},
		{"a comment", func() error { _, err := s.Comment(bg, "default", held.ID, "x", bot("one")); return err }},
		{"a refused comment", func() error { _, err := s.Comment(bg, "default", held.ID, "x", bot("two")); return err }},
		{"a finish", func() error { _, err := s.Finish(bg, "default", held.ID, Outcome{Result: "r"}, bot("one")); return err }},
		{"a refused finish", func() error { _, err := s.Finish(bg, "default", held.ID, Outcome{Result: "r"}, bot("one")); return err }},
		{"a release", func() error { _, err := s.Release(bg, "default", ready.ID, "n", bot("two")); return err }},
	} {
		_ = c.call()
		if n := db.Stats().InUse; n != 0 {
			t.Errorf("after %s: %d connections are still in use", c.name, n)
		}
	}
}

// failingQueries is the transaction's connection with the queries that hold match turned into one that
// fails, as a database that cannot read would.
type failingQueries struct {
	dbx
	match string
}

func (f failingQueries) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, f.match) {
		return f.dbx.QueryRowContext(ctx, `SELECT id FROM no_such_table`)
	}
	return f.dbx.QueryRowContext(ctx, q, args...)
}

// The count of a task's events says so when it cannot be made, and the explanation of a claim that was
// refused passes that failure on: a task the rules allow whose events could not be counted is not "a task the
// rules allow that was not taken". (Only the count fails here: the reads of the same table around it work.)
func TestTheCountOfEventsSaysSoWhenItFails(t *testing.T) {
	s, _, clk := newTestStore(t)
	id := mustAdd(t, s, "default", "a", true).ID
	err := s.tx(bg, func(x dbx) error {
		failing := failingQueries{x, "COUNT(*) FROM task_events"}
		if n, err := s.eventCount(bg, failing, id); err == nil {
			t.Errorf("eventCount with a failing query: %d and no error", n)
		}
		if err := s.claimRefusal(bg, failing, "default", id, "one", clk.t); err == nil || !strings.Contains(err.Error(), "no such table") {
			t.Errorf("the explanation of a claim, with a failing count: %v, want the failure of the count", err)
		}
		return errors.New("undo")
	})
	if err == nil || err.Error() != "undo" {
		t.Fatalf("the transaction: %v", err)
	}
}

// countlessResult is the result of a write that cannot say how many rows it changed.
type countlessResult struct{ sql.Result }

func (countlessResult) RowsAffected() (int64, error) { return 0, errors.New("no count of rows") }

// countlessUpdates is the transaction's connection whose UPDATE of a claim has a result that cannot be counted.
type countlessUpdates struct{ dbx }

func (c countlessUpdates) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	res, err := c.dbx.ExecContext(ctx, q, args...)
	if err == nil && strings.HasPrefix(strings.TrimSpace(q), "UPDATE tasks SET status = 'in_progress'") {
		return countlessResult{res}, nil
	}
	return res, err
}

// A claim whose UPDATE cannot say how many rows it took says so: it does not read the silence as "none" and
// go on to explain a refusal that did not happen (the task was taken), and the transaction keeps nothing.
func TestAClaimSaysSoWhenTheUpdateCannotCountWhatItTook(t *testing.T) {
	s, db, _ := newTestStore(t)
	id := mustAdd(t, s, "default", "a", true).ID
	before := dumpBoard(t, db)
	err := s.tx(bg, func(x dbx) error {
		if _, err := s.claimTx(bg, countlessUpdates{x}, "default", id, bot("one"), s.now().UTC(), 0); err == nil || !strings.Contains(err.Error(), "no count of rows") {
			t.Errorf("the claim says %v, want the failure of the count", err)
		}
		return errors.New("undo")
	})
	if err == nil || err.Error() != "undo" {
		t.Fatalf("the transaction: %v", err)
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("an undone claim changed the database")
	}
}

// pickNext says so when one of its two queries cannot be read: a failed look for a stale claim is not
// "nothing to do", which would send an agent away from work that is there.
func TestPickNextSaysSoWhenAQueryFails(t *testing.T) {
	s, _, clk := newTestStore(t)
	mustAdd(t, s, "default", "ready", true)
	stale := mustAdd(t, s, "default", "abandoned", true)
	claimsClaim(t, s, stale.ID, "one", 0)
	clk.advance(DefaultLease)
	for _, match := range []string{"status = 'ready'", "status = 'in_progress'"} {
		// each query is reached on its own: the Ready one first, the stale one when no Ready task is left
		err := s.tx(bg, func(x dbx) error {
			if match == "status = 'in_progress'" {
				if _, err := x.ExecContext(bg, `UPDATE tasks SET status = 'review' WHERE status = 'ready'`); err != nil {
					t.Fatal(err)
				}
			}
			id, err := s.pickNext(bg, failingQueries{x, match}, "default", clk.t)
			if err == nil {
				t.Errorf("pickNext with a failing query on %s: %d and no error", match, id)
			}
			return errors.New("undo")
		})
		if err == nil || err.Error() != "undo" {
			t.Fatalf("the transaction: %v", err)
		}
	}
}
