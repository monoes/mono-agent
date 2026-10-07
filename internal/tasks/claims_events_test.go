package tasks

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// What each verb writes in the history of the task, as spec 4.4 names the kinds, dated by the store's
// clock; and the row is dated by it too.
func TestEachAgentVerbWritesTheEventTheSpecNames(t *testing.T) {
	s, db, clk := newTestStore(t)
	a, b, c, d := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true), mustAdd(t, s, "default", "d", true)
	do := func(id int64, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		got, events, err := s.Get(bg, "default", id)
		if err != nil || !got.UpdatedAt.Equal(clk.t) || !events[len(events)-1].At.Equal(clk.t) {
			t.Errorf("after the verb at %v: row updated at %v, last event at %v, err %v", clk.t, got.UpdatedAt, events[len(events)-1].At, err)
		}
		clk.advance(time.Minute)
	}
	clk.advance(time.Minute)
	_, err := s.Claim(bg, "default", a.ID, bot("one"), 0)
	do(a.ID, err)
	_, err = s.Claim(bg, "default", a.ID, bot("one"), time.Hour)
	do(a.ID, err)
	_, err = s.Comment(bg, "default", a.ID, "progress", bot("one"))
	do(a.ID, err)
	_, err = s.Comment(bg, "default", a.ID, "from the operator", human)
	do(a.ID, err)
	clk.advance(2 * time.Hour) // the lease has run out
	_, err = s.Claim(bg, "default", a.ID, bot("two"), 0)
	do(a.ID, err)
	_, err = s.Finish(bg, "default", a.ID, Outcome{Result: "all done"}, bot("two"))
	do(a.ID, err)

	_, err = s.Next(bg, "default", bot("one"), true, 0) // b: the top of Ready now
	do(b.ID, err)
	_, err = s.Finish(bg, "default", b.ID, Outcome{Question: "which database?"}, bot("one"))
	do(b.ID, err)

	_, err = s.Claim(bg, "default", c.ID, bot("one"), 0)
	do(c.ID, err)
	_, err = s.Release(bg, "default", c.ID, "needs the VPN", bot("one"))
	do(c.ID, err)

	_, err = s.Claim(bg, "default", d.ID, bot("one"), 0)
	do(d.ID, err)
	_, err = s.Release(bg, "default", d.ID, "", bot("one"))
	do(d.ID, err)

	for id, want := range map[int64][]string{
		a.ID: {
			"created|you|>ready|",
			"claimed|one|ready>in_progress|",
			"claimed|one|in_progress>in_progress|renewed",
			"comment|one|>|progress",
			"comment|you|>|from the operator",
			"reclaimed|two|in_progress>in_progress|the claim of one had expired",
			"result|two|in_progress>review|all done",
		},
		b.ID: {"created|you|>ready|", "claimed|one|ready>in_progress|", "question|one|in_progress>review|which database?"},
		c.ID: {"created|you|>ready|", "claimed|one|ready>in_progress|", "released|one|in_progress>ready|needs the VPN"},
		d.ID: {"created|you|>ready|", "claimed|one|ready>in_progress|", "released|one|in_progress>ready|"},
	} {
		if got := opsEvents(t, s, id); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("task #%d events:\n%s\nwant:\n%s", id, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		// a finished or released task is held by nobody: both claim columns of the row are empty
		if by, until := opsClaim(t, db, id); by != "" || until != "" {
			t.Errorf("task #%d: claim columns %q and %q after it was handed back, want both empty", id, by, until)
		}
	}
}

