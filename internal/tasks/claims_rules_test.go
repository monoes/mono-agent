package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// claimsVerb is one verb as a call: who makes it, and on which task of which profile.
type claimsVerb struct {
	name string
	run  func(profile string, id int64, a Actor) error
}

// claimsAgentVerbs are the verbs that only an agent with a name may use, with arguments that are right
// whoever makes the call.
func claimsAgentVerbs(s *Store) []claimsVerb {
	return []claimsVerb{
		{"Claim", func(p string, id int64, a Actor) error { _, err := s.Claim(bg, p, id, a, 0); return err }},
		{"Next with claim", func(p string, id int64, a Actor) error { _, err := s.Next(bg, p, a, true, 0); return err }},
		{"Finish", func(p string, id int64, a Actor) error {
			_, err := s.Finish(bg, p, id, Outcome{Result: "r"}, a)
			return err
		}},
		{"Release", func(p string, id int64, a Actor) error { _, err := s.Release(bg, p, id, "why", a); return err }},
	}
}

// Spec 5.1: the labels you, agent, capture, chrome and os are reserved, in any case, and a name is 1-64
// characters of letters, digits and . _ # @ : -. Every verb refuses an agent that has no usable name,
// whatever profile and task it names (who is asking is looked at first), and writes nothing.
func TestEveryAgentVerbRefusesAnAgentWithoutAUsableName(t *testing.T) {
	s, db, _ := newTestStore(t)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	ready := mustAdd(t, s, "default", "ready", true)
	verbs := append(claimsAgentVerbs(s), claimsVerb{"Comment", func(p string, id int64, a Actor) error {
		_, err := s.Comment(bg, p, id, "text", a)
		return err
	}})
	before := dumpBoard(t, db)
	for _, c := range []struct {
		name  string
		actor Actor
		want  string
	}{
		{"no name", bot(""), "needs a name"},
		{"a space", bot("bad name"), "letters, digits"},
		{"a name too long", bot(strings.Repeat("n", MaxNameLen+1)), "letters, digits"},
		{"an escape", bot("a\x1b[31m"), "letters, digits"},
		{"you", bot("you"), "reserved"},
		{"YOU", bot("YOU"), "reserved"},
		{"agent", bot("agent"), "reserved"},
		{"Agent", bot("Agent"), "reserved"},
		{"capture", bot("capture"), "reserved"},
		{"CAPTURE", bot("CAPTURE"), "reserved"},
		{"chrome", bot("chrome"), "reserved"},
		{"Chrome", bot("Chrome"), "reserved"},
		{"os", bot("os"), "reserved"},
		{"oS", bot("oS"), "reserved"},
	} {
		for _, v := range verbs {
			for _, id := range []int64{held.ID, ready.ID, 424242} {
				if err := v.run("default", id, c.actor); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s by an agent with %s on #%d: %v, want ErrInvalid saying %q", v.name, c.name, id, err, c.want)
				}
			}
			// before the profile: this profile does not exist and the name is what the refusal says
			if err := v.run("no-such-profile", 424242, c.actor); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s by an agent with %s, on a profile that does not exist: %v, want the refusal of the name", v.name, c.name, err)
			}
		}
		if got, err := s.Next(bg, "default", c.actor, false, 0); err != nil || got == nil || got.ID != ready.ID {
			t.Errorf("a peek by an agent with %s: %+v, %v, want the Ready task: a peek needs no name", c.name, got, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A name that holds a reserved word, or is as long as a name may be, is a name.
func TestNamesThatOnlyLookLikeReservedOnesAreNames(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, name := range []string{"you2", "my-agent", "agent-7", "agent:Claude-Code#beef", "os.bot", "chrome#1", "capture:2", "yo", strings.Repeat("n", MaxNameLen)} {
		task := mustAdd(t, s, "default", "for "+name, true)
		got, err := s.Claim(bg, "default", task.ID, bot(name), 0)
		if err != nil || got.Claim == nil || got.Claim.By != name || got.LastEvent == nil || got.LastEvent.Actor != name {
			t.Errorf("an agent named %q: %+v, %v, want the claim and its event under that name", name, got, err)
		}
	}
}

// The name is not authenticated: two agents that choose the same name are one claimant (spec 5.2). The
// name is what is written, to the letter: "One" is not "one".
func TestAClaimantIsTheNameToTheLetter(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, task.ID, "one", 0)
	if _, err := s.Claim(bg, "default", task.ID, bot("One"), 0); !errors.Is(err, ErrClaimed) {
		t.Errorf("claim by One: %v, want ErrClaimed", err)
	}
	if _, err := s.Comment(bg, "default", task.ID, "x", bot("One")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("comment by One: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "x"}, bot("One")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("finish by One: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Release(bg, "default", task.ID, "x", bot("One")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("release by One: %v, want ErrNotClaimant", err)
	}
	got, err := s.Claim(bg, "default", task.ID, bot("one"), 0) // the same name from anywhere is the same claimant
	if err != nil || got.Claim == nil || got.Claim.By != "one" {
		t.Errorf("claim again by one: %+v, %v", got, err)
	}
}

// Spec 5.1: the operator moves the cards, a capture only captures: neither claims, finishes or releases.
// Both are refused as invalid input, as who is asking comes before everything else.
func TestAgentVerbsRefuseWhoIsNotAnAgent(t *testing.T) {
	s, db, _ := newTestStore(t)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	mustAdd(t, s, "default", "ready", true)
	before := dumpBoard(t, db)
	for name, a := range map[string]Actor{
		"the operator": human, "the operator under an agent's name": {Kind: Human, Name: "one"},
		"the chrome capture": {Kind: Capture, Name: SourceChrome}, "the os capture": {Kind: Capture, Name: SourceOS},
		"an unnamed capture": {Kind: Capture}, "the zero actor": {}, "an unknown kind": {Kind: ActorKind(99)}, "a negative kind": {Kind: ActorKind(-1), Name: "one"},
	} {
		for _, v := range claimsAgentVerbs(s) {
			if err := v.run("default", held.ID, a); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrOperatorOnly) {
				t.Errorf("%s by %s: %v, want ErrInvalid", v.name, name, err)
			}
		}
		if a.Kind == Human {
			continue
		}
		if _, err := s.Comment(bg, "default", held.ID, "text", a); !errors.Is(err, ErrInvalid) {
			t.Errorf("Comment by %s: %v, want ErrInvalid", name, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The operator comments on any task, as "you", whatever name it carries, and its comment is not an
// agent's: it renews no lease.
func TestTheOperatorsCommentIsNoAgentsAndRenewsNothing(t *testing.T) {
	s, db, clk := newTestStore(t)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	var all []Task
	for _, st := range []Status{StatusInbox, StatusReady, StatusReview, StatusDone, StatusArchived} {
		task := mustAdd(t, s, "default", "in "+string(st), false)
		switch st {
		case StatusInbox:
		case StatusArchived:
			if _, err := s.Archive(bg, "default", []int64{task.ID}, human); err != nil {
				t.Fatal(err)
			}
		default:
			if _, err := s.Move(bg, "default", task.ID, st, Placement{}, human); err != nil {
				t.Fatal(err)
			}
		}
		all = append(all, task)
	}
	clk.advance(20 * time.Minute) // the lease of "one" has ten minutes left, which a renewal would make thirty
	_, until := opsClaim(t, db, held.ID)
	for _, a := range []Actor{human, {Kind: Human, Name: "one"}} {
		for _, task := range append(all, held) {
			got, err := s.Comment(bg, "default", task.ID, "from the operator", a)
			if err != nil || got.LastEvent == nil || got.LastEvent.Kind != "comment" || got.LastEvent.Actor != "you" {
				t.Errorf("a comment on #%d (%s): %+v, %v, want a comment by you", task.ID, task.Title, got.LastEvent, err)
			}
		}
	}
	if _, u := opsClaim(t, db, held.ID); u != until {
		t.Errorf("the operator's comment moved the lease from %s to %s", until, u)
	}
	if got, _, _ := s.Get(bg, "default", held.ID); got.Claim == nil || got.Claim.By != "one" || got.Status != StatusInProgress {
		t.Errorf("the held task after the operator's comments: %+v", got.Claim)
	}
}

// Spec 14: an unknown profile is refused by every method that writes, before the task is looked for (the
// id is a real one of the default profile: ErrNotFound would be the wrong answer), and a peek says so
// too, instead of saying there is nothing to do. The message says the profile is unknown and repeats
// at most a bounded part of it.
func TestEveryAgentVerbRefusesAnUnknownProfileAndWritesNothing(t *testing.T) {
	s, db, _ := newTestStore(t)
	held := mustAdd(t, s, "default", "held", true)
	claimsClaim(t, s, held.ID, "one", 0)
	mustAdd(t, s, "default", "ready", true)
	before := dumpBoard(t, db)
	calls := []struct {
		name string
		run  func(profile string) error
	}{
		{"Claim", func(p string) error { _, err := s.Claim(bg, p, held.ID, bot("one"), 0); return err }},
		{"Next with claim", func(p string) error { _, err := s.Next(bg, p, bot("one"), true, 0); return err }},
		{"Next", func(p string) error { _, err := s.Next(bg, p, bot("one"), false, 0); return err }},
		{"Comment by an agent", func(p string) error { _, err := s.Comment(bg, p, held.ID, "text", bot("one")); return err }},
		{"Comment by the operator", func(p string) error { _, err := s.Comment(bg, p, held.ID, "text", human); return err }},
		{"Finish", func(p string) error { _, err := s.Finish(bg, p, held.ID, Outcome{Result: "r"}, bot("one")); return err }},
		{"Release", func(p string) error { _, err := s.Release(bg, p, held.ID, "why", bot("one")); return err }},
	}
	for _, c := range calls {
		for _, profile := range []string{"no-such-profile", "", strings.Repeat("x", 1<<16)} {
			err := c.run(profile)
			switch {
			case !errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "unknown profile"):
				t.Errorf("%s on profile %.20q: %v, want ErrInvalid saying the profile is unknown", c.name, profile, err)
			case len(err.Error()) > 400:
				t.Errorf("%s: an error of %d bytes repeats what the caller sent", c.name, len(err.Error()))
			}
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Spec 14: every method refuses another profile's ids, as not found, whoever holds the task there.
func TestEveryAgentVerbRefusesAnotherProfilesTasksAsNotFound(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	ready := mustAdd(t, s, other, "theirs, ready", true)
	held := mustAdd(t, s, other, "theirs, held", true)
	if _, err := s.Claim(bg, other, held.ID, bot("one"), 0); err != nil {
		t.Fatal(err)
	}
	before := dumpBoard(t, db)
	for _, id := range []int64{ready.ID, held.ID} {
		for _, v := range claimsAgentVerbs(s) {
			if v.name == "Next with claim" {
				continue // it names no task
			}
			if err := v.run("default", id, bot("one")); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s of #%d through the default profile: %v, want ErrNotFound", v.name, id, err)
			}
		}
		for _, a := range []Actor{bot("one"), human} {
			if _, err := s.Comment(bg, "default", id, "text", a); !errors.Is(err, ErrNotFound) {
				t.Errorf("Comment of #%d by %+v through the default profile: %v, want ErrNotFound", id, a, err)
			}
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused calls changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// the same calls through the task's own profile work
	if _, err := s.Comment(bg, other, held.ID, "text", bot("one")); err != nil {
		t.Errorf("comment through its own profile: %v", err)
	}
	if _, err := s.Finish(bg, other, held.ID, Outcome{Result: "r"}, bot("one")); err != nil {
		t.Errorf("finish through its own profile: %v", err)
	}
}

// scanTask shows a claim on any row that has one, so a stray claim must be neither an obstacle nor a
// hold. A Ready task that carries one is claimed like any other, by id and by next; a claim under
// the agent's own name on a card that is not In progress holds nothing: the agent may not comment on,
// finish or release it, and nothing is written when it tries.
func TestAStrayClaimIsNoObstacleToAClaimAndNoHold(t *testing.T) {
	s, db, clk := newTestStore(t)
	byID, byNext := mustAdd(t, s, "default", "stray, by id", true), mustAdd(t, s, "default", "stray, by next", true)
	claimsStray(t, db, byID.ID, "ghost", clk.t.Add(time.Hour))
	claimsStray(t, db, byNext.ID, "ghost", clk.t.Add(-time.Hour))
	got, err := s.Claim(bg, "default", byID.ID, bot("one"), 0)
	if err != nil || got.Status != StatusInProgress || got.Claim == nil || got.Claim.By != "one" || got.Claim.Stale {
		t.Fatalf("claim of a Ready task with a stray claim: %+v, %v", got, err)
	}
	if ev := opsEvents(t, s, byID.ID); ev[len(ev)-1] != "claimed|one|ready>in_progress|" {
		t.Errorf("events: %v", ev)
	}
	got2, err := s.Next(bg, "default", bot("two"), true, 0)
	if err != nil || got2 == nil || got2.ID != byNext.ID || got2.Claim == nil || got2.Claim.By != "two" {
		t.Fatalf("next --claim of a Ready task with a stray claim: %+v, %v", got2, err)
	}
	if by, until := opsClaim(t, db, byNext.ID); by != "two" || until != clk.t.Add(DefaultLease).Format(timeFmt) {
		t.Errorf("claim columns %q and %q", by, until)
	}

	// a claim under the agent's own name on a card that is not In progress
	for _, st := range []Status{StatusInbox, StatusReady, StatusReview, StatusDone, StatusArchived} {
		task := mustAdd(t, s, "default", "stray in "+string(st), false)
		switch st {
		case StatusInbox:
		case StatusArchived:
			if _, err := s.Archive(bg, "default", []int64{task.ID}, human); err != nil {
				t.Fatal(err)
			}
		default:
			if _, err := s.Move(bg, "default", task.ID, st, Placement{}, human); err != nil {
				t.Fatal(err)
			}
		}
		claimsStray(t, db, task.ID, "three", clk.t.Add(time.Hour))
		before := dumpBoard(t, db)
		if _, err := s.Comment(bg, "default", task.ID, "x", bot("three")); !errors.Is(err, ErrNotClaimant) {
			t.Errorf("a comment by the name of a stray claim in %s: %v, want ErrNotClaimant", st, err)
		}
		if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "x"}, bot("three")); !errors.Is(err, ErrNotClaimant) {
			t.Errorf("a finish by the name of a stray claim in %s: %v, want ErrNotClaimant", st, err)
		}
		if _, err := s.Release(bg, "default", task.ID, "x", bot("three")); !errors.Is(err, ErrNotClaimant) {
			t.Errorf("a release by the name of a stray claim in %s: %v, want ErrNotClaimant", st, err)
		}
		if st != StatusReady { // a Ready card is claimable, and is the one thing a claim may take
			if _, err := s.Claim(bg, "default", task.ID, bot("three"), 0); !errors.Is(err, ErrNotReady) {
				t.Errorf("a claim of a card in %s: %v, want ErrNotReady", st, err)
			}
		}
		if after := dumpBoard(t, db); after != before {
			t.Errorf("refused calls changed the database in %s:\nbefore:\n%s\nafter:\n%s", st, before, after)
		}
	}
}

// Spec 5.2: stale claims stay where they are until someone takes them: the holder can still comment
// (which renews the lease), finish and release; once another agent has taken the task, it cannot.
func TestAStaleClaimIsStillItsHoldersUntilSomeoneTakesIt(t *testing.T) {
	s, _, clk := newTestStore(t)
	a, b, c, d := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true), mustAdd(t, s, "default", "d", true)
	for _, task := range []Task{a, b, c, d} {
		claimsClaim(t, s, task.ID, "one", 0)
	}
	clk.advance(DefaultLease + time.Minute) // every lease has run out
	if got, err := s.Comment(bg, "default", a.ID, "still here", bot("one")); err != nil || got.Claim == nil || got.Claim.Stale || !got.Claim.Until.Equal(clk.t.Add(DefaultLease)) {
		t.Errorf("a comment on a stale claim of its holder: %+v, %v, want the lease renewed", got.Claim, err)
	}
	if got, err := s.Finish(bg, "default", b.ID, Outcome{Result: "done"}, bot("one")); err != nil || got.Status != StatusReview {
		t.Errorf("finish of a stale claim by its holder: %+v, %v", got, err)
	}
	if got, err := s.Release(bg, "default", c.ID, "no", bot("one")); err != nil || got.Status != StatusReady {
		t.Errorf("release of a stale claim by its holder: %+v, %v", got, err)
	}
	if got, err := s.Claim(bg, "default", d.ID, bot("one"), 0); err != nil || got.LastEvent == nil || got.LastEvent.Kind != "claimed" || got.Claim.Stale {
		t.Errorf("a claim of a stale claim by its holder is a renewal, not a takeover: %+v, %v", got.LastEvent, err)
	}
	if ev := opsEvents(t, s, d.ID); ev[len(ev)-1] != "claimed|one|in_progress>in_progress|renewed" {
		t.Errorf("events: %v", ev)
	}
	// once another agent has taken a stale claim, the first is no longer its holder
	e := mustAdd(t, s, "default", "e", true)
	claimsClaim(t, s, e.ID, "one", 0)
	clk.advance(2 * DefaultLease)
	claimsClaim(t, s, e.ID, "two", 0)
	if _, err := s.Comment(bg, "default", e.ID, "x", bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a comment after the takeover: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Finish(bg, "default", e.ID, Outcome{Result: "x"}, bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a finish after the takeover: %v, want ErrNotClaimant", err)
	}
	if _, err := s.Release(bg, "default", e.ID, "x", bot("one")); !errors.Is(err, ErrNotClaimant) {
		t.Errorf("a release after the takeover: %v, want ErrNotClaimant", err)
	}
	if got, _, _ := s.Get(bg, "default", e.ID); got.Claim == nil || got.Claim.By != "two" {
		t.Errorf("after the takeover: %+v, want the task held by two", got.Claim)
	}
}

// A task the operator works on, or one in another column, is not an agent's to claim, and the message
// says which it is. (The In progress card has no claim, and "" is not after any time: the operator's
// card is not a stale claim.)
func TestAClaimOfWhatIsNotClaimableSaysWhy(t *testing.T) {
	s, _, _ := newTestStore(t)
	byHand := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", byHand.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	review := mustAdd(t, s, "default", "review", false)
	if _, err := s.Move(bg, "default", review.ID, StatusReview, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		id   int64
		say  string
	}{
		{"the operator's card", byHand.ID, "operator"},
		{"a card in review", review.ID, "review"},
	} {
		_, err := s.Claim(bg, "default", c.id, bot("one"), 0)
		if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), c.say) {
			t.Errorf("%s: %v, want ErrNotReady saying %q", c.name, err, c.say)
		}
	}
	if got, err := s.Next(bg, "default", bot("one"), true, 0); err != nil || got != nil {
		t.Errorf("next --claim: %+v, %v, want nothing: the operator's card is no stale claim", got, err)
	}
}
