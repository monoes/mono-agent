package dynorg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Isolated writers (#230), against real git in temp folders. No model
// runs: the fake exec writes the files its brief names.

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRun(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// newRepo is a repository with one commit holding a.txt and b.txt.
func newRepo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet")
	gitT(t, dir, "config", "user.name", "Test")
	gitT(t, dir, "config", "user.email", "test@example.com")
	gitT(t, dir, "config", "commit.gpgsign", "false")
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "--quiet", "-m", "base")
	return dir
}

// fileExec is a fake agent exec: a brief "write <file> <text>" writes text
// to file in the run's folder, then it holds for `hold`.
type fileExec struct {
	hold    time.Duration
	mu      sync.Mutex
	cwds    []string
	running int32
	maxRun  int32
}

func (e *fileExec) exec(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
	e.mu.Lock()
	e.cwds = append(e.cwds, o.Cwd)
	e.mu.Unlock()
	n := atomic.AddInt32(&e.running, 1)
	defer atomic.AddInt32(&e.running, -1)
	for {
		m := atomic.LoadInt32(&e.maxRun)
		if n <= m || atomic.CompareAndSwapInt32(&e.maxRun, m, n) {
			break
		}
	}
	on(monomind.Event{Type: monomind.EventStart})
	if f := strings.Fields(o.Prompt); len(f) == 3 && f[0] == "write" {
		if err := os.WriteFile(filepath.Join(o.Cwd, f[1]), []byte(f[2]+"\n"), 0o644); err != nil {
			return nil, err
		}
		in, _ := json.Marshal(map[string]string{"file_path": f[1]})
		on(monomind.Event{Type: monomind.EventToolActivity, CoderFields: monomind.CoderFields{Phase: "start", Input: in}, ID: "t1", Name: "Write"})
	}
	select {
	case <-time.After(e.hold):
	case <-ctx.Done():
		return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
	}
	return okTurn("done"), nil
}

func newWriterConductor(t *testing.T, ex *fileExec, cwd, writers, turn string) (*Conductor, *recEmitter) {
	t.Helper()
	em := &recEmitter{}
	c := New(context.Background(), Config{
		Cwd: cwd, Limits: Limits{MaxAgents: 4, MaxConcurrent: 3},
		Staffer: &Staffer{Roster: []Model{opus}, Lead: opus}, ReadAccess: true,
		Exec: ex.exec, Emit: em, Writers: writers, TurnID: turn,
	})
	return c, em
}

func spawnWriter(t *testing.T, c *Conductor, brief string) WorkerInfo {
	t.Helper()
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: brief, Role: "coder", Access: ProfileCoding})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func notices(em *recEmitter, code string) []string {
	var out []string
	for _, p := range em.find(chatevents.EventNotice) {
		if n := p.(chatevents.NoticePayload); n.Code == code {
			out = append(out, n.Message)
		}
	}
	return out
}

func waitingForLease(em *recEmitter) bool {
	for _, p := range em.find(chatevents.EventAgentStatus) {
		if s := p.(chatevents.AgentStatusPayload); s.To == chatevents.AgentWaitingLease && s.Detail == "write" {
			return true
		}
	}
	return false
}

