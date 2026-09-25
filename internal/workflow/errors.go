package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PartialFailureError reports that an execution completed but one or more nodes
// failed under an on_error=continue/skip/error_branch policy. The engine maps it
// to a SUCCESS_WITH_ERRORS status so a run that had failures isn't shown as green.
type PartialFailureError struct {
	Nodes []string // names of nodes that failed non-fatally
}

func (e *PartialFailureError) Error() string {
	return fmt.Sprintf("%d node(s) failed but the workflow continued: %s",
		len(e.Nodes), strings.Join(e.Nodes, ", "))
}

var (
	ErrQueueFull          = errors.New("workflow: execution queue is full")
	ErrQueueClosed        = errors.New("workflow: execution queue is closed")
	ErrWorkflowNotFound   = errors.New("workflow: workflow not found")
	ErrExecutionNotFound  = errors.New("workflow: execution not found")
	ErrCycleDetected      = errors.New("workflow: cycle detected in workflow graph")
	ErrDanglingConnection = errors.New("workflow: connection references a node id that does not exist")
	ErrInvalidConfig      = errors.New("workflow: invalid node configuration")
	ErrNodeTypeUnknown    = errors.New("workflow: unknown node type")
	ErrNoTriggerNode      = errors.New("workflow: workflow has no trigger node")
	ErrWorkflowInactive   = errors.New("workflow: workflow is not active")
	// ErrRetryOrgStarted: a run an org role started through a grant or an
	// automation-role message cannot be retried here, since a retry would
	// skip the admission it went through (ledger row, grant caps, grant
	// still in force). Re-issue it from the org side instead.
	ErrRetryOrgStarted    = errors.New("workflow: a run started from an org cannot be retried; call the automation again from the org")
	ErrExecutionCancelled = errors.New("workflow: execution was cancelled")
	ErrExecutionTimeout   = errors.New("workflow: execution timed out")
	ErrTriggerActive      = errors.New("workflow: trigger already active for this workflow")

	// ErrNodePaused is returned by a node (e.g. Human-in-Loop) to suspend the
	// execution until an external event (an approval) lets it resume, without
	// holding a goroutine. RunExecution serializes its state and returns
	// ErrExecutionPaused; the engine marks the execution WAITING.
	ErrNodePaused      = errors.New("workflow: node paused, awaiting resume")
	ErrExecutionPaused = errors.New("workflow: execution paused, awaiting resume")
)

// PermanentError marks a node failure that retrying cannot fix (a 404, a
// rejected payload, a missing resource). executeWithRetry never retries an
// error that wraps one, whatever the node's retry_policy says.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying error to errors.Is / errors.As.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so the engine does not retry it. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// isNonRetryable reports errors whose outcome a retry cannot change: a
// Human-in-Loop pause (retrying would re-create the approval), invalid
// configuration, cancellation/deadline, and errors marked Permanent.
func isNonRetryable(err error) bool {
	var pe *PermanentError
	return errors.Is(err, ErrNodePaused) ||
		errors.Is(err, ErrInvalidConfig) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &pe)
}
