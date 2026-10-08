package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// seedAt inserts a row of the given status and position straight into the table.
func seedAt(t *testing.T, db *sql.DB, profile, status string, position int64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES (?, 'x', ?, ?, ?, ?)`,
		profile, status, position, rowTime, rowTime)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// holdUntil makes the task an in-progress one that "bot" holds until the given time.
func holdUntil(t *testing.T, db *sql.DB, id int64, until time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET status = 'in_progress', claimed_by = 'bot', claim_until = ? WHERE id = ?`, until.Format(timeFmt), id); err != nil {
		t.Fatal(err)
	}
}

func idsOf(ts []Task) []int64 {
	ids := make([]int64, 0, len(ts))
	for _, task := range ts {
		ids = append(ids, task.ID)
	}
	return ids
}

// A lease that ends exactly now has ended (spec 4.2). The count and the filter each write the
// comparison on their own, and the task's own flag a third time.
func TestAClaimThatEndsExactlyNowIsStaleInTheCountAndTheFilter(t *testing.T) {
	for _, k := range []struct {
		name  string
		until time.Duration // from now
		stale int           // the stale claims there are
	}{
		{"a second ago", -time.Second, 1},
		{"exactly now", 0, 1},
		{"a second from now", time.Second, 0},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, c := newTestStore(t)
			holdUntil(t, db, seedRow(t, db, "default", "in_progress"), c.t.Add(k.until))
			if cn, err := s.Counts(bg, "default"); err != nil || cn != (Counts{InProgress: 1, Stale: k.stale}) {
				t.Errorf("counts %+v, err %v, want 1 in progress and %d stale", cn, err, k.stale)
			}
			b, err := s.Board(bg, "default", 0, human)
			if err != nil || b.Counts.Stale != k.stale {
				t.Errorf("the board counts %d stale (err %v), want %d", b.Counts.Stale, err, k.stale)
			}
			ts, err := s.List(bg, "default", Filter{Stale: true}, human)
			if err != nil || len(ts) != k.stale {
				t.Errorf("the stale filter lists %d tasks (err %v), want %d", len(ts), err, k.stale)
			}
			all, err := s.List(bg, "default", Filter{}, human)
			if err != nil || len(all) != 1 || all[0].Claim == nil || all[0].Claim.Stale != (k.stale == 1) {
				t.Errorf("the claim in a list: %+v (err %v), want stale %v", all, err, k.stale == 1)
			}
		})
	}
}

