package dynorg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Isolated writers (#230). With the coder setting `--org-writers isolated`
// each writing worker gets its own git worktree on its own branch instead
// of queueing for the one write lease, and the lead merges the branches
// back with org_merge.
//
// Where the worktrees live: <chat folder>/.monoagent-worktrees/<turn>/<worker>.
// It is inside the folder the chat already owns (writable, the same disk,
// so `git worktree add` is cheap, and inside what the runtimes already
// allow), not a sibling folder the chat may not be able to create. The
// folder is listed in the repository's own .git/info/exclude, so it never
// shows in `git status` or gets added, and the user's tracked .gitignore
// is left alone. Tools that don't read git's excludes (jest's haste map,
// some globs and file watchers) do see the worktrees while they exist.
// Everything removed is checked to be inside that folder.
//
// Branches are monoagent/<turn>/<worker>, cut from the chat folder's HEAD
// when the worker is spawned: the lead's uncommitted edits are not in
// them. After each run the worker's changes are committed on its branch
// as a checkpoint: hooks are skipped (--no-verify) and so is signing, so
// merged work never passed the repository's pre-commit hooks. A commit is
// only ever made in a folder that is the top of a worktree on the worker's
// own branch, so git can never fall through to the chat folder's
// repository and commit the user's own work.
//
// Cleanup: at the end of the turn, and when a turn that is no longer
// running left worktrees behind (`chat history reconcile`, and the start
// of the next isolated turn in the same folder), each worktree is removed.
// A running turn holds a lock file in its folder, and cleanup skips a
// locked folder. A worktree with changes that can't be committed is kept.
// Its branch goes too only once another branch contains it; otherwise it
// is kept and journaled (a notice, or the reconcile result), never
// deleted. Files the repository ignores (build output, local config) go
// with the worktree; they are journaled first.
//
// A folder that is not in a git repository (or one with no commit yet)
// keeps the write lease, with a notice.

// Writers settings: WritersShared is the one-writer lease (the default).
const (
	WritersShared   = "shared"
	WritersIsolated = "isolated"
)

// WorktreeDirName is the folder under the chat folder that holds the
// worktrees.
const WorktreeDirName = ".monoagent-worktrees"

// BranchPrefix starts every worker branch.
const BranchPrefix = "monoagent/"

// turnLockName is the lock file a running turn holds in its folder.
const turnLockName = ".lock"

// Notice codes.
const (
	// NoticeWritersShared: isolated writers were asked for but this turn (or
	// this worker) runs under the write lease, with the reason.
	NoticeWritersShared = "org_writers_shared"
	// NoticeBranchKept: a worker's branch has commits that were never
	// merged, so it outlives its worktree.
	NoticeBranchKept = "org_branch_kept"
	// NoticeIgnoredRemoved: files git ignores were removed with a worktree.
	NoticeIgnoredRemoved = "org_ignored_removed"
	// NoticeCheckpointFailed: a worker's changes could not be committed on
	// its branch.
	NoticeCheckpointFailed = "org_checkpoint_failed"
)

// ToolMerge is the lead's merge tool, given only with isolated writers.
const ToolMerge = "org_merge"

// gitTimeout bounds one git command.
var gitTimeout = 2 * time.Minute

// treeID is what a turn or worker id must look like to name a folder and
// a branch component: no dots or slashes, so never "..", ".lock" or a path.
var treeID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,127}$`)

// maxIgnoredListed caps the ignored paths a notice names.
const maxIgnoredListed = 10

// isolation is a turn's worktree setup.
type isolation struct {
	cwd     string // the chat folder, where merges happen
	prefix  string // the chat folder relative to the repository's top ("" or "sub/dir/")
	base    string // <cwd>/.monoagent-worktrees
	turnDir string // <base>/<turn>
	turn    string
	unlock  func() // releases the turn's lock file
}

// MergeToolSpec is org_merge's spec.
func MergeToolSpec() monomind.ToolSpec {
	return monomind.ToolSpec{
		Name: ToolMerge,
		Description: "Merge a finished writer's branch into your working tree. On a conflict the merge is aborted, " +
			"your tree is left as it was, and the error lists the conflicting files. The writer's commits skipped the " +
			"repository's hooks, so run its checks after merging.",
		Schema: obj(map[string]any{"agent_id": str("The writing worker.")}, "agent_id"),
	}
}

// IsolatedLeadPrompt is added to the lead's prompt when its writers are
// isolated.
const IsolatedLeadPrompt = " Writers are isolated this turn: each writing worker (coding, qa, automation) gets its own git " +
	"worktree on its own branch, cut from the last commit (not your uncommitted edits), so writers run in parallel " +
	"and don't wait for a lease. After you read a writer's report, org_merge it to bring its branch into your working " +
	"tree. A conflict aborts the merge, leaves your tree as it was and lists the files: resolve it with an org_message " +
	"follow-up to the worker or by editing yourself. Commit your own edits first if a writer needs them or a merge " +
	"touches them. The writers' commits skip the repository's hooks, so run the project's checks after merging. " +
	"The worktrees sit in " + WorktreeDirName + "/ inside this folder until the turn ends: git ignores them, but " +
	"tools that don't read git's excludes (jest, some globs, file watchers) may see duplicate files there, so point " +
	"such tools away from it. Branches you don't merge are kept after the turn."

// initWriters sets up isolated writers when the setting asks for them, or
// journals why this turn keeps the write lease.
func (c *Conductor) initWriters() {
	if c.cfg.Writers != WritersIsolated {
		return
	}
	iso, err := newIsolation(c.ctx, c.cfg.Cwd, c.cfg.TurnID)
	if err != nil {
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeWritersShared, Severity: chatevents.SeverityWarning,
			Message: "Writers share the folder under the write lease this turn: " + err.Error() + ".",
		})
		return
	}
	c.iso = iso
}

// Isolated reports whether this turn's writers get their own worktrees.
func (c *Conductor) Isolated() bool { return c.iso != nil }

// newIsolation checks that cwd is in a git repository with a commit to
// branch from, makes sure the worktree folder is excluded, and takes the
// turn's lock.
func newIsolation(ctx context.Context, cwd, turn string) (*isolation, error) {
	if !treeID.MatchString(turn) {
		return nil, fmt.Errorf("the turn id %q can't name a branch", turn)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("git is not installed")
	}
	prefix, err := gitRun(ctx, cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, errors.New("the chat folder is not in a git repository")
	}
	if _, err := gitRun(ctx, cwd, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return nil, errors.New("the repository has no commit to branch from yet")
	}
	base, err := safeBase(cwd)
	if err != nil {
		return nil, err
	}
	if err := excludeWorktrees(ctx, cwd); err != nil {
		return nil, err
	}
	turnDir := filepath.Join(base, turn)
	unlock, err := daemonhb.LockFile(filepath.Join(turnDir, turnLockName))
	if err != nil {
		return nil, fmt.Errorf("can't lock %s: %v", turnDir, err)
	}
	return &isolation{cwd: cwd, prefix: strings.TrimSpace(prefix), base: base, turnDir: turnDir, turn: turn, unlock: unlock}, nil
}

// safeBase is cwd's worktree folder, refused when it is anything but a
// plain folder inside cwd (a symlink could point removals elsewhere).
func safeBase(cwd string) (string, error) {
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("the chat folder can't be resolved: %v", err)
	}
	base := filepath.Join(real, WorktreeDirName)
	if fi, err := os.Lstat(base); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", base)
	}
	return base, nil
}

// excludeWorktrees lists the worktree folder in the repository's
// info/exclude (once), so git never sees the worktrees as untracked files.
func excludeWorktrees(ctx context.Context, cwd string) error {
	p, err := gitRun(ctx, cwd, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fmt.Errorf("can't find the repository's exclude file: %v", err)
	}
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) { // relative to cwd
		p = filepath.Join(cwd, p)
	}
	const line = WorktreeDirName + "/"
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %v", p, err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("writing %s: %v", p, err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %v", p, err)
	}
	defer f.Close()
	sep := ""
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		sep = "\n"
	}
	_, err = f.WriteString(sep + "# mono-agent: isolated org writers (#230)\n" + line + "\n")
	return err
}

// branchName is the branch of a turn's worker.
func branchName(turn, worker string) string { return BranchPrefix + turn + "/" + worker }

// addWorktree gives a writing worker its own worktree and branch. When it
// can't, the worker keeps the write lease and the journal says why.
func (c *Conductor) addWorktree(w *worker) {
	if c.iso == nil || !writes(w.staff.Access) || !treeID.MatchString(w.id) {
		return
	}
	path := filepath.Join(c.iso.turnDir, w.id)
	branch := branchName(c.iso.turn, w.id)
	if _, err := gitRun(c.ctx, c.iso.cwd, "worktree", "add", "--quiet", "-b", branch, path, "HEAD"); err != nil {
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeWritersShared, Severity: chatevents.SeverityWarning,
			Message: fmt.Sprintf("%s works in the chat folder under the write lease: its worktree could not be created (%v).", w.id, err),
		})
		return
	}
	c.mu.Lock()
	w.worktree, w.branch = path, branch
	w.workDir = filepath.Join(path, filepath.FromSlash(c.iso.prefix))
	c.mu.Unlock()
}

// workDir is where w runs: its worktree, else the chat folder.
func (c *Conductor) workDir(w *worker) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.workDir != "" {
		return w.workDir
	}
	return c.cfg.Cwd
}

// worktreeRule is the part of w's prompt about its worktree, or "".
func (c *Conductor) worktreeRule(w *worker) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.branch == "" {
		return ""
	}
	return "- You work alone in your own git worktree on branch " + w.branch + ", cut from the last commit. " +
		"Edit files only there, stay on that branch, and don't push. Your changes are committed for you when you " +
		"finish, and the lead merges your branch.\n"
}

// commitWorktree commits whatever w changed in its worktree during its
// run, and journals a failure.
func (c *Conductor) commitWorktree(w *worker) error {
	c.mu.Lock()
	dir, branch, role, brief := w.worktree, w.branch, w.staff.Role, w.brief
	c.mu.Unlock()
	if dir == "" {
		return nil
	}
	ctx := context.WithoutCancel(c.ctx)
	subject, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	err := commitAll(ctx, dir, branch, fmt.Sprintf("%s (%s): %s", w.id, role, clipText(subject, 60)))
	if err != nil {
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeCheckpointFailed, Severity: chatevents.SeverityWarning,
			Message: fmt.Sprintf("%s's changes could not be committed on %s: %v", w.id, branch, err),
		})
	}
	return err
}

// MergeResult is org_merge's result.
type MergeResult struct {
	AgentID string   `json:"agent_id"`
	Branch  string   `json:"branch"`
	Merged  bool     `json:"merged"`
	Commit  string   `json:"commit,omitempty"`
	Files   []string `json:"files_merged,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Merge merges a finished writer's branch into the chat folder. A conflict
