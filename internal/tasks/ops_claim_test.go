package tasks

import (
	"strings"
	"testing"
	"time"
)

// Spec 5.1: for the operator's move, leaving In progress clears a claim and writes released. The
// released event comes first, so the last event of the card is the operator's own.
func TestMoveOutOfInProgressEndsTheClaimAndSaysSo(t *testing.T) {
	for _, to := range []Status{StatusInbox, StatusReady, StatusReview, StatusDone} {
		t.Run(string(to), func(t *testing.T) {
			s, db, c := newTestStore(t)
			held := opsHeld(t, s, db, c, "held", "bob", time.Hour)
			got, err := s.Move(bg, "default", held.ID, to, Placement{}, human)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != to || got.Claim != nil {
				t.Errorf("after the move: status %s, claim %+v", got.Status, got.Claim)
			}
			if by, until := opsClaim(t, db, held.ID); by != "" || until != "" {
				t.Errorf("claim columns %q and %q, want both empty", by, until)
			}
			want := []string{
				"created|you|>ready|",
				"moved|you|ready>in_progress|",
				"released|you|in_progress>" + string(to) + "|the claim of bob ended: the operator moved the card",
				"moved|you|in_progress>" + string(to) + "|",
			}
			if ev := opsEvents(t, s, held.ID); strings.Join(ev, "\n") != strings.Join(want, "\n") {
				t.Errorf("events:\n%s\nwant:\n%s", strings.Join(ev, "\n"), strings.Join(want, "\n"))
			}
			if got.LastEvent == nil || got.LastEvent.Kind != "moved" {
				t.Errorf("last event %+v, want the move", got.LastEvent)
			}
		})
	}
}

