package tasks

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

const (
	claimsHelperDB   = "TASKS_CLAIMS_HELPER_DB"
	claimsHelperName = "TASKS_CLAIMS_HELPER_NAME"

	// claimsKidsPatience bounds the whole of the test of the processes: every wait for a process, to start,
	// to finish a stage and to exit, ends by then, with the process killed and its output in the failure.
	// It is wide, because the test runs under the race detector on a machine that is busy with other work.
	claimsKidsPatience = 2 * time.Minute
)

// claimsSlowStore is a store over the database whose clock takes its time. A claim asks the clock inside
// the write lock (once for its instant, and again when it shows the task it took), so the claims of the
// processes really are in each other's way: a process that does not wait for the lock would read what
// another is about to take.
func claimsSlowStore(db *storage.Database) *Store {
	s := NewStore(db.DB)
	s.now = func() time.Time {
		time.Sleep(2 * time.Millisecond)
		return time.Now()
	}
	return s
}

// TestClaimsHelperProcess is no test: it is what the other processes of
// TestSessionsInOtherProcessesNeverTakeTheSameTask are. It opens the database it is given, says it is
// ready, and then does what it is told on its input, a command to a line, and says done after each. Each
// command gives the instant to begin at, in nanoseconds since the epoch, so that the processes start
// together:
//
//	next AT         take the next task until there is none, saying "claimed ID" for each one it took
//	claim AT ID...  claim each task in turn, saying "claimed ID" for one it took and "refused ID BY"
//	                for one that the agent BY holds
//
// It ends when its input does.
func TestClaimsHelperProcess(t *testing.T) {
	path := os.Getenv(claimsHelperDB)
	if path == "" {
		t.Skip("the helper of TestSessionsInOtherProcessesNeverTakeTheSameTask")
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		fmt.Println("error", err)
		os.Exit(2)
	}
	defer db.Close()
	s := claimsSlowStore(db)
	me := bot(os.Getenv(claimsHelperName))
	fmt.Println("ready")
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return // the input has ended: the test is over
		}
		words := strings.Fields(line)
		if len(words) < 2 {
			fmt.Println("error", "unknown command", strconv.Quote(line))
			os.Exit(2)
		}
		at, err := strconv.ParseInt(words[1], 10, 64)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(2)
		}
		time.Sleep(time.Until(time.Unix(0, at)))
		switch {
		case len(words) == 2 && words[0] == "next":
			for {
				got, err := s.Next(bg, "default", me, true, 0)
				if err != nil {
					fmt.Println("error", err)
					os.Exit(1)
				}
				if got == nil {
					break
				}
				fmt.Println("claimed", got.ID)
				time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
			}
		case len(words) > 2 && words[0] == "claim":
			for _, w := range words[2:] {
				id, err := strconv.ParseInt(w, 10, 64)
				if err != nil {
					fmt.Println("error", err)
					os.Exit(2)
				}
				var held *ClaimedError
				switch _, err := s.Claim(bg, "default", id, me, 0); {
				case err == nil:
					fmt.Println("claimed", id)
				case errors.As(err, &held):
					fmt.Println("refused", id, held.By)
				default:
					fmt.Println("error", err)
					os.Exit(1)
				}
				time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
			}
		default:
			fmt.Println("error", "unknown command", strconv.Quote(line))
			os.Exit(2)
		}
		fmt.Println("done")
	}
}

// claimsOutput is what a process has printed, for the message of a test that has to say what went wrong.
type claimsOutput struct {
	mu sync.Mutex
	b  strings.Builder
}