// Spec 4.2: a stale claim is an in-progress task that somebody holds and whose lease has run out,
// in this profile. An empty claim_until sorts before every time, so a task nobody holds would pass
// the lease test alone.
func TestOnlyAHeldInProgressTaskOfTheProfileIsStale(t *testing.T) {
	s, db, c := newTestStore(t)
	other := addProfile(t, db, "p2")
	past := c.t.Add(-time.Hour)
	held := seedRow(t, db, "default", "in_progress")
	holdUntil(t, db, held, past)
	seedRow(t, db, "default", "in_progress") // the operator moved it by hand: nobody holds it
	left := seedRow(t, db, "default", "review")
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'bot', claim_until = ? WHERE id = ?`, past.Format(timeFmt), left); err != nil {
		t.Fatal(err)
	}
	holdUntil(t, db, seedRow(t, db, other, "in_progress"), past)

	if cn, err := s.Counts(bg, "default"); err != nil || cn != (Counts{InProgress: 2, Review: 1, Stale: 1}) {
		t.Errorf("counts %+v, err %v, want 2 in progress, 1 in review and 1 stale", cn, err)
	}
	if ts, err := s.List(bg, "default", Filter{Stale: true}, human); err != nil || !slices.Equal(idsOf(ts), []int64{held}) {
		t.Errorf("the stale filter lists %v (err %v), want only #%d", idsOf(ts), err, held)
	}
	if cn, err := s.Counts(bg, other); err != nil || cn != (Counts{InProgress: 1, Stale: 1}) {
		t.Errorf("counts of the other profile %+v, err %v", cn, err)
	}
}

// Spec 4.2 defines a stale claim as an in-progress one. A claim left on a task in another column
// (nothing in the store leaves one there) stays visible but is not stale, so that the flag of the
// card agrees with the count and the filter, which carry the status, in every read that shows a card.
func TestAClaimPastItsLeaseIsStaleOnlyOnAnInProgressTask(t *testing.T) {
	s, db, c := newTestStore(t)
	past := c.t.Add(-time.Hour).Format(timeFmt)
	ids := map[int64]string{}
	for _, st := range []string{"inbox", "ready", "in_progress", "review", "done", "archived"} {
		id := seedRow(t, db, "default", st)
		if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'bot', claim_until = ? WHERE id = ?`, past, id); err != nil {
			t.Fatal(err)
		}
		ids[id] = st
	}
	check := func(read string, task Task) {
		t.Helper()
		st := ids[task.ID]
		if task.Claim == nil || task.Claim.Stale != (st == "in_progress") {
			t.Errorf("%s: the claim past its lease of a task in %s is %+v, want it shown, and stale only in progress", read, st, task.Claim)
		}
	}
	for id := range ids {
		got, _, err := s.Get(bg, "default", id)
		if err != nil {
			t.Fatal(err)
		}
		check("get", got)
	}
	all, err := s.List(bg, "default", Filter{Statuses: []Status{StatusInbox, StatusReady, StatusInProgress, StatusReview, StatusDone, StatusArchived}}, human)
	if err != nil || len(all) != len(ids) {
		t.Fatalf("list: %d tasks (err %v), want %d", len(all), err, len(ids))
	}
	for _, task := range all {
		check("list", task)
	}
	b, err := s.Board(bg, "default", 0, human)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range b.Tasks {
		for _, task := range column {
			check("board", task)
		}
	}
	if b.Counts.Stale != 1 {
		t.Errorf("the board counts %d stale claims, want the one in progress", b.Counts.Stale)
	}
}

func TestCountsHaveAFieldPerColumnAndSkipTheArchive(t *testing.T) {
	s, db, _ := newTestStore(t)
	for i, st := range []string{"inbox", "ready", "in_progress", "review", "done", "archived"} {
		for n := 0; n <= i; n++ { // 1, 2, 3, 4, 5 and 6 cards
			seedRow(t, db, "default", st)
		}
	}
	want := Counts{Inbox: 1, Ready: 2, InProgress: 3, Review: 4, Done: 5}
	if got, err := s.Counts(bg, "default"); err != nil || got != want {
		t.Errorf("counts %+v, err %v, want %+v", got, err, want)
	}
	b, err := s.Board(bg, "default", 0, human)
	if err != nil || b.Counts != want {
		t.Fatalf("board counts %+v, err %v, want %+v", b.Counts, err, want)
	}
	for st, n := range map[Status]int{StatusInbox: 1, StatusReady: 2, StatusInProgress: 3, StatusReview: 4, StatusDone: 5} {
		if len(b.Tasks[st]) != n {
			t.Errorf("column %s holds %d cards, want %d", st, len(b.Tasks[st]), n)
		}
	}
	if len(b.Tasks) != 5 {
		t.Errorf("the board has %d columns, want the five (the archive is not one)", len(b.Tasks))
	}
}

func TestBoardColumnsAreInOrderAndOnlyDoneIsCut(t *testing.T) {
	s, db, _ := newTestStore(t)
	i1, i2, i3 := mustAdd(t, s, "default", "i1", false).ID, mustAdd(t, s, "default", "i2", false).ID, mustAdd(t, s, "default", "i3", false).ID
	r1, r2, r3 := mustAdd(t, s, "default", "r1", true).ID, mustAdd(t, s, "default", "r2", true).ID, mustAdd(t, s, "default", "r3", true).ID
	var done []int64 // positions 50, 40, 30, 20, 10: the card at the top of the column has the lowest
	for _, pos := range []int64{50, 40, 30, 20, 10} {
		done = append(done, seedAt(t, db, "default", "done", pos))
	}
	top := []int64{done[4], done[3], done[2], done[1], done[0]}
	for _, k := range []struct{ limit, shown int }{{0, 5}, {-1, 5}, {1, 1}, {2, 2}, {4, 4}, {5, 5}, {6, 5}} {
		b, err := s.Board(bg, "default", k.limit, human)
		if err != nil {
			t.Fatal(err)
		}
		if got := idsOf(b.Tasks[StatusDone]); !slices.Equal(got, top[:k.shown]) {
			t.Errorf("done limit %d: %v, want the top %d: %v", k.limit, got, k.shown, top[:k.shown])
		}
		if b.Counts.Done != 5 {
			t.Errorf("done limit %d: %d done in the counts, want 5 (the limit cuts the column, not the count)", k.limit, b.Counts.Done)
		}
		if got := idsOf(b.Tasks[StatusInbox]); !slices.Equal(got, []int64{i3, i2, i1}) {
			t.Errorf("done limit %d: inbox %v, want every card, newest first: %v", k.limit, got, []int64{i3, i2, i1})
		}
		if got := idsOf(b.Tasks[StatusReady]); !slices.Equal(got, []int64{r1, r2, r3}) {
			t.Errorf("done limit %d: ready %v, want every card, in queue order: %v", k.limit, got, []int64{r1, r2, r3})
		}
	}
}

