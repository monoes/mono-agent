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
	cost    float64
	// The lead's own file edits (#260): the call ids of its edit tool
	// calls in flight, whether it holds the write lease for them, and the
	// warnings for edits it made while a writer held the lease.
	leadEdits    map[string]bool
	leadHolds    bool
	leadWarnings []string
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
	started time.Time
	// followups counts org_message runs, capped at MaxFollowups.
	followups int
	// unconfined: a research worker whose read-only sandbox could not be
	// applied at run time; it runs holding the write lease instead.
	unconfined bool
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
	return &Conductor{
		cfg: cfg, ctx: cctx, cancel: cancel,
		slots: make(chan struct{}, cfg.Limits.MaxConcurrent),
		write: newLease(), browser: newLease(),
		workers: map[string]*worker{}, leadEdits: map[string]bool{},
	}
}

// Close cancels every worker still running and waits for them to finish.
// The lead's turn is over, so nothing is left to read their reports.
func (c *Conductor) Close() {
	c.cancel()
	c.wg.Wait()
	c.leadStopsEditing()
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
}

func (c *Conductor) infoLocked(w *worker, withReport bool) WorkerInfo {
	info := WorkerInfo{
		ID: w.id, Role: w.staff.Role, Status: w.status, Runtime: w.model.Runtime, Model: w.model.Model,
		Effort: w.staff.Effort, Access: w.staff.Access, Error: w.errText, Files: sortedKeys(w.changed),
	}
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
	c.mu.Lock()
	if c.spawned >= c.cfg.Limits.MaxAgents { // another spawn won the race
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("this turn already has its %d workers", c.cfg.Limits.MaxAgents)
	}
	c.spawned++
	w := &worker{
		id: fmt.Sprintf("w%d", c.spawned), staff: st, brief: req.Brief, files: req.Files,
		model: st.Model, status: chatevents.AgentQueued, changed: map[string]bool{}, started: c.cfg.Now(),
	}
	c.workers[w.id] = w
	c.order = append(c.order, w.id)
	c.mu.Unlock()

	skills := make([]string, len(st.Skills))
	for i, s := range st.Skills {
		skills[i] = s.Name
	}
	c.cfg.Emit.Emit(chatevents.EventAgentSpawned, chatevents.AgentSpawnedPayload{
		AgentID: w.id, Role: st.Role, AgentType: st.AgentType, Skills: skills,
		Runtime: st.Model.Runtime, Model: st.Model.Model, Effort: st.Effort, Access: st.Access,
		Brief: boundText(req.Brief, 2000), Why: strings.Join(st.Why, "; "), PickConfidence: st.PickConf, JevConfidence: st.JevConf,
	})
	c.emitMessage(w.id, "brief", "lead", w.id, req.Brief)
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, To: chatevents.AgentQueued})

	c.mu.Lock()
	ctx2, cancel, done := c.prepareRunLocked(w)
	c.mu.Unlock()
	c.launch(w, ctx2, cancel, done, req.Brief, "", true)
	if req.Wait {
		infos := c.Wait(ctx, []string{w.id}, MaxWait)
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
	if timeout <= 0 || timeout > MaxWait {
		timeout = MaxWait
	}
	// A lead that waits for workers is not editing: a writer it waits for
	// must not wait on the lead's lease.
	c.leadStopsEditing()
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
			out = append(out, c.infoLocked(w, !running(w.status)))
		} else {
			out = append(out, WorkerInfo{ID: id, Status: "unknown", Error: "no such worker"})
		}
	}
	return out
}

// Message sends a follow-up to a worker that has finished: its session is
// resumed when its runtime can, else it gets its earlier report as context.
func (c *Conductor) Message(ctx context.Context, id, text string) (WorkerInfo, error) {
	if strings.TrimSpace(text) == "" {
		return WorkerInfo{}, fmt.Errorf("text is required")
	}
	c.mu.Lock()
	w := c.workers[id]
	if w == nil {
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("no worker %q", id)
	}
	if running(w.status) {
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("%s is still working; org_wait for it first", id)
	}
	if err := c.budgetErrLocked(); err != nil {
		c.mu.Unlock()
		return WorkerInfo{}, err
	}
	if w.followups >= MaxFollowups {
		c.mu.Unlock()
		return WorkerInfo{}, fmt.Errorf("%s already had its %d follow-ups this turn; spawn a new worker if the turn's limit allows", id, MaxFollowups)
	}
	prompt, resume := text, ""
	if w.session != "" && w.model.Resume {
		resume = w.session
	} else {
		prompt = "Your earlier report:\n" + w.report + "\n\nFollow-up from the lead:\n" + text
	}
	w.report, w.errText = "", ""
	w.followups++
	w.started = c.cfg.Now()
	// Marked queued in the same critical section as the running check, so
	// two concurrent follow-ups can't both start a run.
	ctx2, cancel, done := c.prepareRunLocked(w)
	info := c.infoLocked(w, false)
	c.mu.Unlock()
	c.emitMessage(id, "followup", "lead", id, text)
	c.launch(w, ctx2, cancel, done, prompt, resume, false)
	return info, nil
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
	case chatevents.AgentQueued, chatevents.AgentStarting, chatevents.AgentWorking, chatevents.AgentWaitingLease:
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
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
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
		c.finish(w, outcome, report, errText)
	}()
}

