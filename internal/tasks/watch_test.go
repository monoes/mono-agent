package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

func waitChange(t *testing.T, ch <-chan Change) Change {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no change was reported within 5 seconds")
		return Change{}
	}
}

// watcher is a Watch that runs in a goroutine of its own. What it reports arrives on changes; err is what
// Watch returned, once finished is closed. The end of the test cancels it and waits for it, so that none
// outlives the test.
type watcher struct {
	changes  chan Change
	finished chan struct{}
	err      error
	cancel   context.CancelFunc
}

// startWatch runs Watch for the profile in a goroutine. Its callback is fn, or, when fn is nil, a send of
// the change to the changes channel.
func startWatch(t *testing.T, s *Store, profile string, interval time.Duration, fn func(Change)) *watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(bg)
	w := &watcher{changes: make(chan Change, 1024), finished: make(chan struct{}), cancel: cancel}
	if fn == nil {
		fn = func(c Change) {
			select {
			case w.changes <- c:
			case <-ctx.Done():
			}
		}
	}
	go func() {
		defer close(w.finished)
		w.err = s.Watch(ctx, profile, interval, fn)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.finished:
		case <-time.After(10 * time.Second):
			t.Error("Watch did not stop when the test ended")
		}
	})
	return w
}

// stop ends the watcher the way its owner does, by its context, and returns what Watch returned.
func (w *watcher) stop(t *testing.T) error {
	t.Helper()
	w.cancel()
	select {
	case <-w.finished:
		return w.err
	case <-time.After(time.Second):
		t.Fatal("Watch did not return within a second of its context ending")
		return nil
	}
}

// noChange fails the test when the watcher reports anything within d.
func noChange(t *testing.T, w *watcher, d time.Duration) {
	t.Helper()
	select {
	case c := <-w.changes:
		t.Fatalf("no write, no report: %+v", c)
	case <-time.After(d):
	}
}

// setRev sets the revision of a profile's board by hand: a number, or text that is none.
func setRev(t *testing.T, db *sql.DB, profile string, rev any) {
	t.Helper()
	if _, err := db.Exec(`UPDATE task_board_rev SET rev = ? WHERE profile_id = ?`, rev, profile); err != nil {
		t.Fatal(err)
	}
}

func TestWatchReportsTheStartAndEachChange(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	changes := make(chan Change, 10)
	done := make(chan struct{})
	var err error
	go func() {
		err = s.Watch(ctx, "default", 5*time.Millisecond, func(c Change) { changes <- c })
		close(done)
	}()
	if first := waitChange(t, changes); first.Rev != 0 {
		t.Fatalf("the first report is the starting point: %+v", first)
	}
	mustAdd(t, s, "default", "x", false)
	if second := waitChange(t, changes); second.Rev != 1 || second.Counts.Inbox != 1 {
		t.Fatalf("after an add: %+v", second)
	}
	select {
	case c := <-changes:
		t.Fatalf("no write, no report: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not stop when its context ended")
	}
	if err != nil {
		t.Fatalf("Watch returned %v when its context ended, want nil", err)
	}
}

// Every write moves the revision, whatever column it touches, and the report carries the counts of the
// board as that write left it.
func TestWatchReportsTheCountsOfEveryWrite(t *testing.T) {
	s, _, _ := newTestStore(t)
	w := startWatch(t, s, "default", 5*time.Millisecond, nil)
	if c := waitChange(t, w.changes); c != (Change{}) {
		t.Fatalf("an empty board: %+v", c)
	}
	task := mustAdd(t, s, "default", "x", true)
	if c := waitChange(t, w.changes); c.Rev != 1 || c.Counts != (Counts{Ready: 1}) {
		t.Fatalf("after an add to Ready: %+v", c)
	}
	if _, err := s.Claim(bg, "default", task.ID, bot("worker"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if c := waitChange(t, w.changes); c.Rev != 2 || c.Counts != (Counts{InProgress: 1}) {
		t.Fatalf("after a claim: %+v", c)
	}
	if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "done"}, bot("worker")); err != nil {
		t.Fatal(err)
	}
	if c := waitChange(t, w.changes); c.Rev != 3 || c.Counts != (Counts{Review: 1}) {
		t.Fatalf("after a finish: %+v", c)
	}
	noChange(t, w, 100*time.Millisecond)
}