func TestListOrdersEveryColumnThenPositionThenID(t *testing.T) {
	s, db, _ := newTestStore(t)
	// Made in reverse board order, so that the order of the ids is not the order of the board.
	arch := seedAt(t, db, "default", "archived", 5)
	done := seedAt(t, db, "default", "done", 5)
	review := seedAt(t, db, "default", "review", 5)
	progress := seedAt(t, db, "default", "in_progress", 5)
	a := seedAt(t, db, "default", "ready", 30)
	b := seedAt(t, db, "default", "ready", 10)
	c := seedAt(t, db, "default", "ready", 20)
	d := seedAt(t, db, "default", "ready", 10) // the same position as b: the lower id first
	inbox := seedAt(t, db, "default", "inbox", 5)
	asked := []Status{StatusArchived, StatusDone, StatusReview, StatusInProgress, StatusReady, StatusInbox}
	ts, err := s.List(bg, "default", Filter{Statuses: asked}, human)
	want := []int64{inbox, b, d, c, a, progress, review, done, arch}
	if err != nil || !slices.Equal(idsOf(ts), want) {
		t.Errorf("order %v (err %v), want %v", idsOf(ts), err, want)
	}
}

func TestListStopsAtTheLimitItIsGiven(t *testing.T) {
	s, db, _ := newTestStore(t)
	seedTasks(t, db, "default", MaxListLimit+1)
	for _, k := range []struct{ limit, shown int }{
		{0, DefaultListLimit}, {-1, DefaultListLimit}, {1, 1}, {DefaultListLimit + 1, DefaultListLimit + 1},
		{MaxListLimit, MaxListLimit}, {MaxListLimit + 1, MaxListLimit}, {1 << 30, MaxListLimit},
	} {
		if ts, err := s.List(bg, "default", Filter{Limit: k.limit}, human); err != nil || len(ts) != k.shown {
			t.Errorf("limit %d: %d tasks (err %v), want %d", k.limit, len(ts), err, k.shown)
		}
	}
	if ts, _ := s.List(bg, "default", Filter{Limit: 2}, human); len(ts) != 2 || ts[0].Title != "seed 0" || ts[1].Title != "seed 1" {
		t.Errorf("a limit keeps the first cards of the order: %+v", ts)
	}
}

