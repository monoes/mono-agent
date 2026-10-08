package tasks

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"testing"
	"time"
)

// Spec 5.4: one revision for each write transaction. ArchiveStatus of a column of several cards is
// one transaction, so the revision moves by one and not by the number of cards.
func TestArchiveStatusMovesTheRevisionOnceForAColumnOfCards(t *testing.T) {
	s, _, _ := newTestStore(t)
	for i := 0; i < 5; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("c%d", i), false)
	}
	before, _ := s.Rev(bg, "default")
	if n, err := s.ArchiveStatus(bg, "default", StatusInbox, human); err != nil || n != 5 {
		t.Fatalf("archive Inbox: %d, %v", n, err)
	}
	if after, _ := s.Rev(bg, "default"); after != before+1 {
		t.Errorf("the revision moved from %d to %d for one ArchiveStatus of five cards, want one step", before, after)
	}
}

// A call that fails reports no count: the transaction was undone, so nothing was archived. (The count
// is known before the revision is written, which is where this call is made to fail.)
func TestArchiveStatusReturnsNoCountWhenItFails(t *testing.T) {
	s, db, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("c%d", i), false)
	}
	for _, q := range []string{
		`CREATE TRIGGER refuse BEFORE INSERT ON task_board_rev BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`,
		`CREATE TRIGGER refuse_update BEFORE UPDATE ON task_board_rev BEGIN SELECT RAISE(ABORT, 'refused for the test'); END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	before := dumpBoard(t, db)
	n, err := s.ArchiveStatus(bg, "default", StatusInbox, human)
	if err == nil || !strings.Contains(err.Error(), "refused for the test") {
		t.Fatalf("archive Inbox: %d, %v, want the refusal of the revision", n, err)
	}
	if n != 0 {
		t.Errorf("a failed ArchiveStatus returned %d archived cards, want 0", n)
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("a failed call left changes behind:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The hourly allowance is the agents': 20 tasks added by agents in the last hour do not stop the
// operator from bringing a card back from the archive, by Unarchive or by Move.
func TestMovingOutOfTheArchiveIsNotLimitedByTheAgentsHourlyAllowance(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false)
	if _, err := s.Archive(bg, "default", []int64{a.ID, b.ID}, human); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < AgentTasksPerHour; i++ {
		if _, _, err := s.Add(bg, "default", AddInput{Title: fmt.Sprintf("agent %d", i)}, bot("bob")); err != nil {
			t.Fatalf("setup: agent task %d: %v", i, err)
		}
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "one too many"}, bot("bob")); !errors.Is(err, ErrLimit) {
		t.Fatalf("setup: the 21st agent task: %v, want ErrLimit", err)
	}
	if got, err := s.Unarchive(bg, "default", []int64{a.ID}, human); err != nil || got[0].Status != StatusInbox {
		t.Errorf("unarchive after the agents used their hour: %+v, %v", got, err)
	}
	if got, err := s.Move(bg, "default", b.ID, StatusReady, Placement{}, human); err != nil || got.Status != StatusReady {
		t.Errorf("move out of the archive after the agents used their hour: %+v, %v", got, err)
	}
}

// The 2,000 open tasks are counted for the profile the card is in: a full board elsewhere does not
// stop a card coming back, and a full one here does.
func TestTheOpenTaskLimitIsThatOfTheProfileTheCardIsIn(t *testing.T) {
	t.Run("another profile is full", func(t *testing.T) {
		s, db, _ := newTestStore(t)
		other := addProfile(t, db, "p2")
		seedTasks(t, db, other, MaxOpenTasks)
		mine, theirs := seedRow(t, db, "default", "archived"), seedRow(t, db, other, "archived")
		if _, err := s.Unarchive(bg, "default", []int64{mine}, human); err != nil {
			t.Errorf("unarchive on a board with room, another profile being full: %v", err)
		}
		if _, err := s.Unarchive(bg, other, []int64{theirs}, human); !errors.Is(err, ErrLimit) {
			t.Errorf("unarchive on the full board: %v, want ErrLimit", err)
		}
	})
	t.Run("the default profile is full", func(t *testing.T) {
		s, db, _ := newTestStore(t)
		other := addProfile(t, db, "p2")
		seedTasks(t, db, "default", MaxOpenTasks)
		theirs := seedRow(t, db, other, "archived")
		if _, err := s.Unarchive(bg, other, []int64{theirs}, human); err != nil {
			t.Errorf("unarchive on a board with room, the default profile being full: %v", err)
		}
	})
}

// A card goes where one of before, after, top and bottom says, so two or more of them are refused,
// whichever two, and nothing is written.
func TestEveryCombinationOfPlacementsIsRefusedAndWritesNothing(t *testing.T) {
	s, db, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true)
	x := mustAdd(t, s, "default", "x", false)
	before := dumpBoard(t, db)
	for mask := 0; mask < 16; mask++ {
		if bits.OnesCount(uint(mask)) < 2 {
			continue
		}
		p := Placement{Top: mask&1 != 0, Bottom: mask&2 != 0}
		if mask&4 != 0 {
			p.Before = a.ID
		}
		if mask&8 != 0 {
			p.After = b.ID
		}
		if _, err := s.Move(bg, "default", x.ID, StatusReady, p, human); !errors.Is(err, ErrInvalid) {
			t.Errorf("placement %+v: %v, want ErrInvalid", p, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused placements changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Move takes one of the five columns: anything else, not only the archive, is refused as invalid
// input before the database is asked (which would refuse an unknown status in its own words).
func TestMoveRefusesAnythingThatIsNotOneOfTheFiveColumns(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "a", false)
	before := dumpBoard(t, db)
	for _, to := range []Status{"", "bogus", "ready'; DROP TABLE tasks; --", StatusArchived} {
		if _, err := s.Move(bg, "default", task.ID, to, Placement{}, human); !errors.Is(err, ErrInvalid) {
			t.Errorf("move to %q: %v, want ErrInvalid", to, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused moves changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Every event of a move that ends a claim is dated by the store's clock, the released one as well
// as the one of the move itself.
func TestTheEventsOfAMoveThatEndsAClaimAreDatedByTheClock(t *testing.T) {
	exits := []struct {
		name string
		run  func(s *Store, id int64) error
	}{
		{"Move", func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, StatusReview, Placement{}, human)
			return err
		}},
		{"Archive", func(s *Store, id int64) error { _, err := s.Archive(bg, "default", []int64{id}, human); return err }},
		{"ArchiveStatus", func(s *Store, id int64) error {
			_, err := s.ArchiveStatus(bg, "default", StatusInProgress, human)
			return err
		}},
	}
	for _, k := range exits {
		t.Run(k.name, func(t *testing.T) {
			s, db, c := newTestStore(t)
			held := opsHeld(t, s, db, c, "held", "bob", time.Hour)
			c.advance(10 * time.Minute)
			if err := k.run(s, held.ID); err != nil {
				t.Fatal(err)
			}
			_, events, err := s.Get(bg, "default", held.ID)
			if err != nil {
				t.Fatal(err)
			}
			released := 0
			for _, e := range events {
				switch e.Kind {
				case "released", "moved", "archived":
					if e.Kind == "moved" && e.ToStatus == "in_progress" {
						continue // the move into In progress, before the clock was advanced
					}
					if !e.At.Equal(c.t) {
						t.Errorf("the %s event is dated %v, want %v", e.Kind, e.At, c.t)
					}
					if e.Kind == "released" {
						released++
					}
				}
			}
			if released != 1 {
				t.Errorf("%d released events, want 1", released)
			}
		})
	}
}

// What an event repeats of a claimant's name is cut (a claim is written by another verb, in another
// task, with its own checks; the row here is as such a writer could have left it).
func TestTheReleasedNoteRepeatsAtMostSixtyFourRunesOfAClaimantsName(t *testing.T) {
	s, db, c := newTestStore(t)
	id := seedRow(t, db, "default", "in_progress")
	name := strings.Repeat("n", 300)
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ?, claim_until = ? WHERE id = ?`, name, c.t.Add(time.Hour).Format(timeFmt), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Move(bg, "default", id, StatusReview, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	var note string
	if err := db.QueryRow(`SELECT note FROM task_events WHERE task_id = ? AND kind = 'released'`, id).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, strings.Repeat("n", 65)) || len(note) > 200 {
		t.Errorf("the released note repeats the claimant's name whole (%d bytes): %.100q", len(note), note)
	}
}

