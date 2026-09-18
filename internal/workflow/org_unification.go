package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Trigger types for executions started from the org side (plan C-40).
// Filters and the Logs page show unknown trigger types verbatim.
const (
	TriggerTypeOrgTool    = "org_tool"    // a role called a granted automation
	TriggerTypeOrgMessage = "org_message" // a message reached an automation role
	TriggerTypeOrgEvent   = "org_event"   // a trigger.org event subscription fired
)

// PauseError suspends an execution like ErrNodePaused, and additionally
// keeps the engine's 3-second resume poll from re-running the node until
// ResumeAfter has passed. An explicit ResumeExecution — the bridge waking
// the run when the event it waits for arrives — resumes it immediately.
// org.run, org.ask, and waiting automations use it so a paused run does not
// spawn a monomind status check every 3 seconds (C-33).
type PauseError struct {
	ResumeAfter time.Duration
	Reason      string
}

func (e *PauseError) Error() string {
	if e.Reason != "" {
		return "workflow: node paused: " + e.Reason
	}
	return ErrNodePaused.Error()
}

// Unwrap lets errors.Is(err, ErrNodePaused) keep matching.
func (e *PauseError) Unwrap() error { return ErrNodePaused }

// PauseFor returns a PauseError whose poll safety window is d.
func PauseFor(d time.Duration, reason string) error {
	return &PauseError{ResumeAfter: d, Reason: reason}
}

// resumeAfterLayout is fixed-width UTC so resume_after compares correctly as
// text in SQL.
const resumeAfterLayout = "2006-01-02T15:04:05.000Z"

func formatResumeAfter(t time.Time) string { return t.UTC().Format(resumeAfterLayout) }

// resumeAfterSetter is implemented by stores that persist resume_after
// (SQLite, Hybrid). Other stores (test fakes) simply resume on every poll.
type resumeAfterSetter interface {
	SetExecutionResumeAfter(ctx context.Context, id string, until *time.Time) error
}

// recordPauseWindow stores (or clears) the poll safety window for a paused
// execution.
func recordPauseWindow(ctx context.Context, store WorkflowStore, executionID string, pauseErr error) {
	s, ok := store.(resumeAfterSetter)
	if !ok {
		return
	}
	var until *time.Time
	var pe *PauseError
	if errors.As(pauseErr, &pe) && pe.ResumeAfter > 0 {
		t := time.Now().Add(pe.ResumeAfter)
		until = &t
	}
	_ = s.SetExecutionResumeAfter(ctx, executionID, until)
}

// SetExecutionResumeAfter sets or clears workflow_executions.resume_after.
func (s *SQLiteWorkflowStore) SetExecutionResumeAfter(ctx context.Context, id string, until *time.Time) error {
	var v interface{}
	if until != nil {
		v = formatResumeAfter(*until)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE workflow_executions SET resume_after = ? WHERE id = ?`, v, id); err != nil {
		return fmt.Errorf("setting execution resume_after %s: %w", id, err)
	}
	return nil
}

// SetExecutionResumeAfter delegates to the SQLite store.
func (h *HybridWorkflowStore) SetExecutionResumeAfter(ctx context.Context, id string, until *time.Time) error {
	return h.sql.SetExecutionResumeAfter(ctx, id, until)
}

// UnownedExecutionOptions describes an execution created for another
// process's engine to adopt.
type UnownedExecutionOptions struct {
	WorkflowID  string
	ProfileID   string // must match the workflow's profile ("" = default)
	TriggerType string
	TriggerData map[string]interface{}
	// AllowInactive admits workflows that are not activated. Granted
	// automations run on demand, like `workflow run`, and need no active
	// trigger.
	AllowInactive bool
}

// ErrWorkflowProfileMismatch is returned when a caller scoped to one
// profile targets another profile's workflow.
var ErrWorkflowProfileMismatch = errors.New("workflow: workflow belongs to a different profile")

// CreateUnownedExecution persists a QUEUED execution with pid 0 and no
// resume state — exactly the row `workflow run --no-wait` leaves — so the
// daemon's engine adopts and runs it. It needs no engine in the calling
// process, which is what lets the grant-mode MCP server stay a thin client
// that never bootstraps an engine, vault, or browser (C-22, C-38).
func CreateUnownedExecution(ctx context.Context, store WorkflowStore, opts UnownedExecutionOptions) (*WorkflowExecution, error) {
	wf, err := store.GetWorkflow(ctx, opts.WorkflowID)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, ErrWorkflowNotFound
	}
	owner := wf.ProfileID
	if owner == "" {
		owner = "default"
	}
	want := opts.ProfileID
	if want == "" {
		want = "default"
	}
	if owner != want {
		return nil, ErrWorkflowProfileMismatch
	}
	if !wf.IsActive && !opts.AllowInactive {
		return nil, ErrWorkflowInactive
	}
	data := opts.TriggerData
	if data == nil {
		data = map[string]interface{}{}
	}
	exec := &WorkflowExecution{
		WorkflowID:  wf.ID,
		ProfileID:   owner,
		Status:      "QUEUED",
		TriggerType: opts.TriggerType,
		TriggerData: data,
	}
	if err := store.CreateExecution(ctx, exec); err != nil {
		return nil, fmt.Errorf("create execution: %w", err)
	}
	return exec, nil
}

// TriggerSource registers trigger node types beyond the built-in manual,
// schedule, and webhook kinds (trigger.org is one). Activate starts
// delivering fires for one node and returns the function that stops it.
type TriggerSource interface {
	Activate(workflow *Workflow, node *WorkflowNode, fire func(items []Item)) (deactivate func(), err error)
}

// RegisterTriggerSource routes nodes of nodeType to p. Register before
// Start so restored workflows pick it up.
func (e *WorkflowEngine) RegisterTriggerSource(nodeType string, p TriggerSource) {
	e.triggerMgr.RegisterProvider(nodeType, p)
}