func branchExists(t *testing.T, repo, branch string) bool {
	_, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIsolatedWritersRunInParallelInTheirOwnWorktrees(t *testing.T) {
	repo := newRepo(t)
	ex := &fileExec{hold: 300 * time.Millisecond}
	c, em := newWriterConductor(t, ex, repo, WritersIsolated, "t1")
	defer c.Close()
	if !c.Isolated() {
		t.Fatalf("not isolated: %v", notices(em, NoticeWritersShared))
	}
	w1 := spawnWriter(t, c, "write one.txt from-w1")
	w2 := spawnWriter(t, c, "write two.txt from-w2")
	if w1.Branch != "monoagent/t1/w1" || w2.Branch != "monoagent/t1/w2" {
		t.Errorf("branches = %q, %q", w1.Branch, w2.Branch)
	}
	c.Wait(context.Background(), nil, 10*time.Second)
	if ex.maxRun != 2 {
		t.Errorf("max running = %d, want both writers at once", ex.maxRun)
	}
	if waitingForLease(em) {
		t.Error("isolated writers must not wait for the write lease")
	}
	base := filepath.Join(realPath(t, repo), WorktreeDirName, "t1")
	want := map[string]bool{filepath.Join(base, "w1"): true, filepath.Join(base, "w2"): true}
	for _, d := range ex.cwds {
		if !want[d] {
			t.Errorf("worker ran in %s, want its own worktree under %s", d, base)
		}
	}
	// The chat folder is untouched, and each branch holds its worker's commit.
	if _, err := os.Stat(filepath.Join(repo, "one.txt")); err == nil {
		t.Error("a writer edited the chat folder")
	}
	if got := gitT(t, repo, "show", "monoagent/t1/w1:one.txt"); got != "from-w1" {
		t.Errorf("w1's branch has %q", got)
	}
	if got := gitT(t, repo, "show", "monoagent/t1/w2:two.txt"); got != "from-w2" {
		t.Errorf("w2's branch has %q", got)
	}
	if st := gitT(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("the worktrees show in git status:\n%s", st)
	}
	// The stage reads the branch from agent.spawned and agent.status.
	sp := em.find(chatevents.EventAgentSpawned)[0].(chatevents.AgentSpawnedPayload)
	if sp.Branch != "monoagent/t1/w1" {
		t.Errorf("spawned branch = %q", sp.Branch)
	}
	for _, p := range em.find(chatevents.EventAgentStatus) {
		if s := p.(chatevents.AgentStatusPayload); s.AgentID == "w1" && s.Branch != "monoagent/t1/w1" {
			t.Errorf("status without the branch: %+v", s)
		}
	}
}

func TestMergeBringsAWritersBranchIn(t *testing.T) {
	repo := newRepo(t)
	c, _ := newWriterConductor(t, &fileExec{}, repo, WritersIsolated, "t2")
	defer c.Close()
	spawnWriter(t, c, "write one.txt merged")
	c.Wait(context.Background(), nil, 10*time.Second)
	out, err := c.Handle(context.Background(), ToolMerge, json.RawMessage(`{"agent_id":"w1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res MergeResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Merged || res.Branch != "monoagent/t2/w1" || len(res.Files) != 1 || res.Files[0] != "one.txt" || res.Commit == "" {
		t.Errorf("merge = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "one.txt")); string(b) != "merged\n" {
		t.Errorf("one.txt in the chat folder = %q", b)
	}
	if st := gitT(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("tree not clean after the merge:\n%s", st)
	}
	// Merging again changes nothing.
	again, err := c.Merge(context.Background(), "w1")
	if err != nil || !again.Merged || again.Note == "" {
		t.Errorf("second merge = %+v, %v", again, err)
	}
}

func TestMergeConflictIsAToolErrorAndLeavesTheTreeClean(t *testing.T) {
	repo := newRepo(t)
	c, _ := newWriterConductor(t, &fileExec{}, repo, WritersIsolated, "t3")
	defer c.Close()
	spawnWriter(t, c, "write a.txt from-worker")
	c.Wait(context.Background(), nil, 10*time.Second)
	// Meanwhile the lead changed the same file and committed it.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("from-lead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "commit", "--quiet", "-am", "lead")
	head := gitT(t, repo, "rev-parse", "HEAD")

	_, err := c.Handle(context.Background(), ToolMerge, json.RawMessage(`{"agent_id":"w1"}`))
	if err == nil || !strings.Contains(err.Error(), "conflicts") || !strings.Contains(err.Error(), "a.txt") || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("err = %v, want a conflict listing a.txt", err)
	}
	if st := gitT(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("tree not clean after the aborted merge:\n%s", st)
	}
	if got := gitT(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if _, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil {
		t.Error("a merge is still in progress")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "from-lead\n" {
		t.Errorf("a.txt = %q", b)
	}
}

func TestMergeRefusals(t *testing.T) {
	repo := newRepo(t)
	c, _ := newWriterConductor(t, &fileExec{hold: time.Second}, repo, WritersIsolated, "t4")
	defer c.Close()
	spawnWriter(t, c, "write one.txt x")
	if _, err := c.Merge(context.Background(), "w1"); err == nil || !strings.Contains(err.Error(), "still working") {
		t.Errorf("merge of a running writer: %v", err)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate it", Access: ProfileResearch}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Merge(context.Background(), "w2"); err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Errorf("merge of a researcher: %v", err)
	}
	if _, err := c.Merge(context.Background(), "w9"); err == nil {
		t.Error("merge of an unknown worker")
	}
}

func TestTurnEndRemovesWorktreesAndKeepsUnmergedBranches(t *testing.T) {
	repo := newRepo(t)
	c, em := newWriterConductor(t, &fileExec{}, repo, WritersIsolated, "t5")
	spawnWriter(t, c, "write one.txt merged")
	spawnWriter(t, c, "write two.txt unmerged")
	spawnWriter(t, c, "look around") // edits nothing
	c.Wait(context.Background(), nil, 10*time.Second)
	if _, err := c.Merge(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	if _, err := os.Stat(filepath.Join(repo, WorktreeDirName)); !os.IsNotExist(err) {
		t.Errorf("the worktree folder is still there: %v", err)
	}
	if branchExists(t, repo, "monoagent/t5/w1") || branchExists(t, repo, "monoagent/t5/w3") {
		t.Error("merged or empty branches must go")
	}
	if !branchExists(t, repo, "monoagent/t5/w2") {
		t.Fatal("the unmerged branch was deleted")
	}
	if got := gitT(t, repo, "show", "monoagent/t5/w2:two.txt"); got != "unmerged" {
		t.Errorf("kept branch has %q", got)
	}
	kept := notices(em, NoticeBranchKept)
	if len(kept) != 1 || !strings.Contains(kept[0], "monoagent/t5/w2") {
		t.Errorf("kept notices = %q", kept)
	}
	if list := gitT(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, WorktreeDirName) {
		t.Errorf("git still lists the worktrees:\n%s", list)
	}
}

func TestReconcileRemovesStaleWorktrees(t *testing.T) {
	repo := newRepo(t)
	base := filepath.Join(repo, WorktreeDirName)
	add := func(turn, w string) string {
		p := filepath.Join(base, turn, w)
		gitT(t, repo, "worktree", "add", "--quiet", "-b", branchName(turn, w), p, "HEAD")
		return p
	}
	crashed := add("old", "w1") // left with uncommitted work by a crash
	if err := os.WriteFile(filepath.Join(crashed, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add("old", "w2") // nothing in it
	live := add("live", "w1")
	stray := filepath.Join(base, "old", "notatree")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := ReconcileWorktrees(context.Background(), repo, func(id string) bool { return id == "live" })
	by := map[string]TreeCleanup{}
	for _, r := range res {
		by[r.Turn+"/"+r.Agent] = r
	}
	if r := by["old/w1"]; !r.Removed || !r.BranchKept {
		t.Errorf("old/w1 = %+v, want removed with its branch kept", r)
	}
	if r := by["old/w2"]; !r.Removed || r.BranchKept {
		t.Errorf("old/w2 = %+v, want removed with its branch", r)
	}
	if r := by["old/notatree"]; r.Removed || r.Error == "" {
		t.Errorf("a non-empty folder that is not a worktree must stay: %+v", r)
	}
	if _, ok := by["live/w1"]; ok {
		t.Error("an active turn's worktree was touched")
	}
	if _, err := os.Stat(crashed); !os.IsNotExist(err) {
		t.Error("the stale worktree is still there")
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the live worktree is gone: %v", err)
	}
	if got := gitT(t, repo, "show", "monoagent/old/w1:wip.txt"); got != "wip" {
		t.Errorf("the crashed worker's work was not kept on its branch: %q", got)
	}
	if branchExists(t, repo, "monoagent/old/w2") {
		t.Error("the empty branch should go")
	}
	if _, err := os.Stat(filepath.Join(stray, "keep.txt")); err != nil {
		t.Errorf("the stray folder's file is gone: %v", err)
	}
}

func TestPathGuard(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, WorktreeDirName)
	outside := filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(base, "t"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "t", "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		base, p string
		want    bool
	}{
		{base, filepath.Join(base, "t", "w1"), true},
		{base, filepath.Join(base, "t"), true},
		{base, base, false},
		{base, root, false},
		{base, filepath.Join(base, "..", "outside"), false},
		{base, filepath.Join(base, "t", "..", "..", "outside"), false},
		{base, filepath.Join(root, WorktreeDirName+"-evil", "t"), false},
		{base, filepath.Join(link, "w1"), false},      // through a symlink that leaves base
		{outside, filepath.Join(outside, "t"), false}, // not a worktree folder
		{"", filepath.Join(base, "t"), false},
	}
	for _, tc := range cases {
		if got := within(tc.base, tc.p); got != tc.want {
			t.Errorf("within(%s, %s) = %v, want %v", tc.base, tc.p, got, tc.want)
		}
	}
	// removeTree refuses a path outside the worktree folder and leaves it.
	victim := filepath.Join(outside, "work")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	r := removeTree(context.Background(), root, base, victim, "monoagent/t/w1")
	if r.Removed || !strings.Contains(r.Error, "outside") {
		t.Errorf("removeTree outside = %+v", r)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("the outside folder was removed: %v", err)
	}
	// A symlinked worktree folder is refused altogether.
	repo := newRepo(t)
	if err := os.Symlink(outside, filepath.Join(repo, WorktreeDirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := safeBase(repo); err == nil {
		t.Error("a symlinked worktree folder must be refused")
	}
}

func TestNonGitFolderFallsBackToTheLease(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	ex := &fileExec{hold: 100 * time.Millisecond}
	c, em := newWriterConductor(t, ex, dir, WritersIsolated, "t6")
	defer c.Close()
	if c.Isolated() {
		t.Fatal("a folder outside git can't be isolated")
	}
	if n := notices(em, NoticeWritersShared); len(n) != 1 || !strings.Contains(n[0], "not in a git repository") {
		t.Errorf("notices = %q", n)
	}
	w1 := spawnWriter(t, c, "write one.txt x")
	spawnWriter(t, c, "write two.txt y")
	c.Wait(context.Background(), nil, 10*time.Second)
	if w1.Branch != "" || ex.maxRun != 1 || !waitingForLease(em) {
		t.Errorf("writers must take turns under the lease: branch %q, max running %d", w1.Branch, ex.maxRun)
	}
	if _, err := os.Stat(filepath.Join(dir, "one.txt")); err != nil {
		t.Errorf("the writer should edit the chat folder: %v", err)
	}
	if _, err := c.Merge(context.Background(), "w1"); err == nil || !strings.Contains(err.Error(), "not isolated") {
		t.Errorf("merge without isolation: %v", err)
	}
}

func TestSharedWritersAreUnchanged(t *testing.T) {
	repo := newRepo(t)
	for _, writers := range []string{"", WritersShared} {
		ex := &fileExec{hold: 100 * time.Millisecond}
		c, em := newWriterConductor(t, ex, repo, writers, "t7")
		if c.Isolated() || len(notices(em, NoticeWritersShared)) != 0 {
			t.Errorf("%q: isolated or noticed", writers)
		}
		w1 := spawnWriter(t, c, "write one.txt x")
		spawnWriter(t, c, "write two.txt y")
		c.Wait(context.Background(), nil, 10*time.Second)
		c.Close()
		if w1.Branch != "" || ex.maxRun != 1 || !waitingForLease(em) {
			t.Errorf("%q: branch %q, max running %d", writers, w1.Branch, ex.maxRun)
		}
		for _, d := range ex.cwds {
			if d != repo {
				t.Errorf("%q: worker ran in %s, want the chat folder", writers, d)
			}
		}
		sp := em.find(chatevents.EventAgentSpawned)[0].(chatevents.AgentSpawnedPayload)
		if sp.Branch != "" {
			t.Errorf("%q: spawned branch %q", writers, sp.Branch)
		}
		if _, err := os.Stat(filepath.Join(repo, WorktreeDirName)); !os.IsNotExist(err) {
			t.Errorf("%q: a worktree folder was made", writers)
		}
		if b, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); strings.Contains(string(b), WorktreeDirName) {
			t.Errorf("%q: the exclude file was changed", writers)
		}
		for _, tool := range ToolSpecs() {
			if tool.Name == ToolMerge {
				t.Error("org_merge must only be given with isolated writers")
			}
		}
	}
}

func TestWorkerPromptNamesItsWorktree(t *testing.T) {
	repo := newRepo(t)
	var prompts []string
	var mu sync.Mutex
	ex := &fileExec{}
	c := New(context.Background(), Config{
		Cwd: repo, Limits: Limits{MaxAgents: 2, MaxConcurrent: 2},
		Staffer: &Staffer{Roster: []Model{opus}, Lead: opus}, ReadAccess: true, Emit: &recEmitter{},
		Writers: WritersIsolated, TurnID: "t8",
		Exec: func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
			mu.Lock()
			prompts = append(prompts, o.SystemPrompt)
			mu.Unlock()
			return ex.exec(ctx, o, on)
		},
	})
	defer c.Close()
	spawnWriter(t, c, "write one.txt x")
	c.Wait(context.Background(), nil, 10*time.Second)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "monoagent/t8/w1") || !strings.Contains(prompts[0], filepath.Join(WorktreeDirName, "t8", "w1")) {
		t.Errorf("prompt = %q", prompts)
	}
}