func (o *claimsOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *claimsOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

// claimsKid is another process of the test binary, running TestClaimsHelperProcess.
type claimsKid struct {
	name  string
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan string // what it says, a line at a time, until its output ends
	out   *claimsOutput
}

// startClaimsKids starts n processes over the database at path, and waits for each of them to say that
// it is ready. They are killed when the test ends, whatever has become of them.
func startClaimsKids(t *testing.T, path string, n int, deadline time.Time) []*claimsKid {
	t.Helper()
	var kids []*claimsKid
	t.Cleanup(func() {
		for _, k := range kids {
			_ = k.cmd.Process.Kill()
		}
	})
	// atexit_sleep_ms=0: the race runtime otherwise sleeps a second before a process exits, which every
	// process would add to the test.
	race := strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
	for i := 0; i < n; i++ {
		k := &claimsKid{name: fmt.Sprintf("process-%d", i), lines: make(chan string, 1024), out: &claimsOutput{}}
		k.cmd = exec.Command(os.Args[0], "-test.run=^TestClaimsHelperProcess$")
		k.cmd.Env = append(os.Environ(), claimsHelperDB+"="+path, claimsHelperName+"="+k.name, "GORACE="+race)
		k.cmd.Stderr = k.out
		in, err := k.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		k.in = in
		out, err := k.cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := k.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		kids = append(kids, k)
		go func() {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				fmt.Fprintln(k.out, "stdout:", sc.Text())
				k.lines <- sc.Text()
			}
			close(k.lines)
		}()
	}
	for _, k := range kids {
		if said := k.await(t, deadline, "ready"); len(said) != 0 {
			t.Fatalf("%s said %q before it was ready", k.name, said)
		}
	}
	return kids
}

// await returns what the process says until it says the line until. A process that ends first, or has not
// said it by the deadline (it is killed then), fails the test with everything the process has printed.
func (k *claimsKid) await(t *testing.T, deadline time.Time, until string) []string {
	t.Helper()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	var said []string
	for {
		select {
		case line, ok := <-k.lines:
			switch {
			case !ok:
				t.Fatalf("%s ended before it said %q; its output:\n%s", k.name, until, k.out)
			case line == until:
				return said
			}
			said = append(said, line)
		case <-timer.C:
			_ = k.cmd.Process.Kill()
			t.Fatalf("%s did not say %q in the time allowed, and was killed; its output:\n%s", k.name, until, k.out)
		}
	}
}

// send gives the process a command.
func (k *claimsKid) send(t *testing.T, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(k.in, command); err != nil {
		t.Fatalf("%s: %v; its output:\n%s", k.name, err, k.out)
	}
}

// stop ends the process by closing its input, and fails the test if it does not exit cleanly by the
// deadline (it is killed then).
func (k *claimsKid) stop(t *testing.T, deadline time.Time) {
	t.Helper()
	_ = k.in.Close()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for open := true; open; { // what it prints on its way out is read to the end before it is waited for
		select {
		case _, open = <-k.lines:
		case <-timer.C:
			_ = k.cmd.Process.Kill()
			t.Fatalf("%s did not end in the time allowed, and was killed; its output:\n%s", k.name, k.out)
		}
	}
	waited := make(chan error, 1)
	go func() { waited <- k.cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Errorf("%s: %v; its output:\n%s", k.name, err, k.out)
		}
	case <-timer.C:
		_ = k.cmd.Process.Kill()
		t.Fatalf("%s did not exit in the time allowed, and was killed; its output:\n%s", k.name, k.out)
	}
}

// claimsRefusal is a claim that an agent was told it could not make: BY, asking for the task, was told
// that holder has it.
type claimsRefusal struct {
	id         int64
	by, holder string
}

// claimsRound is what every process made of one stage: who took each task, and who was told what.
type claimsRound struct {
	won      map[int64]string
	refusals []claimsRefusal
}

// took notes that by took the task, and fails the test if somebody has taken it already.
func (r *claimsRound) took(t *testing.T, id int64, by string) {
	t.Helper()
	if prev, dup := r.won[id]; dup {
		t.Errorf("task #%d was taken by %s and by %s", id, prev, by)
	}
	r.won[id] = by
}

