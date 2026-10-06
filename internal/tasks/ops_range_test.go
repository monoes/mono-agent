package tasks

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
)

const (
	nearMin = math.MinInt64 + 100 // a card this close to the limit has no room for a gap above it
	nearMax = math.MaxInt64 - 100 // and this one has none below it
)

// An end placement puts a card one gap beyond the card at that end of the column. When that card sits
// so near the limit of an int64 that a gap more would wrap round, the placement is refused with a plain
// error (the column was written by hand, it is no mistake of the caller) and nothing is written: the
// card does not land at the other end of the column. Every site that computes an end is here: the top,
// the bottom and the default end of a column, before the first card, after the last one, the bottom of
// the archive, and, through the same helper, Add and Approve.
func TestAnEndPlacementThatWouldWrapRefusesAndWritesNothing(t *testing.T) {
	inbox := func(t *testing.T, s *Store) Task { return mustAdd(t, s, "default", "mover", false) }
	ready := func(t *testing.T, s *Store) Task { return mustAdd(t, s, "default", "mover", true) }
	move := func(s *Store, id int64, to Status, p Placement) func() error {
		return func() error { _, err := s.Move(bg, "default", id, to, p, human); return err }
	}
	// archivedFrom is a card that was archived from the column st, as the verb that brings it back needs.
	archivedFrom := func(t *testing.T, s *Store, st Status) int64 {
		t.Helper()
		task := mustAdd(t, s, "default", "archived", false)
		if _, err := s.Move(bg, "default", task.ID, st, Placement{Top: true}, human); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Archive(bg, "default", []int64{task.ID}, human); err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	for _, sc := range []struct {
		name string
		prep func(t *testing.T, s *Store, db *sql.DB) func() error
	}{
		{"Move to the top of a column", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "inbox", nearMin)
			return move(s, ready(t, s).ID, StatusInbox, Placement{Top: true})
		}},
		{"Move to a column whose default end is the top", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "review", nearMin)
			return move(s, ready(t, s).ID, StatusReview, Placement{})
		}},
		{"Move before the first card", func(t *testing.T, s *Store, db *sql.DB) func() error {
			first := seedAt(t, db, "default", "ready", nearMin)
			return move(s, inbox(t, s).ID, StatusReady, Placement{Before: first})
		}},
		{"Approve with --top", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "ready", nearMin)
			id := inbox(t, s).ID
			return func() error { _, err := s.Approve(bg, "default", []int64{id}, true, human); return err }
		}},
		{"Add to the top of Inbox", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "inbox", nearMin)
			return func() error { _, _, err := s.Add(bg, "default", AddInput{Title: "new"}, human); return err }
		}},
		{"Unarchive to a column whose default end is the top", func(t *testing.T, s *Store, db *sql.DB) func() error {
			id := archivedFrom(t, s, StatusDone)
			seedAt(t, db, "default", "done", nearMin)
			return func() error { _, err := s.Unarchive(bg, "default", []int64{id}, human); return err }
		}},
		{"Move to the bottom of a column", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "ready", nearMax)
			return move(s, inbox(t, s).ID, StatusReady, Placement{Bottom: true})
		}},
		{"Move to a column whose default end is the bottom", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "in_progress", nearMax)
			return move(s, inbox(t, s).ID, StatusInProgress, Placement{})
		}},
		{"Move after the last card", func(t *testing.T, s *Store, db *sql.DB) func() error {
			last := seedAt(t, db, "default", "ready", nearMax)
			return move(s, inbox(t, s).ID, StatusReady, Placement{After: last})
		}},
		{"Approve", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "ready", nearMax)
			id := inbox(t, s).ID
			return func() error { _, err := s.Approve(bg, "default", []int64{id}, false, human); return err }
		}},
		{"Archive", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "archived", nearMax)
			id := inbox(t, s).ID
			return func() error { _, err := s.Archive(bg, "default", []int64{id}, human); return err }
		}},
		{"ArchiveStatus", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "archived", nearMax)
			inbox(t, s)
			return func() error { _, err := s.ArchiveStatus(bg, "default", StatusInbox, human); return err }
		}},
		{"Add straight to Ready", func(t *testing.T, s *Store, db *sql.DB) func() error {
			seedAt(t, db, "default", "ready", nearMax)
			return func() error {
				_, _, err := s.Add(bg, "default", AddInput{Title: "new", Ready: true}, human)
				return err
			}
		}},
		{"Unarchive to a column whose default end is the bottom", func(t *testing.T, s *Store, db *sql.DB) func() error {
			id := archivedFrom(t, s, StatusReady)
			seedAt(t, db, "default", "ready", nearMax)
			return func() error { _, err := s.Unarchive(bg, "default", []int64{id}, human); return err }
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			call := sc.prep(t, s, db)
			before, rev := dumpBoard(t, db), boardRev(t, s)
			err := call()
			if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("the call: %v, want a plain error saying the positions are out of range", err)
			}
			if after := dumpBoard(t, db); after != before {
				t.Errorf("a refused placement changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if now := boardRev(t, s); now != rev {
				t.Errorf("the revision moved from %d to %d", rev, now)
			}
		})
	}
}

