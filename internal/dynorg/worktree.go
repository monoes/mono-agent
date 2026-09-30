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
// is left alone. Everything removed is checked to be inside that folder.
//
// Branches are monoagent/<turn>/<worker>, cut from the chat folder's HEAD
// when the worker is spawned: the lead's uncommitted edits are not in
// them. After each run the worker's changes are committed on its branch
// (hooks skipped: it is a checkpoint, not the user's commit).
//
// Cleanup: at the end of the turn, and when a turn that is no longer
// active left worktrees behind (`chat history reconcile`, and the start of
// the next isolated turn in the same folder), each worktree is removed. Its
// branch goes too once it is merged; a branch with unmerged commits is kept
// and journaled (a notice, or the reconcile result), never deleted.
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

// Notice codes.
const (
	// NoticeWritersShared: isolated writers were asked for but this turn (or
	// this worker) runs under the write lease, with the reason.
	NoticeWritersShared = "org_writers_shared"
	// NoticeBranchKept: a worker's branch has commits that were never
	// merged, so it outlives its worktree.
	NoticeBranchKept = "org_branch_kept"
)

// ToolMerge is the lead's merge tool, given only with isolated writers.
const ToolMerge = "org_merge"

// gitTimeout bounds one git command.
var gitTimeout = 2 * time.Minute

// treeID is what a turn or worker id must look like to name a folder and
// a branch component: no dots or slashes, so never "..", ".lock" or a path.
var treeID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,127}$`)

// isolation is a turn's worktree setup.
type isolation struct {
	cwd     string // the chat folder, where merges happen
	prefix  string // the chat folder relative to the repository's top ("" or "sub/dir/")
	base    string // <cwd>/.monoagent-worktrees
	turnDir string // <base>/<turn>
	turn    string
}

// MergeToolSpec is org_merge's spec.
func MergeToolSpec() monomind.ToolSpec {
	return monomind.ToolSpec{
		Name: ToolMerge,
		Description: "Merge a finished writer's branch into your working tree. On a conflict the merge is aborted, " +
			"your tree is left as it was, and the error lists the conflicting files.",
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
	"touches them. Branches you don't merge are kept after the turn."

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
// branch from, and makes sure the worktree folder is excluded.
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
	return &isolation{cwd: cwd, prefix: strings.TrimSpace(prefix), base: base, turnDir: filepath.Join(base, turn), turn: turn}, nil
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
	err := os.MkdirAll(c.iso.turnDir, 0o755)
	if err == nil {
		_, err = gitRun(c.ctx, c.iso.cwd, "worktree", "add", "--quiet", "-b", branch, path, "HEAD")
	}
	if err != nil {
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

// commitWorktree commits whatever w changed in its worktree during its run.
func (c *Conductor) commitWorktree(w *worker) {
	c.mu.Lock()
	dir, role, brief := w.worktree, w.staff.Role, w.brief
	c.mu.Unlock()
	if dir == "" {
		return
	}
	ctx := context.WithoutCancel(c.ctx)
	subject, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	if err := commitAll(ctx, dir, fmt.Sprintf("%s (%s): %s", w.id, role, clipText(subject, 60))); err != nil {
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeWritersShared, Severity: chatevents.SeverityWarning,
			Message: fmt.Sprintf("%s's changes could not be committed on %s: %v", w.id, filepath.Base(dir), err),
		})
	}
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
// error listing the conflicting files.
func (c *Conductor) Merge(ctx context.Context, id string) (MergeResult, error) {
	if c.iso == nil {
		return MergeResult{}, errors.New("writers are not isolated this turn, so there is nothing to merge: they edit the chat folder directly")
	}
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
	// The merge writes the chat folder, so it takes the write lease like any
	// edit there (the lead's own edits, an unconfined researcher).
	holds := c.leadHolds
	c.mu.Unlock()
	if !holds {
		if !c.write.tryAcquire() {
			c.mu.Lock()
			writers := c.writersLocked()
			c.mu.Unlock()
			return MergeResult{}, fmt.Errorf("%s holds the write lease on the chat folder; org_wait for it, then merge", strings.Join(writers, ", "))
		}
		defer c.write.release()
	}
	c.commitWorktree(w)
	res := MergeResult{AgentID: id, Branch: branch}
	before, _ := gitRun(ctx, c.iso.cwd, "rev-parse", "HEAD")
	msg := fmt.Sprintf("Merge %s (%s)", branch, id)
	if _, err := gitRun(ctx, c.iso.cwd, withIdentity(ctx, c.iso.cwd, "merge", "--no-ff", "--no-edit", "-m", msg, branch)...); err != nil {
		out, _ := gitRun(context.WithoutCancel(ctx), c.iso.cwd, "diff", "--name-only", "--diff-filter=U")
		conflicts := strings.Fields(out)
		if _, merging := gitRun(context.WithoutCancel(ctx), c.iso.cwd, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); merging == nil {
			if _, aerr := gitRun(context.WithoutCancel(ctx), c.iso.cwd, "merge", "--abort"); aerr != nil {
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
	return res, nil
}

// cleanupWorktrees removes the turn's worktrees at its end, keeping the
// branches that hold unmerged work.
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
		switch {
		case r.Error != "":
			c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
				Code: NoticeBranchKept, Severity: chatevents.SeverityWarning,
				Message: fmt.Sprintf("%s's worktree %s was left in place: %s", w.id, w.path, r.Error),
			})
		case r.BranchKept:
			c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
				Code: NoticeBranchKept, Severity: chatevents.SeverityInfo,
				Message: fmt.Sprintf("%s's work was not merged; it is kept on branch %s (git merge %s to take it, git branch -D %s to drop it).", w.id, w.branch, w.branch, w.branch),
			})
		}
	}
	removeEmptyDir(c.iso.base, c.iso.turnDir)
	_, _ = gitRun(ctx, c.iso.cwd, "worktree", "prune")
}

// TreeCleanup is what cleaning one worker's worktree did.
type TreeCleanup struct {
	Turn       string `json:"turn,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Removed    bool   `json:"removed"`
	BranchKept bool   `json:"branch_kept"`
	Error      string `json:"error,omitempty"`
}