// claimsStartAfter is how long the processes of a stage are given to take their command before they all
// begin. A process that is slower than that begins late, and the stage is as right as before, with less to
// contest.
const claimsStartAfter = 150 * time.Millisecond

// claimsStage has every process, this one too, do the same work at the same time: the command (a verb
// and what it takes after the instant to begin at) goes to the others, and own does the work here at that
// instant. It returns what all of them took and were refused.
func claimsStage(t *testing.T, kids []*claimsKid, deadline time.Time, verb, args string, own func(r *claimsRound)) claimsRound {
	t.Helper()
	round := claimsRound{won: map[int64]string{}}
	begin := time.Now().Add(claimsStartAfter)
	for _, k := range kids {
		k.send(t, strings.TrimSpace(fmt.Sprintf("%s %d %s", verb, begin.UnixNano(), args)))
	}
	time.Sleep(time.Until(begin))
	own(&round)
	for _, k := range kids {
		for _, line := range k.await(t, deadline, "done") {
			f := strings.Fields(line)
			id := int64(0)
			if len(f) > 1 {
				id, _ = strconv.ParseInt(f[1], 10, 64)
			}
			switch {
			case len(f) == 2 && f[0] == "claimed" && id != 0:
				round.took(t, id, k.name)
			case len(f) == 3 && f[0] == "refused" && id != 0:
				round.refusals = append(round.refusals, claimsRefusal{id: id, by: k.name, holder: f[2]})
			default:
				t.Errorf("%s said %q", k.name, line)
			}
		}
	}
	return round
}

// claimsCheck holds a stage to account. Each task that was contested was taken, once, by one of the
// processes; every refusal names that one as the holder; and the task and its history say the same: the
// row is held by it, and its last claim event is its own, a claimed for a task that was Ready (the only
// claim it has) and a reclaimed for one that was stale (which follows the claim that went stale).
func claimsCheck(t *testing.T, db *sql.DB, what string, ready, stale []int64, round claimsRound) {
	t.Helper()
	contested := map[int64]bool{}
	for _, id := range append(append([]int64{}, ready...), stale...) {
		contested[id] = true
		if _, ok := round.won[id]; !ok {
			t.Errorf("%s: task #%d was taken by nobody", what, id)
		}
	}
	for id, by := range round.won {
		if !contested[id] {
			t.Errorf("%s: %s took task #%d, which nobody asked for", what, by, id)
		}
	}
	for _, r := range round.refusals {
		if r.holder != round.won[r.id] {
			t.Errorf("%s: %s was told that task #%d is held by %s, and %s took it", what, r.by, r.id, r.holder, round.won[r.id])
		}
	}
	for _, c := range []struct {
		ids    []int64
		kind   string
		claims int // the claim events the task holds when the stage is over
	}{{ready, "claimed", 1}, {stale, "reclaimed", 2}} {
		for _, id := range c.ids {
			by := round.won[id]
			if got := claimsText(t, db, `SELECT claimed_by FROM tasks WHERE id = ?`, id); got != by {
				t.Errorf("%s: task #%d is held by %q, want %q, who took it", what, id, got, by)
			}
			if n := countWhere(t, db, "task_events", "task_id = ? AND kind IN ('claimed', 'reclaimed')", id); n != c.claims {
				t.Errorf("%s: task #%d holds %d claim events, want %d", what, id, n, c.claims)
			}
			last := claimsText(t, db, `SELECT kind || '|' || actor FROM task_events WHERE task_id = ? AND kind IN ('claimed', 'reclaimed') ORDER BY id DESC LIMIT 1`, id)
			if last != c.kind+"|"+by {
				t.Errorf("%s: the last claim event of task #%d is %q, want %q", what, id, last, c.kind+"|"+by)
			}
		}
	}
}