// Who is asking is checked before what is asked, as Add does it: a capture has no read access (spec
// 5.1) and an actor that was never set is no actor. The filter is wrong in two ways in half of the
// calls, so that a refusal that came from the filter would show in the error.
func TestListRefusesAnActorThatMayNotReadBeforeLookingAtTheFilter(t *testing.T) {
	s, db, _ := newTestStore(t)
	seedRow(t, db, "default", "ready")
	wrong := Filter{Statuses: []Status{"bogus"}, Source: "pigeon"}
	for _, k := range []struct {
		name  string
		actor Actor
		want  error
		says  string
	}{
		{"a chrome capture", Actor{Kind: Capture, Name: SourceChrome}, ErrOperatorOnly, "list tasks"},
		{"an os capture", Actor{Kind: Capture, Name: SourceOS}, ErrOperatorOnly, "list tasks"},
		{"a capture with no name", Actor{Kind: Capture}, ErrOperatorOnly, "list tasks"},
		{"the zero actor", Actor{}, ErrInvalid, "unknown actor"},
		{"an actor of a kind that does not exist", Actor{Kind: 99, Name: "x"}, ErrInvalid, "unknown actor"},
	} {
		for _, f := range []Filter{{}, wrong} {
			ts, err := s.List(bg, "default", f, k.actor)
			if !errors.Is(err, k.want) || !strings.Contains(err.Error(), k.says) || ts != nil {
				t.Errorf("%s with filter %+v: %v, %v, want no tasks and an error that is %v and says %q", k.name, f, ts, err, k.want, k.says)
			}
		}
	}
	for _, a := range []Actor{human, bot("b")} {
		if ts, err := s.List(bg, "default", Filter{}, a); err != nil || len(ts) != 1 {
			t.Errorf("actor %+v: %d tasks (err %v), want the one ready task", a, len(ts), err)
		}
	}
}

// The board shows the Inbox, which agents do not read (spec 5.1), so the store itself refuses it to
// everyone but the operator, and does so first: an unknown profile and a context that has ended are
// not what the refusal says. A card of the Inbox is there to be handed over if the check were missing.
func TestBoardRefusesAnyoneButTheOperatorBeforeLookingAtAnythingElse(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustAdd(t, s, "default", "private", false)
	ended, cancel := context.WithCancel(bg)
	cancel()
	asks := []struct {
		what    string
		ctx     context.Context
		profile string
	}{
		{"its profile", bg, "default"},
		{"an unknown profile", bg, "no-such-profile"},
		{"a context that has ended", ended, "default"},
	}
	for _, k := range []struct {
		name  string
		actor Actor
	}{
		{"an agent", bot("b")},
		{"an agent with no name", Actor{Kind: Agent}},
		{"a chrome capture", Actor{Kind: Capture, Name: SourceChrome}},
		{"an os capture", Actor{Kind: Capture, Name: SourceOS}},
		{"a capture with no name", Actor{Kind: Capture}},
		{"the zero actor", Actor{}},
		{"an actor of a kind that does not exist", Actor{Kind: 99, Name: "x"}},
	} {
		for _, ask := range asks {
			b, err := s.Board(ask.ctx, ask.profile, 0, k.actor)
			if !errors.Is(err, ErrOperatorOnly) || !strings.Contains(err.Error(), "show the board") || b.Tasks != nil {
				t.Errorf("%s asking for %s: %+v, %v, want no board and an error that is ErrOperatorOnly and says \"show the board\"", k.name, ask.what, b, err)
			}
		}
	}
	for _, a := range []Actor{human, {Kind: Human, Name: "morteza"}} {
		if b, err := s.Board(bg, "default", 0, a); err != nil || len(b.Tasks[StatusInbox]) != 1 {
			t.Errorf("operator %+v: %d Inbox cards (err %v), want the one", a, len(b.Tasks[StatusInbox]), err)
		}
	}
	if _, err := s.Board(bg, "no-such-profile", 0, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("the operator asking for an unknown profile: %v, want ErrInvalid", err)
	}
}

// ParseStatus accepts "progress" and "In-Progress" as in_progress: a name that is accepted must find it.
func TestListFindsTheStatusesItAcceptsByAnyName(t *testing.T) {
	s, db, _ := newTestStore(t)
	progress := seedRow(t, db, "default", "in_progress")
	ready := seedRow(t, db, "default", "ready")
	seedRow(t, db, "default", "done")
	named := []Status{"Progress", " READY\n"}
	ts, err := s.List(bg, "default", Filter{Statuses: named}, human)
	if err != nil || !slices.Equal(idsOf(ts), []int64{ready, progress}) {
		t.Errorf("listed %v (err %v), want #%d and #%d", idsOf(ts), err, ready, progress)
	}
	if named[0] != "Progress" || named[1] != " READY\n" {
		t.Errorf("the caller's statuses became %q", named)
	}
	if _, err := s.List(bg, "default", Filter{Statuses: []Status{"in-progress", "bogus"}}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("a bogus status among good ones: %v, want ErrInvalid", err)
	}
}

