package tasks

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Unarchive goes back to the column of the newest archived event, newest by the order they were
// written in, not by the time they carry: a clock that was set back must not make an old archived
// event the newest.
func TestUnarchiveTakesTheNewestArchivedEventEvenWhenTheClockWentBack(t *testing.T) {
	s, _, c := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	c.advance(2 * time.Hour)
	for _, step := range []func() error{
		func() error { _, err := s.Move(bg, "default", task.ID, StatusDone, Placement{}, human); return err },
		func() error { _, err := s.Archive(bg, "default", []int64{task.ID}, human); return err },
		func() error { _, err := s.Unarchive(bg, "default", []int64{task.ID}, human); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	c.advance(-3 * time.Hour) // the clock was set back: what follows is dated before the first archive
	for _, step := range []func() error{
		func() error { _, err := s.Move(bg, "default", task.ID, StatusReview, Placement{}, human); return err },
		func() error { _, err := s.Archive(bg, "default", []int64{task.ID}, human); return err },
		func() error { _, err := s.Unarchive(bg, "default", []int64{task.ID}, human); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if got, _, _ := s.Get(bg, "default", task.ID); got.Status != StatusReview {
		t.Errorf("unarchived to %s, want review: the second archive is the newest event", got.Status)
	}
}

// strengthThree adds three Inbox cards, archived when archive is set.
func strengthThree(t *testing.T, s *Store, archive bool) []int64 {
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

// Every UPDATE of a card is checked. When the table refuses one, the verb fails and nothing is
// written, no event and no revision, instead of going on to record a change that did not happen.
// (The renumber's UPDATEs are told from the move's by whose rows they are: the neighbours a and b,
// not the card x that is moved.)
func TestAnUpdateThatTheTableRefusesFailsTheVerbAndWritesNothing(t *testing.T) {
	const refuseAll = `CREATE TRIGGER refuse BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`
	const refuseRenumber = `CREATE TRIGGER refuse BEFORE UPDATE ON tasks WHEN NEW.title <> 'x' BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`
	for _, sc := range []struct {
		name    string
		trigger string
		prep    func(t *testing.T, s *Store, db *sql.DB) func() error
	}{
		{"Edit", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			id, title := mustAdd(t, s, "default", "a", false).ID, "b"
			return func() error { _, err := s.Edit(bg, "default", id, Edit{Title: &title}, human); return err }
		}},
		{"Move", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			id := mustAdd(t, s, "default", "a", false).ID
			return func() error { _, err := s.Move(bg, "default", id, StatusReview, Placement{}, human); return err }
		}},
		{"Approve of three", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			ids := strengthThree(t, s, false)
			return func() error { _, err := s.Approve(bg, "default", ids, false, human); return err }
		}},
		{"Archive of three", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			ids := strengthThree(t, s, false)
			return func() error { _, err := s.Archive(bg, "default", ids, human); return err }
		}},
		{"ArchiveStatus of three", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			strengthThree(t, s, false)
			return func() error { _, err := s.ArchiveStatus(bg, "default", StatusInbox, human); return err }
		}},
		{"Unarchive of three", refuseAll, func(t *testing.T, s *Store, db *sql.DB) func() error {
			ids := strengthThree(t, s, true)
			return func() error { _, err := s.Unarchive(bg, "default", ids, human); return err }
		}},
		{"Move that renumbers its column", refuseRenumber, func(t *testing.T, s *Store, db *sql.DB) func() error {
			x := mustAdd(t, s, "default", "x", false)
			a, b := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true)
			for id, pos := range map[int64]int64{a.ID: 10, b.ID: 11} { // no integer between them
				if _, err := db.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, pos, id); err != nil {
					t.Fatal(err)
				}
			}
			return func() error {
				_, err := s.Move(bg, "default", x.ID, StatusReady, Placement{After: a.ID}, human)
				return err
			}
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			call := sc.prep(t, s, db)
			before := dumpBoard(t, db)
			if _, err := db.Exec(sc.trigger); err != nil {
				t.Fatal(err)
			}
			if err := call(); err == nil || !strings.Contains(err.Error(), "refused for the test") {
				t.Fatalf("the call: %v, want the refusal of the table", err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Fatalf("a failed call left changes behind:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if _, err := db.Exec(`DROP TRIGGER refuse`); err != nil {
				t.Fatal(err)
			}
			if err := call(); err != nil {
				t.Errorf("the same call once the obstacle is gone: %v", err)
			}
		})
	}
}

