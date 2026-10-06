package tasks

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Spec 4.4: a claim is refused (limit) once a task holds 2,000 events, so a loop of claims and releases
// cannot grow its history without bound. The three ways a claim is made each write an event, so each
// is held to the cap: a claim of a Ready task, a renewal by the holder, a takeover of a stale claim.
func TestAClaimIsRefusedOnceATaskHoldsTwoThousandEvents(t *testing.T) {
	branches := []struct {
		name string
		kind string // the event a claim that goes through writes
		prep func(t *testing.T, s *Store, clk *clock) (int64, Actor)
	}{
		{"a claim of a Ready task", "claimed", func(t *testing.T, s *Store, clk *clock) (int64, Actor) {
			return mustAdd(t, s, "default", "ready", true).ID, bot("two")
		}},
		{"a renewal by the holder", "claimed", func(t *testing.T, s *Store, clk *clock) (int64, Actor) {
			id := mustAdd(t, s, "default", "held", true).ID
			claimsClaim(t, s, id, "one", 0)
			return id, bot("one")
		}},
		{"a takeover of a stale claim", "reclaimed", func(t *testing.T, s *Store, clk *clock) (int64, Actor) {
			id := mustAdd(t, s, "default", "abandoned", true).ID
			claimsClaim(t, s, id, "one", 0)
			clk.advance(DefaultLease)
			return id, bot("two")
		}},
	}
	for _, b := range branches {
		for _, events := range []int{MaxEventsToClaim - 1, MaxEventsToClaim} {
			t.Run(b.name+", with "+strconv.Itoa(events)+" events", func(t *testing.T) {
				s, db, clk := newTestStore(t)
				id, who := b.prep(t, s, clk)
				claimsSeedEvents(t, db, id, events)
				before := dumpBoard(t, db)
				got, err := s.Claim(bg, "default", id, who, 0)
				if events < MaxEventsToClaim {
					if err != nil || got.LastEvent == nil || got.LastEvent.Kind != b.kind {
						t.Fatalf("the claim that makes the %dth event: %+v, %v, want it to go through as %s", MaxEventsToClaim, got.LastEvent, err, b.kind)
					}
					if n := countWhere(t, db, "task_events", "task_id = ?", id); n != MaxEventsToClaim {
						t.Errorf("%d events, want %d", n, MaxEventsToClaim)
					}
					return
				}
				if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "leave it to the operator") {
					t.Fatalf("a claim of a task with %d events: %v, want ErrLimit telling the agent to leave it to the operator", events, err)
				}
				if after := dumpBoard(t, db); after != before {
					t.Errorf("a refused claim changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
				}
			})
		}
	}
}

// The cap is the last thing a claim looks at: a task that cannot be claimed anyway says why.
func TestTheClaimCapComesAfterTheOtherRefusals(t *testing.T) {
	s, db, _ := newTestStore(t)
	inbox := mustAdd(t, s, "default", "inbox", false)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	byHand := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", byHand.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{inbox.ID, held.ID, byHand.ID} {
		claimsSeedEvents(t, db, id, MaxEventsToClaim+10)
	}
	for _, c := range []struct {
		name string
		id   int64
		want error
	}{
		{"an Inbox task", inbox.ID, ErrNotReady},
		{"a task another agent holds", held.ID, ErrClaimed},
		{"a task the operator works on", byHand.ID, ErrNotReady},
	} {
		if _, err := s.Claim(bg, "default", c.id, bot("two"), 0); !errors.Is(err, c.want) || errors.Is(err, ErrLimit) {
			t.Errorf("%s with %d events: %v, want %v", c.name, MaxEventsToClaim+10, err, c.want)
		}
	}
}

// Next passes over a task that a claim would refuse, so one such task at the head of the queue does not
// block it for ever; and a peek offers what next --claim would take, never what claim would refuse.
func TestNextPassesOverTasksThatHoldTwoThousandEvents(t *testing.T) {
	s, db, _ := newTestStore(t)
	head, behind := mustAdd(t, s, "default", "head", true), mustAdd(t, s, "default", "behind", true)
	claimsSeedEvents(t, db, head.ID, MaxEventsToClaim)
	if got, err := s.Next(bg, "default", bot("one"), false, 0); err != nil || got == nil || got.ID != behind.ID {
		t.Errorf("a peek: %+v, %v, want the task behind the one that cannot be claimed", got, err)
	}
	got, err := s.Next(bg, "default", bot("one"), true, 0)
	if err != nil || got == nil || got.ID != behind.ID {
		t.Fatalf("next --claim: %+v, %v, want the task behind the one that cannot be claimed", got, err)
	}
	rev := claimsRev(t, s)
	if got, err := s.Next(bg, "default", bot("two"), false, 0); err != nil || got != nil {
		t.Errorf("a peek with only the capped task left: %+v, %v, want nothing", got, err)
	}
	if got, err := s.Next(bg, "default", bot("two"), true, 0); err != nil || got != nil {
		t.Errorf("next --claim with only the capped task left: %+v, %v, want nothing, not an error", got, err)
	}
	if claimsRev(t, s) != rev {
		t.Error("a next that found nothing moved the revision")
	}
	if _, err := s.Claim(bg, "default", head.ID, bot("two"), 0); !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "operator") {
		t.Errorf("claiming it by id: %v, want ErrLimit and the operator", err)
	}
	if task, _, err := s.Get(bg, "default", head.ID); err != nil || task.Status != StatusReady || task.Claim != nil {
		t.Errorf("the capped task: %+v, %v, want it left in Ready", task, err)
	}
}

