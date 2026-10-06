package tasks

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// notTheOperator is every way a caller can be somebody other than the operator.
var notTheOperator = []struct {
	name string
	a    Actor
}{
	{"a named agent", bot("bob")},
	{"an unnamed agent", Actor{Kind: Agent}},
	{"the chrome capture", Actor{Kind: Capture, Name: SourceChrome}},
	{"the os capture", Actor{Kind: Capture, Name: SourceOS}},
	{"an unnamed capture", Actor{Kind: Capture}},
	{"the zero actor", Actor{}},
	{"an unknown kind", Actor{Kind: ActorKind(99)}},
	{"a negative kind", Actor{Kind: ActorKind(-1)}},
}

// Spec 5.1: the six verbs are the operator's. The CLI and MCP rely on the store for it, so it is the
// first thing each verb looks at: before the profile, the task, and what is asked.
func TestEveryVerbRefusesEveryoneButTheOperatorBeforeAnythingElse(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	before := dumpBoard(t, db)
	for _, v := range opsVerbs(s) {
		for _, k := range notTheOperator {
			if err := v.run("default", task.ID, k.a, false); !errors.Is(err, ErrOperatorOnly) {
				t.Errorf("%s by %s: %v, want ErrOperatorOnly", v.name, k.name, err)
			}
			// an unknown profile, an unknown task and a request that is refused whoever makes it
			if err := v.run("no-such-profile", 424242, k.a, true); !errors.Is(err, ErrOperatorOnly) {
				t.Errorf("%s by %s, with nothing else right: %v, want ErrOperatorOnly first", v.name, k.name, err)
			}
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The operator is the one kind, whatever name it carries: its events are written as "you".
func TestTheOperatorIsLabelledYouWhateverItsName(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	named := Actor{Kind: Human, Name: "bob"}
	if _, err := s.Move(bg, "default", task.ID, StatusReview, Placement{}, named); err != nil {
		t.Fatal(err)
	}
	if ev := opsEvents(t, s, task.ID); ev[len(ev)-1] != "moved|you|inbox>review|" {
		t.Errorf("events: %v", ev)
	}
}

// Spec 14: an unknown profile is refused by every method that writes, before the task is looked
// for (the id here is a real one of another profile: ErrNotFound would be the wrong answer), and
// what the refusal repeats of the profile id is cut.
func TestEveryVerbRefusesAnUnknownProfileAndWritesNothing(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	before := dumpBoard(t, db)
	for _, v := range opsVerbs(s) {
		for _, profile := range []string{"no-such-profile", "", strings.Repeat("x", 1<<16)} {
			err := v.run(profile, task.ID, human, false)
			if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
				t.Errorf("%s on profile %.20q: %v, want ErrInvalid (an unknown profile)", v.name, profile, err)
			} else if len(err.Error()) > 400 {
				t.Errorf("%s: an error of %d bytes repeats what the caller sent", v.name, len(err.Error()))
			}
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Spec 14: every method refuses another profile's ids, as not found.
func TestEveryVerbRefusesAnotherProfilesTasksAsNotFound(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	own := mustAdd(t, s, "default", "own", false)
	archived := mustAdd(t, s, "default", "archived", false)
	if _, err := s.Archive(bg, "default", []int64{archived.ID}, human); err != nil {
		t.Fatal(err)
	}
	theirs := mustAdd(t, s, other, "theirs", false)
	before := dumpBoard(t, db)
	for _, v := range opsVerbs(s) {
		if v.name == "ArchiveStatus" { // it names no task: it is checked below
			continue
		}
		for _, id := range []int64{own.ID, archived.ID} {
			if err := v.run(other, id, human, false); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s of #%d through the other profile: %v, want ErrNotFound", v.name, id, err)
			}
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// a bulk call with one id that is not the profile's changes none of the others
	if _, err := s.Archive(bg, "default", []int64{own.ID, theirs.ID}, human); !errors.Is(err, ErrNotFound) {
		t.Errorf("archiving a task of the profile and one of another: %v, want ErrNotFound", err)
	}
	if got, _, _ := s.Get(bg, "default", own.ID); got.Status != StatusInbox {
		t.Errorf("the profile's own task was archived by a call that failed: %s", got.Status)
	}
	// ArchiveStatus acts on the profile it is given and no other
	if n, err := s.ArchiveStatus(bg, other, StatusInbox, human); err != nil || n != 1 {
		t.Errorf("archive the other profile's Inbox: %d, %v, want its one card", n, err)
	}
	if got, _, _ := s.Get(bg, "default", own.ID); got.Status != StatusInbox {
		t.Errorf("ArchiveStatus of one profile archived a card of another: %s", got.Status)
	}
	if got, _, _ := s.Get(bg, other, theirs.ID); got.Status != StatusArchived {
		t.Errorf("the other profile's card: %s", got.Status)
	}
}

// A change, its events and the revision are one transaction: when any of them cannot be written,
// none is, whichever card of a bulk call it was, and the same call works once the obstacle is gone.
func TestEveryVerbWritesTheChangeTheEventsAndTheRevisionOrNone(t *testing.T) {
	three := func(t *testing.T, s *Store, archive bool) []int64 {
		t.Helper()
		var ids []int64
		for i := 0; i < 3; i++ {
			ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("c%d", i), false).ID)
		}
		if archive {
			if _, err := s.Archive(bg, "default", ids, human); err != nil {
				t.Fatal(err)
			}
		}
		return ids
	}
	scenarios := []struct {
		name     string
		events   int  // the events the call writes
		released bool // one of them is a released event
		prep     func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error
	}{
		{"Edit", 1, false, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			id, title := mustAdd(t, s, "default", "a", false).ID, "b"
			return func() error { _, err := s.Edit(bg, "default", id, Edit{Title: &title}, human); return err }
		}},
		{"Move of a held card", 2, true, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			id := opsHeld(t, s, db, c, "held", "bob", time.Hour).ID
			return func() error { _, err := s.Move(bg, "default", id, StatusReview, Placement{}, human); return err }
		}},
		{"Approve of three", 3, false, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			ids := three(t, s, false)
			return func() error { _, err := s.Approve(bg, "default", ids, false, human); return err }
		}},
		{"Archive of three", 3, false, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			ids := three(t, s, false)
			return func() error { _, err := s.Archive(bg, "default", ids, human); return err }
		}},
		{"ArchiveStatus of three", 3, false, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			three(t, s, false)
			return func() error { _, err := s.ArchiveStatus(bg, "default", StatusInbox, human); return err }
		}},
		{"Unarchive of three", 3, false, func(t *testing.T, s *Store, db *sql.DB, c *clock) func() error {
			ids := three(t, s, true)
			return func() error { _, err := s.Unarchive(bg, "default", ids, human); return err }
		}},
	}
	for _, sc := range scenarios {
		failures := []string{"the first event", "the last event", "the revision"}
		if sc.released {
			// refusing the released event alone: the move's own event after it would fail too, and hide a
			// released event whose failure was not passed on
			failures = append(failures, "the released event")
		}
		for _, fail := range failures {
			t.Run(sc.name+", refusing "+fail, func(t *testing.T) {
				s, db, c := newTestStore(t)
				call := sc.prep(t, s, db, c)
				before := dumpBoard(t, db)
				var maxEvent int
				if err := db.QueryRow(`SELECT MAX(id) FROM task_events`).Scan(&maxEvent); err != nil {
					t.Fatal(err)
				}
				var stmts []string
				switch fail {
				case "the first event":
					stmts = []string{`CREATE TRIGGER refuse BEFORE INSERT ON task_events BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`}
				case "the last event":
					// AFTER: a BEFORE INSERT trigger does not know the id the row is going to get
					stmts = []string{fmt.Sprintf(`CREATE TRIGGER refuse AFTER INSERT ON task_events WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`, maxEvent+sc.events)}
				case "the released event":
					stmts = []string{`CREATE TRIGGER refuse BEFORE INSERT ON task_events WHEN NEW.kind = 'released' BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`}
				default:
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
				for _, name := range []string{"refuse", "refuse_update"} {
					_, _ = db.Exec(`DROP TRIGGER IF EXISTS ` + name)
				}
				if err := call(); err != nil {
					t.Errorf("the same call once the obstacle is gone: %v", err)
				}
			})
		}
	}
}

