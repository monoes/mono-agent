package tasks

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func TestEditChangesTheTextForTheOperatorOnly(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "old title", false)
	title, notes := "  new   title ", "some notes"
	got, err := s.Edit(bg, "default", task.ID, Edit{Title: &title, Notes: &notes}, human)
	if err != nil || got.Title != "new title" || got.Notes != "some notes" {
		t.Fatalf("edit: %+v, %v", got, err)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "edited" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	for _, a := range []Actor{bot("b"), {Kind: Capture, Name: SourceOS}} {
		if _, err := s.Edit(bg, "default", task.ID, Edit{Title: &title}, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v editing: %v, want ErrOperatorOnly", a, err)
		}
	}
	empty := " \x1b "
	if _, err := s.Edit(bg, "default", task.ID, Edit{Title: &empty}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty title: %v, want ErrInvalid", err)
	}
	if _, err := s.Edit(bg, "default", task.ID, Edit{}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("nothing to change: %v, want ErrInvalid", err)
	}
	if n := countWhere(t, db, "task_events", "task_id = ? AND kind = 'edited'", task.ID); n != 1 {
		t.Errorf("%d edited events, want 1", n)
	}
}

func TestEditWithNothingNewWritesNothing(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "same", false)
	before, _ := s.Rev(bg, "default")
	same := "same"
	if _, err := s.Edit(bg, "default", task.ID, Edit{Title: &same}, human); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.Rev(bg, "default"); after != before {
		t.Errorf("revision moved from %d to %d for an edit that changed nothing", before, after)
	}
}