// What a comment, a result, a question and a note are made of: cleaned of what a terminal or a reader
// should not get, and cut to a comment's size, a cut that says so. Each text an agent or the operator
// writes goes through both.
func TestWhatIsWrittenIsCleanedAndCut(t *testing.T) {
	dirty := "red \x1b[31mtext\U0000202e\U000E0041\xff end"
	clean := "red [31mtext\U0000fffd end"
	huge := strings.Repeat("x", MaxCommentBytes+500)
	for _, k := range []struct {
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
	} {
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
			id := held("dirty")
			if err := k.call(s, id, dirty); err != nil {
				t.Fatal(err)
			}
			if got := noteOf(id); got != clean {
				t.Errorf("stored %q, want %q", got, clean)
			}
			id = held("huge")
			if err := k.call(s, id, huge); err != nil {
				t.Fatal(err)
			}
			if got := noteOf(id); len(got) > MaxCommentBytes || !strings.Contains(got, "[truncated:") || !strings.HasPrefix(got, "xxxx") {
				t.Errorf("%d bytes were stored as %d bytes, %q at the end", len(huge), len(got), got[max(0, len(got)-60):])
			}
		})
	}
}

// Spec 5.4: one revision for each write transaction (not one for each event), none for a call that was
// refused or had nothing to do. A call that is refused ends in the refusal the rules name for it.
func TestEveryAgentVerbMovesTheRevisionOnceAndARefusalNotAtAll(t *testing.T) {
	s, _, clk := newTestStore(t)
	ready := []int64{mustAdd(t, s, "default", "a", true).ID, mustAdd(t, s, "default", "b", true).ID, mustAdd(t, s, "default", "c", true).ID, mustAdd(t, s, "default", "d", true).ID}
	a, b, c, d := ready[0], ready[1], ready[2], ready[3]
	for _, k := range []struct {
		name    string
		want    int64  // how far the revision moves
		refused error  // the refusal the call ends in, nil for a call that goes through
		prep    func() // what the case needs first, which is not measured
		call    func() error
	}{
		{"a claim", 1, nil, nil, func() error { _, err := s.Claim(bg, "default", a, bot("one"), 0); return err }},
		{"a renewal", 1, nil, nil, func() error { _, err := s.Claim(bg, "default", a, bot("one"), time.Hour); return err }},
		{"a refused claim", 0, ErrClaimed, nil, func() error { _, err := s.Claim(bg, "default", a, bot("two"), 0); return err }},
		{"next --claim", 1, nil, nil, func() error { _, err := s.Next(bg, "default", bot("one"), true, 0); return err }},
		{"a peek", 0, nil, nil, func() error { _, err := s.Next(bg, "default", bot("one"), false, 0); return err }},
		{"a comment by the holder", 1, nil, nil, func() error { _, err := s.Comment(bg, "default", a, "x", bot("one")); return err }},
		{"a comment by the operator", 1, nil, nil, func() error { _, err := s.Comment(bg, "default", a, "x", human); return err }},
		{"a comment by another agent", 0, ErrNotClaimant, nil, func() error { _, err := s.Comment(bg, "default", a, "x", bot("two")); return err }},
		{"an empty comment", 0, ErrInvalid, nil, func() error { _, err := s.Comment(bg, "default", a, " ", bot("one")); return err }},
		{"a finish", 1, nil, nil, func() error { _, err := s.Finish(bg, "default", a, Outcome{Result: "r"}, bot("one")); return err }},
		{"a finish of a task that is in review", 0, ErrNotClaimant, nil, func() error {
			_, err := s.Finish(bg, "default", a, Outcome{Result: "r"}, bot("one"))
			return err
		}},
		{"a finish with both a result and a question", 0, ErrInvalid, nil, func() error {
			_, err := s.Finish(bg, "default", b, Outcome{Result: "r", Question: "q"}, bot("one"))
			return err
		}},
		{"a release", 1, nil, nil, func() error { _, err := s.Release(bg, "default", b, "no", bot("one")); return err }},
		{"a release by another agent", 0, ErrNotClaimant, nil, func() error { _, err := s.Release(bg, "default", b, "no", bot("two")); return err }},
		{"next --claim with nothing to take", 0, nil, func() { // the Ready column is empty
			for _, id := range []int64{b, c, d} {
				claimsClaim(t, s, id, "three", 0)
			}
		}, func() error {
			got, err := s.Next(bg, "default", bot("four"), true, 0)
			if err == nil && got != nil {
				return errors.New("next took " + got.Title)
			}
			return err
		}},
		{"a takeover by next --claim", 1, nil, func() { clk.advance(2 * time.Hour) }, func() error {
			got, err := s.Next(bg, "default", bot("four"), true, 0)
			if err == nil && (got == nil || got.LastEvent == nil || got.LastEvent.Kind != "reclaimed") {
				return errors.New("next took nothing, or did not take a stale claim")
			}
			return err
		}},
	} {
		if k.prep != nil {
			k.prep()
		}
		before := claimsRev(t, s)
		err := k.call()
		if got := claimsRev(t, s) - before; got != k.want {
			t.Errorf("%s: the revision moved by %d (err %v), want %d", k.name, got, err, k.want)
		}
		if k.refused == nil && err != nil || k.refused != nil && !errors.Is(err, k.refused) {
			t.Errorf("%s: err %v, want %v", k.name, err, k.refused)
		}
	}
}