// The cap is exact for next as for claim: with one event fewer the task is taken, and gets its 2,000th.
func TestNextTakesATaskWithOneEventToSpare(t *testing.T) {
	s, db, _ := newTestStore(t)
	head, behind := mustAdd(t, s, "default", "head", true), mustAdd(t, s, "default", "behind", true)
	claimsSeedEvents(t, db, head.ID, MaxEventsToClaim-1)
	for _, claim := range []bool{false, true} {
		if got, err := s.Next(bg, "default", bot("one"), claim, 0); err != nil || got == nil || got.ID != head.ID {
			t.Fatalf("next (claim %v): %+v, %v, want #%d, which has room for one more event", claim, got, err, head.ID)
		}
	}
	if n := countWhere(t, db, "task_events", "task_id = ?", head.ID); n != MaxEventsToClaim {
		t.Errorf("%d events, want %d", n, MaxEventsToClaim)
	}
	if got, err := s.Next(bg, "default", bot("two"), true, 0); err != nil || got == nil || got.ID != behind.ID {
		t.Errorf("then the next: %+v, %v", got, err)
	}
}

// Stale claims are passed over too, and the next one is taken.
func TestNextPassesOverAStaleClaimThatHoldsTwoThousandEvents(t *testing.T) {
	s, db, clk := newTestStore(t)
	old, newer := mustAdd(t, s, "default", "oldest lease", true), mustAdd(t, s, "default", "newer lease", true)
	claimsClaim(t, s, old.ID, "one", 10*time.Minute)
	claimsClaim(t, s, newer.ID, "two", 20*time.Minute)
	clk.advance(time.Hour)
	claimsSeedEvents(t, db, old.ID, MaxEventsToClaim)
	if got, err := s.Next(bg, "default", bot("three"), false, 0); err != nil || got == nil || got.ID != newer.ID {
		t.Errorf("a peek: %+v, %v, want the stale claim that can be taken", got, err)
	}
	if got, err := s.Next(bg, "default", bot("three"), true, 0); err != nil || got == nil || got.ID != newer.ID || got.Claim.By != "three" {
		t.Fatalf("next --claim: %+v, %v, want the stale claim that can be taken", got, err)
	}
	if got, err := s.Next(bg, "default", bot("four"), true, 0); err != nil || got != nil {
		t.Errorf("only the capped stale claim is left: %+v, %v, want nothing", got, err)
	}
	if _, err := s.Claim(bg, "default", old.ID, bot("four"), 0); !errors.Is(err, ErrLimit) {
		t.Errorf("claiming it by id: %v, want ErrLimit", err)
	}
	if task, _, _ := s.Get(bg, "default", old.ID); task.Claim == nil || task.Claim.By != "one" {
		t.Errorf("the capped claim: %+v, want it left with its holder", task.Claim)
	}
}

// Spec 4.4: finish, release and the operator's actions are still recorded and still work. The holder of
// a task that has grown past the cap can still hand it back; only a new claim is refused.
func TestFinishReleaseAndTheOperatorStillWorkOnATaskPastTheClaimCap(t *testing.T) {
	s, db, _ := newTestStore(t)
	finishing, releasing, moving := mustAdd(t, s, "default", "finishing", true), mustAdd(t, s, "default", "releasing", true), mustAdd(t, s, "default", "moving", true)
	for _, task := range []Task{finishing, releasing, moving} {
		claimsClaim(t, s, task.ID, "one", 0)
		claimsSeedEvents(t, db, task.ID, MaxEventsToClaim+100)
	}
	if got, err := s.Finish(bg, "default", finishing.ID, Outcome{Result: "done"}, bot("one")); err != nil || got.Status != StatusReview || got.Claim != nil {
		t.Errorf("finish: %+v, %v", got, err)
	}
	if got, err := s.Release(bg, "default", releasing.ID, "no", bot("one")); err != nil || got.Status != StatusReady || got.Claim != nil {
		t.Errorf("release: %+v, %v", got, err)
	}
	if got, err := s.Move(bg, "default", moving.ID, StatusDone, Placement{}, human); err != nil || got.Status != StatusDone {
		t.Errorf("the operator's move: %+v, %v", got, err)
	}
	for _, id := range []int64{finishing.ID, releasing.ID, moving.ID} {
		if n := countWhere(t, db, "task_events", "task_id = ?", id); n < MaxEventsToClaim+101 {
			t.Errorf("task #%d holds %d events: the change was not recorded", id, n)
		}
	}
	if _, err := s.Claim(bg, "default", releasing.ID, bot("one"), 0); !errors.Is(err, ErrLimit) {
		t.Errorf("claiming the released task again: %v, want ErrLimit: it holds too many events", err)
	}
}

// A comment is refused from MaxEventsPerTask events, the operator's as well as an agent's, and the
// refusal tells the agent what to do instead; a change of state is not refused (TestEventCapRefusesCommentsOnly).
func TestACommentIsRefusedFromFiveHundredEvents(t *testing.T) {
	for _, who := range []struct {
		name  string
		actor Actor
	}{{"an agent", bot("one")}, {"the operator", human}} {
		t.Run(who.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			task := mustAdd(t, s, "default", "chatty", true)
			claimsClaim(t, s, task.ID, "one", 0)
			claimsSeedEvents(t, db, task.ID, MaxEventsPerTask-1)
			if _, err := s.Comment(bg, "default", task.ID, "the last one", who.actor); err != nil {
				t.Fatalf("the comment that makes the %dth event: %v", MaxEventsPerTask, err)
			}
			before := dumpBoard(t, db)
			_, err := s.Comment(bg, "default", task.ID, "one too many", who.actor)
			if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "finish or release") {
				t.Fatalf("the %dth event as a comment: %v, want ErrLimit telling an agent to finish or release", MaxEventsPerTask+1, err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Errorf("a refused comment changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}
