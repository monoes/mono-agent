package tasks

import (
	"errors"
	"testing"
	"time"
)

// Spec 4.1: next never returns Inbox, and an agent is offered only what it may claim: a Ready task, or
// an In progress task whose lease has run out. Nothing else is, whatever claim it carries.
func TestNextOffersOnlyWhatAnAgentMayClaim(t *testing.T) {
	s, db, clk := newTestStore(t)
	other := addProfile(t, db, "p2")
	// another profile has a Ready task and a stale claim, which are not this profile's to offer
	mustAdd(t, s, other, "theirs, ready", true)
	stale := mustAdd(t, s, other, "theirs, abandoned", true)
	if _, err := s.Claim(bg, other, stale.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Hour)

	inbox := mustAdd(t, s, "default", "inbox", false)
	review := mustAdd(t, s, "default", "review", false)
	done := mustAdd(t, s, "default", "done", false)
	archived := mustAdd(t, s, "default", "archived", false)
	for id, to := range map[int64]Status{review.ID: StatusReview, done.ID: StatusDone} {
		if _, err := s.Move(bg, "default", id, to, Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Archive(bg, "default", []int64{archived.ID}, human); err != nil {
		t.Fatal(err)
	}
	byHand := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", byHand.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	live := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, live.ID, "one", 0)
	// a lease that has run out on a row that is not In progress is not a claim anybody may take
	for _, id := range []int64{inbox.ID, review.ID, done.ID, archived.ID} {
		claimsStray(t, db, id, "bob", clk.t.Add(-time.Hour))
	}

	before := dumpBoard(t, db)
	if got, err := s.Next(bg, "default", bot("z"), false, 0); err != nil || got != nil {
		t.Errorf("a peek: %+v, %v, want nothing", got, err)
	}
	if got, err := s.Next(bg, "default", bot("z"), true, 0); err != nil || got != nil {
		t.Errorf("next --claim: %+v, %v, want nothing", got, err)
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("asking for the next task changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Spec 5.2: the Ready tasks, lowest position first. The order is the column's, not the order of the ids.
func TestNextTakesTheReadyColumnFromTheTop(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b, c := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true)
	if _, err := s.Move(bg, "default", c.ID, StatusReady, Placement{Top: true}, human); err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, name := range []string{"x1", "x2", "x3"} {
		got, err := s.Next(bg, "default", bot(name), true, 0)
		if err != nil || got == nil {
			t.Fatalf("%s: %+v, %v", name, got, err)
		}
		order = append(order, got.ID)
	}
	if want := []int64{c.ID, a.ID, b.ID}; !sameIDs(order, want) {
		t.Errorf("the Ready column is taken from the top: %v, want %v", order, want)
	}
	if ids := column(t, s, StatusInProgress); !sameIDs(ids, []int64{c.ID, a.ID, b.ID}) {
		t.Errorf("a claimed card goes to the bottom of In progress, so it is a queue: %v", ids)
	}
	// two tasks at one place are taken in the order of their ids
	s2, db, _ := newTestStore(t)
	first, second := seedAt(t, db, "default", "ready", 7), seedAt(t, db, "default", "ready", 7)
	for _, want := range []int64{first, second} {
		if got, err := s2.Next(bg, "default", bot("x"), true, 0); err != nil || got == nil || got.ID != want {
			t.Errorf("two cards at one place: %+v, %v, want #%d", got, err, want)
		}
	}
}

// Spec 5.2: then the stale claims, oldest lease first, and of two that ended together the lower id.
// The lease that ended first belongs to the card with the highest id here, so the two orders differ.
func TestNextTakesStaleClaimsOldestLeaseFirst(t *testing.T) {
	s, _, clk := newTestStore(t)
	x, y, z := mustAdd(t, s, "default", "x", true), mustAdd(t, s, "default", "y", true), mustAdd(t, s, "default", "z", true)
	claimsClaim(t, s, z.ID, "w3", 10*time.Minute)
	claimsClaim(t, s, x.ID, "w1", 20*time.Minute)
	claimsClaim(t, s, y.ID, "w2", 20*time.Minute)
	clk.advance(time.Hour)
	fresh := mustAdd(t, s, "default", "fresh", true)
	var taken []int64
	for _, name := range []string{"t1", "t2", "t3", "t4"} {
		got, err := s.Next(bg, "default", bot(name), true, 0)
		if err != nil || got == nil {
			t.Fatalf("%s: %+v, %v", name, got, err)
		}
		taken = append(taken, got.ID)
	}
	if want := []int64{fresh.ID, z.ID, x.ID, y.ID}; !sameIDs(taken, want) {
		t.Errorf("a Ready task, then the stale claims oldest lease first and the lower id of a tie: %v, want %v", taken, want)
	}
	if got, err := s.Next(bg, "default", bot("t5"), true, 0); err != nil || got != nil {
		t.Errorf("every claim is live again: %+v, %v", got, err)
	}
}

// A claim takes a card to the bottom of In progress; a renewal and a takeover leave it where it is.
func TestARenewalOrATakeoverKeepsTheCardsPlace(t *testing.T) {
	s, _, clk := newTestStore(t)
	a, b, c := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true)
	for _, task := range []Task{a, b, c} {
		claimsClaim(t, s, task.ID, "one", 0)
	}
	place := func() map[int64]int64 {
		t.Helper()
		out := map[int64]int64{}
		for _, id := range []int64{a.ID, b.ID, c.ID} {
			got, _, err := s.Get(bg, "default", id)
			if err != nil {
				t.Fatal(err)
			}
			out[id] = got.Position
		}
		return out
	}
	before := place()
	if !(before[a.ID] < before[b.ID] && before[b.ID] < before[c.ID]) {
		t.Fatalf("positions %v: claims in the order a, b, c should queue", before)
	}
	claimsClaim(t, s, b.ID, "one", time.Hour) // a renewal
	clk.advance(2 * time.Hour)                // every lease has run out
	claimsClaim(t, s, c.ID, "two", 0)         // a takeover
	if after := place(); after[a.ID] != before[a.ID] || after[b.ID] != before[b.ID] || after[c.ID] != before[c.ID] {
		t.Errorf("positions %v after a renewal and a takeover, want %v", after, before)
	}
}

// A peek is one read: what it says agrees with the board as it was when the read began, whatever is
// written while it runs. The operator here approves a task and moves the stale card away in the middle
// of the read; the peek must not say "nothing to do" for a board that always had something to do.
func TestAPeekIsReadInOneSnapshot(t *testing.T) {
	s, db, clk := newTestStore(t)
	task := mustAdd(t, s, "default", "abandoned", true)
	claimsClaim(t, s, task.ID, "one", 0)
	clk.advance(DefaultLease)
	calls := 0
	s.now = func() time.Time {
		calls++
		if calls == 1 { // the peek has read the profile, which opens its snapshot, and asks for the time
			if _, err := db.Exec(`UPDATE tasks SET status = 'review', claimed_by = '', claim_until = '' WHERE id = ?`, task.ID); err != nil {
				t.Error(err)
			}
			if _, err := db.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES ('default', 'new', 'ready', 1, ?, ?)`, rowTime, rowTime); err != nil {
				t.Error(err)
			}
		}
		return clk.t
	}
	got, err := s.Next(bg, "default", bot("two"), false, 0)
	if calls == 0 {
		t.Fatal("the peek never asked for the time: the test no longer writes in the middle of it")
	}
	if err != nil || got == nil || got.ID != task.ID || got.Status != StatusInProgress || got.Claim == nil || got.Claim.By != "one" || !got.Claim.Stale {
		t.Errorf("peek %+v, err %v, want the stale claim the board held when the read began", got, err)
	}
	s.now = clk.now
	if got, err := s.Next(bg, "default", bot("two"), false, 0); err != nil || got == nil || got.Title != "new" {
		t.Errorf("the next peek: %+v, %v, want the task that was approved", got, err)
	}
}

// next --claim reads the clock once, so the instant at which a claim is found stale is the one at which it
// is taken: the lease ends at 12:30:00, the first reading of the clock is that second, and every reading
// after it is a second earlier than the one before, as a wall clock that is set back would show. A claim
// that read the clock again would find the task stale and then refuse it as one that is held.
func TestNextClaimTakesWhatItFoundStaleWhateverTheClockDoesNext(t *testing.T) {
	s, db, clk := newTestStore(t)
	task := mustAdd(t, s, "default", "abandoned", true)
	claimsClaim(t, s, task.ID, "one", 0)
	end := clk.t.Add(DefaultLease)
	reads := 0
	s.now = func() time.Time {
		reads++
		return end.Add(-time.Duration(reads-1) * time.Second)
	}
	got, err := s.Next(bg, "default", bot("two"), true, 0)
	if reads < 2 {
		t.Fatalf("the clock was read %d times: the test no longer tells one reading from several", reads)
	}
	if err != nil || got == nil || got.ID != task.ID || got.Claim == nil || got.Claim.By != "two" {
		t.Fatalf("next --claim: %+v, %v, want the task that was found stale taken by two", got, err)
	}
	if got.LastEvent == nil || got.LastEvent.Kind != "reclaimed" || got.LastEvent.Actor != "two" {
		t.Errorf("last event: %+v", got.LastEvent)
	}
	if _, until := opsClaim(t, db, task.ID); until != end.Add(DefaultLease).Format(timeFmt) {
		t.Errorf("the new lease is stored as ending %q, want half an hour after the instant that was read first, %q", until, end.Add(DefaultLease).Format(timeFmt))
	}
}

// A peek needs no name, so any agent may look; the board is not a capture's to read (spec 5.1), and an
// actor that was never set is refused, as List does before anything else.
func TestAPeekNeedsNoNameButNotJustAnyActor(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "ready", true)
	for name, a := range map[string]Actor{
		"the operator": human, "a named agent": bot("b"), "an agent with no name": bot(""),
		"an agent named like a label": bot("Agent"), "the operator with a name": {Kind: Human, Name: "x"},
	} {
		if got, err := s.Next(bg, "default", a, false, 0); err != nil || got == nil || got.ID != task.ID {
			t.Errorf("%s: %+v, %v, want the Ready task", name, got, err)
		}
	}
	for name, a := range map[string]Actor{
		"the chrome capture": {Kind: Capture, Name: SourceChrome}, "an unnamed capture": {Kind: Capture},
	} {
		if got, err := s.Next(bg, "default", a, false, 0); !errors.Is(err, ErrOperatorOnly) || got != nil {
			t.Errorf("%s: %+v, %v, want ErrOperatorOnly", name, got, err)
		}
	}
	for name, a := range map[string]Actor{"the zero actor": {}, "an unknown kind": {Kind: ActorKind(99)}} {
		if got, err := s.Next(bg, "default", a, false, 0); !errors.Is(err, ErrInvalid) || got != nil {
			t.Errorf("%s: %+v, %v, want ErrInvalid", name, got, err)
		}
	}
}

// What a profile offers is its own: a claim in one profile touches nothing of another's.
func TestNextOffersAndClaimsWithinOneProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	mine, theirs := mustAdd(t, s, "default", "mine", true), mustAdd(t, s, other, "theirs", true)
	for profile, want := range map[string]int64{"default": mine.ID, other: theirs.ID} {
		if got, err := s.Next(bg, profile, bot("one"), false, 0); err != nil || got == nil || got.ID != want {
			t.Errorf("a peek in %s: %+v, %v, want #%d", profile, got, err, want)
		}
	}
	got, err := s.Next(bg, other, bot("one"), true, 0)
	if err != nil || got == nil || got.ID != theirs.ID || got.ProfileID != other {
		t.Fatalf("next --claim in the other profile: %+v, %v", got, err)
	}
	if again, err := s.Next(bg, other, bot("two"), true, 0); err != nil || again != nil {
		t.Errorf("the other profile has nothing left, and must not take this one's task: %+v, %v", again, err)
	}
	if task, _, err := s.Get(bg, "default", mine.ID); err != nil || task.Status != StatusReady || task.Claim != nil {
		t.Errorf("this profile's task: %+v, %v, want it untouched", task, err)
	}
	if rev, _ := s.Rev(bg, "default"); rev != 1 {
		t.Errorf("revision %d, want 1: a claim moves its own profile's revision only", rev)
	}
}
