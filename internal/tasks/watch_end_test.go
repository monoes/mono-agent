package tasks

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// Rev and Counts do not check the profile, so a watcher that did not would read revision 0 and no cards of a
// profile that does not exist for ever: it refuses at once, before it reports anything.
func TestWatchRefusesAnUnknownProfileAtOnce(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(bg)
	defer cancel() // a Watch that does not refuse would run until here
	var called atomic.Bool
	result := make(chan error, 1)
	go func() { result <- s.Watch(ctx, "nobody", time.Hour, func(Change) { called.Store(true) }) }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("Watch of an unknown profile returned %v, want ErrInvalid", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch of an unknown profile did not return")
	}
	if called.Load() {
		t.Error("a profile that does not exist was reported")
	}
}

// Deleting a profile deletes its board, and with it the revision: a watcher that went on would report
// revision 0 and an empty board as a change. It ends instead, with ErrNotFound, and reports nothing for it.
func TestWatchEndsWhenItsProfileIsDeleted(t *testing.T) {
	s, db, _ := newTestStore(t)
	p := addProfile(t, db, "p2")
	mustAdd(t, s, p, "x", false)
	w := startWatch(t, s, p, 5*time.Millisecond, nil)
	if c := waitChange(t, w.changes); c.Rev != 1 {
		t.Fatalf("the first report: %+v", c)
	}
	if _, err := db.Exec(`DELETE FROM profiles WHERE id = ?`, p); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch went on watching a profile that was deleted")
	}
	if !errors.Is(w.err, ErrNotFound) || errors.Is(w.err, ErrInvalid) {
		t.Errorf("Watch returned %v, want an error that is ErrNotFound and not ErrInvalid", w.err)
	}
	select {
	case c := <-w.changes:
		t.Errorf("the deletion was reported as a change: %+v", c)
	default:
	}
}

// A read that fails does not end the watcher: that poll is skipped and the next one tries again. A revision
// that is not a number is a read that fails while the profile is there, so it is no deletion.
func TestWatchSurvivesAFailingRead(t *testing.T) {
	setRev := func(t *testing.T, db *sql.DB, rev any) {
		t.Helper()
		if _, err := db.Exec(`UPDATE task_board_rev SET rev = ? WHERE profile_id = 'default'`, rev); err != nil {
			t.Fatal(err)
		}
	}
	// stillWatching lets many polls pass (the interval is 5 ms) and fails if the watcher ended or reported.
	stillWatching := func(t *testing.T, w *watcher) {
		t.Helper()
		select {
		case <-w.finished:
			t.Fatalf("Watch ended on a read that failed: %v", w.err)
		case c := <-w.changes:
			t.Fatalf("a read that failed was reported: %+v", c)
		case <-time.After(150 * time.Millisecond):
		}
	}
	t.Run("after a report", func(t *testing.T) {
		s, db, _ := newTestStore(t)
		mustAdd(t, s, "default", "x", false)
		w := startWatch(t, s, "default", 5*time.Millisecond, nil)
		waitChange(t, w.changes)
		setRev(t, db, "not a number")
		stillWatching(t, w)
		setRev(t, db, 1)
		mustAdd(t, s, "default", "y", false)
		if c := waitChange(t, w.changes); c.Rev != 2 || c.Counts.Inbox != 2 {
			t.Fatalf("after the read worked again: %+v", c)
		}
	})
	t.Run("before the first report", func(t *testing.T) {
		s, db, _ := newTestStore(t)
		mustAdd(t, s, "default", "x", false)
		setRev(t, db, "not a number")
		w := startWatch(t, s, "default", 5*time.Millisecond, nil)
		stillWatching(t, w)
		setRev(t, db, 1)
		if c := waitChange(t, w.changes); c.Rev != 1 || c.Counts.Inbox != 1 {
			t.Fatalf("the first report once the read worked: %+v", c)
		}
	})
}

// The context ends a watcher that is waiting for its next poll at once, however long the interval, and
// Watch then returns nil: ending it is not a failure.
func TestWatchStopsAtOnceWhenItsContextEnds(t *testing.T) {
	s, db, _ := newTestStore(t)
	w := startWatch(t, s, "default", time.Hour, nil)
	waitChange(t, w.changes)
	if err := w.stop(t); err != nil {
		t.Fatalf("Watch returned %v when its context ended, want nil", err)
	}
	if n := db.Stats().InUse; n != 0 {
		t.Errorf("%d connections are in use after Watch returned", n)
	}
}

// A context that has ended before Watch is called ends it before it reports anything.
func TestWatchWithAnEndedContextReturnsNilAtOnce(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	var called atomic.Bool
	result := make(chan error, 1)
	go func() { result <- s.Watch(ctx, "default", time.Hour, func(Change) { called.Store(true) }) }()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Watch returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return")
	}
	if called.Load() {
		t.Error("a watcher whose context had ended reported a change")
	}
}

// The context ends in the middle of a poll (the poll reads the clock for the stale claims, so the clock is
// where the test ends it): the poll fails, which is no reason for the watcher to go on or to report, and Watch
// returns nil at once.
func TestWatchEndedByItsContextDuringAPollReportsNothing(t *testing.T) {
	s, db, c := newTestStore(t)
	mustAdd(t, s, "default", "x", false)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	s.now = func() time.Time { cancel(); return c.t }
	var called atomic.Bool
	result := make(chan error, 1)
	go func() { result <- s.Watch(ctx, "default", time.Hour, func(Change) { called.Store(true) }) }()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Watch returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return when its context ended during a poll")
	}
	if called.Load() {
		t.Error("a poll that the context ended was reported")
	}
	if n := db.Stats().InUse; n != 0 {
		t.Errorf("%d connections are in use after Watch returned", n)
	}
}

// Watchers that end leave nothing behind: no goroutine and no connection.
func TestWatchersThatEndLeaveNothingRunning(t *testing.T) {
	s, db, _ := newTestStore(t)
	run := func() {
		w := startWatch(t, s, "default", time.Hour, nil)
		waitChange(t, w.changes)
		if err := w.stop(t); err != nil {
			t.Fatal(err)
		}
	}
	run() // the pool's own goroutines are running by the time the count is taken
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		run()
	}
	after := runtime.NumGoroutine()
	for deadline := time.Now().Add(2 * time.Second); after > before && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("%d goroutines before 20 watchers ran and ended, %d after", before, after)
	}
	if n := db.Stats().InUse; n != 0 {
		t.Errorf("%d connections are in use after the watchers ended", n)
	}
}
