package tasks

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// A verb reads the state it acts on inside its own write transaction, so of several callers that
// act on the same task at once exactly one finds it in the state the verb needs.
func TestApprovingOrArchivingOneTaskAtOnceSucceedsExactlyOnce(t *testing.T) {
	for _, k := range []struct {
		name string
		kind string // the event the one that wins writes
		call func(s *Store, id int64) error
	}{
		{"Approve", "moved", func(s *Store, id int64) error {
			_, err := s.Approve(bg, "default", []int64{id}, false, human)
			return err
		}},
		{"Archive", "archived", func(s *Store, id int64) error { _, err := s.Archive(bg, "default", []int64{id}, human); return err }},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			task := mustAdd(t, s, "default", "a", false)
			errs := make([]error, 8)
			var wg sync.WaitGroup
			for i := range errs {
				wg.Go(func() { errs[i] = k.call(s, task.ID) })
			}
			wg.Wait()
			won := 0
			for _, err := range errs {
				switch {
				case err == nil:
					won++
				case !errors.Is(err, ErrInvalid):
					t.Errorf("a caller failed with %v, want ErrInvalid for the ones that lost", err)
				}
			}
			if won != 1 {
				t.Errorf("%d of %d callers succeeded, want 1", won, len(errs))
			}
			if n := countWhere(t, db, "task_events", "task_id = ? AND kind = ?", task.ID, k.kind); n != 1 {
				t.Errorf("%d %s events, want 1", n, k.kind)
			}
			if rev, _ := s.Rev(bg, "default"); rev != 2 {
				t.Errorf("revision %d, want 2: the add and the one call that won", rev)
			}
		})
	}
}

// Moves from several callers at once are all served, each in a transaction of its own: no card is
// lost or doubled and every column keeps a strict order, with one revision for each move.
func TestMovesAtOnceKeepEveryColumnInOrder(t *testing.T) {
	s, _, _ := newTestStore(t)
	var ids []int64
	for i := 0; i < 12; i++ {
		ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), i%2 == 0).ID)
	}
	const callers, perCaller = 6, 15
	errs := make(chan error, callers*perCaller)
	var wg sync.WaitGroup
	for w := 0; w < callers; w++ {
		wg.Go(func() {
			r := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < perCaller; i++ {
				// always a placement: a card that is in the column and asks for none is not written
				p := []Placement{{Top: true}, {Bottom: true}}[r.Intn(2)]
				if _, err := s.Move(bg, "default", ids[r.Intn(len(ids))], BoardStatuses[r.Intn(len(BoardStatuses))], p, human); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a move failed: %v", err)
	}
	b, err := s.Board(bg, "default", 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, st := range BoardStatuses {
		seen += len(b.Tasks[st])
		for i := 1; i < len(b.Tasks[st]); i++ {
			if b.Tasks[st][i].Position <= b.Tasks[st][i-1].Position {
				t.Errorf("column %s has positions %d then %d", st, b.Tasks[st][i-1].Position, b.Tasks[st][i].Position)
			}
		}
	}
	if seen != len(ids) {
		t.Errorf("%d cards on the board, want %d", seen, len(ids))
	}
	if want := int64(len(ids) + callers*perCaller); b.Rev != want {
		t.Errorf("revision %d, want %d: one for each add and each move", b.Rev, want)
	}
}