// The operator's reordering of a held card inside In progress is not the end of the claim, and does
// not renew it either; a card nobody holds leaves In progress with no released event.
func TestMoveWithinInProgressKeepsTheClaimAsItIs(t *testing.T) {
	s, db, c := newTestStore(t)
	bob := opsHeld(t, s, db, c, "bob's", "bob", time.Hour)
	alice := opsHeld(t, s, db, c, "alice's", "alice", 2*time.Hour)
	byHand := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", byHand.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	carol := opsHeld(t, s, db, c, "carol's", "carol", time.Minute)
	_, until := opsClaim(t, db, bob.ID)
	c.advance(10 * time.Minute) // carol's lease has run out
	for _, p := range []Placement{{Top: true}, {Bottom: true}, {After: alice.ID}, {Before: alice.ID}, {}} {
		got, err := s.Move(bg, "default", bob.ID, StatusInProgress, p, human)
		if err != nil || got.Claim == nil || got.Claim.By != "bob" {
			t.Fatalf("%+v: claim %+v, err %v, want it kept", p, got.Claim, err)
		}
		if by, u := opsClaim(t, db, bob.ID); by != "bob" || u != until {
			t.Errorf("%+v: claim columns %q and %q, want bob and %q, the lease it had", p, by, u, until)
		}
	}
	if ev := opsEvents(t, s, bob.ID); strings.Contains(strings.Join(ev, ","), "released") {
		t.Errorf("a held card moved within In progress: events %v", ev)
	}
	// a stale claim is kept too, and stays stale: the operator's move renews nothing
	if got, err := s.Move(bg, "default", carol.ID, StatusInProgress, Placement{Top: true}, human); err != nil || got.Claim == nil || got.Claim.By != "carol" || !got.Claim.Stale {
		t.Errorf("a stale claim moved within In progress: %+v, %v", got.Claim, err)
	}
	if ev := opsEvents(t, s, carol.ID); strings.Contains(strings.Join(ev, ","), "released") {
		t.Errorf("a stale claim moved within In progress: events %v", ev)
	}
	if got, _, _ := s.Get(bg, "default", alice.ID); got.Claim == nil || got.Claim.By != "alice" {
		t.Errorf("the other held card: %+v", got.Claim)
	}
	if _, err := s.Move(bg, "default", byHand.ID, StatusReview, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if ev := opsEvents(t, s, byHand.ID); strings.Contains(strings.Join(ev, ","), "released") {
		t.Errorf("a card nobody held leaves In progress: events %v", ev)
	}
}

// Spec 5.2: stale claims stay until someone takes them or the operator sends the card back to
// Ready. A lease that has run out is still a claim: its end is written down too.
func TestMovingAStaleClaimToReadyEndsIt(t *testing.T) {
	s, db, c := newTestStore(t)
	held := opsHeld(t, s, db, c, "held", "bob", time.Minute)
	c.advance(2 * time.Minute)
	if cn, _ := s.Counts(bg, "default"); cn.Stale != 1 {
		t.Fatalf("setup: counts %+v, want one stale claim", cn)
	}
	got, err := s.Move(bg, "default", held.ID, StatusReady, Placement{}, human)
	if err != nil || got.Claim != nil {
		t.Fatalf("move: claim %+v, err %v", got.Claim, err)
	}
	if cn, _ := s.Counts(bg, "default"); cn.Stale != 0 || cn.Ready != 1 {
		t.Errorf("counts %+v, want no stale claim and the card in Ready", cn)
	}
	ev := opsEvents(t, s, held.ID)
	if len(ev) != 4 || !strings.HasPrefix(ev[2], "released|you|in_progress>ready|") {
		t.Errorf("events %v", ev)
	}
}

func TestArchiveEndsTheClaimToo(t *testing.T) {
	s, db, c := newTestStore(t)
	held := opsHeld(t, s, db, c, "held", "bob", time.Hour)
	got, err := s.Archive(bg, "default", []int64{held.ID}, human)
	if err != nil || got[0].Claim != nil || got[0].Status != StatusArchived {
		t.Fatalf("archive: %+v, %v", got, err)
	}
	if by, until := opsClaim(t, db, held.ID); by != "" || until != "" {
		t.Errorf("claim columns %q and %q, want both empty", by, until)
	}
	ev := opsEvents(t, s, held.ID)
	tail := ev[len(ev)-2:]
	if tail[0] != "released|you|in_progress>archived|the claim of bob ended: the operator archived the card" || tail[1] != "archived|you|in_progress>archived|" {
		t.Errorf("the last events:\n%s", strings.Join(tail, "\n"))
	}
	// and it comes back free: In progress returns as Ready, held by nobody
	back, err := s.Unarchive(bg, "default", []int64{held.ID}, human)
	if err != nil || back[0].Status != StatusReady || back[0].Claim != nil {
		t.Errorf("unarchive: %+v, %v", back, err)
	}
	if by, until := opsClaim(t, db, held.ID); by != "" || until != "" {
		t.Errorf("claim columns after unarchiving: %q and %q", by, until)
	}
}

func TestArchiveStatusEndsEveryClaimOfTheColumnAndOnlyOfThatProfile(t *testing.T) {
	s, db, c := newTestStore(t)
	other := addProfile(t, db, "p2")
	opsHeld(t, s, db, c, "bob's", "bob", time.Hour)
	opsHeld(t, s, db, c, "alice's", "alice", time.Minute)
	byHand := mustAdd(t, s, "default", "by hand", true)
	if _, err := s.Move(bg, "default", byHand.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	theirs := mustAdd(t, s, other, "carol's", true)
	if _, err := db.Exec(`UPDATE tasks SET status = 'in_progress', claimed_by = 'carol', claim_until = ? WHERE id = ?`, c.t.Add(time.Hour).Format(timeFmt), theirs.ID); err != nil {
		t.Fatal(err)
	}
	c.advance(5 * time.Minute) // alice's lease has run out: the claim is stale, still a claim
	n, err := s.ArchiveStatus(bg, "default", StatusInProgress, human)
	if err != nil || n != 3 {
		t.Fatalf("archive in progress: %d, %v", n, err)
	}
	if held := countWhere(t, db, "tasks", "profile_id = 'default' AND claimed_by <> ''"); held != 0 {
		t.Errorf("%d claims left on the profile's board", held)
	}
	if released, archived := countWhere(t, db, "task_events", "kind = 'released'"), countWhere(t, db, "task_events", "kind = 'archived'"); released != 2 || archived != 3 {
		t.Errorf("%d released and %d archived events, want 2 and 3", released, archived)
	}
	if by, _ := opsClaim(t, db, theirs.ID); by != "carol" {
		t.Errorf("another profile's claim: %q, want it untouched", by)
	}
	if st := countWhere(t, db, "tasks", "id = ? AND status = 'in_progress'", theirs.ID); st != 1 {
		t.Error("another profile's card was archived")
	}
}

// scanTask shows a claim on any row whose claimed_by is set, whatever its status, so whatever moves
// such a card ends the claim: no verb may keep one except a move within In progress.
func TestAStrayClaimIsEndedByWhateverMovesTheCard(t *testing.T) {
	type step struct {
		name string
		prep func(t *testing.T, s *Store, id int64) // brings the card to its status
		run  func(s *Store, id int64) error
		from string
		to   string
		kind string // the verb the released note names
	}
	archive := func(t *testing.T, s *Store, id int64) {
		t.Helper()
		if _, err := s.Archive(bg, "default", []int64{id}, human); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []step{
		{"Approve", nil, func(s *Store, id int64) error {
			_, err := s.Approve(bg, "default", []int64{id}, false, human)
			return err
		}, "inbox", "ready", "moved"},
		{"Move into In progress", nil, func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, StatusInProgress, Placement{}, human)
			return err
		}, "inbox", "in_progress", "moved"},
		{"Move to Ready", nil, func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, StatusReady, Placement{Top: true}, human)
			return err
		}, "inbox", "ready", "moved"},
		{"Archive", nil, func(s *Store, id int64) error { _, err := s.Archive(bg, "default", []int64{id}, human); return err }, "inbox", "archived", "archived"},
		{"ArchiveStatus", nil, func(s *Store, id int64) error {
			_, err := s.ArchiveStatus(bg, "default", StatusInbox, human)
			return err
		}, "inbox", "archived", "archived"},
		{"Unarchive", archive, func(s *Store, id int64) error { _, err := s.Unarchive(bg, "default", []int64{id}, human); return err }, "archived", "inbox", "unarchived"},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, c := newTestStore(t)
			task := mustAdd(t, s, "default", "stray", false)
			if k.prep != nil {
				k.prep(t, s, task.ID)
			}
			opsStray(t, db, c, task.ID, "bob")
			if got, _, _ := s.Get(bg, "default", task.ID); got.Claim == nil {
				t.Fatal("setup: the reader does not show the claim")
			}
			if err := k.run(s, task.ID); err != nil {
				t.Fatal(err)
			}
			if by, until := opsClaim(t, db, task.ID); by != "" || until != "" {
				t.Errorf("claim columns %q and %q, want both empty", by, until)
			}
			want := "released|you|" + k.from + ">" + k.to + "|the claim of bob ended: the operator " + k.kind + " the card"
			ev := opsEvents(t, s, task.ID)
			if len(ev) < 2 || ev[len(ev)-2] != want {
				t.Errorf("events:\n%s\nwant the second last to be:\n%s", strings.Join(ev, "\n"), want)
			}
		})
	}
}

