package tasks

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Several agents claim one task at the same moment: the claim is one write transaction that takes the
// lock first, so exactly one of them gets it, and the others are told who holds it.
func TestSeveralAgentsClaimingOneTaskAtOnceTakeItOnce(t *testing.T) {
	s, db, _ := newTestStore(t)
	task := mustAdd(t, s, "default", "contested", true)
	const agents = 12
	errs := make([]error, agents)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.Claim(bg, "default", task.ID, bot(fmt.Sprintf("agent-%d", i)), 0) })
	}
	wg.Wait()
	winner := ""
	for i, err := range errs {
		if err == nil {
			if winner != "" {
				t.Errorf("two agents took the task: %s and agent-%d", winner, i)
			}
			winner = fmt.Sprintf("agent-%d", i)
		}
	}
	if winner == "" {
		t.Fatal("no agent took the task")
	}
	for i, err := range errs {
		var ce *ClaimedError
		if err != nil && (!errors.Is(err, ErrClaimed) || !errors.As(err, &ce) || ce.By != winner) {
			t.Errorf("agent-%d: %v, want ErrClaimed naming %s", i, err, winner)
		}
	}
	if by, _ := opsClaim(t, db, task.ID); by != winner {
		t.Errorf("the task is held by %q, want %q", by, winner)
	}
	if n := countWhere(t, db, "task_events", "task_id = ? AND kind = 'claimed'", task.ID); n != 1 {
		t.Errorf("%d claimed events, want 1", n)
	}
	if rev := claimsRev(t, s); rev != 2 {
		t.Errorf("revision %d, want 2: the add and the one claim that was made", rev)
	}
}

// More agents than tasks ask for the next one at the same moment: every task is taken once, and the
// agents that came too late are told there is nothing (not an error).
func TestSeveralAgentsTakingTheNextTaskAtOnceTakeOneEach(t *testing.T) {
	s, db, _ := newTestStore(t)
	const tasks, agents = 8, 20
	for i := 0; i < tasks; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true)
	}
	got := make([]*Task, agents)
	errs := make([]error, agents)
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() { got[i], errs[i] = s.Next(bg, "default", bot(fmt.Sprintf("agent-%d", i)), true, 0) })
	}
	wg.Wait()
	taken := map[int64]string{}
	for i, task := range got {
		if errs[i] != nil {
			t.Errorf("agent-%d: %v", i, errs[i])
			continue
		}
		if task == nil {
			continue
		}
		if by, dup := taken[task.ID]; dup {
			t.Errorf("task #%d was taken by %s and by agent-%d", task.ID, by, i)
		}
		taken[task.ID] = fmt.Sprintf("agent-%d", i)
		if task.Claim == nil || task.Claim.By != taken[task.ID] {
			t.Errorf("agent-%d was given #%d with the claim %+v", i, task.ID, task.Claim)
		}
	}
	if len(taken) != tasks {
		t.Errorf("%d tasks were taken, want %d", len(taken), tasks)
	}
	if n := countWhere(t, db, "task_events", "kind = 'claimed'"); n != tasks {
		t.Errorf("%d claimed events, want %d", n, tasks)
	}
	if rev := claimsRev(t, s); rev != 2*tasks {
		t.Errorf("revision %d, want %d: each add and each claim", rev, 2*tasks)
	}
}