// A change, its event and the revision are one transaction: when either cannot be written, nothing is,
// and the same call works once the obstacle is gone.
func TestEveryAgentVerbWritesTheChangeTheEventAndTheRevisionOrNone(t *testing.T) {
	scenarios := []struct {
		name string
		prep func(t *testing.T, s *Store, clk *clock) func() error
	}{
		{"Claim of a Ready task", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			return func() error { _, err := s.Claim(bg, "default", id, bot("one"), 0); return err }
		}},
		{"Claim by the holder", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			claimsClaim(t, s, id, "one", 0)
			return func() error { _, err := s.Claim(bg, "default", id, bot("one"), time.Hour); return err }
		}},
		{"Claim of a stale claim", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			claimsClaim(t, s, id, "one", 0)
			clk.advance(DefaultLease)
			return func() error { _, err := s.Claim(bg, "default", id, bot("two"), 0); return err }
		}},
		{"Next with claim", func(t *testing.T, s *Store, clk *clock) func() error {
			mustAdd(t, s, "default", "a", true)
			return func() error { _, err := s.Next(bg, "default", bot("one"), true, 0); return err }
		}},
		{"Comment by an agent", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			claimsClaim(t, s, id, "one", 0)
			return func() error { _, err := s.Comment(bg, "default", id, "text", bot("one")); return err }
		}},
		{"Comment by the operator", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			return func() error { _, err := s.Comment(bg, "default", id, "text", human); return err }
		}},
		{"Finish", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			claimsClaim(t, s, id, "one", 0)
			return func() error { _, err := s.Finish(bg, "default", id, Outcome{Result: "r"}, bot("one")); return err }
		}},
		{"Release", func(t *testing.T, s *Store, clk *clock) func() error {
			id := mustAdd(t, s, "default", "a", true).ID
			claimsClaim(t, s, id, "one", 0)
			return func() error { _, err := s.Release(bg, "default", id, "no", bot("one")); return err }
		}},
	}
	for _, sc := range scenarios {
		for _, fail := range []string{"the event", "the revision"} {
			t.Run(sc.name+", refusing "+fail, func(t *testing.T) {
				s, db, clk := newTestStore(t)
				call := sc.prep(t, s, clk)
				before := dumpBoard(t, db)
				stmts := []string{`CREATE TRIGGER refuse BEFORE INSERT ON task_events BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`}
				if fail == "the revision" {
					stmts = []string{
						`CREATE TRIGGER refuse BEFORE INSERT ON task_board_rev BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`,
						`CREATE TRIGGER refuse_update BEFORE UPDATE ON task_board_rev BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`,
					}
				}
				for _, q := range stmts {
					if _, err := db.Exec(q); err != nil {
						t.Fatal(err)
					}
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
}

func dropRefusals(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{"refuse", "refuse_update"} {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
}
