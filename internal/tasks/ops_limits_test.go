package tasks

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Every add and every card that comes back from the archive counts the open tasks of the profile, and
// the archive is the one column that has no limit: the count must seek the five columns of the board in
// the index and not read the whole range of the profile, archive included. A range that leaves one
// value out (status <> 'archived') seeks the profile only; the plan of the statement the store runs
// names the status in the seek. Pinned by the plan and not by a timing, which would be flaky: the plan
// must name the index, the status as well as the profile, and no scan of the table.
func TestTheOpenTaskCountSeeksTheFiveColumnsAndDoesNotReadTheArchive(t *testing.T) {
	_, db, _ := newTestStore(t)
	rows, err := db.Query("EXPLAIN QUERY PLAN "+openTasksSQL, "default")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "idx_tasks_board") || !strings.Contains(text, "status=?") || strings.Contains(text, "SCAN") {
		t.Errorf("the plan of the open-task count:\n%s\nwant a seek in idx_tasks_board on the status as well as the profile, and no scan", text)
	}
}

// The limit of 2,000 open tasks is taken in the same BEGIN IMMEDIATE transaction as the card that is let
// in, so of several callers for the last slot exactly one gets it and the others are refused as over the
// limit; the board ends with 2,000 open tasks, not more.
func TestTheLastSlotOfTheBoardIsGivenToOneCallerAtOnce(t *testing.T) {
	const callers = 8
	for _, k := range []struct {
		name string
		call func(s *Store, i int, archived []int64) error
	}{
		{"Unarchive", func(s *Store, i int, archived []int64) error {
			_, err := s.Unarchive(bg, "default", []int64{archived[i]}, human)
			return err
		}},
		{"Add", func(s *Store, i int, archived []int64) error {
			_, _, err := s.Add(bg, "default", AddInput{Title: fmt.Sprintf("new %d", i)}, human)
			return err
		}},
	} {
		t.Run(k.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			seedTasks(t, db, "default", MaxOpenTasks-1) // 1,999 open: one slot left
			var archived []int64
			for i := 0; i < callers; i++ {
				archived = append(archived, seedRow(t, db, "default", "archived"))
			}
			errs := make([]error, callers)
			var wg sync.WaitGroup
			for i := range errs {
				wg.Go(func() { errs[i] = k.call(s, i, archived) })
			}
			wg.Wait()
			won := 0
			for _, err := range errs {
				switch {
				case err == nil:
					won++
				case !errors.Is(err, ErrLimit):
					t.Errorf("a caller failed with %v, want ErrLimit for the ones that lost", err)
				}
			}
			if won != 1 {
				t.Errorf("%d of %d callers got the last slot, want 1", won, callers)
			}
			if open := countWhere(t, db, "tasks", "profile_id = 'default' AND status <> 'archived'"); open != MaxOpenTasks {
				t.Errorf("%d open tasks, want %d", open, MaxOpenTasks)
			}
			if rev := boardRev(t, s); rev != 1 {
				t.Errorf("revision %d, want 1: only the call that won is a write", rev)
			}
		})
	}
}
