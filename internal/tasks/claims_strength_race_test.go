package tasks

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// claimsTogether runs call(0..n-1) at the same moment: every goroutine is parked before any of them starts.
func claimsTogether(n int, call func(i int)) {
	var parked, wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		parked.Add(1)
		wg.Go(func() {
			parked.Done()
			<-start
			call(i)
		})
	}
	parked.Wait()
	close(start)
	wg.Wait()
}

// 8 goroutines claim one Ready task: exactly one wins and the others are told who it is.
func TestSixteenAgentsClaimingOneTaskAtOnceTakeItOnce(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "contested", true)
	const agents = 8
	errs := make([]error, agents)
	claimsTogether(agents, func(i int) {
		_, errs[i] = s.Claim(bg, "default", task.ID, bot(fmt.Sprintf("agent-%d", i)), 0)
	})
	winner, wins := "", 0
	for i, err := range errs {
		if err == nil {
			wins++
			winner = fmt.Sprintf("agent-%d", i)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners, want 1: %v", wins, errs)
	}
	for i, err := range errs {
		var ce *ClaimedError
		if err != nil && (!errors.Is(err, ErrClaimed) || !errors.As(err, &ce) || ce.By != winner) {
			t.Errorf("agent-%d: %v, want ErrClaimed naming %s", i, err, winner)
		}
	}
	if by, _ := opsClaim(t, db, task.ID); by != winner {
		t.Errorf("held by %q, want %q", by, winner)
	}
	if n := countWhere(t, db, "task_events", "task_id = ? AND kind = 'claimed'", task.ID); n != 1 {
		t.Errorf("%d claimed events, want 1", n)
	}
	if rev := claimsRev(t, s); rev != 2 {
		t.Errorf("revision %d, want 2", rev)
	}
}

// 8 goroutines take the next of 5 Ready tasks: 5 distinct winners, 3 told there is nothing, no error.
func TestSixteenAgentsTakingTheNextOfFiveTasksTakeOneEach(t *testing.T) {
	s, db, _ := newTestStore(t)
	const tasks, agents = 5, 8
	for i := 0; i < tasks; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true)
	}
	got := make([]*Task, agents)
	errs := make([]error, agents)
	claimsTogether(agents, func(i int) {
		got[i], errs[i] = s.Next(bg, "default", bot(fmt.Sprintf("agent-%d", i)), true, 0)
	})
	taken, nothing := map[int64]int{}, 0
	for i := range got {
		switch {
		case errs[i] != nil:
			t.Errorf("agent-%d: %v", i, errs[i])
		case got[i] == nil:
			nothing++
		default:
			if prev, dup := taken[got[i].ID]; dup {
				t.Errorf("#%d taken by agent-%d and agent-%d", got[i].ID, prev, i)
			}
			taken[got[i].ID] = i
			if got[i].Claim == nil || got[i].Claim.By != fmt.Sprintf("agent-%d", i) {
				t.Errorf("agent-%d got #%d with claim %+v", i, got[i].ID, got[i].Claim)
			}
		}
	}
	if len(taken) != tasks || nothing != agents-tasks {
		t.Errorf("%d winners and %d told nothing, want %d and %d", len(taken), nothing, tasks, agents-tasks)
	}
	if n := countWhere(t, db, "task_events", "kind = 'claimed'"); n != tasks {
		t.Errorf("%d claimed events, want %d", n, tasks)
	}
	if rev := claimsRev(t, s); rev != 2*tasks {
		t.Errorf("revision %d, want %d", rev, 2*tasks)
	}
}