// The query has a slot for each status named: a caller's list of a hundred thousand names (an agent's,
// through a tool) must not ask SQLite for a hundred thousand variables.
func TestListAsksForEachStatusOnceHoweverOftenItIsNamed(t *testing.T) {
	s, db, _ := newTestStore(t)
	ready := seedRow(t, db, "default", "ready")
	seedRow(t, db, "default", "done")
	ts, err := s.List(bg, "default", Filter{Statuses: slices.Repeat([]Status{StatusReady}, 100000)}, human)
	if err != nil || !slices.Equal(idsOf(ts), []int64{ready}) {
		t.Errorf("listed %v (err %v), want only #%d", idsOf(ts), err, ready)
	}
}

func TestEveryTaskCarriesItsOwnLastEvent(t *testing.T) {
	s, db, c := newTestStore(t)
	a := mustAdd(t, s, "default", "a", false)
	b := mustAdd(t, s, "default", "b", true)
	d := mustAdd(t, s, "default", "d", true)
	note := func(id int64, actor, kind string, after time.Duration) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, ?, ?)`, id, c.t.Add(after).Format(timeFmt), actor, kind); err != nil {
			t.Fatal(err)
		}
	}
	note(b.ID, "bob", "comment", time.Minute)
	note(d.ID, "amy", "comment", 2*time.Minute)
	note(d.ID, "amy", "result", 3*time.Minute)
	want := map[int64]LastEvent{
		a.ID: {Actor: "you", Kind: "created", At: c.t},
		b.ID: {Actor: "bob", Kind: "comment", At: c.t.Add(time.Minute)},
		d.ID: {Actor: "amy", Kind: "result", At: c.t.Add(3 * time.Minute)},
	}
	check := func(what string, ts []Task) {
		t.Helper()
		if len(ts) != 3 {
			t.Fatalf("%s: %d tasks, want 3", what, len(ts))
		}
		for _, task := range ts {
			w, got := want[task.ID], task.LastEvent
			if got == nil || got.Actor != w.Actor || got.Kind != w.Kind || !got.At.Equal(w.At) {
				t.Errorf("%s: #%d has the last event %+v, want %+v", what, task.ID, got, w)
			}
		}
	}
	ts, err := s.List(bg, "default", Filter{}, human)
	if err != nil {
		t.Fatal(err)
	}
	check("list", ts)
	board, err := s.Board(bg, "default", 0, human)
	if err != nil {
		t.Fatal(err)
	}
	check("board", slices.Concat(board.Tasks[StatusInbox], board.Tasks[StatusReady]))
	var viaGet []Task
	for id := range want {
		task, _, err := s.Get(bg, "default", id)
		if err != nil {
			t.Fatal(err)
		}
		viaGet = append(viaGet, task)
	}
	check("get", viaGet)
}

func TestRevIsEachProfilesOwn(t *testing.T) {
	s, db, _ := newTestStore(t)
	p2, p3 := addProfile(t, db, "p2"), addProfile(t, db, "p3")
	mustAdd(t, s, "default", "a", false)
	mustAdd(t, s, p2, "b", false)
	mustAdd(t, s, p2, "c", false)
	for profile, want := range map[string]int64{"default": 1, p2: 2, p3: 0} {
		if got, err := s.Rev(bg, profile); err != nil || got != want {
			t.Errorf("revision of %s: %d (err %v), want %d", profile, got, err, want)
		}
	}
}

func TestGetShowsATaskOfAnyStatusIncludingTheArchive(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, st := range []string{"inbox", "ready", "in_progress", "review", "done", "archived"} {
		if got, _, err := s.Get(bg, "default", seedRow(t, db, "default", st)); err != nil || string(got.Status) != st {
			t.Errorf("a %s task: %+v (err %v)", st, got, err)
		}
	}
}

func TestAnEmptyListIsAnEmptyArray(t *testing.T) {
	s, _, _ := newTestStore(t)
	ts, err := s.List(bg, "default", Filter{}, human)
	raw, _ := json.Marshal(ts)
	if err != nil || ts == nil || string(raw) != "[]" {
		t.Errorf("an empty list is %s (nil %v, err %v), want []", raw, ts == nil, err)
	}
}
