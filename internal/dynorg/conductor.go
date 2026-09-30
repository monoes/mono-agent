package dynorg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// MaxWait caps one org_wait (and a waiting org_spawn), so each call returns
// well inside the lead's tool timeout; the lead waits again for more.
const MaxWait = 100 * time.Second

// maxReport caps a worker's report journaled as agent.message.
const maxReport = 8000

// Config configures a turn's conductor.
type Config struct {
	Cwd        string
	Limits     Limits
	Staffer    *Staffer
	Base       monomind.ExecOptions // Bin, Settings, MaxTurns, Timeout, EffortFlag for every worker
	ReadAccess bool                 // monomind has --access read (agent-exec-access-read)
	Exec       ExecFunc
	Emit       Emitter
	Outcome    Outcome // nil = don't record
	Now        func() time.Time
	// Answers brings the user's answers to workers' questions (#256); nil
	// gives workers no ask_user tool.
	Answers AnswerSource
	// Remember keeps a worker for the conversation's later turns after
	// each of its runs (#230, veterans.go); nil keeps nothing.
	Remember func(Veteran)

	// Quality records worker results and the lead's ratings (#230); nil
	// records nothing.
	Quality *Quality

	// Writers is WritersIsolated to give each writer its own git worktree
	// (worktree.go); anything else keeps the write lease. TurnID names the
	// worktrees and branches.
	Writers string
	TurnID  string
}

// Conductor runs one lead turn's workers.
type Conductor struct {
	cfg     Config
	ctx     context.Context
	cancel  context.CancelFunc
	slots   chan struct{}
	write   *lease
	browser *lease
	wg      sync.WaitGroup

	mu      sync.Mutex
	workers map[string]*worker
	order   []string
	spawned int
	seq     int // the highest worker number so far, veterans included
	cost    float64
	// costEstimated: some of cost is estimated from tokens (estimate.go).
	costEstimated bool
	// The lead's own file edits (#260): the call ids of its edit tool
	// calls in flight, whether it holds the write lease for them, and the
	// warnings for edits it made while a writer held the lease.
	leadEdits    map[string]bool
	leadHolds    bool
	leadWarnings []string
	// iso is set when writers get their own worktrees (worktree.go);
	// mergeMu runs org_merge calls one at a time.
	iso     *isolation
	mergeMu sync.Mutex
	// subagents journals the workers' native subagents (monomind#387).
	subagents Subagents
}

type worker struct {
	id      string
	staff   Staff
	brief   string
	files   []string
	model   Model
	status  string
	report  string
	errText string
	session string
	cancel  context.CancelFunc
	done    chan struct{}
	changed map[string]bool
	inTok   int64
	outTok  int64
	hasTok  bool
	cost    float64
	hasCost bool
	// costEstimated: some of cost is estimated from tokens (estimate.go).
	costEstimated bool
	started       time.Time
	// followups counts org_message runs, capped at MaxFollowups.
	followups int
	// askedThisRun counts this run's ask_user calls (capped at
	// MaxQuestions); questionSeq numbers its questions across runs, so an
	// id never repeats within the turn (#256). hasSlot says whether it holds
	// a concurrency slot right now: it lets go of the slot and its leases
	// while it waits for an answer.
	askedThisRun int
	questionSeq  int
	openQuestion string
	hasSlot      bool
	// The spawn tree (#230, tree.go): the worker that spawned it (nil =
	// the lead), whether the lead let it spawn, and whether it must run
	// confined (a research worker's sub-worker). runCtx is its current
	// run's context, which its sub-workers' runs derive from, so they end
	// with it.
	parent      *worker
	allowSpawn  bool
	mustConfine bool
	runCtx      context.Context
	// suspended counts the waits (a question, its sub-workers) it holds
	// nothing for; held is what it gave up for them.
	suspended int
	held      []heldLease
	// veteran: loaded from an earlier turn of the conversation, and cwd
	// the folder its session ran in (veterans.go).
	veteran  bool
	cwd      string
	notReady bool // a veteran whose model is no longer ready
	treeDone bool // a veteran's worktree was tried (veteranWorktree)
	// unconfined: a research worker whose read-only sandbox could not be
	// applied at run time; it runs holding the write lease instead.
	unconfined bool
	// confinement is what confines its current run (chatevents.Confinement*).
	confinement string
	// unusable holds the models (runtime/model) that couldn't run this
	// worker (auth, quota, …), so a retry doesn't try them again.
	unusable map[string]bool
	stream   workerStream // its text and live usage (workerstream.go)
	leases   []heldLease  // the leases it holds, for agent.status (lease.go)
	// worktree, branch and workDir are set for an isolated writer
	// (worktree.go): it runs in workDir, inside worktree, on branch.
	// merging is set while org_merge commits and merges its branch.
	worktree, branch, workDir string
	merging                   bool
}