// aborts the merge (the tree is left as it was) and is returned as an
// error listing the conflicting files. Merges run one at a time, and a
// worker being merged can't take a follow-up.
func (c *Conductor) Merge(ctx context.Context, id string) (MergeResult, error) {
	if c.iso == nil {
		return MergeResult{}, errors.New("writers are not isolated this turn, so there is nothing to merge: they edit the chat folder directly")
	}
	c.mergeMu.Lock()
	defer c.mergeMu.Unlock()
	c.mu.Lock()
	w := c.workers[id]
	switch {
	case w == nil:
		c.mu.Unlock()
		return MergeResult{}, fmt.Errorf("no worker %q", id)
	case w.branch == "":
		c.mu.Unlock()
		return MergeResult{}, fmt.Errorf("%s has no branch of its own: it edited the chat folder directly", id)
	case running(w.status):
		c.mu.Unlock()
		return MergeResult{}, fmt.Errorf("%s is still working; org_wait for it first", id)
	}
	branch := w.branch
	w.merging = true // claimed with the running check: no follow-up starts now
	// The merge writes the chat folder, so it takes the write lease like any
	// edit there (the lead's own edits, an unconfined researcher).
	holds := c.leadHolds
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		w.merging = false
		c.mu.Unlock()
	}()
	if !holds {
		if !c.write.tryAcquire() {
			c.mu.Lock()
			writers := c.writersLocked()
			c.mu.Unlock()
			return MergeResult{}, fmt.Errorf("%s holds the write lease on the chat folder; org_wait for it, then merge", strings.Join(writers, ", "))
		}
		defer c.write.release()
	}
	// From here git runs to the end even if the turn is stopped (gitTimeout
	// still bounds each command): a merge killed halfway leaves
	// .git/index.lock and a half-updated tree that `merge --abort` can't fix.
	ctx = context.WithoutCancel(ctx)
	res := MergeResult{AgentID: id, Branch: branch}
	if err := c.commitWorktree(w); err != nil {
		return res, fmt.Errorf("%s's latest changes could not be committed on %s, so nothing was merged: %v", id, branch, err)
	}
	before, _ := gitRun(ctx, c.iso.cwd, "rev-parse", "HEAD")
	msg := fmt.Sprintf("Merge %s (%s)", branch, id)
	if _, err := gitRun(ctx, c.iso.cwd, withIdentity(ctx, c.iso.cwd, "merge", "--no-ff", "--no-edit", "-m", msg, "refs/heads/"+branch)...); err != nil {
		out, _ := gitRun(ctx, c.iso.cwd, "diff", "--name-only", "--diff-filter=U")
		conflicts := strings.Fields(out)
		if _, merging := gitRun(ctx, c.iso.cwd, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); merging == nil {
			if _, aerr := gitRun(ctx, c.iso.cwd, "merge", "--abort"); aerr != nil {
				return res, fmt.Errorf("merging %s failed and the merge could not be aborted (%v); run `git merge --abort` in the chat folder", branch, aerr)
			}
		}
		if len(conflicts) > 0 {
			return res, fmt.Errorf("merging %s conflicts in %d file(s): %s. The merge was aborted and your working tree is as it was; "+
				"send %s a follow-up to rebase onto your changes, or resolve it yourself", branch, len(conflicts), strings.Join(conflicts, ", "), id)
		}
		return res, fmt.Errorf("merging %s failed: %v", branch, err)
	}
	after, _ := gitRun(ctx, c.iso.cwd, "rev-parse", "HEAD")
	res.Merged = true
	if strings.TrimSpace(before) == strings.TrimSpace(after) {
		res.Note = "already up to date: the branch has nothing your tree doesn't"
		return res, nil
	}
	res.Commit = strings.TrimSpace(after)
	files, _ := gitRun(ctx, c.iso.cwd, "diff", "--name-only", strings.TrimSpace(before), res.Commit)
	res.Files = strings.Fields(files)
	res.Note = "the worker's commits skipped the repository's hooks; run the project's checks"
	return res, nil
}

