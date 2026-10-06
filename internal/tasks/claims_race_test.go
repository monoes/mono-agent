package tasks

import (
	"errors"
	"fmt"
	"math/rand"
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

	rows, err := db.Query(`SELECT id, status, claimed_by FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id         int64
		status, by string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.status, &r.by); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	claims := 0
	for _, r := range all {
		state, holder := "", ""
		_, events, err := s.Get(bg, "default", r.id)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range events {
			if e.ToStatus != "" {
				if e.FromStatus != "" && e.FromStatus != state {
					t.Errorf("task #%d, event %d (%s): from %s while the task was in %q", r.id, i, e.Kind, e.FromStatus, state)
				}
				state = e.ToStatus
			}
			switch e.Kind {
			case "claimed":
				claims++
				switch {
				case e.Note == "renewed" && holder != e.Actor:
					t.Errorf("task #%d, event %d: %s renewed a claim held by %q", r.id, i, e.Actor, holder)
				case e.Note != "renewed" && holder != "":
					t.Errorf("task #%d, event %d: %s claimed a task held by %q", r.id, i, e.Actor, holder)
				case e.Note != "renewed" && e.FromStatus != "ready":
					t.Errorf("task #%d, event %d: a claim from %q", r.id, i, e.FromStatus)
				}
				holder = e.Actor
			case "reclaimed":
				if holder == "" || holder == e.Actor {
					t.Errorf("task #%d, event %d: %s took over a claim held by %q", r.id, i, e.Actor, holder)
				}
				holder = e.Actor
			case "comment":
				if e.Actor != "you" && holder != e.Actor {
					t.Errorf("task #%d, event %d: %s commented on a task held by %q", r.id, i, e.Actor, holder)
				}
			case "result", "question", "released":
				if holder != e.Actor {
					t.Errorf("task #%d, event %d: %s handed back a task held by %q", r.id, i, e.Actor, holder)
				}
				holder = ""
			}
		}
		if state != r.status || holder != r.by {
			t.Errorf("task #%d: its history says %q held by %q, the row says %q held by %q", r.id, state, holder, r.status, r.by)
		}
	}
	if claims == 0 {
		t.Error("no claim was made")
	}
}