// A watcher is for one profile: the revision it follows and the counts it reports are its profile's, and
// a write to another profile is no change.
func TestWatchFollowsItsOwnProfileOnly(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	for i := 0; i < 3; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("d%d", i), false)
	}
	mustAdd(t, s, other, "r", true)
	w := startWatch(t, s, other, 5*time.Millisecond, nil)
	if c := waitChange(t, w.changes); c.Rev != 1 || c.Counts != (Counts{Ready: 1}) {
		t.Fatalf("the first report is p2's, not the default profile's (revision 3, three in Inbox): %+v", c)
	}
	mustAdd(t, s, "default", "more", false)
	noChange(t, w, 100*time.Millisecond)
	mustAdd(t, s, other, "r2", false)
	if c := waitChange(t, w.changes); c.Rev != 2 || c.Counts != (Counts{Inbox: 1, Ready: 1}) {
		t.Fatalf("after an add to p2: %+v", c)
	}
}

// watchGap runs a watcher with the interval and returns how long it waited between its first poll and the
// next one. The first call writes to the board, so that the next poll has a change to report, and the gap is
// the time from the end of that call to the start of the next one: none of it passes in the test goroutine,
// so a test goroutine that is slow to run cannot move it.
func watchGap(t *testing.T, interval time.Duration) time.Duration {
	t.Helper()
	s, _, _ := newTestStore(t)
	type call struct {
		at time.Time
		c  Change
	}
	second := make(chan call, 1)
	var calls int
	var firstEnded time.Time // written by the first call, read after the second has sent: no race
	startWatch(t, s, "default", interval, func(c Change) {
		calls++
		switch calls {
		case 1:
			if _, _, err := s.Add(bg, "default", AddInput{Title: "x"}, human); err != nil {
				t.Error(err)
			}
			firstEnded = time.Now()
		case 2:
			second <- call{time.Now(), c}
		}
	})
	select {
	case got := <-second:
		if got.c.Rev != 1 {
			t.Errorf("the second call: %+v", got.c)
		}
		return got.at.Sub(firstEnded)
	case <-time.After(10 * time.Second):
		t.Fatalf("no second call within 10 seconds, with an interval of %v", interval)
		return 0
	}
}

// Without an interval, or with one that is not above 0, a watcher polls every two seconds: neither without
// waiting (an interval of 0 left as it is would make a loop that never waits) nor rarely.
func TestWatchPollsEveryTwoSecondsWithoutAnInterval(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			if gap := watchGap(t, d); gap < 1900*time.Millisecond || gap > 8*time.Second {
				t.Fatalf("the watcher waited %v between two polls, want about two seconds", gap)
			}
		})
	}
}

// An interval above 0 is the one the watcher waits, a shorter one than the default included.
func TestWatchPollsAtTheIntervalItIsGiven(t *testing.T) {
	const interval = 100 * time.Millisecond
	if gap := watchGap(t, interval); gap < interval || gap > time.Second {
		t.Fatalf("with an interval of %v the watcher waited %v between two polls", interval, gap)
	}
}

// The revision is compared for difference, not for increase: a board whose revision went down (it was
// recreated, or restored from a copy) has changed, and the watcher reports it.
func TestWatchReportsARevisionThatWentDown(t *testing.T) {
	s, db, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), false)
	}
	w := startWatch(t, s, "default", 5*time.Millisecond, nil)
	if c := waitChange(t, w.changes); c.Rev != 3 || c.Counts.Inbox != 3 {
		t.Fatalf("the first report: %+v", c)
	}
	setRev(t, db, "default", 1)
	if c := waitChange(t, w.changes); c.Rev != 1 || c.Counts.Inbox != 3 {
		t.Fatalf("after the revision went from 3 to 1: %+v", c)
	}
}

// A poll that finds the revision where it was reads no counts (spec 5.4): the app polls for as long as it
// runs, and a poll should be a primary-key read. The clock is asked for by the read of the counts, so the
// calls to it count the polls that read them.
func TestWatchReadsTheCountsOnlyWhenTheRevisionMoves(t *testing.T) {
	s, _, c := newTestStore(t)
	var asked atomic.Int32
	s.now = func() time.Time { asked.Add(1); return c.t }
	w := startWatch(t, s, "default", 5*time.Millisecond, nil)
	waitChange(t, w.changes)
	atStart := asked.Load()
	if atStart == 0 {
		t.Fatal("the report at the start did not ask the clock: this test no longer sees the counts being read")
	}
	noChange(t, w, 150*time.Millisecond) // some thirty polls find nothing new
	if n := asked.Load() - atStart; n != 0 {
		t.Errorf("the counts were read %d times while the revision stood still", n)
	}
}

