package capturetask

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/capture"
)

// The board is monomind's file, not this package's: the dashboard, the
// mastermind skills and a second `monoagentcli capture task` all write it.
// A read-modify-write with no lock turns every one of those into a lost
// write, which is why these tests are about concurrency rather than shape.

// boardIDs returns every issue id on a board, in file order.
func boardIDs(t *testing.T, root, org string) []string {
	t.Helper()
	doc := board(t, root, org)
	raw, ok := doc["issues"].([]any)
	if !ok {
		t.Fatalf("board issues = %v", doc["issues"])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		issue, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("issue is not an object: %v", item)
		}
		id, _ := issue["id"].(string)
		out = append(out, id)
	}
	return out
}

// TestCreateConcurrentlyLosesNothing files many tasks at once. Each one is
// a full load-modify-save of the same document, so without a lock the last
// writer wins and every task filed while it was thinking is gone.
func TestCreateConcurrentlyLosesNothing(t *testing.T) {
	root := orgRoot(t, "acme")
	const n = 16

	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = seedCapture(t, capture.Meta{
			URL:        fmt.Sprintf("https://example.com/%d", i),
			Title:      fmt.Sprintf("Page %d", i),
			CapturedAt: "2026-09-20T10:00:00Z",
		})
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	ids := make([]string, n)
	errs := make([]error, n)
	for i := range dirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dirs[i]})
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = res.Issue.ID
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	assertBoardHolds(t, root, "acme", ids)
}

// TestCreateAcrossProcessesLosesNothing is the same race between separate
// processes, which is the one that actually happens: an agent files a task
// while another tool has the board open. An in-process lock would not see
// it.
func TestCreateAcrossProcessesLosesNothing(t *testing.T) {
	if os.Getenv(helperEnvRoot) != "" {
		return // this is the helper invocation; see runHelper
	}
	root := orgRoot(t, "acme")
	const n = 8

	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = seedCapture(t, capture.Meta{
			URL:        fmt.Sprintf("https://example.com/proc-%d", i),
			Title:      fmt.Sprintf("Page %d", i),
			CapturedAt: "2026-09-20T10:00:00Z",
		})
	}

	var wg sync.WaitGroup
	out := make([]string, n)
	errs := make([]error, n)
	for i := range dirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i], errs[i] = runHelper(root, "acme", dirs[i])
		}(i)
	}
	wg.Wait()

	ids := make([]string, 0, n)
	for i := range dirs {
		if errs[i] != nil {
			t.Fatalf("helper %d: %v\n%s", i, errs[i], out[i])
		}
		id := helperID(out[i])
		if id == "" {
			t.Fatalf("helper %d printed no issue id:\n%s", i, out[i])
		}
		ids = append(ids, id)
	}
	assertBoardHolds(t, root, "acme", ids)
}

// assertBoardHolds checks the board is valid JSON holding exactly these
// ids, each once — no lost write, no duplicate id.
func assertBoardHolds(t *testing.T, root, org string, ids []string) {
	t.Helper()
	seen := map[string]int{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("Create returned an empty issue id")
		}
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("issue id %s was minted %d times", id, n)
		}
	}

	onBoard := boardIDs(t, root, org)
	if len(onBoard) != len(ids) {
		t.Fatalf("board holds %d issues, %d were filed: %v", len(onBoard), len(ids), onBoard)
	}
	have := map[string]bool{}
	for _, id := range onBoard {
		if have[id] {
			t.Errorf("issue id %s is on the board twice", id)
		}
		have[id] = true
	}
	for _, id := range ids {
		if !have[id] {
			t.Errorf("task %s was filed but is not on the board", id)
		}
	}
}

// The helper process: `go test` re-runs this binary with the environment
// below set, and the test body above turns into one Create.
const (
	helperEnvRoot     = "CAPTURETASK_TEST_ROOT"
	helperEnvOrg      = "CAPTURETASK_TEST_ORG"
	helperEnvEnvelope = "CAPTURETASK_TEST_ENVELOPE"
	helperIDPrefix    = "helper-issue-id: "
)

// TestHelperCreate is not a test: it is the body of the child process the
// cross-process race test spawns. It does nothing at all unless the
// environment names a board.
func TestHelperCreate(t *testing.T) {
	root := os.Getenv(helperEnvRoot)
	if root == "" {
		t.Skip("helper process only")
	}
	res, err := Create(Options{
		Root:         root,
		Org:          os.Getenv(helperEnvOrg),
		EnvelopePath: os.Getenv(helperEnvEnvelope),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fmt.Println(helperIDPrefix + res.Issue.ID)
}

func runHelper(root, org, envelope string) (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperCreate", "-test.v")
	cmd.Env = append(os.Environ(),
		helperEnvRoot+"="+root,
		helperEnvOrg+"="+org,
		helperEnvEnvelope+"="+envelope,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func helperID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), helperIDPrefix); ok {
			return id
		}
	}
	return ""
}

// TestCreateKeepsAForeignWritersIssue is the other half of the same
// problem: something that is not this tool appended to the board between
// the read and the write. That issue must still be there afterwards.
func TestCreateKeepsAForeignWritersIssue(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	path, err := IssuesPath(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// A board written by monomind's own tooling, with a field this build
	// has never heard of.
	if err := os.WriteFile(path, []byte(`{"issues":[{"id":"issue-1","title":"theirs","cycleId":"c-1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ids := boardIDs(t, root, "acme")
	if len(ids) != 2 || ids[0] != "issue-1" || ids[1] != res.Issue.ID {
		t.Fatalf("board = %v, want theirs then ours", ids)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"cycleId"`) {
		t.Errorf("a field this build does not know was dropped:\n%s", blob)
	}
}

// TestNextIDStepsPastAFullMillisecond covers the id minting itself: a board
// already holding every id for this millisecond must still yield a free
// one rather than handing back a duplicate.
func TestNextIDStepsPastAFullMillisecond(t *testing.T) {
	at := fixedNow()()
	millis := at.UTC().UnixMilli()
	issues := make([]json.RawMessage, 0, 999)
	for n := 1; n < 1000; n++ {
		issues = append(issues, json.RawMessage(fmt.Sprintf(`{"id":"issue-%d-%03d"}`, millis, n)))
	}
	got := nextID(issues, at)
	if got == "" || hasIssue(issues, got) {
		t.Fatalf("nextID = %q, which is already on the board", got)
	}
	if !strings.HasPrefix(got, "issue-") {
		t.Errorf("nextID = %q, which is not monomind's id shape", got)
	}
}

// The lock lives in monomind's own orgs directory, so it has to be
// invisible to everything that reads that directory — above all to Boards,
// which would otherwise start offering a lock file as a place to file a
// task.
func TestTheLockIsNotMistakenForABoard(t *testing.T) {
	root := orgRoot(t, "acme")
	dir := seedCapture(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	if _, err := Create(Options{Root: root, Org: "acme", EnvelopePath: dir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	boards, err := Boards(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) != 1 || boards[0] != "acme" {
		t.Errorf("Boards = %v, want just the org", boards)
	}
	path, err := IssuesPath(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath(path)); err != nil {
		t.Fatalf("the lock was not taken where it says it is: %v", err)
	}
	if name := filepath.Base(lockPath(path)); !strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".json") {
		t.Errorf("the lock is named %q, which is not hidden from a reader of this directory", name)
	}
}