// Spec 5.4: one revision for each write transaction (not one for each card, not one for each
// event), none for a call that changed nothing or failed.
func TestEveryWriteBumpsTheRevisionOnceAndNothingElseDoes(t *testing.T) {
	s, db, c := newTestStore(t)
	rev := func() int64 {
		t.Helper()
		r, err := s.Rev(bg, "default")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	add := func(n int, ready bool) []int64 {
		var ids []int64
		for i := 0; i < n; i++ {
			ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), ready).ID)
		}
		return ids
	}
	a, b, r := add(3, false), add(3, false), add(3, true)
	held := opsHeld(t, s, db, c, "held", "bob", time.Hour)
	z := add(1, false)
	if _, err := s.Archive(bg, "default", z, human); err != nil {
		t.Fatal(err)
	}
	title, same := "a new title", "t1" // what a[1] is called already
	for _, k := range []struct {
		name  string
		want  int64 // how far the revision moves
		fails bool
		call  func() error
	}{
		{"an edit", 1, false, func() error { _, err := s.Edit(bg, "default", a[0], Edit{Title: &title}, human); return err }},
		{"an edit that changes nothing", 0, false, func() error { _, err := s.Edit(bg, "default", a[1], Edit{Title: &same}, human); return err }},
		{"a refused edit", 0, true, func() error { _, err := s.Edit(bg, "default", a[1], Edit{}, human); return err }},
		{"a move", 1, false, func() error { _, err := s.Move(bg, "default", r[0], StatusReview, Placement{}, human); return err }},
		{"a move of a held card, which writes two events", 1, false, func() error {
			_, err := s.Move(bg, "default", held.ID, StatusReady, Placement{}, human)
			return err
		}},
		{"a refused move", 0, true, func() error {
			_, err := s.Move(bg, "default", r[1], StatusReady, Placement{Top: true, Bottom: true}, human)
			return err
		}},
		{"an approval of three", 1, false, func() error { _, err := s.Approve(bg, "default", a, false, human); return err }},
		{"an approval that fails on its last card", 0, true, func() error {
			_, err := s.Approve(bg, "default", []int64{b[0], b[1], r[1]}, false, human)
			return err
		}},
		{"an archive of three", 1, false, func() error { _, err := s.Archive(bg, "default", b, human); return err }},
		{"an archive of the Review column", 1, false, func() error { _, err := s.ArchiveStatus(bg, "default", StatusReview, human); return err }},
		{"an archive of a column with nothing in it", 0, false, func() error { _, err := s.ArchiveStatus(bg, "default", StatusDone, human); return err }},
		{"an unarchive of four", 1, false, func() error {
			_, err := s.Unarchive(bg, "default", append([]int64{r[0]}, b...), human)
			return err
		}},
		{"an unarchive that fails on its last card", 0, true, func() error {
			_, err := s.Unarchive(bg, "default", []int64{z[0], r[1]}, human)
			return err
		}},
	} {
		before := rev()
		err := k.call()
		if got := rev() - before; got != k.want {
			t.Errorf("%s: the revision moved by %d (err %v), want %d", k.name, got, err, k.want)
		}
		if (err != nil) != k.fails {
			t.Errorf("%s: err %v, want a failure: %v", k.name, err, k.fails)
		}
	}
}