// Every way out of In progress ends the claim in the table itself, not only in what a reader shows:
// afterwards both claim columns are empty and the exit is a released event from In progress. The
// held cards are rows written straight into the table, with a lease that is live and with one that
// has run out. (Archive and ArchiveStatus go to the archive; Move to the four other columns.)
func TestEveryExitFromInProgressEmptiesBothClaimColumnsOfTheTable(t *testing.T) {
	move := func(to Status) func(s *Store, id int64) error {
		return func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, to, Placement{}, human)
			return err
		}
	}
	exits := []struct {
		name string
		to   Status
		run  func(s *Store, id int64) error
	}{
		{"Move to inbox", StatusInbox, move(StatusInbox)},
		{"Move to ready", StatusReady, move(StatusReady)},
		{"Move to review", StatusReview, move(StatusReview)},
		{"Move to done", StatusDone, move(StatusDone)},
		{"Archive", StatusArchived, func(s *Store, id int64) error {
			_, err := s.Archive(bg, "default", []int64{id}, human)
			return err
		}},
		{"ArchiveStatus", StatusArchived, func(s *Store, id int64) error {
			_, err := s.ArchiveStatus(bg, "default", StatusInProgress, human)
			return err
		}},
	}
	for _, lease := range []struct {
		name string
		d    time.Duration
	}{{"a live lease", time.Hour}, {"a lease that has run out", -time.Hour}} {
		for _, k := range exits {
			t.Run(k.name+", "+lease.name, func(t *testing.T) {
				s, db, c := newTestStore(t)
				id := seedRow(t, db, "default", "in_progress")
				if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'bob', claim_until = ? WHERE id = ?`, c.t.Add(lease.d).Format(timeFmt), id); err != nil {
					t.Fatal(err)
				}
				if by, until := opsClaim(t, db, id); by != "bob" || until == "" {
					t.Fatalf("setup: claim columns %q and %q", by, until)
				}
				if err := k.run(s, id); err != nil {
					t.Fatal(err)
				}
				if by, until := opsClaim(t, db, id); by != "" || until != "" {
					t.Errorf("claim columns after the exit: %q and %q, want both empty", by, until)
				}
				if n := countWhere(t, db, "task_events", "task_id = ? AND kind = 'released' AND from_status = 'in_progress' AND to_status = ?", id, string(k.to)); n != 1 {
					t.Errorf("%d released events from in_progress to %s, want 1", n, k.to)
				}
				if n := countWhere(t, db, "tasks", "id = ? AND status = ?", id, string(k.to)); n != 1 {
					t.Errorf("the card is not in %s", k.to)
				}
			})
		}
	}
}