// The limit of the notes is the limit of what is stored, which is the cleaned text: notes that are
// over it only because they carry carriage returns are stored whole, as Add stores them.
func TestEditCleansTheNotesBeforeItCutsThem(t *testing.T) {
	s, _, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "t", false)
	raw := strings.Repeat("a\r\n", 30000) // 90,000 bytes; 59,999 once cleaned
	want := strings.TrimSpace(strings.Repeat("a\n", 30000))
	if len(raw) <= MaxNotesBytes || len(want) > MaxNotesBytes {
		t.Fatalf("setup: %d raw bytes and %d cleaned, the limit is %d", len(raw), len(want), MaxNotesBytes)
	}
	got, err := s.Edit(bg, "default", task.ID, Edit{Notes: &raw}, human)
	if err != nil || got.Notes != want {
		t.Errorf("edit: %d bytes stored (%.60q...), err %v, want the %d cleaned bytes whole", len(got.Notes), got.Notes[max(0, len(got.Notes)-60):], err, len(want))
	}
	added, _, err := s.Add(bg, "default", AddInput{Title: "same", Notes: raw}, human)
	if err != nil || added.Notes != got.Notes {
		t.Errorf("Add stores %d bytes of the same notes and Edit %d", len(added.Notes), len(got.Notes))
	}
}

// An id list that is empty, not only one that is nil, names no task: refused, and the revision does
// not move (a caller that builds the list with make has an empty one, not a nil one).
func TestAnEmptyListOfIdsIsRefusedWhetherNilOrNot(t *testing.T) {
	s, _, _ := newTestStore(t)
	mustAdd(t, s, "default", "a", false)
	for _, k := range []struct {
		name string
		call func(ids []int64) error
	}{
		{"Approve", func(ids []int64) error { _, err := s.Approve(bg, "default", ids, false, human); return err }},
		{"Archive", func(ids []int64) error { _, err := s.Archive(bg, "default", ids, human); return err }},
		{"Unarchive", func(ids []int64) error { _, err := s.Unarchive(bg, "default", ids, human); return err }},
	} {
		for _, ids := range [][]int64{nil, {}, make([]int64, 0, 4)} {
			before, _ := s.Rev(bg, "default")
			if err := k.call(ids); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s of %#v: %v, want ErrInvalid", k.name, ids, err)
			}
			if after, _ := s.Rev(bg, "default"); after != before {
				t.Errorf("%s of %#v moved the revision from %d to %d", k.name, ids, before, after)
			}
		}
	}
}
