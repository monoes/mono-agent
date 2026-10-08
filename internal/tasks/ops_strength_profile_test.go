package tasks

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// profileState is what a profile's board holds, as text, to compare before and after.
func profileState(t *testing.T, db *sql.DB, profile string) string {
	t.Helper()
	var sb strings.Builder
	rows, err := db.Query(`SELECT id, title, notes, status, position, claimed_by, claim_until, updated_at FROM tasks WHERE profile_id = ? ORDER BY id`, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, pos int64
		var title, notes, status, by, until, updated string
		if err := rows.Scan(&id, &title, &notes, &status, &pos, &by, &until, &updated); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&sb, id, title, notes, status, pos, by, until, updated)
	}
	return sb.String()
}

// Every verb does its work on the profile it is given when that is not `default`: the card changes,
// the cards are placed among that profile's cards, a renumber renumbers that profile's column, the
// events are written, and the revision that moves is that profile's, once, and not the default's,
// whose board stays as it was. (The default profile has a busy board here, so that a verb that
// reads or writes it by mistake shows in a number.)
func TestEveryVerbWorksOnAProfileOtherThanDefault(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	for i := 0; i < 3; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("default ready %d", i), true) // 1024, 2048, 3072
		mustAdd(t, s, "default", fmt.Sprintf("default inbox %d", i), false)
	}
	seedAt(t, db, "default", "archived", 9_000_000)
	defaultBoard, defaultRev := profileState(t, db, "default"), func() int64 { r, _ := s.Rev(bg, "default"); return r }()

	rev := func() int64 { r, _ := s.Rev(bg, other); return r }
	task := func(id int64) Task {
		t.Helper()
		got, _, err := s.Get(bg, other, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	positions := func(ts []Task) []int64 {
		var out []int64
		for _, x := range ts {
			out = append(out, x.Position)
		}
		return out
	}
	// step runs one verb and requires that it moved the revision of `other` by exactly one
	step := func(name string, call func() error) {
		t.Helper()
		before := rev()
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := rev() - before; got != 1 {
			t.Errorf("%s: the revision of the profile moved by %d, want 1", name, got)
		}
		if r, _ := s.Rev(bg, "default"); r != defaultRev {
			t.Errorf("%s: the revision of the default profile moved from %d to %d", name, defaultRev, r)
		}
	}
	t1, t2, t3, x := mustAdd(t, s, other, "t1", false), mustAdd(t, s, other, "t2", false), mustAdd(t, s, other, "t3", false), mustAdd(t, s, other, "x", false)

	title := "t1 edited"
	step("Edit", func() error { _, err := s.Edit(bg, other, t1.ID, Edit{Title: &title}, human); return err })
	if got := task(t1.ID); got.Title != title || got.LastEvent == nil || got.LastEvent.Kind != "edited" {
		t.Errorf("after Edit: %+v", got)
	}

	var approved []Task
	step("Approve", func() (err error) { approved, err = s.Approve(bg, other, []int64{t1.ID, t2.ID}, false, human); return })
	if fmt.Sprint(positions(approved)) != "[1024 2048]" {
		t.Errorf("approved cards are at %v, want [1024 2048] at the bottom of the profile's own Ready", positions(approved))
	}

	var moved Task
	step("Move After", func() (err error) {
		moved, err = s.Move(bg, other, t3.ID, StatusReady, Placement{After: t1.ID}, human)
		return
	})
	if moved.Status != StatusReady || moved.Position != 1536 {
		t.Errorf("a card placed after the first: %+v, want Ready at 1536", moved)
	}

	// squeeze the column, so that the next card placed between two renumbers it
	for id, pos := range map[int64]int64{t1.ID: 10, t3.ID: 11} {
		if _, err := db.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, pos, id); err != nil {
			t.Fatal(err)
		}
	}
	step("Move with a renumber", func() (err error) {
		moved, err = s.Move(bg, other, x.ID, StatusReady, Placement{After: t1.ID}, human)
		return
	})
	ts, err := s.List(bg, other, Filter{Statuses: []Status{StatusReady}}, human)
	if err != nil || fmt.Sprint(idsOf(ts)) != fmt.Sprint([]int64{t1.ID, x.ID, t3.ID, t2.ID}) || fmt.Sprint(positions(ts)) != "[1024 2048 3072 4096]" {
		t.Errorf("Ready after the renumber: ids %v positions %v, err %v, want t1, x, t3, t2 at 1024, 2048, 3072, 4096", idsOf(ts), positions(ts), err)
	}

	var archived []Task
	step("Archive", func() (err error) { archived, err = s.Archive(bg, other, []int64{t1.ID}, human); return })
	if archived[0].Status != StatusArchived || archived[0].Position != 1024 {
		t.Errorf("archived: %+v, want the first place of the profile's own archive", archived[0])
	}

	var n int
	step("ArchiveStatus", func() (err error) { n, err = s.ArchiveStatus(bg, other, StatusReady, human); return })
	ts, err = s.List(bg, other, Filter{Statuses: []Status{StatusArchived}}, human)
	if err != nil || n != 3 || fmt.Sprint(positions(ts)) != "[1024 2048 3072 4096]" {
		t.Errorf("the archive after ArchiveStatus: %d archived, positions %v, err %v, want 3 more cards at 2048, 3072, 4096", n, positions(ts), err)
	}

	var back []Task
	step("Unarchive", func() (err error) { back, err = s.Unarchive(bg, other, []int64{t1.ID, t2.ID}, human); return })
	if back[0].Status != StatusReady || back[1].Status != StatusReady || back[0].Position != 1024 || back[1].Position != 2048 {
		t.Errorf("unarchived: %+v and %+v, want Ready at 1024 and 2048", back[0], back[1])
	}

	for _, id := range []int64{t1.ID, t2.ID, t3.ID, x.ID} {
		if _, events, err := s.Get(bg, other, id); err != nil || len(events) < 2 {
			t.Errorf("events of #%d: %d, %v", id, len(events), err)
		}
	}
	if got := profileState(t, db, "default"); got != defaultBoard {
		t.Errorf("the default profile's board changed:\nbefore:\n%s\nafter:\n%s", defaultBoard, got)
	}
}
