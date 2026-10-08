//go:build !windows

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
)

// waitTaskWarning waits until the app's log holds a warning that contains want.
func waitTaskWarning(t *testing.T, a *App, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range a.GetLogs() {
			if e.Level == "WARN" && strings.Contains(e.Message, want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no warning holding %q in the log: %+v", want, a.GetLogs())
}

// The watcher reports the board once at the start and after each write,
// with the Inbox and Review counts the sidebar badge shows.
func TestTaskWatcherEmitsTheStartAndEachChange(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("default", 10*time.Millisecond, rec.emit)
	defer stop()
	names, evts := waitEvents(t, rec, 1)
	if names[0] != "tasks:changed" || evts[0]["profile_id"] != "default" || evts[0]["rev"] != int64(0) || evts[0]["inbox"] != 0 || evts[0]["review"] != 0 {
		t.Fatalf("first event = %s %+v", names[0], evts[0])
	}
	addTestTask(t, a, "default", "first")
	_, evts = waitEvents(t, rec, 2)
	if evts[1]["rev"] != int64(1) || evts[1]["inbox"] != 1 {
		t.Fatalf("after an add: %+v", evts[1])
	}
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 2 {
		t.Fatalf("no write, no event: %v", names)
	}
}

// Once stop has returned, a write emits nothing: after a profile switch the
// page never hears the old profile's board.
func TestTaskWatcherIsSilentOnceStopped(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("default", 10*time.Millisecond, rec.emit)
	waitEvents(t, rec, 1)
	stop()
	addTestTask(t, a, "default", "after the stop")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 1 {
		t.Fatalf("a stopped watcher emitted: %v", names)
	}
}

// A watcher reports its own profile's board only (spec D9).
func TestTaskWatcherWatchesItsOwnProfile(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("work", 10*time.Millisecond, rec.emit)
	defer stop()
	waitEvents(t, rec, 1)
	addTestTask(t, a, "default", "on the other board")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 1 {
		t.Fatalf("a write to another profile's board emitted: %v", names)
	}
	addTestTask(t, a, "work", "on this board")
	_, evts := waitEvents(t, rec, 2)
	if evts[1]["profile_id"] != "work" || evts[1]["inbox"] != 1 {
		t.Fatalf("event = %+v", evts[1])
	}
}

// A watcher on a profile that does not exist ends at once and says so in the
// app's log: Watch returns an error for it (ErrInvalid), where a watcher that
// went on would report revision 0 and an empty board for ever. It emits
// nothing and does not start over, so a profile that appears later is not
// watched until the next restart. A profile deleted while it is watched ends
// Watch the same way, with an error that wraps ErrNotFound (P1's
// TestWatchEndsWhenItsProfileIsDeleted pins that); the branch here is the same.
func TestTaskWatcherEndsWithAWarningForAProfileThatDoesNotExist(t *testing.T) {
	a := newTestApp(t)
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("nobody", 10*time.Millisecond, rec.emit)
	waitTaskWarning(t, a, `unknown profile "nobody"`)
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('nobody', 'Nobody')`); err != nil {
		t.Fatal(err)
	}
	addTestTask(t, a, "nobody", "after the watcher ended")
	time.Sleep(100 * time.Millisecond)
	if names, _ := rec.snapshot(); len(names) != 0 {
		t.Fatalf("a watcher that had ended reported: %v", names)
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return for a watcher that had ended")
	}
}

// TaskPulse answers what the watcher would emit, read now: the badge asks
// once when it mounts, because the first event fired before it listened.
func TestTaskPulseReadsTheActiveBoard(t *testing.T) {
	a := newTestApp(t)
	addTestTask(t, a, "default", "one")
	addTestTask(t, a, "default", "two")
	got := a.TaskPulse()
	if got["profile_id"] != "default" || got["rev"] != int64(2) || got["inbox"] != 2 || got["review"] != 0 {
		t.Fatalf("TaskPulse = %+v", got)
	}
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}
	a.setActiveProfileID("work")
	if got := a.TaskPulse(); got["profile_id"] != "work" || got["inbox"] != 0 {
		t.Fatalf("for the active profile, which has no task: %+v", got)
	}
	// A profile that is not there has no board: {}, as for any failed read, not
	// revision 0 with no cards, which the badge would take for a real, empty
	// board (Rev and Counts do not check the profile; Profile does).
	a.setActiveProfileID("nobody")
	if got := a.TaskPulse(); got == nil || len(got) != 0 {
		t.Fatalf("for a profile that does not exist: %#v, want an empty map", got)
	}
	a.db = nil
	if got := a.TaskPulse(); got == nil || len(got) != 0 {
		t.Fatalf("without a database: %#v, want an empty map", got)
	}
}

// restartTaskWatcher watches the active profile (a switch must move the
// badge and the board to the new board) and stopTaskWatcher ends it.
func TestRestartAndStopTaskWatcher(t *testing.T) {
	a := newTestApp(t)
	a.restartTaskWatcher()
	if a.taskWatchStop == nil || a.taskWatchProfile != "default" {
		t.Fatalf("after a restart: stop set %v, profile %q", a.taskWatchStop != nil, a.taskWatchProfile)
	}
	a.setActiveProfileID("work")
	a.restartTaskWatcher()
	if a.taskWatchStop == nil || a.taskWatchProfile != "work" {
		t.Fatalf("after a switch: stop set %v, profile %q", a.taskWatchStop != nil, a.taskWatchProfile)
	}
	a.stopTaskWatcher()
	if a.taskWatchStop != nil || a.taskWatchProfile != "" {
		t.Fatal("the watcher survived stopTaskWatcher")
	}
	a.db = nil
	a.restartTaskWatcher()
	if a.taskWatchStop != nil {
		t.Fatal("a watcher started without a database")
	}
}

// stop waits for a report that is being emitted: once it returns, nothing
// of the old board can reach the page.
func TestTaskWatcherStopWaitsForAReportInFlight(t *testing.T) {
	a := newTestApp(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	stop := a.startTaskWatcher("default", 10*time.Millisecond, func(string, map[string]interface{}) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	<-entered // the first report is inside emit now
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while a report was still being emitted")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return once the report was done")
	}
}

// The badge is Inbox plus Review: the watcher's report and TaskPulse count a
// card in Review as Review, and the two columns' counts are not swapped.
func TestTaskReportsCountInboxAndReviewApart(t *testing.T) {
	a := newTestApp(t)
	ctx, store, human := context.Background(), tasks.NewStore(a.db), tasks.Actor{Kind: tasks.Human}
	card, _, err := store.Add(ctx, "default", tasks.AddInput{Title: "to review"}, human)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Move(ctx, "default", card.ID, tasks.StatusReview, tasks.Placement{}, human); err != nil {
		t.Fatal(err)
	}
	addTestTask(t, a, "default", "still in the inbox")
	addTestTask(t, a, "default", "also in the inbox")
	rec := &eventRecorder{}
	stop := a.startTaskWatcher("default", 10*time.Millisecond, rec.emit)
	defer stop()
	_, evts := waitEvents(t, rec, 1)
	if evts[0]["inbox"] != 2 || evts[0]["review"] != 1 {
		t.Fatalf("the watcher's report = %+v, want inbox 2 and review 1", evts[0])
	}
	if got := a.TaskPulse(); got["inbox"] != 2 || got["review"] != 1 {
		t.Fatalf("TaskPulse = %+v, want inbox 2 and review 1", got)
	}
}

// A restart and a stop end the watcher they replace: a switch must not leave
// the old board's watcher running beside the new one, and shutdown must end
// it before the database closes.
func TestRestartAndStopTaskWatcherEndTheWatcherTheyReplace(t *testing.T) {
	a := newTestApp(t)
	stopped := 0
	a.taskWatchStop = func() { stopped++ }
	a.restartTaskWatcher()
	defer a.stopTaskWatcher()
	if stopped != 1 {
		t.Fatalf("a restart called the old watcher's stop %d times, want once", stopped)
	}
	a.stopTaskWatcher() // ends the watcher the restart started
	a.taskWatchStop = func() { stopped++ }
	a.stopTaskWatcher()
	if stopped != 2 {
		t.Fatalf("stopTaskWatcher called the watcher's stop %d times, want once", stopped-1)
	}
}
