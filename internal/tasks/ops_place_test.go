package tasks

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// slot is one card of a column in the model below: its id and its position.
type slot struct{ id, pos int64 }

// colModel is the five columns as spec 4.6 says they behave, written without the store's code:
// each column top to bottom; a card goes before, after, to the top or the bottom of the column, or
// to its default end (the top of Inbox, Review and Done, the bottom of Ready and In progress); an
// end takes a gap of 1024 beyond the end card, two neighbours the midpoint of theirs, and when no
// integer is left between the neighbours the column is renumbered 1024, 2048, ... top to bottom
// with the card in its place.
type colModel map[Status][]slot

func modelOf(t *testing.T, s *Store) colModel {
	t.Helper()
	b, err := s.Board(bg, "default", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := colModel{}
	for _, st := range BoardStatuses {
		for _, task := range b.Tasks[st] {
			m[st] = append(m[st], slot{task.ID, task.Position})
		}
	}
	return m
}

func (m colModel) move(id int64, to Status, p Placement) {
	for st, col := range m {
		for i, c := range col {
			if c.id == id {
				m[st] = append(append([]slot{}, col[:i]...), col[i+1:]...)
				break
			}
		}
	}
	col := m[to]
	idx := len(col)
	switch {
	case p.Before != 0 || p.After != 0:
		for i, c := range col {
			if c.id == p.Before || c.id == p.After {
				idx = i
			}
		}
		if p.After != 0 {
			idx++
		}
	case p.Top:
		idx = 0
	case p.Bottom:
		idx = len(col)
	case to == StatusInbox || to == StatusReview || to == StatusDone:
		idx = 0
	}
	var pos int64
	switch {
	case len(col) == 0:
		pos = 1024
	case idx == 0:
		pos = col[0].pos - 1024
	case idx == len(col):
		pos = col[len(col)-1].pos + 1024
	case col[idx].pos-col[idx-1].pos >= 2:
		pos = col[idx-1].pos + (col[idx].pos-col[idx-1].pos)/2
	default:
		out := append(append(append([]slot{}, col[:idx]...), slot{id: id}), col[idx:]...)
		for i := range out {
			out[i].pos = int64(i+1) * 1024
		}
		m[to] = out
		return
	}
	m[to] = append(append(append([]slot{}, col[:idx]...), slot{id, pos}), col[idx:]...)
}

// check compares every column of the board, ids and positions, with the model.
func (m colModel) check(t *testing.T, s *Store, when string) {
	t.Helper()
	got := modelOf(t, s)
	for _, st := range BoardStatuses {
		if fmt.Sprint(got[st]) != fmt.Sprint(m[st]) {
			t.Fatalf("%s: column %s is %v, want %v", when, st, got[st], m[st])
		}
	}
}

// squeeze renumbers every column 1, 2, 3, ... straight in the table: the order stays and no integer
// is left between neighbours, so the next card placed between two of them renumbers its column.
func (m colModel) squeeze(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, col := range m {
		for i := range col {
			col[i].pos = int64(i + 1)
			if _, err := db.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, col[i].pos, col[i].id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// randomPlacement picks no placement, the top, the bottom, or a place next to a card of the target
// column, mostly near its top so that squeezed neighbours are met often.
func (m colModel) randomPlacement(r *rand.Rand, id int64, to Status) Placement {
	var others []int64
	for _, c := range m[to] {
		if c.id != id {
			others = append(others, c.id)
		}
	}
	switch k := r.Intn(6); {
	case k == 0 || (k > 2 && len(others) == 0):
		return Placement{}
	case k == 1:
		return Placement{Top: true}
	case k == 2:
		return Placement{Bottom: true}
	}
	ref := others[r.Intn(min(len(others), 2))]
	if r.Intn(3) == 0 {
		ref = others[r.Intn(len(others))]
	}
	if r.Intn(2) == 0 {
		return Placement{Before: ref}
	}
	return Placement{After: ref}
}

// Every position the spec names, as a number: the midpoint between two neighbours, one gap beyond
// an end card, the first gap in an empty column, the default end of Ready.
func TestPlaceTakesTheMidpointAndAnEndTakesOneGap(t *testing.T) {
	s, _, _ := newTestStore(t)
	a := mustAdd(t, s, "default", "a", true) // 1024
	mustAdd(t, s, "default", "b", true)      // 2048
	c := mustAdd(t, s, "default", "c", true) // 3072
	n := make([]Task, 9)
	for i := range n {
		n[i] = mustAdd(t, s, "default", fmt.Sprintf("n%d", i), false)
	}
	for _, k := range []struct {
		name string
		id   int64
		to   Status
		p    Placement
		want int64
	}{
		{"after the first card: the midpoint of the first two", n[0].ID, StatusReady, Placement{After: a.ID}, 1536},
		{"before that card: half of the room again", n[1].ID, StatusReady, Placement{Before: n[0].ID}, 1280},
		{"before the last card: the midpoint of the last two", n[2].ID, StatusReady, Placement{Before: c.ID}, 2560},
		{"top: one gap above the first card", n[3].ID, StatusReady, Placement{Top: true}, 0},
		{"bottom: one gap below the last card", n[4].ID, StatusReady, Placement{Bottom: true}, 4096},
		{"no placement in Ready: one gap below the last card", n[5].ID, StatusReady, Placement{}, 5120},
		{"before the first card: one gap above it", n[6].ID, StatusReady, Placement{Before: n[3].ID}, -1024},
		{"after the last card: one gap below it", n[7].ID, StatusReady, Placement{After: n[5].ID}, 6144},
		{"into an empty column: the first gap", n[8].ID, StatusReview, Placement{}, 1024},
	} {
		got, err := s.Move(bg, "default", k.id, k.to, k.p, human)
		if err != nil || got.Position != k.want || got.Status != k.to {
			t.Errorf("%s: position %d in %s, err %v, want %d", k.name, got.Position, got.Status, err, k.want)
		}
	}
}

// No placement means the default end of the column the card goes to, in every column.
func TestPlaceWithNoPlacementGoesToTheDefaultEndOfEachColumn(t *testing.T) {
	for _, k := range []struct {
		to   Status
		want int64 // after cards at 1024 and 2048
	}{
		{StatusInbox, 0}, {StatusReady, 3072}, {StatusInProgress, 3072}, {StatusReview, 0}, {StatusDone, 0},
	} {
		t.Run(string(k.to), func(t *testing.T) {
			s, _, _ := newTestStore(t)
			for i := 0; i < 2; i++ {
				if _, err := s.Move(bg, "default", mustAdd(t, s, "default", "x", false).ID, k.to, Placement{Bottom: true}, human); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Move(bg, "default", mustAdd(t, s, "default", "y", false).ID, k.to, Placement{}, human)
			if err != nil || got.Position != k.want {
				t.Errorf("position %d, err %v, want %d", got.Position, err, k.want)
			}
		})
	}
}

// A card moved to the column it is in is placed like any other: with no placement at the default
// end of the column, with one where it says.
func TestMoveToItsOwnColumnWithNoPlacementGoesToTheDefaultEnd(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b, c := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true), mustAdd(t, s, "default", "c", true)
	if _, err := s.Move(bg, "default", b.ID, StatusReady, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, StatusReady); !sameIDs(got, []int64{a.ID, c.ID, b.ID}) {
		t.Errorf("Ready, the middle card moved to Ready: %v, want it at the bottom", got)
	}
	x, y, z := mustAdd(t, s, "default", "x", false), mustAdd(t, s, "default", "y", false), mustAdd(t, s, "default", "z", false)
	for _, task := range []Task{x, y, z} {
		if _, err := s.Move(bg, "default", task.ID, StatusDone, Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Move(bg, "default", y.ID, StatusDone, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, StatusDone); !sameIDs(got, []int64{y.ID, z.ID, x.ID}) {
		t.Errorf("Done, the middle card moved to Done: %v, want it at the top", got)
	}
}

// A card goes between two neighbours at the midpoint while an integer is left strictly between
// them, so a gap of two or more; with a gap of one (or none) the column is renumbered. The mover is
// the first task made, so a tie with its new neighbour would put it on the wrong side.
func TestPlaceRenumbersOnlyWhenNoIntegerIsLeftBetween(t *testing.T) {
	for _, k := range []struct {
		name       string
		aPos, bPos int64
		mid        int64 // the mover's position when there is room
		renumber   bool
	}{
		{"neighbours one apart", 10, 11, 0, true},
		{"neighbours with the same position", 10, 10, 0, true},
		{"neighbours two apart", 10, 12, 11, false},
		{"neighbours three apart", 10, 13, 11, false},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			x := mustAdd(t, s, "default", "x", false)
			a, b := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true)
			for id, pos := range map[int64]int64{a.ID: k.aPos, b.ID: k.bPos} {
				if _, err := db.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, pos, id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Move(bg, "default", x.ID, StatusReady, Placement{After: a.ID}, human)
			if err != nil {
				t.Fatal(err)
			}
			if ids := column(t, s, StatusReady); !sameIDs(ids, []int64{a.ID, x.ID, b.ID}) {
				t.Fatalf("order %v, want a, x, b", ids)
			}
			ts, _ := s.List(bg, "default", Filter{Statuses: []Status{StatusReady}}, human)
			var pos []int64
			for _, task := range ts {
				pos = append(pos, task.Position)
			}
			want := []int64{k.aPos, k.mid, k.bPos}
			if k.renumber {
				want = []int64{1024, 2048, 3072}
			}
			if fmt.Sprint(pos) != fmt.Sprint(want) || got.Position != want[1] {
				t.Errorf("positions %v (the mover's: %d), want %v", pos, got.Position, want)
			}
		})
	}
}

// Eleven cards put after the same card use up the 1024 between it and the next one: the eleventh
// has no room and the column is renumbered with every relative order kept. Every step is compared
// with the model, the numbers of the first twelve steps are written out.
func TestPlaceElevenInsertionsAfterOneCardUseUpTheRoomAndRenumber(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", true), mustAdd(t, s, "default", "b", true) // 1024, 2048
	added := make([]Task, 12)
	for i := range added {
		added[i] = mustAdd(t, s, "default", fmt.Sprintf("c%d", i+1), false)
	}
	m := modelOf(t, s)
	want := []int64{1536, 1280, 1152, 1088, 1056, 1040, 1032, 1028, 1026, 1025, 2048, 1536}
	for i, c := range added {
		p := Placement{After: a.ID}
		got, err := s.Move(bg, "default", c.ID, StatusReady, p, human)
		if err != nil || got.Position != want[i] {
			t.Fatalf("insertion %d: position %d, err %v, want %d", i+1, got.Position, err, want[i])
		}
		m.move(c.ID, StatusReady, p)
		m.check(t, s, fmt.Sprintf("insertion %d", i+1))
	}
	// c12, c11 ... c1 follow a, then b: the order of the column is the order they were put in, reversed.
	wantIDs := []int64{a.ID}
	for i := len(added) - 1; i >= 0; i-- {
		wantIDs = append(wantIDs, added[i].ID)
	}
	wantIDs = append(wantIDs, b.ID)
	if got := column(t, s, StatusReady); !sameIDs(got, wantIDs) {
		t.Errorf("order %v, want %v", got, wantIDs)
	}
}

// After every one of many random moves the five columns equal the model, ids and positions: where
// each card went, that no other card moved, what a renumber does. The columns are squeezed every few
// steps so that most placements between two cards renumber. The seed is in every failure message.
func TestPlaceMatchesTheModelAfterEveryRandomMove(t *testing.T) {
	for _, seed := range []int64{7, 2026, 31337} {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			s, db, _ := newTestStore(t)
			r := rand.New(rand.NewSource(seed))
			var ids []int64
			for i := 0; i < 14; i++ {
				ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), i%2 == 0).ID)
			}
			m := modelOf(t, s)
			for step := 0; step < 200; step++ {
				if step%15 == 0 {
					m.squeeze(t, db)
				}
				id, to := ids[r.Intn(len(ids))], BoardStatuses[r.Intn(len(BoardStatuses))]
				p := m.randomPlacement(r, id, to)
				if _, err := s.Move(bg, "default", id, to, p, human); err != nil {
					t.Fatalf("seed %d, step %d: move #%d to %s %+v: %v", seed, step, id, to, p, err)
				}
				m.move(id, to, p)
				m.check(t, s, fmt.Sprintf("seed %d, step %d (move #%d to %s %+v)", seed, step, id, to, p))
			}
		})
	}
}