// cleanupWorktrees removes the turn's worktrees at its end, keeping the
// branches that hold unmerged work, then lets go of the turn's lock.
func (c *Conductor) cleanupWorktrees() {
	if c.iso == nil {
		return
	}
	ctx := context.WithoutCancel(c.ctx)
	type tree struct{ id, path, branch string }
	var trees []tree
	c.mu.Lock()
	for _, id := range c.order {
		if w := c.workers[id]; w.worktree != "" {
			trees = append(trees, tree{w.id, w.worktree, w.branch})
		}
	}
	c.mu.Unlock()
	for _, w := range trees {
		r := removeTree(ctx, c.iso.cwd, c.iso.base, w.path, w.branch)
		c.reportCleanup(w.id, r)
	}
	c.iso.unlock()
	releaseTurnDir(c.iso.base, c.iso.turnDir)
	_, _ = gitRun(ctx, c.iso.cwd, "worktree", "prune")
}

// reportCleanup journals what removing a worker's worktree left behind.
func (c *Conductor) reportCleanup(id string, r TreeCleanup) {
	if len(r.Ignored) > 0 {
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeIgnoredRemoved, Severity: chatevents.SeverityInfo,
			Message: fmt.Sprintf("%s's worktree held files git ignores, removed with it: %s", id, strings.Join(r.Ignored, ", ")),
		})
	}
	switch {
	case r.Error != "":
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeBranchKept, Severity: chatevents.SeverityWarning,
			Message: fmt.Sprintf("%s's worktree %s was left in place: %s", id, r.Path, r.Error),
		})
	case r.BranchKept:
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeBranchKept, Severity: chatevents.SeverityInfo,
			Message: fmt.Sprintf("%s's work was not merged into a branch; it is kept on branch %s (git merge %s to take it, git branch -D %s to drop it).", id, r.Branch, r.Branch, r.Branch),
		})
	}
}

