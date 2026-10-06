package tasks

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// approve ids ... --top puts the group on top in the order it was given (the first id on the very
// top), as without --top it goes to the bottom in that order. Fifteen ids use up the room between
// the neighbours and renumber the column on the way; the answer lists the tasks in the order of the
// ids, as they are after the call.
func TestApproveKeepsTheOrderTheIdsAreGivenIn(t *testing.T) {
	for _, top := range []bool{false, true} {
		t.Run(fmt.Sprintf("top %v", top), func(t *testing.T) {
			s, _, _ := newTestStore(t)
			r1, r2 := mustAdd(t, s, "default", "r1", true), mustAdd(t, s, "default", "r2", true)
			var ids []int64
			for i := 0; i < 15; i++ {
				ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), false).ID)
			}
			given := []int64{ids[3], ids[0]} // not the order they were made in
			given = append(given, ids[4:]...)
			given = append(given, ids[1], ids[2])
			got, err := s.Approve(bg, "default", given, top, human)
			if err != nil {
				t.Fatal(err)
			}
			for i, task := range got {
				if task.ID != given[i] || task.Status != StatusReady || task.LastEvent == nil || task.LastEvent.Kind != "moved" {
					t.Fatalf("answer %d: %+v, want task #%d in Ready", i, task, given[i])
				}
			}
			want := append(append([]int64{}, given...), r1.ID, r2.ID)
			if !top {
				want = append([]int64{r1.ID, r2.ID}, given...)
			}
			if col := column(t, s, StatusReady); !sameIDs(col, want) {
				t.Errorf("Ready: %v, want %v", col, want)
			}
		})
	}
}

// Only an Inbox task is approved, and the refusal says which task and where it is.
func TestApproveRefusesAnyTaskThatIsNotInInbox(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, st := range []Status{StatusReady, StatusInProgress, StatusReview, StatusDone, StatusArchived} {
		id := seedRow(t, db, "default", string(st))
		before := dumpBoard(t, db)
		_, err := s.Approve(bg, "default", []int64{id}, false, human)
		want := fmt.Sprintf("#%d is %s, not inbox", id, st)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("approving a %s task: %v, want ErrInvalid saying %q", st, err, want)
		}
		if after := dumpBoard(t, db); after != before {
			t.Errorf("approving a %s task changed the database", st)
		}
	}
}

