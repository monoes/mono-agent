package tasks

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

func TestConcurrentClaimsNeverShareATask(t *testing.T) {
	s, _, _ := newTestStore(t)
	const nTasks, nAgents = 6, 12
	for i := 0; i < nTasks; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true)
	}
	var mu sync.Mutex
	got := map[int64]string{}
	var wg sync.WaitGroup
	for a := 0; a < nAgents; a++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			task, err := s.Next(bg, "default", bot(name), true, 0)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				return
			}
			if task == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if prev, dup := got[task.ID]; dup {
				t.Errorf("task #%d was claimed by %s and by %s", task.ID, prev, name)
			}
			got[task.ID] = name
		}(fmt.Sprintf("agent-%d", a))
	}
	wg.Wait()
	if len(got) != nTasks {
		t.Fatalf("%d of %d tasks were claimed", len(got), nTasks)
	}
}

// checkAddedOnce is what one client id added by many callers at once leaves behind, besides the one
// row: one created event and one bump of the revision, the ones of the add that made the task.
func checkAddedOnce(t *testing.T, s *Store, db *sql.DB) {
	t.Helper()
	if n := countWhere(t, db, "task_events", "kind = 'created'"); n != 1 {
		t.Errorf("%d created events, want 1", n)
	}
	if rev, err := s.Rev(bg, "default"); err != nil || rev != 1 {
		t.Errorf("revision %d (%v), want 1: only the add that made the task writes", rev, err)
	}
}

func TestConcurrentAddsWithOneClientIDMakeOneTask(t *testing.T) {
	s, db, _ := newTestStore(t)
	var created int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.Add(bg, "default",
				AddInput{Title: "once", ClientID: "shared-1", SourceKind: SourceChrome}, Actor{Kind: Capture, Name: SourceChrome})
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				atomic.AddInt32(&created, 1)
			}
		}()
	}
	wg.Wait()
	if n := countWhere(t, db, "tasks", "client_id = 'shared-1'"); n != 1 || created != 1 {
		t.Fatalf("%d rows and %d creations, want one of each", n, created)
	}
	checkAddedOnce(t, s, db)
}

// TestHelperProcess is not a test: the tests below run it in other processes,
// so that several processes share one database file with this one. A helper
// opens the database, says READY and waits for the word to start, so that the
// helpers begin together and are in each other's way; its store's clock takes a
// moment (claimsSlowStore), asked inside the write lock, which keeps them there
// long enough for a lock that is not taken first to show.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("TASKS_HELPER") != "1" {
		t.Skip("the helper of the tests that run other processes")
	}
	db, err := storage.NewDatabase(os.Getenv("TASKS_HELPER_DB"))
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	defer db.Close()
	s := claimsSlowStore(db)
	fmt.Println("READY")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		os.Exit(1)
	}
	switch os.Getenv("TASKS_HELPER_MODE") {
	case "claim":
		for {
			task, err := s.Next(bg, "default", bot(os.Getenv("TASKS_HELPER_NAME")), true, 0)
			if err != nil {
				fmt.Println("ERROR", err)
				os.Exit(1)
			}
			if task == nil {
				return
			}
			fmt.Printf("RESULT %d\n", task.ID)
			time.Sleep(3 * time.Millisecond) // the lock goes to whoever waits for it
		}
	case "add":
		_, created, err := s.Add(bg, "default",
			AddInput{Title: "once", ClientID: "shared-1", SourceKind: SourceOS}, Actor{Kind: Capture, Name: SourceOS})
		if err != nil {
			fmt.Println("ERROR", err)
			os.Exit(1)
		}
		fmt.Printf("RESULT %v\n", created)
	}
}

// runHelpers runs n helper processes at once against the database file and
// returns the RESULT lines they printed. It waits until every helper has the
// database open before it lets any of them start, and a helper that hangs ends
// with the deadline and fails the test, not the run. What a helper wrote to its
// stderr (a panic, a report of the race detector) is in the message of the
// failure.
func runHelpers(t *testing.T, path, mode string, n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 2*time.Minute)
	defer cancel() // ends the helpers still running when this returns, a failure included
	cmds := make([]*exec.Cmd, n)
	ins := make([]io.WriteCloser, n)
	outs := make([]*bufio.Reader, n)
	stderr := make([]*claimsOutput, n)
	// atexit_sleep_ms=0: the race runtime would otherwise sleep a second before a process exits. It is added
	// to the options this run has, not put in their place.
	race := strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
	for i := range cmds {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(),
			"TASKS_HELPER=1", "TASKS_HELPER_DB="+path, "TASKS_HELPER_MODE="+mode,
			fmt.Sprintf("TASKS_HELPER_NAME=proc-%d", i), "GORACE="+race)
		stderr[i] = &claimsOutput{}
		cmd.Stderr = stderr[i]
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i], ins[i], outs[i] = cmd, in, bufio.NewReader(out)
	}
	for i, out := range outs {
		if line, err := out.ReadString('\n'); strings.TrimSpace(line) != "READY" {
			t.Fatalf("helper %d said %q (%v), want READY; its stderr:\n%s", i, line, err, stderr[i])
		}
	}
	for i, in := range ins {
		if _, err := fmt.Fprintln(in, "go"); err != nil {
			t.Fatalf("helper %d: %v; its stderr:\n%s", i, err, stderr[i])
		}
	}
	var lines []string
	for i, out := range outs {
		rest, err := io.ReadAll(out)
		if err != nil {
			t.Fatalf("helper %d: %v; its stderr:\n%s", i, err, stderr[i])
		}
		if err := cmds[i].Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s\nits stderr:\n%s", i, err, rest, stderr[i])
		}
		var mine int
		for _, l := range strings.Split(string(rest), "\n") {
			if r, ok := strings.CutPrefix(l, "RESULT "); ok {
				lines = append(lines, r)
				mine++
			}
		}
		t.Logf("helper %d printed %d results", i, mine)
	}
	return lines
}

func TestClaimsAcrossProcessesNeverShareATask(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db.DB)
	const nTasks = 20
	for i := 0; i < nTasks; i++ {
		mustAdd(t, s, "default", fmt.Sprintf("t%d", i), true)
	}
	db.Close()

	lines := runHelpers(t, path, "claim", 4)
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l] {
			t.Fatalf("task #%s was claimed twice: %v", l, lines)
		}
		seen[l] = true
	}
	if len(seen) != nTasks {
		t.Fatalf("%d of %d tasks were claimed: %v", len(seen), nTasks, lines)
	}

	// What the helpers said is what the board says: every task is held, each by one claim.
	db, err = storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := countWhere(t, db.DB, "task_events", "kind = 'claimed'"); n != nTasks {
		t.Errorf("%d claimed events, want %d", n, nTasks)
	}
	if n := countWhere(t, db.DB, "tasks", "status = 'in_progress' AND claimed_by <> ''"); n != nTasks {
		t.Errorf("%d tasks are held, want %d", n, nTasks)
	}
}

func TestAddsWithOneClientIDAcrossProcessesMakeOneTask(t *testing.T) {
	path := testdb.Path(t)
	lines := runHelpers(t, path, "add", 5)
	created := 0
	for _, l := range lines {
		if l == "true" {
			created++
		}
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := countWhere(t, db.DB, "tasks", "client_id = 'shared-1'"); n != 1 || created != 1 || len(lines) != 5 {
		t.Fatalf("%d rows, %d creations among %d answers: %v", n, created, len(lines), lines)
	}
	checkAddedOnce(t, NewStore(db.DB), db.DB)
}