// The holder renews its lease with different lengths at the same moment: whatever order the renewals
// are made in, the lease ends up the longest of them. None of them shortens it.
func TestRenewalsAtOnceNeverShortenALease(t *testing.T) {
	s, db, clk := newTestStore(t)
	task := mustAdd(t, s, "default", "long job", true)
	claimsClaim(t, s, task.ID, "one", 10*time.Minute)
	const callers = 12
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range errs {
		lease := time.Duration(i+1) * 15 * time.Minute // 15 minutes to 3 hours
		wg.Go(func() { _, errs[i] = s.Claim(bg, "default", task.ID, bot("one"), lease) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("renewal %d: %v", i, err)
		}
	}
	longest := clk.t.Add(callers * 15 * time.Minute).Format(timeFmt)
	if _, until := opsClaim(t, db, task.ID); until != longest {
		t.Errorf("the lease ends at %s, want the longest of the renewals, %s", until, longest)
	}
	if n := countWhere(t, db, "task_events", "task_id = ? AND note = 'renewed'", task.ID); n != callers {
		t.Errorf("%d renewed events, want %d", n, callers)
	}
}

// Agents and the operator work the same few tasks at once, and time passes while they do, so claims run
// out and are taken over. Whatever the order was, the history of every task has to explain the task: it
// is held by one agent at a time, only its holder comments, finishes or releases it, and a takeover is
// of a claim somebody holds; and the revision counts the writes that went through.
func TestAWorkloadAtOnceLeavesAHistoryThatExplainsEveryTask(t *testing.T) {
	s, db, _ := newTestStore(t)
	var nanos atomic.Int64
	nanos.Store(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC).UnixNano())
	s.now = func() time.Time { return time.Unix(0, nanos.Load()).UTC() }

	const first = 5
	var mu sync.Mutex
	var ids []int64
	for i := 0; i < first; i++ {
		ids = append(ids, mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true).ID)
	}
	pick := func(r *rand.Rand) int64 {
		mu.Lock()
		defer mu.Unlock()
		return ids[r.Intn(len(ids))]
	}
	var written atomic.Int64 // the writes that went through, adds included
	failures := make(chan error, 1000)
	const workers, steps = 6, 60
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			r := rand.New(rand.NewSource(int64(w) + 1))
			me := bot(fmt.Sprintf("w%d", w%4)) // two workers share a name: they are one claimant
			for i := 0; i < steps; i++ {
				var err error
				wrote := true
				switch r.Intn(9) {
				case 0, 1:
					_, err = s.Claim(bg, "default", pick(r), me, time.Duration(r.Intn(45))*time.Minute)
				case 2, 3:
					var got *Task
					got, err = s.Next(bg, "default", me, true, time.Duration(r.Intn(45))*time.Minute)
					wrote = got != nil
				case 4:
					_, err = s.Comment(bg, "default", pick(r), "note", me)
				case 5:
					_, err = s.Comment(bg, "default", pick(r), "from the operator", human)
				case 6:
					_, err = s.Finish(bg, "default", pick(r), Outcome{Result: "done"}, me)
				case 7:
					_, err = s.Release(bg, "default", pick(r), "no", me)
				case 8:
					if r.Intn(2) == 0 {
						nanos.Add(int64(time.Duration(r.Intn(30)) * time.Minute)) // time passes: leases run out
						continue
					}
					var task Task
					task, _, err = s.Add(bg, "default", AddInput{Title: "later", Ready: true}, human)
					if err == nil {
						mu.Lock()
						ids = append(ids, task.ID)
						mu.Unlock()
					}
				}
				switch {
				case err == nil && wrote:
					written.Add(1)
				case err == nil, errors.Is(err, ErrClaimed), errors.Is(err, ErrNotClaimant), errors.Is(err, ErrNotReady):
				default:
					failures <- err
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("a call failed with %v: only the refusals of the rules are expected", err)
	}
	if written.Load() == 0 {
		t.Fatal("nothing was written: the workload did not run")
	}
	if want := first + written.Load(); claimsRev(t, s) != want {
		t.Errorf("revision %d, want %d: one for every write that went through", claimsRev(t, s), want)
	}

	if claimsExplainEveryTask(t, s, db) == 0 {
		t.Error("no claim was made")
	}
}

