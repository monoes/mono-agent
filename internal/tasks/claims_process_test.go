package tasks

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

const (
	claimsHelperDB   = "TASKS_CLAIMS_HELPER_DB"
	claimsHelperName = "TASKS_CLAIMS_HELPER_NAME"
)

// claimsSlowStore is a store over the database whose clock takes its time. A claim asks the clock after
// it has read the task and before it writes, inside the write lock, so the claims of the processes
// really are in each other's way: a process that does not wait for the lock would read what another is
// about to take.
func claimsSlowStore(db *storage.Database) *Store {
	s := NewStore(db.DB)
	s.now = func() time.Time {
		time.Sleep(2 * time.Millisecond)
		return time.Now()
	}
	return s
}

// TestClaimsHelperProcess is no test: it is what TestSessionsInOtherProcessesNeverTakeTheSameTask runs in
// the other processes. It opens the database it is given, says it is ready, waits for the word to start,
// and then takes the next task and prints its id until there is none.
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
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		os.Exit(2)
	}
	for {
		got, err := s.Next(bg, "default", bot(os.Getenv(claimsHelperName)), true, 0)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		if got == nil {
			return
		}
		fmt.Println("claimed", got.ID)
		time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
	}
}

// The sessions that share a board are processes, not goroutines: the write lock of the database is what
// keeps them from taking the same task. Three other processes and this one take the next task from one
// board, all starting together; every task is taken, and by one of them only.
func TestSessionsInOtherProcessesNeverTakeTheSameTask(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	const tasks, others = 60, 3
	for i := 0; i < tasks; i++ {
		mustAdd(t, NewStore(db.DB), "default", fmt.Sprintf("t%d", i), true)
	}
	s := claimsSlowStore(db)

	type child struct {
		cmd   *exec.Cmd
		in    io.WriteCloser
		lines chan string
	}
	var kids []*child
	t.Cleanup(func() {
		for _, k := range kids {
			_ = k.cmd.Process.Kill()
		}
	})
	for i := 0; i < others; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestClaimsHelperProcess$")
		cmd.Env = append(os.Environ(), claimsHelperDB+"="+path, fmt.Sprintf("%s=process-%d", claimsHelperName, i))
		cmd.Stderr = os.Stderr
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
		k := &child{cmd: cmd, in: in, lines: make(chan string, tasks+8)}
		kids = append(kids, k)
		go func() {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				k.lines <- sc.Text()
			}
			close(k.lines)
		}()
	}
	for i, k := range kids {
		select {
		case line := <-k.lines:
			if line != "ready" {
				t.Fatalf("process %d said %q, want ready", i, line)
			}
		case <-time.After(2 * time.Minute):
			t.Fatalf("process %d never said it was ready", i)
		}
	}
	for _, k := range kids {
		if _, err := fmt.Fprintln(k.in, "go"); err != nil {
			t.Fatal(err)
		}
	}

	taken := map[int64]string{}
	record := func(id int64, by string) {
		if prev, dup := taken[id]; dup {
			t.Errorf("task #%d was taken by %s and by %s", id, prev, by)
		}
		taken[id] = by
	}
	for { // this process takes tasks too, while the others do
		got, err := s.Next(bg, "default", bot("this-process"), true, 0)
		if err != nil {
			t.Fatalf("this process: %v", err)
		}
		if got == nil {
			break
		}
		record(got.ID, "this-process")
		time.Sleep(5 * time.Millisecond) // the lock goes to whoever waits for it
	}
	for i, k := range kids {
		for line := range k.lines {
			switch {
			case strings.HasPrefix(line, "claimed "):
				id, err := strconv.ParseInt(strings.TrimPrefix(line, "claimed "), 10, 64)
				if err != nil {
					t.Fatalf("process %d said %q", i, line)
				}
				record(id, fmt.Sprintf("process-%d", i))
			case strings.HasPrefix(line, "error "):
				t.Errorf("process %d: %s", i, line)
			}
		}
		if err := k.cmd.Wait(); err != nil {
			t.Errorf("process %d: %v", i, err)
		}
	}
	share := map[string]int{}
	for _, by := range taken {
		share[by]++
	}
	t.Logf("who took how many of the %d tasks: %v", tasks, share)
	if len(taken) != tasks {
		t.Errorf("%d tasks were taken, want %d", len(taken), tasks)
	}
	if n := countWhere(t, db.DB, "task_events", "kind = 'claimed'"); n != tasks {
		t.Errorf("%d claimed events, want %d", n, tasks)
	}
	if n := countWhere(t, db.DB, "tasks", "status = 'in_progress' AND claimed_by <> ''"); n != tasks {
		t.Errorf("%d tasks are held, want %d", n, tasks)
	}
}
