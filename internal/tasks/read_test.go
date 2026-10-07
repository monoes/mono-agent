package tasks

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// seedRow inserts a row of the given status straight into the table.
func seedRow(t *testing.T, db *sql.DB, profile, status string) int64 {
	t.Helper()
	id, err := insertRow(db, profile, status, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGetReturnsEventsOldestFirstAndNeverNil(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind, note) VALUES (?, ?, 'bot', 'comment', 'later')`, task.ID, rowTime); err != nil {
		t.Fatal(err)
	}
	got, events, err := s.Get(bg, "default", task.ID)
	if err != nil || got.ID != task.ID {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if len(events) != 2 || events[0].Kind != "created" || events[1].Kind != "comment" || events[1].Note != "later" {
		t.Errorf("events: %+v", events)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "comment" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	_, none, err := s.Get(bg, "default", seedRow(t, db, "default", "inbox"))
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("events of a task with none: %v, %v", none, err)
	}
	if _, _, err := s.Get(bg, "default", 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v, want ErrNotFound", err)
	}
}

func TestListDefaultsDependOnTheCaller(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, st := range []string{"inbox", "ready", "in_progress", "review", "done", "archived"} {
		seedRow(t, db, "default", st)
	}
	names := func(f Filter, a Actor) string {
		ts, err := s.List(bg, "default", f, a)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range ts {
			out = append(out, string(task.Status))
		}
		return strings.Join(out, ",")
	}
	if got := names(Filter{}, human); got != "inbox,ready,in_progress,review,done" {
		t.Errorf("the operator sees %q", got)
	}
	if got := names(Filter{}, bot("b")); got != "ready,in_progress,review" {
		t.Errorf("an agent sees %q", got)
	}
	if got := names(Filter{Statuses: []Status{StatusInbox, StatusArchived}}, bot("b")); got != "inbox,archived" {
		t.Errorf("naming statuses overrides the default, for an agent too: %q", got)
	}
}

func TestListFilters(t *testing.T) {
	s, db, c := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "from chrome", SourceKind: SourceChrome}, chrome); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, "default", "manual", false)
	past := c.t.Add(-time.Minute).Format(timeFmt)
	future := c.t.Add(time.Hour).Format(timeFmt)
	stale, live := seedRow(t, db, "default", "in_progress"), seedRow(t, db, "default", "in_progress")
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'old', claim_until = ? WHERE id = ?`, past, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'live', claim_until = ? WHERE id = ?`, future, live); err != nil {
		t.Fatal(err)
	}
	only := func(f Filter) []Task {
		ts, err := s.List(bg, "default", f, human)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	if ts := only(Filter{Source: SourceChrome}); len(ts) != 1 || ts[0].Title != "from chrome" {
		t.Errorf("source filter: %+v", ts)
	}
	if ts := only(Filter{ClaimedBy: "live"}); len(ts) != 1 || ts[0].ID != live || ts[0].Claim == nil || ts[0].Claim.Stale {
		t.Errorf("claimed-by filter: %+v", ts)
	}
	if ts := only(Filter{Stale: true}); len(ts) != 1 || ts[0].ID != stale || !ts[0].Claim.Stale {
		t.Errorf("stale filter: %+v", ts)
	}
	if ts := only(Filter{Limit: 2}); len(ts) != 2 {
		t.Errorf("limit: %d tasks", len(ts))
	}
	if _, err := s.List(bg, "default", Filter{Source: "pigeon"}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown source: %v", err)
	}
	if _, err := s.List(bg, "default", Filter{Statuses: []Status{"bogus"}}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown status: %v", err)
	}
}

func TestListOrdersByColumnThenPosition(t *testing.T) {
	s, _, _ := newTestStore(t)
	r1 := mustAdd(t, s, "default", "r1", true)
	i1 := mustAdd(t, s, "default", "i1", false)
	r2 := mustAdd(t, s, "default", "r2", true)
	i2 := mustAdd(t, s, "default", "i2", false)
	ts, err := s.List(bg, "default", Filter{}, human)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, task := range ts {
		got = append(got, task.ID)
	}
	want := []int64{i2.ID, i1.ID, r1.ID, r2.ID} // inbox newest first, then the ready queue
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBoardCarriesFiveColumnsCountsAndTheRevision(t *testing.T) {
	s, db, c := newTestStore(t)
	empty, err := s.Board(bg, "default", 0, human)
	if err != nil || empty.Profile.ID != "default" || empty.Rev != 0 {
		t.Fatalf("empty board: %+v, %v", empty, err)
	}
	raw, _ := json.Marshal(empty)
	for _, col := range []string{"inbox", "ready", "in_progress", "review", "done"} {
		if !strings.Contains(string(raw), `"`+col+`":[]`) {
			t.Errorf("column %s is not an empty array in %s", col, raw)
		}
	}

	mustAdd(t, s, "default", "i1", false)
	mustAdd(t, s, "default", "i2", false)
	mustAdd(t, s, "default", "r1", true)
	for i := 0; i < 3; i++ {
		seedRow(t, db, "default", "done")
	}
	stale := seedRow(t, db, "default", "in_progress")
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'old', claim_until = ? WHERE id = ?`, c.t.Add(-time.Minute).Format(timeFmt), stale); err != nil {
		t.Fatal(err)
	}
	b, err := s.Board(bg, "default", 2, human)
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Inbox: 2, Ready: 1, InProgress: 1, Done: 3, Stale: 1}
	if b.Counts != want {
		t.Errorf("counts %+v, want %+v", b.Counts, want)
	}
	if len(b.Tasks[StatusDone]) != 2 || len(b.Tasks[StatusInbox]) != 2 || len(b.Tasks[StatusReady]) != 1 {
		t.Errorf("columns: done %d inbox %d ready %d", len(b.Tasks[StatusDone]), len(b.Tasks[StatusInbox]), len(b.Tasks[StatusReady]))
	}
	if b.Rev != 3 {
		t.Errorf("revision %d, want 3 (one per add)", b.Rev)
	}
}

func TestEveryReadIsScopedToItsProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	mine := mustAdd(t, s, "default", "mine", true)
	theirs := mustAdd(t, s, other, "theirs", true)
	if _, _, err := s.Get(bg, "default", theirs.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another profile's task by id: %v, want ErrNotFound", err)
	}
	if _, _, err := s.Get(bg, other, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another profile's task by id: %v, want ErrNotFound", err)
	}
	if ts, _ := s.List(bg, "default", Filter{}, human); len(ts) != 1 || ts[0].ID != mine.ID {
		t.Errorf("list: %+v", ts)
	}
	if b, _ := s.Board(bg, other, 0, human); len(b.Tasks[StatusReady]) != 1 || b.Tasks[StatusReady][0].ID != theirs.ID {
		t.Errorf("board of the other profile: %+v", b.Tasks)
	}
	if cn, _ := s.Counts(bg, "default"); cn.Ready != 1 {
		t.Errorf("counts: %+v", cn)
	}
	if _, err := s.Board(bg, "no-such-profile", 0, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("board of an unknown profile: %v, want ErrInvalid", err)
	}
}

func TestRevMovesOnWritesNotOnReads(t *testing.T) {
	s, _, _ := newTestStore(t)
	rev := func() int64 {
		r, err := s.Rev(bg, "default")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if rev() != 0 {
		t.Fatal("a new board starts at revision 0")
	}
	task := mustAdd(t, s, "default", "a", false)
	_, _, _ = s.Get(bg, "default", task.ID)
	_, _ = s.List(bg, "default", Filter{}, human)
	_, _ = s.Board(bg, "default", 0, human)
	if rev() != 1 {
		t.Errorf("revision %d after one add and some reads, want 1", rev())
	}
	mustAdd(t, s, "default", "b", false)
	if rev() != 2 {
		t.Errorf("revision %d after two adds, want 2", rev())
	}
}