// TreeCleanup is what cleaning one worker's worktree did.
type TreeCleanup struct {
	Turn       string `json:"turn,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Removed    bool   `json:"removed"`
	BranchKept bool   `json:"branch_kept"`
	// Ignored lists files git ignores that were removed with the worktree
	// (at most maxIgnoredListed, then "…").
	Ignored []string `json:"ignored_removed,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// statusPorcelain is `git status --porcelain` in dir; a var so a test can
// make it fail.
var statusPorcelain = func(ctx context.Context, dir string) (string, error) {
	return gitRun(ctx, dir, "status", "--porcelain")
}

// removeTree removes one worktree (after committing anything left in it)
// and its branch once another branch contains it. A worktree whose changes
// can't be committed, or whose state can't be read, is kept. Nothing
// outside base is touched.
func removeTree(ctx context.Context, repo, base, path, branch string) TreeCleanup {
	r := TreeCleanup{Path: path, Branch: branch}
	if !within(base, path) {
		r.Error = "refused: the path is outside " + base
		return r
	}
	if _, err := os.Lstat(path); err == nil {
		if !isWorktreeRoot(ctx, path) {
			// Not (or no longer) a worktree: git would work on the chat
			// folder's repository from here. Only an empty leftover goes.
			if os.Remove(path) != nil {
				r.Error = "it is not a git worktree and not empty"
				return r
			}
			r.Removed = true
			return r
		}
		if err := commitAll(ctx, path, branch, "mono-agent: work left in the worktree"); err != nil {
			// Changes that can't be committed stay where they are, and so
			// does everything when their state can't even be read.
			dirty, serr := statusPorcelain(ctx, path)
			if serr != nil {
				r.Error = "its state could not be read: " + serr.Error()
				return r
			}
			if strings.TrimSpace(dirty) != "" {
				r.Error = "it has changes that could not be committed: " + err.Error() +
					". Commit them on a branch there to keep them, or drop them with `git worktree remove --force " + path + "`"
				return r
			}
		}
		r.Ignored = ignoredFiles(ctx, path)
		if _, err := gitRun(ctx, repo, "worktree", "remove", "--force", path); err != nil {
			if !within(base, path) {
				r.Error = "refused: the path is outside " + base
				return r
			}
			if err := os.RemoveAll(path); err != nil {
				r.Error = err.Error()
				return r
			}
		}
	}
	_, _ = gitRun(ctx, repo, "worktree", "prune")
	r.Removed = true
	if _, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return r // no branch (never created, or already gone)
	}
	if !containedElsewhere(ctx, repo, branch) {
		r.BranchKept = true
		return r
	}
	if _, err := gitRun(ctx, repo, "branch", "-D", branch); err != nil {
		r.BranchKept = true
	}
	return r
}

