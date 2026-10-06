package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNextPeeksTheTopReadyTask(t *testing.T) {
	s, _, _ := newTestStore(t)
	if got, err := s.Next(bg, "default", bot("b"), false, 0); err != nil || got != nil {
		t.Fatalf("an empty board: %+v, %v", got, err)
	}
	mustAdd(t, s, "default", "in the inbox", false) // never offered
	first := mustAdd(t, s, "default", "first", true)
	second := mustAdd(t, s, "default", "second", true)
	got, err := s.Next(bg, "default", bot("b"), false, 0)
	if err != nil || got == nil || got.ID != first.ID {
		t.Fatalf("next: %+v, %v, want #%d", got, err, first.ID)
	}
	if again, _ := s.Next(bg, "default", bot("b"), false, 0); again == nil || again.ID != first.ID || again.Status != StatusReady {
		t.Errorf("a peek claims nothing: %+v", again)
	}
	if _, err := s.Move(bg, "default", second.ID, StatusReady, Placement{Top: true}, human); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Next(bg, "default", bot("b"), false, 0); got == nil || got.ID != second.ID {
		t.Errorf("the top of the column is next: %+v", got)
	}
}

func TestNextClaimTakesItWithALease(t *testing.T) {
	s, _, c := newTestStore(t)
	task := mustAdd(t, s, "default", "work", true)
	got, err := s.Next(bg, "default", bot("claude-1"), true, 0)
	if err != nil || got == nil || got.ID != task.ID || got.Status != StatusInProgress {
		t.Fatalf("next --claim: %+v, %v", got, err)
	}
	if got.Claim == nil || got.Claim.By != "claude-1" || !got.Claim.Until.Equal(c.t.Add(DefaultLease)) || got.Claim.Stale {
		t.Errorf("claim: %+v", got.Claim)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "claimed" || got.LastEvent.Actor != "claude-1" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	if again, err := s.Next(bg, "default", bot("claude-2"), true, 0); err != nil || again != nil {
		t.Errorf("nothing is left to claim: %+v, %v", again, err)
	}
	if rev, _ := s.Rev(bg, "default"); rev != 2 {
		t.Errorf("revision %d, want 2 (the add and the claim)", rev)
	}
}

func TestClaimRefusals(t *testing.T) {
	s, _, _ := newTestStore(t)
	inbox, ready := mustAdd(t, s, "default", "inbox", false), mustAdd(t, s, "default", "ready", true)
	cases := []struct {
		name  string
		id    int64
		actor Actor
		want  error
	}{
		{"an inbox task", inbox.ID, bot("b"), ErrNotReady},
		{"an unknown task", 99999, bot("b"), ErrNotFound},
		{"the operator", ready.ID, human, ErrInvalid},
		{"an agent with no name", ready.ID, bot(""), ErrInvalid},
		{"an agent with a bad name", ready.ID, bot("bad name"), ErrInvalid},
		{"a capture", ready.ID, Actor{Kind: Capture, Name: SourceOS}, ErrInvalid},
	}
	for _, c := range cases {
		if _, err := s.Claim(bg, "default", c.id, c.actor, 0); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	held, err := s.Claim(bg, "default", ready.ID, bot("one"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Claim(bg, "default", ready.ID, bot("two"), 0)
	var ce *ClaimedError
	if !errors.Is(err, ErrClaimed) || !errors.As(err, &ce) || ce.By != "one" || !ce.Until.Equal(held.Claim.Until) {
		t.Errorf("a task another agent holds is never taken: %v", err)
	}
	manual := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", manual.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(bg, "default", manual.ID, bot("one"), 0); !errors.Is(err, ErrNotReady) {
		t.Errorf("a task the operator works on: %v, want ErrNotReady", err)
	}
}

func TestSameNameClaimRenewsButNeverShortens(t *testing.T) {
	s, _, c := newTestStore(t)
	start := c.t
	task := mustAdd(t, s, "default", "long job", true)
	if _, err := s.Claim(bg, "default", task.ID, bot("one"), 4*time.Hour); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	again, err := s.Claim(bg, "default", task.ID, bot("one"), 0) // the default lease is shorter
	if err != nil || !again.Claim.Until.Equal(start.Add(4*time.Hour)) {
		t.Fatalf("claiming again must not shorten the lease: %+v, %v", again.Claim, err)
	}
	longer, err := s.Claim(bg, "default", task.ID, bot("one"), 6*time.Hour)
	if err != nil || !longer.Claim.Until.Equal(c.t.Add(6*time.Hour)) {
		t.Fatalf("a longer lease extends: %+v, %v", longer.Claim, err)
	}
	capped := mustAdd(t, s, "default", "capped", true)
	got, err := s.Claim(bg, "default", capped.ID, bot("one"), 48*time.Hour)
	if err != nil || !got.Claim.Until.Equal(c.t.Add(MaxLease)) {
		t.Errorf("a lease is at most 24 hours: %+v, %v", got.Claim, err)
	}
}

func TestStaleClaimsAreTakenOver(t *testing.T) {
	s, _, c := newTestStore(t)
	task := mustAdd(t, s, "default", "abandoned", true)
	if _, err := s.Claim(bg, "default", task.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	c.advance(DefaultLease - time.Second)
	if _, err := s.Claim(bg, "default", task.ID, bot("two"), 0); !errors.Is(err, ErrClaimed) {
		t.Fatalf("a second before the lease ends the claim holds: %v", err)
	}
	if cn, _ := s.Counts(bg, "default"); cn.Stale != 0 {
		t.Errorf("stale claims a second before the end: %d", cn.Stale)
	}
	c.advance(time.Second) // exactly at the lease's end
	if cn, _ := s.Counts(bg, "default"); cn.Stale != 1 {
		t.Errorf("a claim that expires exactly now is stale: %d", cn.Stale)
	}
	ready := mustAdd(t, s, "default", "fresh", true)
	if got, _ := s.Next(bg, "default", bot("two"), false, 0); got == nil || got.ID != ready.ID {
		t.Fatalf("a ready task comes before a stale claim: %+v", got)
	}
	if _, err := s.Claim(bg, "default", ready.ID, bot("two"), 0); err != nil {
		t.Fatal(err)
	}
	took, err := s.Next(bg, "default", bot("three"), true, 0)
	if err != nil || took == nil || took.ID != task.ID || took.Claim.By != "three" {
		t.Fatalf("a stale claim is taken over by next: %+v, %v", took, err)
	}
	if took.LastEvent == nil || took.LastEvent.Kind != "reclaimed" {
		t.Errorf("last event: %+v", took.LastEvent)
	}
}

func TestCommentRenewsButNeverShortens(t *testing.T) {
	s, _, c := newTestStore(t)
	start := c.t
	long := mustAdd(t, s, "default", "long", true)
	if _, err := s.Claim(bg, "default", long.ID, bot("one"), 4*time.Hour); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	got, err := s.Comment(bg, "default", long.ID, "still going", bot("one"))
	if err != nil || !got.Claim.Until.Equal(start.Add(4*time.Hour)) {
		t.Fatalf("a comment must not shorten a four hour lease: %+v, %v", got.Claim, err)
	}
	short := mustAdd(t, s, "default", "short", true)
	if _, err := s.Claim(bg, "default", short.ID, bot("two"), 0); err != nil {
		t.Fatal(err)
	}
	c.advance(25 * time.Minute)
	got, err = s.Comment(bg, "default", short.ID, "progress", bot("two"))
	if err != nil || !got.Claim.Until.Equal(c.t.Add(DefaultLease)) {
		t.Fatalf("a comment renews the default lease: %+v, %v", got.Claim, err)
	}
}

func TestCommentRules(t *testing.T) {
	s, _, _ := newTestStore(t)
	held := mustAdd(t, s, "default", "held", true)
	inbox := mustAdd(t, s, "default", "inbox", false)
	ready := mustAdd(t, s, "default", "ready", true)
	if _, err := s.Claim(bg, "default", held.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Comment(bg, "default", held.ID, "not mine", bot("two")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("another agent: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Comment(bg, "default", ready.ID, "not claimed", bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a task nobody holds: %v, want ErrNotClaimant", err)
	}
	for _, id := range []int64{held.ID, inbox.ID} {
		if _, err := s.Comment(bg, "default", id, "from the operator", human); err != nil {
			t.Errorf("the operator comments on #%d: %v", id, err)
		}
	}
	if _, err := s.Comment(bg, "default", held.ID, " \x1b ", bot("one")); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty comment: %v", err)
	}
	if _, err := s.Comment(bg, "default", held.ID, "hi", Actor{Kind: Capture, Name: SourceOS}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a capture: %v", err)
	}
	if _, err := s.Comment(bg, "default", held.ID, "red \x1b[31mtext", bot("one")); err != nil {
		t.Fatal(err)
	}
	_, events, _ := s.Get(bg, "default", held.ID)
	last := events[len(events)-1]
	if last.Kind != "comment" || strings.ContainsRune(last.Note, 0x1b) || last.Actor != "one" {
		t.Errorf("last event: %+v", last)
	}
}

func TestEventCapRefusesCommentsOnly(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "chatty", true)
	if _, err := s.Claim(bg, "default", task.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	have := countWhere(t, db, "task_events", "task_id = ?", task.ID)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := have; i < MaxEventsPerTask; i++ {
		if _, err := tx.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, 'one', 'comment')`, task.ID, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Comment(bg, "default", task.ID, "one more", bot("one")); !errors.Is(err, ErrLimit) {
		t.Fatalf("the 501st event as a comment: %v, want ErrLimit", err)
	}
	got, err := s.Finish(bg, "default", task.ID, Outcome{Result: "done anyway"}, bot("one"))
	if err != nil || got.Status != StatusReview {
		t.Fatalf("a change of state is always recorded: %+v, %v", got, err)
	}
	if n := countWhere(t, db, "task_events", "task_id = ?", task.ID); n != MaxEventsPerTask+1 {
		t.Errorf("%d events", n)
	}
}

func TestFinishHandsTheTaskBack(t *testing.T) {
	s, _, _ := newTestStore(t)
	older := mustAdd(t, s, "default", "older review", true)
	task := mustAdd(t, s, "default", "job", true)
	for _, id := range []int64{older.ID, task.ID} {
		if _, err := s.Claim(bg, "default", id, bot("one"), 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Finish(bg, "default", older.ID, Outcome{Result: "ok"}, bot("one")); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]Outcome{"both": {Result: "r", Question: "q"}, "neither": {}, "blank": {Result: " \x1b "}} {
		if _, err := s.Finish(bg, "default", task.ID, o, bot("one")); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "r"}, bot("two")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("another agent: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "r"}, human); !errors.Is(err, ErrInvalid) {
		t.Errorf("the operator does not finish: %v", err)
	}
	got, err := s.Finish(bg, "default", task.ID, Outcome{Question: "which database?"}, bot("one"))
	if err != nil || got.Status != StatusReview || got.Claim != nil {
		t.Fatalf("finish with a question: %+v, %v", got, err)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "question" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	if ids := column(t, s, StatusReview); !sameIDs(ids, []int64{task.ID, older.ID}) {
		t.Errorf("review is newest first: %v", ids)
	}
	if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "again"}, bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a task that is no longer in progress: %v", err)
	}
}

func TestReleaseGivesTheTaskBackBehindTheOthers(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "needs the vpn", true)
	other := mustAdd(t, s, "default", "other", true)
	if _, err := s.Claim(bg, "default", task.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Release(bg, "default", task.ID, "x", bot("two")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("another agent: %v", err)
	}
	got, err := s.Release(bg, "default", task.ID, "needs the VPN", bot("one"))
	if err != nil || got.Status != StatusReady || got.Claim != nil {
		t.Fatalf("release: %+v, %v", got, err)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "released" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	if ids := column(t, s, StatusReady); !sameIDs(ids, []int64{other.ID, task.ID}) {
		t.Errorf("a released task goes behind the others, so the agent that gave it up is not offered it again: %v", ids)
	}
	if next, _ := s.Next(bg, "default", bot("one"), false, 0); next == nil || next.ID != other.ID {
		t.Errorf("next: %+v", next)
	}
}

func TestTheOperatorMovingAHeldCardEndsTheClaim(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "held", true)
	if _, err := s.Claim(bg, "default", task.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Move(bg, "default", task.ID, StatusInProgress, Placement{Top: true}, human); err != nil || got.Claim == nil || got.Claim.By != "one" {
		t.Fatalf("reordering a held card keeps the claim: %+v, %v", got, err)
	}
	got, err := s.Move(bg, "default", task.ID, StatusReady, Placement{}, human)
	if err != nil || got.Status != StatusReady || got.Claim != nil {
		t.Fatalf("moving it out: %+v, %v", got, err)
	}
	_, events, _ := s.Get(bg, "default", task.ID)
	n := len(events)
	if events[n-2].Kind != "released" || events[n-1].Kind != "moved" {
		t.Errorf("the claim's end is recorded before the move: %+v", events[n-2:])
	}
}