// Agents claim a card while the operator moves, archives or approves it. Each of these is a write
// transaction that takes the lock first, so every call goes entirely before or entirely after every
// other, and the card ends as one of the orders leaves it: archived whether or not an agent had it
// first, say, or taken by the agent that was quick enough once approved. What the test holds the store
// to, round after round with the calls started together, is all or nothing: every call either goes
// through or is refused by a rule (never by an error of the store), a card the operator took out of the
// queue is taken by at most one agent, the revision counts the calls that went through and no others,
// and the history of every card, replayed, gives the row that it left.
func TestClaimsAtOnceWithTheOperatorsMoveArchiveOrApprovalLeaveTheCardAsAnOrderWould(t *testing.T) {
	const agents, callers, rounds = 3, 2, 12
	for _, op := range []struct {
		name  string
		inbox bool     // the card starts in Inbox, otherwise in Ready
		once  bool     // the card does not come back to Ready, so at most one claim can go through
		wins  int      // how many of the operator's calls go through, of the callers that make them at once
		ends  []Status // where the card may end
		call  func(s *Store, id int64) error
	}{
		{"Move to the top of Review", false, true, callers, []Status{StatusReview}, func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, StatusReview, Placement{Top: true}, human)
			return err
		}},
		{"Move to the top of Ready", false, false, callers, []Status{StatusReady, StatusInProgress}, func(s *Store, id int64) error {
			_, err := s.Move(bg, "default", id, StatusReady, Placement{Top: true}, human)
			return err
		}},
		{"Archive", false, true, 1, []Status{StatusArchived}, func(s *Store, id int64) error {
			_, err := s.Archive(bg, "default", []int64{id}, human)
			return err
		}},
		{"Approve", true, true, 1, []Status{StatusReady, StatusInProgress}, func(s *Store, id int64) error {
			_, err := s.Approve(bg, "default", []int64{id}, false, human)
			return err
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			s, db, _ := newTestStore(t)
			claimed, done := 0, 0
			ended := map[Status]int{}
			for round := 0; round < rounds; round++ {
				id := mustAdd(t, s, "default", fmt.Sprintf("card %d", round), !op.inbox).ID
				claimErrs, callErrs := make([]error, agents), make([]error, callers)
				start := make(chan struct{})
				var parked, wg sync.WaitGroup
				launch := func(delay time.Duration, call func()) {
					parked.Add(1)
					wg.Go(func() {
						parked.Done()
						<-start
						time.Sleep(delay)
						call()
					})
				}
				for i := range claimErrs {
					launch(0, func() { _, claimErrs[i] = s.Claim(bg, "default", id, bot(fmt.Sprintf("agent-%d", i)), 0) })
				}
				for i := range callErrs { // the operator's calls come a little later in some rounds, so that either order is seen
					launch(time.Duration(round%5)*400*time.Microsecond, func() { callErrs[i] = op.call(s, id) })
				}
				parked.Wait()
				close(start)
				wg.Wait()

				through := 0
				for _, err := range callErrs {
					switch {
					case err == nil:
						through++
					case op.wins == callers || !errors.Is(err, ErrInvalid):
						t.Errorf("round %d: a call of the operator failed with %v, want it to go through or to be refused as a second one", round, err)
					}
				}
				if through != op.wins {
					t.Errorf("round %d: %d of the operator's %d calls went through, want %d", round, through, callers, op.wins)
				}
				took := 0
				for _, err := range claimErrs {
					switch {
					case err == nil:
						took++
					case !errors.Is(err, ErrClaimed) && !errors.Is(err, ErrNotReady):
						t.Errorf("round %d: a claim failed with %v, want it to go through or to be refused by a rule", round, err)
					}
				}
				if op.once && took > 1 {
					t.Errorf("round %d: %d agents took a card that does not come back to Ready", round, took)
				}
				claimed, done = claimed+took, done+through

				card, _, err := s.Get(bg, "default", id)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(op.ends, card.Status) {
					t.Errorf("round %d: the card ended in %s, want one of %v", round, card.Status, op.ends)
				}
				if card.Status != StatusInProgress && card.Claim != nil {
					t.Errorf("round %d: the card ended in %s with the claim %+v", round, card.Status, card.Claim)
				}
				ended[card.Status]++
			}
			if n := countWhere(t, db, "task_events", "kind IN ('claimed', 'reclaimed')"); n != claimed {
				t.Errorf("%d claim events, want %d: one for each claim that went through", n, claimed)
			}
			if want := int64(rounds + claimed + done); claimsRev(t, s) != want {
				t.Errorf("revision %d, want %d: one for each card added, claim and call of the operator that went through", claimsRev(t, s), want)
			}
			claimsExplainEveryTask(t, s, db)
			t.Logf("%d claims and %d calls of the operator went through; the cards ended %v", claimed, done, ended)
		})
	}
}
