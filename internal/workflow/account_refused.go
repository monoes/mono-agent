package workflow

import (
	"context"
	"errors"

	"github.com/monoes/mono-agent/internal/account"
)

// errAccountRefused is the cause of a cancel that came from a refusal: the
// guard's handler and the supervisor cancel with it (CancelRunning), and the node
// check ends a run with it (refusedCancelError). The run reads it from its own
// context or its error, so a person's cancel after a refusal is not mistaken for
// one.
var errAccountRefused = errors.New("monoes.me refused this account")

// refusedCancelError is what the node check returns when a refusal lands between
// two nodes: ErrExecutionCancelled to everything that tests for a cancel, and
// errAccountRefused for cancelledMessage.
type refusedCancelError struct{}

func (refusedCancelError) Error() string   { return ErrExecutionCancelled.Error() }
func (refusedCancelError) Unwrap() []error { return []error{ErrExecutionCancelled, errAccountRefused} }

// watchAccount registers the cancel-on-refused handler with the process guard.
// With none installed (a test, or a process nothing guards) there is nothing to hear.
func (e *WorkflowEngine) watchAccount() {
	if g := account.Current(); g != nil {
		g.OnRefused(e.onAccountRefused)
	}
}

// refusalCancels says whether a verdict makes the engine cancel what it is
// running: a refusal, and only a refusal. At the 24 hours (expired) nothing is
// cancelled: the node in flight finishes and the run ends FAILED at its next node
// (spec D8, the node check), and before the enforcement date nothing is
// cancelled. The node check asks it too: a refusal that lands between two nodes
// ends the run CANCELLED, as the cancel of what is in flight does (ruling R18).
func refusalCancels(st account.Status) bool {
	return !st.Allowed() && st.Reason == account.ReasonRefused
}

// onAccountRefused cancels the executions in flight when monoes.me answered no.
// It does the work on its own goroutine: CancelExecution writes to the
// database, and the guard must not wait for that. A callback registered while
// the account is already refused is called at once and finds nothing running:
// that says nothing.
func (e *WorkflowEngine) onAccountRefused(st account.Status) {
	if !refusalCancels(st) {
		return
	}
	go func() {
		if n := e.CancelRunning(); n > 0 {
			e.logger.Warn().Int("cancelled", n).Msg("engine: monoes.me refused this account; running executions cancelled")
		}
	}()
}

// cancelledMessage is what a CANCELLED run records. When the cancel came from a
// refusal by monoes.me the run says so (spec section 12: the run records
// login_required): the cancel came from the guard, not from a person. The cause
// travels with the cancel, in the run's context or in its error, instead of
// being read from the verdict afterwards.
func cancelledMessage(ctx context.Context, runErr error) string {
	if errors.Is(context.Cause(ctx), errAccountRefused) || errors.Is(runErr, errAccountRefused) {
		return "login_required: monoes.me refused this account: " + runErr.Error()
	}
	return runErr.Error()
}