// A claim, the holder's own release, finish, comment and renewal, and the operator's move to Done, all
// started together on one card, round after round: every call goes through or is refused by a rule, the
// revision counts the calls that went through, and the history of each card explains its row.
func TestAClaimRacesTheHoldersReleaseFinishCommentAndRenewalAndTheOperatorsMove(t *testing.T) {
	s, db, _ := newTestStore(t)
	const rounds = 12
	var wrote int64
	endings := map[Status]int{}
	wins := map[string]int{}
	for round := 0; round < rounds; round++ {
		id := mustAdd(t, s, "default", fmt.Sprintf("card %d", round), true).ID
		claimsClaim(t, s, id, "one", 0)
		wrote += 2
		type call struct {
			name string
			run  func() (bool, error) // whether it wrote, and what it said
		}
		calls := []call{
			{"one releases", func() (bool, error) { _, err := s.Release(bg, "default", id, "no", bot("one")); return err == nil, err }},
			{"one finishes", func() (bool, error) {
				_, err := s.Finish(bg, "default", id, Outcome{Result: "r"}, bot("one"))
				return err == nil, err
			}},
			{"one comments", func() (bool, error) { _, err := s.Comment(bg, "default", id, "c", bot("one")); return err == nil, err }},
			{"one renews", func() (bool, error) {
				_, err := s.Claim(bg, "default", id, bot("one"), time.Hour)
				return err == nil, err
			}},
			{"two claims", func() (bool, error) { _, err := s.Claim(bg, "default", id, bot("two"), 0); return err == nil, err }},
			{"three takes the next", func() (bool, error) {
				got, err := s.Next(bg, "default", bot("three"), true, 0)
				return err == nil && got != nil, err
			}},
			{"four comments", func() (bool, error) { _, err := s.Comment(bg, "default", id, "c", bot("four")); return err == nil, err }},
			{"the operator moves it", func() (bool, error) { // to Done in even rounds, to the top of Ready in odd ones
				var err error
				if round%2 == 0 {
					_, err = s.Move(bg, "default", id, StatusDone, Placement{}, human)
				} else {
					_, err = s.Move(bg, "default", id, StatusReady, Placement{Top: true}, human)
				}
				return err == nil, err
			}},
		}
		wroteIt := make([]bool, len(calls))
		errs := make([]error, len(calls))
		claimsTogether(len(calls), func(i int) { wroteIt[i], errs[i] = calls[i].run() })
		for i, err := range errs {
			switch {
			case err == nil:
			case errors.Is(err, ErrClaimed), errors.Is(err, ErrNotClaimant), errors.Is(err, ErrNotReady):
			default:
				t.Errorf("round %d, %s: %v: not one of the refusals of the rules", round, calls[i].name, err)
			}
			if wroteIt[i] {
				wrote++
				wins[calls[i].name]++
			}
		}
		if !wroteIt[len(calls)-1] {
			t.Errorf("round %d: the operator's move did not go through: %v", round, errs[len(calls)-1])
		}
		card, _, err := s.Get(bg, "default", id)
		if err != nil {
			t.Fatal(err)
		}
		endings[card.Status]++
	}
	// The cards "three" took with Next belong to other rounds' cards or none; the replay says whether each is right.
	if rev := claimsRev(t, s); rev != wrote {
		t.Errorf("revision %d, want %d: one for each write that went through", rev, wrote)
	}
	claimsExplainEveryTask(t, s, db)
	t.Logf("cards ended %v; calls that went through, over %d rounds: %v", endings, rounds, wins)
}

// A claim races the end of a lease. Every reading of the clock moves it a second, from five seconds
// before the lease ends, so the claims are made at instants on both sides of the end. 8 agents take the
// stale claim and its holder renews it, together: exactly one of the 9 calls succeeds, and a takeover is
// dated at or after the end of the lease it took over.
func TestClaimsAndARenewalRaceTheEndOfALease(t *testing.T) {
	s, db, clk := newTestStore(t)
	task := mustAdd(t, s, "default", "abandoned", true)
	claimsClaim(t, s, task.ID, "one", 10*time.Minute)
	end := clk.t.Add(10 * time.Minute)
	var nanos atomic.Int64
	nanos.Store(end.Add(-5 * time.Second).UnixNano())
	s.now = func() time.Time { return time.Unix(0, nanos.Add(int64(time.Second))).UTC() }
	const takers = 8
	errs := make([]error, takers+1)
	claimsTogether(takers+1, func(i int) {
		if i == takers {
			_, errs[i] = s.Claim(bg, "default", task.ID, bot("one"), 10*time.Minute)
			return
		}
		_, errs[i] = s.Claim(bg, "default", task.ID, bot(fmt.Sprintf("agent-%d", i)), 0)
	})
	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrClaimed):
			t.Errorf("call %d: %v, want a success or ErrClaimed", i, err)
		}
	}
	if wins != 1 {
		t.Errorf("%d of %d calls succeeded, want exactly 1: %v", wins, takers+1, errs)
	}
	_, events, err := s.Get(bg, "default", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == "reclaimed" && e.At.Before(end) {
			t.Errorf("a takeover dated %v, before the lease ended at %v", e.At, end)
		}
	}
	s.now = clk.now
	claimsExplainEveryTask(t, s, db)
}