// column lists the ids of a column, top to bottom.
func column(t *testing.T, s *Store, st Status) []int64 {
	t.Helper()
	ts, err := s.List(bg, "default", Filter{Statuses: []Status{st}}, human)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, task := range ts {
		out = append(out, task.ID)
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMovePlacesCards(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b, c := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true)
	move := func(id int64, p Placement) {
		t.Helper()
		if _, err := s.Move(bg, "default", id, StatusReady, p, human); err != nil {
			t.Fatalf("move #%d %+v: %v", id, p, err)
		}
	}
	steps := []struct {
		id   int64
		p    Placement
		want []int64
	}{
		{c.ID, Placement{Top: true}, []int64{c.ID, a.ID, b.ID}},
		{c.ID, Placement{Bottom: true}, []int64{a.ID, b.ID, c.ID}},
		{c.ID, Placement{Before: b.ID}, []int64{a.ID, c.ID, b.ID}},
		{a.ID, Placement{After: b.ID}, []int64{c.ID, b.ID, a.ID}},
	}
	for i, st := range steps {
		move(st.id, st.p)
		if got := column(t, s, StatusReady); !sameIDs(got, st.want) {
			t.Fatalf("step %d: %v, want %v", i, got, st.want)
		}
	}
}

func TestMoveToAnotherColumnPlacesByDefault(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false)
	for _, task := range []Task{a, b} { // a first, then b: a queue in Ready
		if _, err := s.Move(bg, "default", task.ID, StatusReady, Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	if got := column(t, s, StatusReady); !sameIDs(got, []int64{a.ID, b.ID}) {
		t.Errorf("ready is a queue: %v", got)
	}
	for _, task := range []Task{a, b} { // a first, then b: newest first in Done
		if _, err := s.Move(bg, "default", task.ID, StatusDone, Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	if got := column(t, s, StatusDone); !sameIDs(got, []int64{b.ID, a.ID}) {
		t.Errorf("done is newest first: %v", got)
	}
}

func TestMoveRenumbersAColumnWithNoRoom(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, b, c := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true)
	// Squeeze a and b to neighbouring positions: there is no integer between them.
	if _, err := db.Exec(`UPDATE tasks SET position = 10 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET position = 11 WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Move(bg, "default", c.ID, StatusReady, Placement{After: a.ID}, human); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, StatusReady); !sameIDs(got, []int64{a.ID, c.ID, b.ID}) {
		t.Errorf("order after renumbering: %v", got)
	}
	ts, _ := s.List(bg, "default", Filter{Statuses: []Status{StatusReady}}, human)
	for i := 1; i < len(ts); i++ {
		if ts[i].Position <= ts[i-1].Position {
			t.Errorf("positions are not strictly increasing: %d then %d", ts[i-1].Position, ts[i].Position)
		}
	}
}

func TestMoveRefusals(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", false)
	cases := []struct {
		name string
		to   Status
		p    Placement
		id   int64
		want error
	}{
		{"before itself", StatusReady, Placement{Before: a.ID}, a.ID, ErrInvalid},
		{"before a card of another column", StatusReady, Placement{Before: b.ID}, a.ID, ErrInvalid},
		{"two placements", StatusReady, Placement{Top: true, Bottom: true}, a.ID, ErrInvalid},
		{"archived is not a board column", StatusArchived, Placement{}, a.ID, ErrInvalid},
		{"unknown task", StatusReady, Placement{}, 99999, ErrNotFound},
	}
	for _, c := range cases {
		if _, err := s.Move(bg, "default", c.id, c.to, c.p, human); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

// The brief's property test, with its seed printed when it fails. (A model of the columns that is
// compared with the board after every move is in ops_place_test.go.)
func TestAnyOrderOfMovesKeepsATotalOrder(t *testing.T) {
	s, _, _ := newTestStore(t)
	var all []Task
	for i := 0; i < 12; i++ {
		all = append(all, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), i%2 == 0))
	}
	const seed = 7
	r := rand.New(rand.NewSource(seed))
	for step := 0; step < 300; step++ {
		task := all[r.Intn(len(all))]
		to := BoardStatuses[r.Intn(len(BoardStatuses))]
		var p Placement
		switch r.Intn(4) {
		case 0:
			p.Top = true
		case 1:
			p.Bottom = true
		default:
			var others []int64
			for _, id := range column(t, s, to) {
				if id != task.ID {
					others = append(others, id)
				}
			}
			switch {
			case len(others) == 0:
				p.Top = true
			case r.Intn(2) == 0:
				p.Before = others[r.Intn(len(others))]
			default:
				p.After = others[r.Intn(len(others))]
			}
		}
		if _, err := s.Move(bg, "default", task.ID, to, p, human); err != nil {
			t.Fatalf("seed %d, step %d: move #%d to %s %+v: %v", seed, step, task.ID, to, p, err)
		}
		seen := 0
		for _, st := range BoardStatuses {
			ts, err := s.List(bg, "default", Filter{Statuses: []Status{st}}, human)
			if err != nil {
				t.Fatal(err)
			}
			seen += len(ts)
			for i := 1; i < len(ts); i++ {
				if ts[i].Position <= ts[i-1].Position {
					t.Fatalf("seed %d, step %d: column %s has positions %d then %d", seed, step, st, ts[i-1].Position, ts[i].Position)
				}
			}
		}
		if seen != len(all) {
			t.Fatalf("seed %d, step %d: %d cards on the board, want %d", seed, step, seen, len(all))
		}
	}
}

func TestApproveMovesInboxTasksToReadyAllOrNothing(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false)
	ready := mustAdd(t, s, "default", "already", true)
	got, err := s.Approve(bg, "default", []int64{a.ID, b.ID}, false, human)
	if err != nil || len(got) != 2 || got[0].Status != StatusReady || got[1].Status != StatusReady {
		t.Fatalf("approve: %+v, %v", got, err)
	}
	if ids := column(t, s, StatusReady); !sameIDs(ids, []int64{ready.ID, a.ID, b.ID}) {
		t.Errorf("approved cards go to the bottom of Ready: %v", ids)
	}
	c, d := mustAdd(t, s, "default", "c", false), mustAdd(t, s, "default", "d", false)
	if _, err := s.Approve(bg, "default", []int64{c.ID, ready.ID, d.ID}, false, human); !errors.Is(err, ErrInvalid) {
		t.Fatalf("approving a task that is not in Inbox: %v, want ErrInvalid", err)
	}
	if ids := column(t, s, StatusInbox); len(ids) != 2 {
		t.Errorf("an approval that failed must change nothing: inbox %v", ids)
	}
	top, err := s.Approve(bg, "default", []int64{c.ID}, true, human)
	if err != nil || column(t, s, StatusReady)[0] != top[0].ID {
		t.Errorf("--top: %+v, %v", top, err)
	}
	if _, err := s.Approve(bg, "default", nil, false, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("no ids: %v", err)
	}
}

func TestArchiveAndUnarchiveRestoreTheColumn(t *testing.T) {
	s, _, _ := newTestStore(t)
	done := mustAdd(t, s, "default", "done one", false)
	if _, err := s.Move(bg, "default", done.ID, StatusDone, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	arch, err := s.Archive(bg, "default", []int64{done.ID}, human)
	if err != nil || arch[0].Status != StatusArchived {
		t.Fatalf("archive: %+v, %v", arch, err)
	}
	if _, err := s.Archive(bg, "default", []int64{done.ID}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("archiving twice: %v, want ErrInvalid", err)
	}
	back, err := s.Unarchive(bg, "default", []int64{done.ID}, human)
	if err != nil || back[0].Status != StatusDone {
		t.Fatalf("unarchive restores the old column: %+v, %v", back, err)
	}
	if _, err := s.Unarchive(bg, "default", []int64{done.ID}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("unarchiving a task that is not archived: %v", err)
	}

	working := mustAdd(t, s, "default", "working", true)
	if _, err := s.Move(bg, "default", working.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive(bg, "default", []int64{working.ID}, human); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Unarchive(bg, "default", []int64{working.ID}, human); err != nil || got[0].Status != StatusReady {
		t.Errorf("an in-progress task comes back as ready (nobody holds it): %+v, %v", got, err)
	}

	for i := 0; i < 3; i++ {
		x := mustAdd(t, s, "default", fmt.Sprintf("d%d", i), false)
		if _, err := s.Move(bg, "default", x.ID, StatusDone, Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ArchiveStatus(bg, "default", StatusDone, human)
	if err != nil || n != 4 { // the three new ones and the first one, restored to Done
		t.Errorf("archive all done: %d, %v", n, err)
	}
	if _, err := s.ArchiveStatus(bg, "default", StatusArchived, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("archive-status of archived: %v", err)
	}
}

func TestOnlyTheOperatorMovesApprovesAndArchives(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	for _, a := range []Actor{bot("b"), {Kind: Capture, Name: SourceChrome}} {
		if _, err := s.Move(bg, "default", task.ID, StatusReady, Placement{}, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v Move: %v", a, err)
		}
		if _, err := s.Approve(bg, "default", []int64{task.ID}, false, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v Approve: %v", a, err)
		}
		if _, err := s.Archive(bg, "default", []int64{task.ID}, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v Archive: %v", a, err)
		}
		if _, err := s.Unarchive(bg, "default", []int64{task.ID}, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v Unarchive: %v", a, err)
		}
		if _, err := s.ArchiveStatus(bg, "default", StatusDone, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v ArchiveStatus: %v", a, err)
		}
	}
	if got, _, _ := s.Get(bg, "default", task.ID); got.Status != StatusInbox {
		t.Errorf("a refused call moved the task to %s", got.Status)
	}
}