// The callback runs after the poll has ended, with no connection held: it may read the board, which is what
// the app does with a report. The test writes through a handle of its own, so that the pool whose
// connections are counted is the watcher's alone: the connection of a write goes back to its pool a moment
// after the COMMIT, and a poll that finds the write can call back in that moment.
func TestWatchCallsBackWithNoConnectionHeld(t *testing.T) {
	path := testdb.Path(t)
	open := func() *storage.Database {
		db, err := storage.NewDatabase(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() }) // after the watcher, which is cancelled by a cleanup of its own
		return db
	}
	watched, writer := open(), open()
	inUse := make(chan int, 8)
	startWatch(t, NewStore(watched.DB), "default", 5*time.Millisecond, func(Change) { inUse <- watched.DB.Stats().InUse })
	connectionsInUse := func(when string) {
		t.Helper()
		select {
		case n := <-inUse:
			if n != 0 {
				t.Errorf("%s: %d connections in use while the callback ran, want none", when, n)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no report %s", when)
		}
	}
	connectionsInUse("at the start")
	mustAdd(t, NewStore(writer.DB), "default", "x", false)
	connectionsInUse("after an add")
}

// A callback that takes its time holds up nothing but the next report: Watch calls it from its own loop, one
// call at a time, and the next poll starts after it returns, so calls never overlap and never come out of
// order, and the last revision is reported in the end.
func TestWatchWithASlowCallbackCallsOneAtATimeAndEndsUpToDate(t *testing.T) {
	s, _, _ := newTestStore(t)
	var running, overlaps atomic.Int32
	var mu sync.Mutex
	var revs []int64
	startWatch(t, s, "default", time.Millisecond, func(c Change) {
		if running.Add(1) > 1 {
			overlaps.Add(1)
		}
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		revs = append(revs, c.Rev)
		mu.Unlock()
		running.Add(-1)
	})
	const adds = 8
	for i := 0; i < adds; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), false)
		time.Sleep(7 * time.Millisecond) // writes come faster than the callback returns
	}
	reported := func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), revs...)
	}
	got := reported()
	for deadline := time.Now().Add(10 * time.Second); len(got) == 0 || got[len(got)-1] != adds; got = reported() {
		if time.Now().After(deadline) {
			t.Fatalf("the last revision, %d, was never reported: %v", adds, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d calls began while another one was running", n)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Errorf("reports came out of order or twice: %v", got)
			break
		}
	}
}

// A report is read in one snapshot, so its counts are those of the revision it carries: when every write is
// an add to Inbox, a report has as many cards in Inbox as its revision says. A revision read in one moment
// and the counts in another would sometimes show counts that are newer than the revision.
func TestWatchReportsTheCountsOfTheRevisionItCarries(t *testing.T) {
	s, _, _ := newTestStore(t)
	w := startWatch(t, s, "default", time.Millisecond, nil)
	const adds = 300
	for i := 0; i < adds; i++ {
		if _, _, err := s.Add(bg, "default", AddInput{Title: "x"}, human); err != nil {
			t.Fatal(err)
		}
	}
	for seen := int64(-1); seen < adds; {
		c := waitChange(t, w.changes)
		if int64(c.Counts.Inbox) != c.Rev {
			t.Fatalf("revision %d with %d cards in Inbox: the two were not read together", c.Rev, c.Counts.Inbox)
		}
		seen = c.Rev
	}
}

// The writes that matter come from other processes (the CLI), and a watcher in this one, with its pool of
// connections open, sees them.
func TestWatchSeesAWriteFromAnotherProcess(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() }) // after the watcher, which is cancelled by a cleanup of its own
	w := startWatch(t, NewStore(db.DB), "default", 5*time.Millisecond, nil)
	if c := waitChange(t, w.changes); c != (Change{}) {
		t.Fatalf("an empty board: %+v", c)
	}
	runHelpers(t, path, "add", 1)
	if c := waitChange(t, w.changes); c.Rev != 1 || c.Counts.Inbox != 1 {
		t.Fatalf("after the other process added a task: %+v", c)
	}
}