// A call that names several tasks is one transaction: the task that fails fails all of them, and
// the error names it. A task named twice is the second time in the state the first left it in.
func TestBulkCallsAreAllOrNothingAndNameTheTaskThatFailed(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	theirs := mustAdd(t, s, other, "theirs", false)
	g1, g2 := mustAdd(t, s, "default", "g1", false), mustAdd(t, s, "default", "g2", false)
	ready := mustAdd(t, s, "default", "ready", true)
	arch := mustAdd(t, s, "default", "arch", false)
	if _, err := s.Archive(bg, "default", []int64{arch.ID}, human); err != nil {
		t.Fatal(err)
	}
	before := dumpBoard(t, db)
	approve := func(ids ...int64) error { _, err := s.Approve(bg, "default", ids, false, human); return err }
	archive := func(ids ...int64) error { _, err := s.Archive(bg, "default", ids, human); return err }
	unarchive := func(ids ...int64) error { _, err := s.Unarchive(bg, "default", ids, human); return err }
	for _, k := range []struct {
		name string
		err  error
		want error
		says string // what the message must name
	}{
		{"approve: a task in Ready", approve(g1.ID, ready.ID, g2.ID), ErrInvalid, fmt.Sprintf("#%d", ready.ID)},
		{"approve: an unknown id", approve(g1.ID, 424242, g2.ID), ErrNotFound, "#424242"},
		{"approve: another profile's task", approve(g1.ID, theirs.ID, g2.ID), ErrNotFound, fmt.Sprintf("#%d", theirs.ID)},
		{"approve: the same task twice", approve(g1.ID, g1.ID), ErrInvalid, fmt.Sprintf("#%d", g1.ID)},
		{"approve: no task", approve(), ErrInvalid, ""},
		{"archive: an archived task", archive(g1.ID, arch.ID, g2.ID), ErrInvalid, fmt.Sprintf("#%d is already archived", arch.ID)},
		{"archive: an unknown id", archive(g1.ID, 424242), ErrNotFound, "#424242"},
		{"archive: the same task twice", archive(g1.ID, g1.ID), ErrInvalid, fmt.Sprintf("#%d is already archived", g1.ID)},
		{"archive: no task", archive(), ErrInvalid, ""},
		{"unarchive: a task that is not archived", unarchive(arch.ID, g1.ID), ErrInvalid, fmt.Sprintf("#%d is inbox, not archived", g1.ID)},
		{"unarchive: an unknown id", unarchive(arch.ID, 424242), ErrNotFound, "#424242"},
		{"unarchive: the same task twice", unarchive(arch.ID, arch.ID), ErrInvalid, fmt.Sprintf("#%d is ", arch.ID)},
		{"unarchive: no task", unarchive(), ErrInvalid, ""},
	} {
		if !errors.Is(k.err, k.want) || (k.err != nil && !strings.Contains(k.err.Error(), k.says)) {
			t.Errorf("%s: %v, want %v naming %q", k.name, k.err, k.want, k.says)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("failed calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Spec 5.1: any transition. From every status, the archive included, to every column, with the
// move written in the history and no claim left.
func TestMoveAllowsEveryTransition(t *testing.T) {
	s, db, _ := newTestStore(t)
	from := append(append([]Status{}, BoardStatuses...), StatusArchived)
	for _, f := range from {
		for _, to := range BoardStatuses {
			id := seedRow(t, db, "default", string(f))
			got, err := s.Move(bg, "default", id, to, Placement{Top: true}, human)
			if err != nil || got.Status != to || got.Claim != nil {
				t.Errorf("%s to %s: %+v, %v", f, to, got, err)
				continue
			}
			if ev := opsEvents(t, s, id); len(ev) != 1 || ev[0] != fmt.Sprintf("moved|you|%s>%s|", f, to) {
				t.Errorf("%s to %s: events %v", f, to, ev)
			}
		}
	}
}

// Unarchive puts a task back in the column it was archived from (the newest archived event says
// which), at that column's default end; a task that was in progress comes back to Ready, and one
// whose past cannot be read comes back to Inbox.
func TestUnarchiveGoesBackToTheColumnItWasArchivedFrom(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, st := range BoardStatuses {
		other, card := mustAdd(t, s, "default", "other "+string(st), false), mustAdd(t, s, "default", "card "+string(st), false)
		for _, task := range []Task{other, card} {
			if _, err := s.Move(bg, "default", task.ID, st, Placement{Bottom: true}, human); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Archive(bg, "default", []int64{card.ID}, human); err != nil {
			t.Fatal(err)
		}
		got, err := s.Unarchive(bg, "default", []int64{card.ID}, human)
		want := st
		if st == StatusInProgress {
			want = StatusReady
		}
		if err != nil || got[0].Status != want {
			t.Fatalf("a task archived from %s: %+v, %v, want %s", st, got, err, want)
		}
		col := column(t, s, want)
		if atTop(want) && col[0] != card.ID || !atTop(want) && col[len(col)-1] != card.ID {
			t.Errorf("a task archived from %s comes back to %s at %v: want the default end of that column", st, want, col)
		}
	}

	// The newest archived event decides, not the first.
	task := mustAdd(t, s, "default", "twice", false)
	for _, to := range []Status{StatusDone, StatusReview} {
		for _, step := range []func() error{
			func() error { _, err := s.Move(bg, "default", task.ID, to, Placement{}, human); return err },
			func() error { _, err := s.Archive(bg, "default", []int64{task.ID}, human); return err },
			func() error { _, err := s.Unarchive(bg, "default", []int64{task.ID}, human); return err },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
		if got, _, _ := s.Get(bg, "default", task.ID); got.Status != to {
			t.Errorf("archived and restored with the history of an earlier round: %s, want %s", got.Status, to)
		}
	}

	// A row nobody archived through the store, and events that say nothing usable.
	for _, k := range []struct{ name, from string }{
		{"no archived event", ""}, {"an archived event from nowhere", "nonsense"}, {"an archived event from the archive", "archived"},
	} {
		id := seedRow(t, db, "default", "archived")
		if k.from != "" {
			if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind, from_status, to_status) VALUES (?, ?, 'you', 'archived', ?, 'archived')`, id, rowTime, k.from); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := s.Unarchive(bg, "default", []int64{id}, human); err != nil || got[0].Status != StatusInbox {
			t.Errorf("%s: %+v, %v, want Inbox", k.name, got, err)
		}
	}
}

// ArchiveStatus takes one column, all of it, in the order it is in, and says how many.
func TestArchiveStatusArchivesOneColumnInTheOrderItIsIn(t *testing.T) {
	s, db, _ := newTestStore(t)
	var done []int64
	for i := 0; i < 3; i++ {
		d := mustAdd(t, s, "default", fmt.Sprintf("d%d", i), false)
		if _, err := s.Move(bg, "default", d.ID, StatusDone, Placement{}, human); err != nil {
			t.Fatal(err)
		}
		done = append([]int64{d.ID}, done...) // newest first
	}
	review := mustAdd(t, s, "default", "review", false)
	ready := mustAdd(t, s, "default", "ready", true)
	if _, err := s.Move(bg, "default", review.ID, StatusReview, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, StatusDone); !sameIDs(got, done) {
		t.Fatalf("setup: Done is %v, want %v", got, done)
	}
	n, err := s.ArchiveStatus(bg, "default", StatusDone, human)
	if err != nil || n != 3 {
		t.Fatalf("archive Done: %d, %v", n, err)
	}
	if got := column(t, s, StatusDone); len(got) != 0 {
		t.Errorf("Done still holds %v", got)
	}
	if !sameIDs(column(t, s, StatusReview), []int64{review.ID}) || !sameIDs(column(t, s, StatusReady), []int64{ready.ID}) {
		t.Error("another column was touched")
	}
	ts, err := s.List(bg, "default", Filter{Statuses: []Status{StatusArchived}}, human)
	if err != nil || !sameIDs(idsOf(ts), done) {
		t.Errorf("the archive lists %v (%v), want the column's order %v", idsOf(ts), err, done)
	}
	for _, id := range done {
		if ev := opsEvents(t, s, id); ev[len(ev)-1] != "archived|you|done>archived|" {
			t.Errorf("events of #%d: %v", id, ev)
		}
	}
	for _, st := range []Status{StatusArchived, "", "bogus"} {
		before := dumpBoard(t, db)
		if _, err := s.ArchiveStatus(bg, "default", st, human); !errors.Is(err, ErrInvalid) {
			t.Errorf("ArchiveStatus(%q): %v, want ErrInvalid", st, err)
		}
		if dumpBoard(t, db) != before {
			t.Errorf("ArchiveStatus(%q) changed the database", st)
		}
	}
}

// A task new to the board counts against the 2,000 open tasks of a profile (spec 4.6, D13), and a
// task that comes back from the archive is one: Unarchive and a move out of the archive stop at
// the same limit as an add, as a whole call.
func TestMovingOutOfTheArchiveStopsAtTheOpenTaskLimit(t *testing.T) {
	s, db, _ := newTestStore(t)
	seedTasks(t, db, "default", MaxOpenTasks-3)
	a, b, c := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false), mustAdd(t, s, "default", "c", false) // 2000 open
	if _, err := s.Archive(bg, "default", []int64{a.ID, b.ID}, human); err != nil {                                             // 1998
		t.Fatal(err)
	}
	d := mustAdd(t, s, "default", "d", false)
	mustAdd(t, s, "default", "e", false) // 2000 again
	before := dumpBoard(t, db)
	if _, err := s.Unarchive(bg, "default", []int64{a.ID}, human); !errors.Is(err, ErrLimit) {
		t.Errorf("unarchive at the limit: %v, want ErrLimit", err)
	}
	if _, err := s.Move(bg, "default", a.ID, StatusReady, Placement{}, human); !errors.Is(err, ErrLimit) {
		t.Errorf("move out of the archive at the limit: %v, want ErrLimit", err)
	}
	if after := dumpBoard(t, db); after != before {
		t.Error("a refusal changed the database")
	}
	// a move that adds nothing to the board is no business of the limit, nor is archiving
	if _, err := s.Move(bg, "default", d.ID, StatusReady, Placement{}, human); err != nil {
		t.Errorf("a move within the board at the limit: %v", err)
	}
	if _, err := s.Archive(bg, "default", []int64{c.ID}, human); err != nil { // 1999 open: room for one
		t.Fatal(err)
	}
	before = dumpBoard(t, db)
	if _, err := s.Unarchive(bg, "default", []int64{a.ID, b.ID}, human); !errors.Is(err, ErrLimit) {
		t.Errorf("unarchive of two with room for one: %v, want ErrLimit", err)
	}
	if after := dumpBoard(t, db); after != before {
		t.Error("a call that went over the limit on its second task kept its first")
	}
	if got, err := s.Unarchive(bg, "default", []int64{a.ID}, human); err != nil || got[0].Status != StatusInbox {
		t.Errorf("unarchive with room for one: %+v, %v", got, err)
	}
	if n := countWhere(t, db, "tasks", "profile_id = 'default' AND status <> 'archived'"); n != MaxOpenTasks {
		t.Errorf("%d open tasks, want %d", n, MaxOpenTasks)
	}
}

// Spec 4.4: the operator's actions are always recorded, whatever the history already holds.
func TestTheOperatorsActionsAreRecordedOnATaskWithManyEvents(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "busy", false)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2100; i++ {
		if _, err := tx.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, 'bob', 'comment')`, task.ID, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	for i, step := range []func() error{
		func() error { _, err := s.Edit(bg, "default", task.ID, Edit{Title: &title}, human); return err },
		func() error { _, err := s.Approve(bg, "default", []int64{task.ID}, false, human); return err },
		func() error { _, err := s.Move(bg, "default", task.ID, StatusReview, Placement{}, human); return err },
		func() error { _, err := s.Archive(bg, "default", []int64{task.ID}, human); return err },
		func() error { _, err := s.Unarchive(bg, "default", []int64{task.ID}, human); return err },
	} {
		before := countWhere(t, db, "task_events", "task_id = ?", task.ID)
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if after := countWhere(t, db, "task_events", "task_id = ?", task.ID); after <= before {
			t.Errorf("step %d wrote no event (%d before, %d after)", i, before, after)
		}
	}
}