// A placement looks at the target column of the profile and nothing else: not at the other columns
// and not at another profile's cards, and a card of either is no reference.
func TestPlaceLooksOnlyAtTheTargetColumnOfTheProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	addProfile(t, db, "p2")
	a := mustAdd(t, s, "default", "a", true) // Ready, 1024
	review := mustAdd(t, s, "default", "review", false)
	if _, err := s.Move(bg, "default", review.ID, StatusReview, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	far := mustAdd(t, s, "p2", "far", true)
	farther := mustAdd(t, s, "p2", "farther", true)
	for id, pos := range map[int64]int64{review.ID: 5_000_000, far.ID: -7_000_000, farther.ID: 9_000_000} {
		if _, err := db.Exec(`UPDATE tasks SET position = ? WHERE id = ?`, pos, id); err != nil {
			t.Fatal(err)
		}
	}
	top, bottom := mustAdd(t, s, "default", "top", false), mustAdd(t, s, "default", "bottom", false)
	if got, err := s.Move(bg, "default", top.ID, StatusReady, Placement{Top: true}, human); err != nil || got.Position != 0 {
		t.Errorf("top of Ready: position %d, err %v, want 0, one gap above a", got.Position, err)
	}
	if got, err := s.Move(bg, "default", bottom.ID, StatusReady, Placement{Bottom: true}, human); err != nil || got.Position != 2048 {
		t.Errorf("bottom of Ready: position %d, err %v, want 2048, one gap below a", got.Position, err)
	}

	n := mustAdd(t, s, "default", "n", false)
	before := dumpBoard(t, db)
	for _, k := range []struct {
		name string
		id   int64
		p    Placement
	}{
		{"before a card of another profile", n.ID, Placement{Before: far.ID}},
		{"after a card of another profile", n.ID, Placement{After: farther.ID}},
		{"before a card of another column", n.ID, Placement{Before: review.ID}},
		{"after a card that does not exist", n.ID, Placement{After: 424242}},
		{"before a negative id", n.ID, Placement{Before: -1}},
		{"after itself", bottom.ID, Placement{After: bottom.ID}},
	} {
		if _, err := s.Move(bg, "default", k.id, StatusReady, k.p, human); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", k.name, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("refused placements changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got, err := s.Move(bg, "default", n.ID, StatusReady, Placement{After: a.ID}, human); err != nil || got.Position != 1536 {
		t.Errorf("after a card of its own column: position %d, err %v, want 1536, between a and the card below it", got.Position, err)
	}
}