// What each verb writes in the history of the task, as the spec names the kinds, dated by the
// store's clock.
func TestEachVerbWritesTheEventTheSpecNames(t *testing.T) {
	s, _, c := newTestStore(t)
	a := mustAdd(t, s, "default", "a", false)
	title := "b"
	do := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		// every verb dates the row with the clock too, not only the event
		if got, _, err := s.Get(bg, "default", a.ID); err != nil || !got.UpdatedAt.Equal(c.t) {
			t.Errorf("after the verb at %v: updated at %v, err %v", c.t, got.UpdatedAt, err)
		}
		c.advance(time.Minute)
	}
	c.advance(time.Minute)
	_, err := s.Edit(bg, "default", a.ID, Edit{Title: &title}, human)
	do(err)
	_, err = s.Approve(bg, "default", []int64{a.ID}, false, human)
	do(err)
	_, err = s.Move(bg, "default", a.ID, StatusDone, Placement{}, human)
	do(err)
	_, err = s.ArchiveStatus(bg, "default", StatusDone, human)
	do(err)
	_, err = s.Unarchive(bg, "default", []int64{a.ID}, human)
	do(err)
	_, err = s.Archive(bg, "default", []int64{a.ID}, human)
	do(err)
	want := []string{
		"created|you|>inbox|",
		"edited|you|>|title",
		"moved|you|inbox>ready|approved",
		"moved|you|ready>done|",
		"archived|you|done>archived|",
		"unarchived|you|archived>done|",
		"archived|you|done>archived|",
	}
	if got := opsEvents(t, s, a.ID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	_, events, _ := s.Get(bg, "default", a.ID)
	for i := 1; i < len(events); i++ {
		if want := events[0].At.Add(time.Duration(i) * time.Minute); !events[i].At.Equal(want) {
			t.Errorf("the %s event is dated %v, want %v: each verb ran a minute after the one before", events[i].Kind, events[i].At, want)
		}
	}
}