func (c *Conductor) run(ctx context.Context, w *worker, prompt, resume string, first bool) (outcome, report, errText string) {
	outcome, report, errText, unconfined := c.runLeased(ctx, w, prompt, resume, first)
	if !unconfined {
		return outcome, report, errText
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
		defer need.l.release()
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return chatevents.AgentCancelled, "", "cancelled before it started", false
	}
	c.setStatus(w, chatevents.AgentStarting, "")

	c.mu.Lock()
	models := []Model{w.model}
	if first {
		for _, m := range w.staff.Fallbacks {
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
			return chatevents.AgentFailed, "", boundText(detail, 500), false
		}
		c.recordOutcome(m, status, detail)
		switch {
		case agentroster.Works(status):
			return chatevents.AgentDone, strings.TrimSpace(res.ResultText), "", false
		case unusable(status) && i+1 < len(models):
			next := models[i+1]
			c.mu.Lock()
			w.model = next
			c.mu.Unlock()
			c.cfg.Emit.Emit(chatevents.EventAgentReassigned, chatevents.AgentReassignedPayload{
				AgentID: w.id, FromRuntime: m.Runtime, FromModel: m.Model, ToRuntime: next.Runtime, ToModel: next.Model,
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

// confined reports whether a research worker on m can't edit files: it
// runs with --access read, or in a read-only sandbox.
func (c *Conductor) confined(m Model) bool {
	return (c.cfg.ReadAccess && m.Read) || m.ReadOnlySandbox
}

// needsWriteLease: every editing profile, and a research worker that
// nothing confines (only its prompt keeps it from editing).
func (c *Conductor) needsWriteLease(w *worker) bool {
	if writes(w.staff.Access) {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return w.unconfined || !c.confined(w.model)
}

// unusable statuses mean the model can't run at all, so the next one is
// tried; any other failure is the worker's own.
func unusable(status string) bool {
	switch status {
	case agentroster.StatusAuth, agentroster.StatusQuota, agentroster.StatusModelUnavailable, agentroster.StatusMissingBinary:
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
	opts := c.cfg.Base
	opts.Runtime, opts.Model, opts.Cwd, opts.Prompt, opts.Resume = m.Runtime, m.Model, c.cfg.Cwd, prompt, resume
	opts.Effort = ""
	if slices.Contains(m.Efforts, w.staff.Effort) {
		opts.Effort = w.staff.Effort
	}
	opts.Access = monomind.AccessFull
	c.mu.Lock()
	unconfined := w.unconfined
	c.mu.Unlock()
	requireSandbox := false
	if w.staff.Access == ProfileResearch {
		switch {
		case c.cfg.ReadAccess && m.Read:
			opts.Access = monomind.AccessRead
		case m.ReadOnlySandbox && !unconfined:
			// No --access read on this runtime: confine the worker with a
			// read-only sandbox instead (Exec's SandboxArgs decides the flag).
			// The sandbox is what lets it run without the write lease, so
			// the run fails closed when Exec can't apply it (#261).
			opts.Sandbox = monomind.SandboxReadOnly
			opts.RequireSandbox = true
			requireSandbox = true
		}
		// Neither: it holds the write lease instead (needsWriteLease).
	}
	// The budget left for the whole org caps this exec. Runtimes that
	// report no cost (codex, …) can't be capped by it; MaxTurns and the
	// timeout bound them.
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
	opts.Tools, opts.OnToolCall = nil, nil
	opts.SystemPrompt = workerSystemPrompt(w.staff, c.cfg.Cwd, w.files)
	var run monomind.TurnResult
	ectx, ecancel := context.WithCancel(ctx)
	defer ecancel()
	// sandbox is the start event's sandbox status; refused is set when it
	// says the confined run isn't sandboxed, and the run is cancelled.
	var sandbox atomic.Value
	var refused atomic.Bool
	res, err := c.cfg.Exec(ectx, opts, func(ev monomind.Event) {
		if requireSandbox && ev.Type == monomind.EventStart {
			sandbox.Store(ev.SandboxStatus)
			if ev.SandboxStatus != monomind.SandboxStatusSandboxed {
				refused.Store(true)
				ecancel()
			}
		}
		monomind.ApplyEventToResult(&run, ev)
		c.workerEvent(w, ev)
	})
	// Exec's result is the turn's final accounting; the events are the
	// fallback when it returned none.
	if res != nil {
		run = *res
	}
	c.mu.Lock()
	if run.HasInputTokens || run.HasOutputTokens {
		w.inTok += run.InputTokens
		w.outTok += run.OutputTokens
		w.hasTok = true
	}
	if run.HasCostUSD {
		w.cost += run.CostUSD
		w.hasCost = true
		c.cost += run.CostUSD
	}
	c.mu.Unlock()
	if requireSandbox {
		status, _ := sandbox.Load().(string)
		if status == "" && res != nil {
			status = res.SandboxStatus
		}
		if refused.Load() || errors.Is(err, monomind.ErrSandboxRequired) || (err == nil && status != monomind.SandboxStatusSandboxed) {
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
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, From: from, To: to, Detail: detail})
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
		payload.CostUSD = &cost
	}
	c.setStatusLocked(w, outcome, "")
	c.mu.Unlock()
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