// Two Store values over two connection pools on one database file, as two processes would be: 8 agents,
// four through each, take the next of five tasks, then claim one task by id.
func TestTwoStoresOnOneFileTakeEachTaskOnce(t *testing.T) {
	path := testdb.Path(t)
	var stores [2]*Store
	for i := range stores {
		db, err := storage.NewDatabase(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		stores[i] = NewStore(db.DB)
	}
	for i := 0; i < 6; i++ {
		mustAdd(t, stores[0], "default", fmt.Sprintf("t%d", i), true)
	}
	const agents = 8
	got := make([]*Task, agents)
	errs := make([]error, agents)
	claimsTogether(agents, func(i int) {
		got[i], errs[i] = stores[i%2].Next(bg, "default", bot(fmt.Sprintf("agent-%d", i)), true, 0)
	})
	var ids []int64
	for i := range got {
		if errs[i] != nil {
			t.Errorf("agent-%d: %v", i, errs[i])
		} else if got[i] != nil {
			ids = append(ids, got[i].ID)
		}
	}
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) || len(ids) != 6 {
		t.Errorf("next took %v, want 6 distinct tasks", ids)
	}
	contested := mustAdd(t, stores[1], "default", "contested", true)
	cerrs := make([]error, agents)
	claimsTogether(agents, func(i int) {
		_, cerrs[i] = stores[i%2].Claim(bg, "default", contested.ID, bot(fmt.Sprintf("agent-%d", i)), 0)
	})
	wins := 0
	for i, err := range cerrs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrClaimed):
			t.Errorf("agent-%d: %v", i, err)
		}
	}
	if wins != 1 {
		t.Errorf("%d winners of one task across two stores, want 1", wins)
	}
}

// A claim that holds the write lock is made to wait, inside the lock, after it has read the task: the
// second reading of the clock is the one in the read of the task, which comes after the pick. The second
// claim is started then and given time to read what it can before the lock is let go. A claim that takes
// the lock first (BEGIN IMMEDIATE) and picks inside it waits, and then sees what the first one did, so
// the two agents never get the same task; one that picked before the lock, or that took it late, would
// have read the task as it was and lost to the first. This is what the races above show by chance, shown
// by construction.
func TestASecondClaimWaitsForTheWriteLockAndThenSeesWhatTheFirstClaimDid(t *testing.T) {
	for _, c := range []struct {
		name  string
		tasks int
		claim func(s *Store, name string, id int64) (*Task, error) // id is the first task's
		check func(t *testing.T, ids []int64, a, b *Task, errA, errB error)
	}{
		{"next --claim and next --claim", 2, func(s *Store, name string, id int64) (*Task, error) {
			return s.Next(bg, "default", bot(name), true, 0)
		}, func(t *testing.T, ids []int64, a, b *Task, errA, errB error) {
			if errA != nil || errB != nil || a == nil || b == nil || a.ID != ids[0] || b.ID != ids[1] {
				t.Errorf("the first took %+v (%v), the second %+v (%v), want #%d and #%d", a, errA, b, errB, ids[0], ids[1])
			}
		}},
		{"claim and claim of one task", 1, func(s *Store, name string, id int64) (*Task, error) {
			got, err := s.Claim(bg, "default", id, bot(name), 0)
			if err != nil {
				return nil, err
			}
			return &got, nil
		}, func(t *testing.T, ids []int64, a, b *Task, errA, errB error) {
			var ce *ClaimedError
			if errA != nil || a == nil || a.ID != ids[0] || !errors.Is(errB, ErrClaimed) || !errors.As(errB, &ce) || ce.By != "a" {
				t.Errorf("the first: %+v (%v); the second: %v, want the first to hold the task and the second to be told so", a, errA, errB)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _, clk := newTestStore(t)
			var ids []int64
			for i := 0; i < c.tasks; i++ {
				ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true).ID)
			}
			var reads atomic.Int32
			reached, release := make(chan struct{}), make(chan struct{})
			s.now = func() time.Time {
				if reads.Add(1) == 2 {
					close(reached)
					<-release
				}
				return clk.t
			}
			var a, b *Task
			var errA, errB error
			var wg sync.WaitGroup
			wg.Go(func() { a, errA = c.claim(s, "a", ids[0]) })
			select {
			case <-reached:
			case <-time.After(30 * time.Second):
				t.Fatal("the first claim never read the task")
			}
			wg.Go(func() { b, errB = c.claim(s, "b", ids[0]) })
			time.Sleep(300 * time.Millisecond) // the second claim reads what it can while the first holds the lock
			close(release)
			wg.Wait()
			c.check(t, ids, a, b, errA, errB)
		})
	}
}