// removeTree removes one worktree (after committing anything left in it)
// and its branch when the chat folder's HEAD already has the branch's
// commits. A branch with unmerged commits is kept. Nothing outside base
// is touched.
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
		if err := commitAll(ctx, path, "mono-agent: work left in the worktree"); err != nil {
			// Uncommitted work that can't be committed stays where it is.
			if dirty, _ := gitRun(ctx, path, "status", "--porcelain"); strings.TrimSpace(dirty) != "" {
				r.Error = "it has changes that could not be committed: " + err.Error()
				return r
			}
		}
		if _, err := gitRun(ctx, repo, "worktree", "remove", "--force", path); err != nil {
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
	if _, err := gitRun(ctx, repo, "merge-base", "--is-ancestor", "refs/heads/"+branch, "HEAD"); err != nil {
		r.BranchKept = true
		return r
	}
	if _, err := gitRun(ctx, repo, "branch", "-D", branch); err != nil {
		r.BranchKept = true
	}
	return r
}

// ReconcileWorktrees removes the worktrees under cwd that belong to turns
// no longer active (active reports whether a turn id is still running),
// keeping their unmerged branches. The app's startup reconcile and the
// start of each isolated turn call it.
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
		trees, _ := os.ReadDir(turnDir)
		for _, w := range trees {
			if !w.IsDir() || !treeID.MatchString(w.Name()) {
				continue
			}
			r := removeTree(ctx, cwd, base, filepath.Join(turnDir, w.Name()), branchName(t.Name(), w.Name()))
			r.Turn, r.Agent = t.Name(), w.Name()
			out = append(out, r)
		}
		removeEmptyDir(base, turnDir)
	}
	_, _ = gitRun(ctx, cwd, "worktree", "prune")
	return out
}

// within reports whether p lies strictly inside base, with symlinks in
// both resolved as far as they exist: the guard before anything under the
// worktree folder is removed.
func within(base, p string) bool {
	if base == "" || p == "" || filepath.Base(base) != WorktreeDirName {
		return false
	}
	b, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	// Resolve the parent, not p itself: removing a symlink removes the link.
	if rp, err := filepath.EvalSymlinks(filepath.Dir(a)); err == nil {
		a = filepath.Join(rp, filepath.Base(a))
	}
	rel, err := filepath.Rel(b, a)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// isWorktreeRoot reports whether dir is the top of a git worktree (not
// just a folder inside the chat folder's repository).
func isWorktreeRoot(ctx context.Context, dir string) bool {
	top, err := gitRun(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	a, err1 := filepath.EvalSymlinks(strings.TrimSpace(top))
	b, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && a == b
}

// removeEmptyDir removes dir if it is empty and inside base.
func removeEmptyDir(base, dir string) {
	if within(base, dir) {
		_ = os.Remove(dir)
	}
	if entries, err := os.ReadDir(base); err == nil && len(entries) == 0 {
		_ = os.Remove(base)
	}
}

// commitAll commits every change in dir, if there is any.
func commitAll(ctx context.Context, dir, msg string) error {
	if _, err := gitRun(ctx, dir, "add", "--all"); err != nil {
		return err
	}
	if _, err := gitRun(ctx, dir, "diff", "--cached", "--quiet"); err == nil {
		return nil // nothing staged
	}
	_, err := gitRun(ctx, dir, withIdentity(ctx, dir, "commit", "--quiet", "--no-verify", "-m", msg)...)
	return err
}

// withIdentity prefixes args with a stand-in committer when the repository
// has none configured, so a checkpoint commit or a merge never fails for
// the lack of one. A configured identity is always used as it is.
func withIdentity(ctx context.Context, dir string, args ...string) []string {
	name, _ := gitRun(ctx, dir, "config", "user.name")
	email, _ := gitRun(ctx, dir, "config", "user.email")
	var pre []string
	if strings.TrimSpace(name) == "" {
		pre = append(pre, "-c", "user.name=mono-agent")
	}
	if strings.TrimSpace(email) == "" {
		pre = append(pre, "-c", "user.email=mono-agent@localhost")
	}
	return append(pre, args...)
}

// gitRun runs git in dir and returns its stdout; an error carries stderr.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_MERGE_AUTOEDIT=no", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(boundText(msg, 600))
	}
	return stdout.String(), nil
}