// The sessions that share a board are processes, not goroutines: the write lock of the database is what
// keeps them from taking the same task. Three other processes and this one contest the same board in
// three stages, each starting together: next --claim over Ready tasks; a claim by id of each of a list
// of Ready tasks and of tasks whose claim has gone stale, the same ids in the same order for everybody;
// and next --claim over stale claims. Every task is taken, once, whoever took it.
func TestSessionsInOtherProcessesNeverTakeTheSameTask(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := claimsSlowStore(db)
	operator := NewStore(db.DB)
	// a claim that went stale long ago: made three hours ago, for half an hour, by an agent that is gone
	past := NewStore(db.DB)
	past.now = func() time.Time { return time.Now().Add(-3 * time.Hour) }
	add := func(n int) []int64 {
		var ids []int64
		for i := 0; i < n; i++ {
			ids = append(ids, mustAdd(t, operator, "default", fmt.Sprintf("ready %d", i), true).ID)
		}
		return ids
	}
	abandon := func(n int) []int64 {
		var ids []int64
		for i := 0; i < n; i++ {
			id := mustAdd(t, past, "default", fmt.Sprintf("abandoned %d", i), true).ID
			claimsClaim(t, past, id, "gone", 0)
			ids = append(ids, id)
		}
		return ids
	}
	next := func(r *claimsRound) { // this process takes tasks too, while the others do
		for {
			got, err := s.Next(bg, "default", bot("this-process"), true, 0)
			if err != nil {
				t.Fatalf("this process: %v", err)
			}
			if got == nil {
				return
			}
			r.took(t, got.ID, "this-process")
			time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
		}
	}
	shares := func(r claimsRound) map[string]int {
		out := map[string]int{}
		for _, by := range r.won {
			out[by]++
		}
		return out
	}

	deadline := time.Now().Add(claimsKidsPatience)
	kids := startClaimsKids(t, path, 3, deadline)

	ready := add(24)
	round := claimsStage(t, kids, deadline, "next", "", next)
	claimsCheck(t, db.DB, "next --claim over Ready tasks", ready, nil, round)
	t.Logf("who took how many of the %d Ready tasks: %v", len(ready), shares(round))

	ready, stale := add(8), abandon(8)
	var ids []int64 // a Ready task and a stale claim by turns
	for i := range ready {
		ids = append(ids, ready[i], stale[i])
	}
	var words []string
	for _, id := range ids {
		words = append(words, strconv.FormatInt(id, 10))
	}
	round = claimsStage(t, kids, deadline, "claim", strings.Join(words, " "), func(r *claimsRound) {
		for _, id := range ids {
			var held *ClaimedError
			switch _, err := s.Claim(bg, "default", id, bot("this-process"), 0); {
			case err == nil:
				r.took(t, id, "this-process")
			case errors.As(err, &held):
				r.refusals = append(r.refusals, claimsRefusal{id: id, by: "this-process", holder: held.By})
			default:
				t.Fatalf("this process: claim of #%d: %v", id, err)
			}
			time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
		}
	})
	claimsCheck(t, db.DB, "claim by id over Ready tasks and stale claims", ready, stale, round)
	t.Logf("who took how many of the %d tasks claimed by id, and how many were refused: %v, %d", len(ids), shares(round), len(round.refusals))

	stale = abandon(8)
	round = claimsStage(t, kids, deadline, "next", "", next)
	claimsCheck(t, db.DB, "next --claim over stale claims", nil, stale, round)
	t.Logf("who took how many of the %d stale claims: %v", len(stale), shares(round))

	for _, k := range kids {
		k.stop(t, deadline)
	}
	// the whole board: every history explains its task, and the revision counts every write
	if claimsExplainEveryTask(t, operator, db.DB) == 0 {
		t.Error("no claim was made")
	}
	writes := countWhere(t, db.DB, "task_events", "kind IN ('created', 'claimed', 'reclaimed')")
	if rev := claimsRev(t, operator); rev != int64(writes) {
		t.Errorf("revision %d, want %d: one for every task added and every claim made", rev, writes)
	}
}