// A position that cannot be read is a failure and no guess: with a row of the column the card is to
// go into, or of the archive, whose position is not a number (SQLite keeps text as it is in an
// INTEGER column), the verb fails and writes nothing, instead of placing the card among positions
// it took for 0.
func TestAPositionThatCannotBeReadFailsThePlacementAndWritesNothing(t *testing.T) {
	for _, sc := range []struct {
		name   string
		status string
		call   func(s *Store, anchor, mover int64) error
	}{
		{"after a card of a column", "ready", func(s *Store, anchor, mover int64) error {
			_, err := s.Move(bg, "default", mover, StatusReady, Placement{After: anchor}, human)
			return err
		}},
		{"the bottom of the archive", "archived", func(s *Store, anchor, mover int64) error {
			_, err := s.Archive(bg, "default", []int64{mover}, human)
			return err
		}},
		{"the whole column archived", "ready", func(s *Store, anchor, mover int64) error {
			_, err := s.ArchiveStatus(bg, "default", StatusReady, human)
			return err
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			anchor, mover := mustAdd(t, s, "default", "anchor", true), mustAdd(t, s, "default", "mover", false)
			if _, err := db.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES ('default', 'corrupt', ?, 'not a number', ?, ?)`,
				sc.status, rowTime, rowTime); err != nil {
				t.Fatal(err)
			}
			before := dumpBoard(t, db)
			err := sc.call(s, anchor.ID, mover.ID)
			if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
				t.Errorf("the call: %v, want the failure of the read", err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Errorf("a failed call left changes behind:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// What happens to a card while it is archived is no business of where it goes back to: an edit made
// in the archive writes an event after the one that archived the card, and must not hide it.
func TestUnarchiveGoesBackToItsColumnAfterTheCardWasEditedInTheArchive(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	if _, err := s.Move(bg, "default", task.ID, StatusDone, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive(bg, "default", []int64{task.ID}, human); err != nil {
		t.Fatal(err)
	}
	title := "renamed in the archive"
	if _, err := s.Edit(bg, "default", task.ID, Edit{Title: &title}, human); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Unarchive(bg, "default", []int64{task.ID}, human); err != nil || got[0].Status != StatusDone {
		t.Errorf("unarchive after an edit in the archive: %+v, %v, want Done, the column it was archived from", got, err)
	}
}

// A history that cannot be read is a failure and no guess, and here it is the lookup alone that
// fails: the events table is read through a view whose from_status is NULL, which cannot be scanned,
// while an insert still lands in the real table. (With the column renamed, as the older test does,
// the insert that follows the lookup fails too, so a lookup whose error is dropped fails all the same.)
func TestUnarchiveFailsWhenOnlyTheLookupOfWhereTheTaskCameFromFails(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", true)
	if _, err := s.Archive(bg, "default", []int64{task.ID}, human); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE task_events RENAME TO task_events_real`,
		`CREATE VIEW task_events AS SELECT id, task_id, at, actor, kind, NULL AS from_status, to_status, note FROM task_events_real`,
		`CREATE TRIGGER task_events_insert INSTEAD OF INSERT ON task_events BEGIN
			INSERT INTO task_events_real (task_id, at, actor, kind, from_status, to_status, note)
			VALUES (NEW.task_id, NEW.at, NEW.actor, NEW.kind, NEW.from_status, NEW.to_status, NEW.note);
		END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rev, _ := s.Rev(bg, "default")
	got, err := s.Unarchive(bg, "default", []int64{task.ID}, human)
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		t.Errorf("unarchive with a history that cannot be read: %+v, %v, want the failure of the read", got, err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, task.ID).Scan(&status); err != nil || status != "archived" {
		t.Errorf("the task is %q (%v), want it left archived", status, err)
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_events_real WHERE task_id = ? AND kind = 'unarchived'`, task.ID).Scan(&events); err != nil || events != 0 {
		t.Errorf("%d unarchived events written (%v), want none", events, err)
	}
	if now, _ := s.Rev(bg, "default"); now != rev {
		t.Errorf("the revision moved from %d to %d", rev, now)
	}
}