// containedElsewhere reports whether another local branch holds every
// commit of branch, so deleting it loses nothing. A detached HEAD doesn't
// count: its commits are lost once it moves.
func containedElsewhere(ctx context.Context, repo, branch string) bool {
	out, err := gitRun(ctx, repo, "for-each-ref", "--format=%(refname)", "--contains", "refs/heads/"+branch, "refs/heads/")
	if err != nil {
		return false
	}
	for _, ref := range strings.Fields(out) {
		if ref != "refs/heads/"+branch {
			return true
		}
	}
	return false
}

// ignoredFiles lists what git ignores in a worktree (folders collapsed),
// capped for a notice.
func ignoredFiles(ctx context.Context, dir string) []string {
	out, err := gitRun(ctx, dir, "status", "--porcelain", "--ignored")
	if err != nil {
		return nil
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "!! "); ok {
			if len(files) == maxIgnoredListed {
				files = append(files, "…")
				break
			}
			files = append(files, p)
		}
	}
	return files
}

// ReconcileWorktrees removes the worktrees under cwd that belong to turns
// no longer running, keeping their unmerged branches. A turn counts as
// running when active says so (not finalized) or when its process still
// holds its lock. The app's startup reconcile and the start of each
// isolated turn call it.
func ReconcileWorktrees(ctx context.Context, cwd string, active func(turnID string) bool) []TreeCleanup {
	base, err := safeBase(cwd)
	if err != nil {
		return nil
	}
	turns, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	if _, err := gitRun(ctx, cwd, "rev-parse", "--git-dir"); err != nil {
		return nil
	}
	var out []TreeCleanup
	for _, t := range turns {
		// Symlinks are not followed, and names that can't be ours are left.
		if !t.IsDir() || !treeID.MatchString(t.Name()) || active(t.Name()) {
			continue
		}
		turnDir := filepath.Join(base, t.Name())
		unlock, err := daemonhb.LockFile(filepath.Join(turnDir, turnLockName))
		if errors.Is(err, daemonhb.ErrHeld) {
			continue // its turn is still running in some process
		}
		if err != nil {
			out = append(out, TreeCleanup{Turn: t.Name(), Path: turnDir, Error: "skipped: its lock could not be taken: " + err.Error()})
			continue
		}
		trees, _ := os.ReadDir(turnDir)
		for _, w := range trees {
			if !w.IsDir() || !treeID.MatchString(w.Name()) {
				continue
			}
			r := removeTree(ctx, cwd, base, filepath.Join(turnDir, w.Name()), branchName(t.Name(), w.Name()))
			r.Turn, r.Agent = t.Name(), w.Name()
			out = append(out, r)
		}
		unlock()
		releaseTurnDir(base, turnDir)
	}
	_, _ = gitRun(ctx, cwd, "worktree", "prune")
	return out
}