// MaxFollowups caps org_message runs per worker, so follow-ups can't stand
// in for new workers past the turn's agent cap.
const MaxFollowups = 3

// errBudgetRefused is execOnce's refusal to start a run once the workers'
// budget is spent. It is the org's cap, not the model's failure, so it is
// not recorded as a model outcome.
var errBudgetRefused = errors.New("budget")

// errUnconfined is execOnce's refusal of a research run whose read-only
// sandbox was not applied at run time (#261).
var errUnconfined = errors.New("the read-only sandbox was not applied")

// errBudgetSpent is returned when the workers' budget has run out.
func (c *Conductor) budgetErrLocked() error {
	if b := c.cfg.Limits.BudgetUSD; b > 0 && c.cost >= b {
		if c.costEstimated {
			return fmt.Errorf("the workers' budget of $%.2f for this turn is spent (≈$%.2f, partly estimated from token counts)", b, c.cost)
		}
		return fmt.Errorf("the workers' budget of $%.2f for this turn is spent ($%.2f)", b, c.cost)
	}
	return nil
}

// New starts a conductor for one lead turn; Close ends it.
func New(ctx context.Context, cfg Config) *Conductor {
	if cfg.Limits.MaxAgents <= 0 || cfg.Limits.MaxConcurrent <= 0 {
		d := DefaultLimits()
		if cfg.Limits.MaxAgents <= 0 {
			cfg.Limits.MaxAgents = d.MaxAgents
		}
		if cfg.Limits.MaxConcurrent <= 0 {
			cfg.Limits.MaxConcurrent = d.MaxConcurrent
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Exec == nil {
		cfg.Exec = monomind.Exec
	}
	cctx, cancel := context.WithCancel(ctx)
	c := &Conductor{
		cfg: cfg, ctx: cctx, cancel: cancel,
		slots: make(chan struct{}, cfg.Limits.MaxConcurrent),
		write: newLease(), browser: newLease(),
		workers: map[string]*worker{}, leadEdits: map[string]bool{},
	}
	c.initWriters()
	return c
}

// Close cancels every worker still running and waits for them to finish.
// The lead's turn is over, so nothing is left to read their reports.
func (c *Conductor) Close() {
	c.cancel()
	c.wg.Wait()
	c.leadStopsEditing()
	c.cleanupWorktrees()
}

// WorkerInfo is what the lead's tools report about a worker.
type WorkerInfo struct {
	ID      string   `json:"agent_id"`
	Role    string   `json:"role"`
	Status  string   `json:"status"`
	Runtime string   `json:"runtime"`
	Model   string   `json:"model,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Access  string   `json:"access"`
	Skills  []string `json:"skills,omitempty"`
	Why     string   `json:"why,omitempty"`
	Report  string   `json:"report,omitempty"`
	Error   string   `json:"error,omitempty"`
	Files   []string `json:"files_changed,omitempty"`
	// ParentID is the worker that spawned it ("" = the lead); AllowSpawn
	// says it may spawn sub-workers; Veteran marks a worker from an earlier
	// turn (#230).
	ParentID   string `json:"parent_id,omitempty"`
	AllowSpawn bool   `json:"allow_spawn,omitempty"`
	Veteran    bool   `json:"veteran,omitempty"`
	// Question is the question a waiting_user worker asked the user (#256).
	Question string `json:"question,omitempty"`
	// Branch is an isolated writer's branch (#230).
	Branch string `json:"branch,omitempty"`
}

func (c *Conductor) infoLocked(w *worker, withReport bool) WorkerInfo {
	info := WorkerInfo{
		ID: w.id, Role: w.staff.Role, Status: w.status, Runtime: w.model.Runtime, Model: w.model.Model,
		Effort: w.staff.Effort, Access: w.staff.Access, Error: w.errText, Files: sortedKeys(w.changed),
		Question: w.openQuestion, Branch: w.branch,
	}
	info.ParentID, info.AllowSpawn, info.Veteran = parentID(w), w.allowSpawn, w.veteran
	for _, s := range w.staff.Skills {
		info.Skills = append(info.Skills, s.Name)
	}
	if withReport {
		info.Report = w.report
	}
	return info
}

// Spawn staffs and starts a worker. With req.Wait it also waits for it
// (up to MaxWait).
func (c *Conductor) Spawn(ctx context.Context, req SpawnRequest) (WorkerInfo, error) {
	return c.spawn(ctx, req, nil)
}

// spawn starts a worker for the lead (parent nil) or a sub-worker for
// parent (tree.go). The limits count the whole tree.
func (c *Conductor) spawn(ctx context.Context, req SpawnRequest, parent *worker) (WorkerInfo, error) {
	if parent != nil {
		var err error
		if req, err = childRequest(parent, req); err != nil {
			return WorkerInfo{}, err
		}
	}
	c.mu.Lock()
	if c.spawned >= c.cfg.Limits.MaxAgents {
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("this turn already has its %d workers; wait for them and use org_message for follow-ups", c.cfg.Limits.MaxAgents)
	}
	if err := c.budgetErrLocked(); err != nil {
		c.mu.Unlock()
		return WorkerInfo{}, err
	}
	c.mu.Unlock()

	st, err := c.cfg.Staffer.Staff(ctx, req)
	if err != nil {
		return WorkerInfo{}, err
	}
	if parent != nil {
		if err := c.fitChild(parent, req, &st); err != nil {
			return WorkerInfo{}, err
		}
	}
	c.mu.Lock()
	if c.spawned >= c.cfg.Limits.MaxAgents { // another spawn won the race
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("this turn already has its %d workers", c.cfg.Limits.MaxAgents)
	}
	c.spawned++
	c.seq++
	w := &worker{
		id: fmt.Sprintf("w%d", c.seq), staff: st, brief: req.Brief, files: req.Files,
		model: st.Model, status: chatevents.AgentQueued, changed: map[string]bool{}, started: c.cfg.Now(),
		parent: parent, allowSpawn: req.AllowSpawn && parent == nil, cwd: c.cfg.Cwd,
		mustConfine: parent != nil && st.Access == ProfileResearch && parent.staff.Access == ProfileResearch,
	}
	c.workers[w.id] = w
	c.order = append(c.order, w.id)
	c.mu.Unlock()

	c.addWorktree(w)
	skills := make([]string, len(st.Skills))
	for i, s := range st.Skills {
		skills[i] = s.Name
	}
	c.cfg.Emit.Emit(chatevents.EventAgentSpawned, chatevents.AgentSpawnedPayload{
		AgentID: w.id, ParentID: parentID(w), AllowSpawn: w.allowSpawn, Role: st.Role, AgentType: st.AgentType, Skills: skills,
		Runtime: st.Model.Runtime, Model: st.Model.Model, Fidelity: st.Model.Fidelity, Effort: st.Effort, Access: st.Access,
		Brief: boundText(req.Brief, 2000), Why: strings.Join(st.Why, "; "), PickConfidence: st.PickConf, JevConfidence: st.JevConf,
		Branch: w.branch,
	})
	c.emitMessage(w.id, "brief", orLead(parent), w.id, req.Brief)
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, To: chatevents.AgentQueued, Branch: w.branch})

	c.mu.Lock()
	ctx2, cancel, done := c.prepareRunLocked(w)
	c.mu.Unlock()
	c.launch(w, ctx2, cancel, done, req.Brief, "", true)
	if req.Wait {
		infos := c.waitFor(ctx, parent, []string{w.id}, MaxWait)
		if len(infos) == 1 {
			return infos[0], nil
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.infoLocked(w, false)
	info.Why = strings.Join(st.Why, "; ")
	return info, nil
}

// Wait blocks until the workers finish or timeout passes, then reports on
// each; ids empty means every worker.
func (c *Conductor) Wait(ctx context.Context, ids []string, timeout time.Duration) []WorkerInfo {
	return c.waitFor(ctx, nil, ids, timeout)
}

// wait is Wait for whoever is waiting; ids empty means every worker.
func (c *Conductor) wait(ctx context.Context, ids []string, timeout time.Duration) []WorkerInfo {
	if timeout <= 0 || timeout > MaxWait {
		timeout = MaxWait
	}
	c.mu.Lock()
	if len(ids) == 0 {
		ids = slices.Clone(c.order)
	}
	var waits []chan struct{}
	for _, id := range ids {
		if w := c.workers[id]; w != nil && w.done != nil {
			waits = append(waits, w.done)
		}
	}
	c.mu.Unlock()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for _, ch := range waits {
		select {
		case <-ch:
		case <-deadline.C:
			goto report
		case <-ctx.Done():
			goto report
		}
	}
report:
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []WorkerInfo{}
	for _, id := range ids {
		if w := c.workers[id]; w != nil {
			// A veteran that hasn't run this turn has no new report.
			out = append(out, c.infoLocked(w, !running(w.status) && w.done != nil))
		} else {
			out = append(out, WorkerInfo{ID: id, Status: "unknown", Error: "no such worker"})
		}
	}
	return out
}

// Message sends a follow-up to a worker that has finished: its session is
// resumed when its runtime can, else it gets its earlier report as context.
func (c *Conductor) Message(ctx context.Context, id, text string) (WorkerInfo, error) {
	return c.message(ctx, nil, id, text)
}

// message is Message from the lead (from nil), or from a worker to one of
// its own sub-workers.
func (c *Conductor) message(ctx context.Context, from *worker, id, text string) (WorkerInfo, error) {
	if strings.TrimSpace(text) == "" {
		return WorkerInfo{}, fmt.Errorf("text is required")
	}
	c.mu.Lock()
	var w *worker
	for {
		var err error
		if w, err = c.followupCheckLocked(from, id); err != nil {
			c.mu.Unlock()
			return WorkerInfo{}, err
		}
		if !c.needsVeteranTreeLocked(w) {
			break
		}
		// A writing veteran gets its worktree only once the follow-up is
		// allowed; the checks run again after the git call.
		w.treeDone = true
		c.mu.Unlock()
		c.addWorktree(w)
		c.mu.Lock()
	}
	prompt, resume := text, ""
	// A session resumes only in the folder it ran in (a veteran's, or an
	// isolated writer's worktree of an earlier turn, is another).
	if w.session != "" && w.model.Resume && w.cwd == orElse(w.workDir, c.cfg.Cwd) {
		resume = w.session
	} else {
		prompt = "Your earlier report:\n" + w.report + "\n\nFollow-up from the lead:\n" + text
	}
	w.report, w.errText = "", ""
	w.followups++
	w.askedThisRun = 0
	w.started = c.cfg.Now()
	// Marked queued in the same critical section as the running check, so
	// two concurrent follow-ups can't both start a run.
	ctx2, cancel, done := c.prepareRunLocked(w)
	info := c.infoLocked(w, false)
	c.mu.Unlock()
	c.emitMessage(id, "followup", orLead(from), id, text)
	c.launch(w, ctx2, cancel, done, prompt, resume, false)
	return info, nil
}

// followupCheckLocked finds the worker id and checks a follow-up to it
// from the lead (from nil) or its parent may start now.
func (c *Conductor) followupCheckLocked(from *worker, id string) (*worker, error) {
	w := c.workers[id]
	if w == nil {
		return nil, fmt.Errorf("no worker %q", id)
	}
	if err := messageScope(from, w); err != nil {
		return nil, err
	}
	if err := c.veteranBlockedLocked(w); err != nil {
		return nil, err
	}
	if running(w.status) {
		return nil, fmt.Errorf("%s is still working; org_wait for it first", id)
	}
	if w.merging {
		return nil, fmt.Errorf("%s's branch is being merged; send the follow-up once org_merge returns", id)
	}
	if err := c.budgetErrLocked(); err != nil {
		return nil, err
	}
	if w.followups >= MaxFollowups {
		return nil, fmt.Errorf("%s already had its %d follow-ups this turn; spawn a new worker if the turn's limit allows", id, MaxFollowups)
	}
	return w, nil
}

// Stop cancels one worker.
func (c *Conductor) Stop(id string) (WorkerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.workers[id]
	if w == nil {
		return WorkerInfo{}, fmt.Errorf("no worker %q", id)
	}
	if w.cancel != nil && running(w.status) {
		w.cancel()
	}
	return c.infoLocked(w, false), nil
}

func running(status string) bool {
	switch status {
	case chatevents.AgentQueued, chatevents.AgentStarting, chatevents.AgentWorking, chatevents.AgentWaitingLease, chatevents.AgentWaitingUser:
		return true
	}
	return false
}

// launch runs a worker in the background: its leases, then a concurrency
// slot, then the exec (falling over to the next model when this one
// can't run, on a first run only).
// prepareRunLocked marks w queued and gives it a fresh cancel and done
// for its next run. Callers hold c.mu, together with the check that w
// isn't already running.
func (c *Conductor) prepareRunLocked(w *worker) (context.Context, context.CancelFunc, chan struct{}) {
	// A sub-worker's run ends with its parent's run (or the turn).
	base := c.ctx
	if w.parent != nil && w.parent.runCtx != nil {
		base = w.parent.runCtx
	}
	ctx, cancel := context.WithCancel(base)
	done := make(chan struct{})
	w.cancel, w.done, w.runCtx = cancel, done, ctx
	w.cwd = orElse(w.workDir, c.cfg.Cwd)
	w.suspended, w.held = 0, nil
	c.setStatusLocked(w, chatevents.AgentQueued, "")
	return ctx, cancel, done
}

func (c *Conductor) launch(w *worker, ctx context.Context, cancel context.CancelFunc, done chan struct{}, prompt, resume string, first bool) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(done)
		defer cancel()
		outcome, report, errText := c.run(ctx, w, prompt, resume, first)
		c.commitWorktree(w)
		c.finish(w, outcome, report, errText)
	}()
}

func (c *Conductor) run(ctx context.Context, w *worker, prompt, resume string, first bool) (outcome, report, errText string) {
	outcome, report, errText, unconfined := c.runLeased(ctx, w, prompt, resume, first)
	if !unconfined {
		return outcome, report, errText
	}
	if w.mustConfine {
		// A research worker's sub-worker never runs unconfined: its access
		// can't exceed its parent's.
		c.mu.Lock()
		m := w.model
		c.mu.Unlock()
		if !c.confined(m) {
			return chatevents.AgentFailed, "", "can't run confined on " + m.Key() + "; a research worker's sub-worker must"
		}
		return chatevents.AgentFailed, "", errUnconfined.Error() + "; a research worker's sub-worker can't run without it"
	}
	// The read-only sandbox this research worker was staffed for was not
	// applied at run time (the runtime changed since the scan, a stale
	// cache): Exec refused it, or its start event said so. Rather than run
	// it unconfined beside a writer, it runs again holding the write lease,
	// like a research worker nothing confines (#261).
	c.mu.Lock()
	w.unconfined = true
	c.mu.Unlock()
	c.setStatus(w, chatevents.AgentQueued, "no read-only sandbox; waiting for the write lease")
	outcome, report, errText, unconfined = c.runLeased(ctx, w, prompt, resume, first)
	if unconfined {
		return chatevents.AgentFailed, "", errUnconfined.Error()
	}
	return outcome, report, errText
}

// runLeased is one attempt at a worker's run under its leases. unconfined
// reports that a sandbox-confined research run was refused (errUnconfined).
func (c *Conductor) runLeased(ctx context.Context, w *worker, prompt, resume string, first bool) (outcome, report, errText string, unconfined bool) {
	// Leases first, then a concurrency slot: a writer queued behind another
	// writer must not hold a slot a reader could use.
	for _, need := range []struct {
		on   bool
		l    *lease
		name string
	}{{c.needsWriteLease(w), c.write, "write"}, {usesBrowser(w.staff.Access), c.browser, "browser"}} {
		if !need.on {
			continue
		}
		if !need.l.tryAcquire() {
			c.setStatus(w, chatevents.AgentWaitingLease, need.name)
			if err := need.l.acquire(ctx); err != nil {
				return chatevents.AgentCancelled, "", "cancelled while waiting for the " + need.name + " lease", false
			}
		}
		defer c.holdLease(w, need.l, need.name)()
	}
	select {
	case c.slots <- struct{}{}:
		c.mu.Lock()
		w.hasSlot = true
		c.mu.Unlock()
		defer c.dropSlot(w)
	case <-ctx.Done():
		return chatevents.AgentCancelled, "", "cancelled before it started", false
	}
	c.setStatus(w, chatevents.AgentStarting, "")

	c.mu.Lock()
	models := []Model{w.model}
	if first {
		for _, m := range w.staff.Fallbacks {
			if w.unusable[modelKey(m)] || modelKey(m) == modelKey(w.model) {
				continue
			}
			// A confined research worker runs without the write lease,
			// so it may only fall back to models that confine it too.
			if w.staff.Access == ProfileResearch && !w.unconfined && c.confined(w.model) && !c.confined(m) {
				continue
			}
			models = append(models, m)
		}
	}
	c.mu.Unlock()
	for i, m := range models {
		res, err := c.execOnce(ctx, w, m, prompt, resume)
		if ctx.Err() != nil {
			return chatevents.AgentCancelled, "", "cancelled", false
		}
		if errors.Is(err, errUnconfined) {
			return "", "", "", true
		}
		status, detail := agentroster.Classify(res, err)
		if errors.Is(err, errBudgetRefused) || (res != nil && res.Err != nil && res.Err.Code == monomind.ErrBudget) {
			// The org's budget stopped it, not the model: nothing to
			// record about the model, and no fallback to try.
			if !strings.HasPrefix(detail, "budget") {
				detail = "budget: " + detail
			}
			// A run monomind stopped at its cap keeps what it wrote so far.
			text := ""
			if res != nil {
				text = strings.TrimSpace(res.ResultText)
			}
			return chatevents.AgentFailed, text, boundText(detail, 500), false
		}
		c.recordOutcome(m, status, detail)
		switch {
		case agentroster.Works(status):
			return chatevents.AgentDone, strings.TrimSpace(res.ResultText), "", false
		case unusable(status) && i+1 < len(models):
			next := models[i+1]
			c.mu.Lock()
			if w.unusable == nil {
				w.unusable = map[string]bool{}
			}
			w.unusable[modelKey(m)] = true
			w.model = next
			c.mu.Unlock()
			c.cfg.Emit.Emit(chatevents.EventAgentReassigned, chatevents.AgentReassignedPayload{
				AgentID: w.id, FromRuntime: m.Runtime, FromModel: m.Model, ToRuntime: next.Runtime, ToModel: next.Model, Fidelity: next.Fidelity,
				Reason: status + ": " + boundText(detail, 300),
			})
			continue
		}
		text := ""
		if res != nil {
			text = strings.TrimSpace(res.ResultText)
		}
		return chatevents.AgentFailed, text, status + ": " + boundText(detail, 500), false
	}
	return chatevents.AgentFailed, "", "no model could run this worker", false
}

func modelKey(m Model) string { return m.Runtime + "/" + m.Model }

// confined reports whether a research worker on m can't edit files: it
// runs with --access read, or in a read-only sandbox.
func (c *Conductor) confined(m Model) bool {
	return (c.cfg.ReadAccess && m.Read) || m.ReadOnlySandbox
}

// needsWriteLease: every editing profile, and a research worker that
// nothing confines (only its prompt keeps it from editing).
func (c *Conductor) needsWriteLease(w *worker) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if writes(w.staff.Access) {
		return w.worktree == "" // an isolated writer edits only its own worktree
	}
	if w.mustConfine {
		// Never: it runs confined or not at all (execOnce).
		return false
	}
	return w.unconfined || !c.confined(w.model)
}

// dropSlot gives back w's concurrency slot if it still holds it.
func (c *Conductor) dropSlot(w *worker) {
	c.mu.Lock()
	had := w.hasSlot
	w.hasSlot = false
	c.mu.Unlock()
	if had {
		<-c.slots
	}
}

// unusable statuses mean the model can't run at all, so the next one is
// tried; any other failure is the worker's own.
func unusable(status string) bool {
	switch status {
	case agentroster.StatusAuth, agentroster.StatusQuota, agentroster.StatusRateLimited, agentroster.StatusModelUnavailable, agentroster.StatusMissingBinary:
		return true
	}
	return false
}

func (c *Conductor) recordOutcome(m Model, status, detail string) {
	if c.cfg.Outcome != nil {
		model := m.Model
		if model == "" {
			model = agentroster.DefaultModel
		}
		c.cfg.Outcome(m.Runtime, model, status, detail, c.cfg.Now())
	}
}

func (c *Conductor) execOnce(ctx context.Context, w *worker, m Model, prompt, resume string) (*monomind.TurnResult, error) {
	if w.mustConfine && !c.confined(m) {
		// A research worker's sub-worker fails closed on a model that
		// can't confine it (a veteran's model may have changed).
		return nil, errUnconfined
	}
	opts := c.cfg.Base
	dir := c.workDir(w)
	opts.Runtime, opts.Model, opts.Cwd, opts.Prompt, opts.Resume = m.Runtime, m.Model, dir, prompt, resume
	opts.Effort = ""
	if slices.Contains(m.Efforts, w.staff.Effort) {
		opts.Effort = w.staff.Effort
	}
	opts.Access = monomind.AccessFull
	c.mu.Lock()
	unconfined := w.unconfined
	c.mu.Unlock()
	requireSandbox := false
	confinement := ""
	if w.staff.Access == ProfileResearch {
		switch {
		case c.cfg.ReadAccess && m.Read:
			opts.Access = monomind.AccessRead
			confinement = chatevents.ConfinementAccessRead
		case m.ReadOnlySandbox && !unconfined:
			// No --access read on this runtime: confine the worker with a
			// read-only sandbox instead (Exec's SandboxArgs decides the flag).
			// The sandbox is what lets it run without the write lease, so
			// the run fails closed when Exec can't apply it (#261).
			opts.Sandbox = monomind.SandboxReadOnly
			opts.RequireSandbox = true
			requireSandbox = true
			confinement = chatevents.ConfinementSandbox
		default:
			// Neither (an older monomind, or a runtime without read
			// access): full access, told to stay read-only, and it holds
			// the write lease (needsWriteLease).
			confinement = chatevents.ConfinementWriteLease
		}
	}
	c.mu.Lock()
	w.confinement = confinement
	c.mu.Unlock()
	// The budget left for the whole org caps this exec. Runtimes that
	// report no cost (codex, …) are capped by their estimated cost
	// (estimate.go); with no tokens either, MaxTurns and the timeout bound
	// them.
	c.mu.Lock()
	if b := c.cfg.Limits.BudgetUSD; b > 0 {
		remaining := b - c.cost
		if remaining <= 0 {
			c.mu.Unlock()
			return nil, fmt.Errorf("%w: the workers' budget of $%.2f for this turn is spent", errBudgetRefused, b)
		}
		opts.BudgetUSD = remaining
	}
	c.mu.Unlock()
	opts.SystemPrompt = workerSystemPrompt(w.staff, dir, w.files, c.spawnsOn(w, m, opts.Access)) + c.worktreeRule(w)
	c.workerTools(w, m, &opts)
	var run monomind.TurnResult
	ectx, ecancel := context.WithCancel(ctx)
	defer ecancel()
	// refused is set when the start event says the confined run isn't
	// sandboxed, and the run is cancelled.
	var refused, overBudget atomic.Bool
	res, err := c.cfg.Exec(ectx, opts, func(ev monomind.Event) {
		if requireSandbox && ev.Type == monomind.EventStart {
			if ev.SandboxStatus != monomind.SandboxStatusSandboxed {
				refused.Store(true)
				ecancel()
			}
		}
		monomind.ApplyEventToResult(&run, ev)
		if overEstimate(m, &run, opts.BudgetUSD) && overBudget.CompareAndSwap(false, true) {
			ecancel()
		}
		if c.subagents.Handle(c.cfg.Emit, ev, w.id+":", w.id) {
			return
		}
		c.stream(w, ev, &run)
		c.workerEvent(w, ev)
	})
	c.flushText(w, true)
	// Exec's result is the turn's final accounting; the events are the
	// fallback when it returned none.
	run = finalRun(res, run)
	c.mu.Lock()
	if run.HasInputTokens || run.HasOutputTokens {
		w.inTok += run.InputTokens
		w.outTok += run.OutputTokens
		w.hasTok = true
	}
	c.addRunCostLocked(w, m, &run)
	c.mu.Unlock()
	// A run that completed anyway stays done; budgetErrLocked refuses
	// what comes next.
	if overBudget.Load() && cutShort(&run) {
		return res, fmt.Errorf("%w: the workers' budget of $%.2f for this turn is spent (≈$%.2f, partly estimated from token counts)", errBudgetRefused, c.cfg.Limits.BudgetUSD, c.spent())
	}
	if requireSandbox {
		// Refused: Exec couldn't apply the sandbox, the start event said it
		// wasn't applied (the run was cancelled), or the turn's own report
		// says so. A run that failed before it started (no start event, no
		// report, e.g. auth) ran nothing, and keeps its own failure.
		status := ""
		if res != nil {
			status = res.SandboxStatus
		}
		if refused.Load() || errors.Is(err, monomind.ErrSandboxRequired) || (status != "" && status != monomind.SandboxStatusSandboxed) {
			return res, errUnconfined
		}
	}
	return res, err
}

// workerEvent journals a worker's own events: its tool calls (with its
// AgentID, and call ids made unique across the org), its session, and the
// switch to working.
func (c *Conductor) workerEvent(w *worker, ev monomind.Event) {
	switch ev.Type {
	case monomind.EventStart:
		c.setStatus(w, chatevents.AgentWorking, "")
	case monomind.EventSession:
		if ev.SessionID != "" {
			c.mu.Lock()
			w.session = ev.SessionID
			c.mu.Unlock()
		}
	case monomind.EventToolActivity:
		callID := w.id + ":" + ev.ID
		switch ev.Phase {
		case "start":
			args, _ := chatevents.RedactAndBoundFields(ev.Input)
			parent := ""
			if ev.ParentToolUseID != "" {
				parent = w.id + ":" + ev.ParentToolUseID
			}
			c.noteChangedFile(w, ev)
			c.cfg.Emit.Emit(chatevents.EventToolStarted, chatevents.ToolStartedPayload{
				AgentID: w.id, CallID: callID, Name: ev.Name, Arguments: args, Native: true, ParentCallID: parent, Kind: ev.Kind,
			})
		case "end":
			out, cut, _ := chatevents.BoundText(ev.Output, chatevents.MaxToolPreviewBytes)
			ok := ev.OK
			if ok == nil {
				v := !ev.Denied && !ev.Cancelled
				ok = &v
			}
			c.cfg.Emit.Emit(chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{
				AgentID: w.id, CallID: callID, OK: ok, Result: out, Truncated: cut, DurationMs: ev.DurationMs,
				Denied: ev.Denied, Cancelled: ev.Cancelled,
			})
		}
	}
}

// noteChangedFile remembers the file a writing tool call targets.
func (c *Conductor) noteChangedFile(w *worker, ev monomind.Event) {
	if p := editTarget(ev); p != "" {
		c.mu.Lock()
		w.changed[p] = true
		c.mu.Unlock()
	}
}

// isEditCall reports whether a tool call edits files.
func isEditCall(ev monomind.Event) bool {
	kind := ev.Kind
	if kind == "" {
		switch ev.Name {
		case "Edit", "MultiEdit", "NotebookEdit":
			kind = "edit"
		case "Write":
			kind = "write"
		}
	}
	return kind == "edit" || kind == "write" || kind == "patch"
}

// editTarget is the file an editing tool call targets, or "".
func editTarget(ev monomind.Event) string {
	if !isEditCall(ev) {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(ev.Input, &input) != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "notebook_path"} {
		if p, ok := input[k].(string); ok && p != "" {
			return p
		}
	}
	return ""
}

func (c *Conductor) setStatus(w *worker, to, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setStatusLocked(w, to, detail)
}

func (c *Conductor) setStatusLocked(w *worker, to, detail string) {
	if w.status == to {
		return
	}
	from := w.status
	w.status = to
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, From: from, To: to, Detail: detail, Leases: leaseNames(w.leases), Branch: w.branch, Confinement: w.confinement})
}

func (c *Conductor) finish(w *worker, outcome, report, errText string) {
	c.mu.Lock()
	w.report, w.errText = boundText(report, maxReport), errText
	payload := chatevents.AgentFinishedPayload{
		AgentID: w.id, Outcome: outcome, Summary: boundText(report, 600),
		DurationMs: c.cfg.Now().Sub(w.started).Milliseconds(), FilesChanged: sortedKeys(w.changed),
	}
	if w.hasTok {
		in, out := w.inTok, w.outTok
		payload.InputTokens, payload.OutputTokens = &in, &out
	}
	if w.hasCost {
		cost := w.cost
		payload.CostUSD, payload.CostEstimated = &cost, w.costEstimated
	}
	c.setStatusLocked(w, outcome, "")
	c.mu.Unlock()
	c.remember(w)
	c.recordResult(w, outcome, errText)
	if report != "" {
		c.emitMessage(w.id, "result", w.id, "lead", report)
	}
	c.cfg.Emit.Emit(chatevents.EventAgentFinished, payload)
}

func (c *Conductor) emitMessage(agentID, direction, from, to, text string) {
	bounded, cut, _ := chatevents.BoundText(text, maxReport)
	c.cfg.Emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
		AgentID: agentID, Direction: direction, From: from, To: to, Text: bounded, Truncated: cut,
	})
}

func boundText(s string, max int) string {
	out, _, _ := chatevents.BoundText(s, max)
	return out
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