// boardRev is the revision of the default profile.
func boardRev(t *testing.T, s *Store) int64 {
	t.Helper()
	r, err := s.Rev(bg, "default")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The refusal is for the end that has no room and for no other, and it is exact: a card one gap from
// the limit still takes the last position there is, one position closer does not.
func TestTheRefusalIsForTheEndThatHasNoRoomAndExactToTheGap(t *testing.T) {
	for _, k := range []struct {
		name   string
		status string
		at     int64
		top    bool
		want   int64 // the position the card gets; 0 when it is refused
		refuse bool
	}{
		{"top, one gap from the limit", "inbox", math.MinInt64 + positionGap, true, math.MinInt64, false},
		{"top, one position closer", "inbox", math.MinInt64 + positionGap - 1, true, 0, true},
		{"bottom, one gap from the limit", "ready", math.MaxInt64 - positionGap, false, math.MaxInt64, false},
		{"bottom, one position closer", "ready", math.MaxInt64 - positionGap + 1, false, 0, true},
		{"bottom of a column whose last card is near the lower limit", "ready", nearMin, false, nearMin + positionGap, false},
		{"top of a column whose first card is near the upper limit", "inbox", nearMax, true, nearMax - positionGap, false},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			seedAt(t, db, "default", k.status, k.at)
			to, p := StatusReady, Placement{Bottom: true}
			if k.top {
				to, p = StatusInbox, Placement{Top: true}
			}
			mover := mustAdd(t, s, "default", "mover", k.top) // in Ready for a move to Inbox, in Inbox for a move to Ready
			got, err := s.Move(bg, "default", mover.ID, to, p, human)
			switch {
			case k.refuse && (err == nil || !strings.Contains(err.Error(), "out of range")):
				t.Errorf("moved to %d, err %v, want the refusal", got.Position, err)
			case !k.refuse && (err != nil || got.Position != k.want):
				t.Errorf("position %d, err %v, want %d", got.Position, err, k.want)
			}
		})
	}
}

// Between two cards the position is the midpoint of theirs, and the difference of two positions that are
// far apart does not wrap into a small number: the column is renumbered when no integer is left, and
// is left alone when there is plenty.
func TestTheMidpointOfTwoFarApartCardsDoesNotWrap(t *testing.T) {
	for _, k := range []struct {
		name    string
		lo, hi  int64
		wantPos []int64
	}{
		{"the two extremes", math.MinInt64, math.MaxInt64, []int64{1024, 2048, 3072}},
		{"a span of exactly 2^63", -1 << 62, 1 << 62, []int64{1024, 2048, 3072}},
		{"zero and the largest position", 0, math.MaxInt64, []int64{0, math.MaxInt64 / 2, math.MaxInt64}},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			a, b := seedAt(t, db, "default", "ready", k.lo), seedAt(t, db, "default", "ready", k.hi)
			mover := mustAdd(t, s, "default", "mover", false)
			if _, err := s.Move(bg, "default", mover.ID, StatusReady, Placement{After: a}, human); err != nil {
				t.Fatal(err)
			}
			ts, err := s.List(bg, "default", Filter{Statuses: []Status{StatusReady}}, human)
			if err != nil || len(ts) != 3 || ts[0].ID != a || ts[1].ID != mover.ID || ts[2].ID != b {
				t.Fatalf("order %v, err %v, want the first card, the mover, the last card", idsOf(ts), err)
			}
			for i, task := range ts {
				if task.Position != k.wantPos[i] {
					t.Errorf("card %d is at %d, want %d", i, task.Position, k.wantPos[i])
				}
			}
		})
	}
}
