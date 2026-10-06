package tasks

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// writeMidRead makes the store's clock run fn the first time a read asks it for the time. A read asks
// between its statements (the stale count of Counts, the claim of a task it has just read), so fn is
// a write by another connection in the middle of the read. ran says whether it did: a test that
// never reached the middle of its read would pass for nothing.
func writeMidRead(s *Store, c *clock, fn func()) (ran func() bool) {
	done := false
	s.now = func() time.Time {
		if !done {
			done = true
			fn()
		}
		return c.t
	}
	return func() bool { return done }
}

// addEvent writes an event straight into the table. It reports with Error and not Fatal, so that it
// can run inside the store's clock too.
func addEvent(t *testing.T, db *sql.DB, id int64, actor, kind string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, ?, ?)`, id, rowTime, actor, kind); err != nil {
		t.Error(err)
	}
}

// What one read shows agrees with itself, whatever is written while it runs: it is one snapshot. Each
// test below writes in the middle of a read and wants the board as it was when the read began.
func TestCountsAreReadInOneSnapshot(t *testing.T) {
	s, db, c := newTestStore(t)
	mustAdd(t, s, "default", "a", true)
	ran := writeMidRead(s, c, func() {
		// a stale claim appears after the counts of the columns and before the count of the stale ones
		holdUntil(t, db, seedRow(t, db, "default", "in_progress"), c.t.Add(-time.Hour))
	})
	got, err := s.Counts(bg, "default")
	if !ran() {
		t.Fatal("the read never asked for the time: the test no longer writes in the middle of it")
	}
	if err != nil || got != (Counts{Ready: 1}) {
		t.Errorf("counts %+v, err %v, want the one ready card the board held when the read began", got, err)
	}
}

func TestAListIsReadInOneSnapshot(t *testing.T) {
	s, db, c := newTestStore(t)
	task := mustAdd(t, s, "default", "a", true)
	holdUntil(t, db, task.ID, c.t.Add(time.Hour)) // a held task: reading it asks the clock
	ran := writeMidRead(s, c, func() {
		addEvent(t, db, task.ID, "bob", "comment") // after the task was read and before its last event is
	})
	ts, err := s.List(bg, "default", Filter{}, human)
	if !ran() {
		t.Fatal("the read never asked for the time: the test no longer writes in the middle of it")
	}
	if err != nil || len(ts) != 1 || ts[0].LastEvent == nil || ts[0].LastEvent.Kind != "created" {
		t.Errorf("list %+v, err %v, want the one task with the last event it had when the read began", ts, err)
	}
}

func TestABoardIsReadInOneSnapshot(t *testing.T) {
	s, db, c := newTestStore(t)
	mustAdd(t, s, "default", "a", false)
	ran := writeMidRead(s, c, func() {
		// a card lands, and the revision with it, after the counts of the columns and before the columns
		seedRow(t, db, "default", "inbox")
		if _, err := db.Exec(`UPDATE task_board_rev SET rev = rev + 1`); err != nil {
			t.Error(err)
		}
	})
	b, err := s.Board(bg, "default", 0)
	if !ran() {
		t.Fatal("the read never asked for the time: the test no longer writes in the middle of it")
	}
	if err != nil || b.Rev != 1 || b.Counts.Inbox != 1 || len(b.Tasks[StatusInbox]) != 1 {
		t.Errorf("revision %d, %d inbox cards counted, %d shown (err %v), want one board: 1, 1 and 1", b.Rev, b.Counts.Inbox, len(b.Tasks[StatusInbox]), err)
	}
}

func TestATaskAndItsEventsAreReadInOneSnapshot(t *testing.T) {
	s, db, c := newTestStore(t)
	task := mustAdd(t, s, "default", "a", true)
	holdUntil(t, db, task.ID, c.t.Add(time.Hour)) // a held task: reading it asks the clock
	ran := writeMidRead(s, c, func() {
		addEvent(t, db, task.ID, "bob", "comment") // after the task was read and before its events are
	})
	got, events, err := s.Get(bg, "default", task.ID)
	if !ran() {
		t.Fatal("the read never asked for the time: the test no longer writes in the middle of it")
	}
	if err != nil || len(events) != 1 || got.LastEvent == nil || got.LastEvent.Kind != "created" {
		t.Errorf("last event %+v and %d events (err %v), want what the task had when the read began: created, 1", got.LastEvent, len(events), err)
	}
}

func TestEveryReadReportsItsContextEnding(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustAdd(t, s, "default", "a", false)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	for name, read := range map[string]func() error{
		"Rev":    func() error { _, err := s.Rev(ctx, "default"); return err },
		"Counts": func() error { _, err := s.Counts(ctx, "default"); return err },
		"Get":    func() error { _, _, err := s.Get(ctx, "default", 1); return err },
		"List":   func() error { _, err := s.List(ctx, "default", Filter{}, human); return err },
		"Board":  func() error { _, err := s.Board(ctx, "default", 0); return err },
	} {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s in a cancelled context: %v, want context.Canceled", name, err)
		}
	}
}

// A read that fails half way gives no half answer, and the connection it used is free again.
func TestACancelInTheMiddleOfAReadFailsItWithoutAnAnswer(t *testing.T) {
	s, _, c := newTestStore(t)
	mustAdd(t, s, "default", "a", true)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	ran := writeMidRead(s, c, cancel)
	got, err := s.Counts(ctx, "default")
	if !ran() {
		t.Fatal("the read never asked for the time: the test no longer cancels in the middle of it")
	}
	if !errors.Is(err, context.Canceled) || got != (Counts{}) {
		t.Errorf("counts %+v, err %v, want no counts and the error of the context", got, err)
	}
	s.now = c.now
	for i := 0; i < 3; i++ { // the pool hands out every connection it has
		if cn, err := s.Counts(bg, "default"); err != nil || cn.Ready != 1 {
			t.Fatalf("counts %d after the cancelled one: %+v, %v", i+1, cn, err)
		}
	}
}

// A stored time that does not read is an error in every read that shows it, not a task skipped and not
// the year 1 (Task 3 pins it for one task: this is for a list and a board, which scan many).
func TestAStoredTimeThatIsNotATimeFailsAListAndABoard(t *testing.T) {
	for _, k := range []struct{ name, spoil string }{
		{"created_at", `UPDATE tasks SET created_at = 'yesterday' WHERE id = ?`},
		{"updated_at", `UPDATE tasks SET updated_at = '' WHERE id = ?`},
		{"claim_until of a claimed task", `UPDATE tasks SET status = 'in_progress', claimed_by = 'bob', claim_until = '2026-10-05 12:00:00' WHERE id = ?`},
		{"the time of a last event", `UPDATE task_events SET at = '5 October' WHERE task_id = ?`},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			mustAdd(t, s, "default", "fine", false)
			spoiled := mustAdd(t, s, "default", "spoiled", false)
			if _, err := db.Exec(k.spoil, spoiled.ID); err != nil {
				t.Fatal(err)
			}
			if ts, err := s.List(bg, "default", Filter{}, human); err == nil || !strings.Contains(err.Error(), "not a time") || ts != nil {
				t.Errorf("list: %v, %v, want no tasks and an error saying the stored value is not a time", ts, err)
			}
			if b, err := s.Board(bg, "default", 0); err == nil || !strings.Contains(err.Error(), "not a time") || b.Tasks != nil {
				t.Errorf("board: %+v, %v, want no board and an error saying the stored value is not a time", b, err)
			}
		})
	}
}

// Get reads the events itself. The last event is fine here, so that the one that is not can only be
// found by Get's own loop and not by the read of the task.
func TestGetFailsOnAnEventWhoseTimeIsNotATime(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	addEvent(t, db, task.ID, "bob", "comment")
	if _, err := db.Exec(`UPDATE task_events SET at = '5 October' WHERE task_id = ? AND kind = 'created'`, task.ID); err != nil {
		t.Fatal(err)
	}
	got, events, err := s.Get(bg, "default", task.ID)
	if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not a time") || got.ID != 0 || events != nil {
		t.Errorf("get: %+v, %v, %v, want no task, no events and an error saying the stored value is not a time", got, events, err)
	}
}

// An error that repeats what a caller sent repeats a bounded part of it (the rule of Add's errors).
func TestAListRepeatsAtMostSixtyFourRunesOfWhatItRefuses(t *testing.T) {
	s, _, _ := newTestStore(t)
	huge := strings.Repeat("x", 1<<20)
	for _, k := range []struct {
		name string
		f    Filter
	}{
		{"a source", Filter{Source: huge}},
		{"a status", Filter{Statuses: []Status{Status(huge)}}},
	} {
		_, err := s.List(bg, "default", k.f, human)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", k.name, err)
			continue
		}
		// 64 runes of it: 63 and an ellipsis
		msg := err.Error()
		if len(msg) > 400 || strings.Contains(msg, strings.Repeat("x", 64)) || !strings.Contains(msg, strings.Repeat("x", 63)+"\U00002026") {
			t.Errorf("%s: a message of %d bytes, want the caller's text cut to 63 runes and an ellipsis", k.name, len(msg))
		}
	}
}
