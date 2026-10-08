package workflow

import (
	"context"
	"errors"
	"strings"

	"github.com/monoes/mono-agent/internal/account"
)

// requireAccountForNode is the account check before every node of a run (spec D8
// and section 6.4, rulings R4 and R18 of 2026-10-07). A run in flight goes on while
// the account is ok or grace; a locked verdict ends it at its next node, so a run
// with no agent or browser node (a polling loop, a long wait) cannot outlive the
// gate. The node in flight is never interrupted: an agent turn or a browser action
// that started before the lock finishes, and only the next node is refused.
//
// A refusal (monoes.me answered invalid_grant) ends the run CANCELLED, as the
// engine's cancel of everything in flight does, whichever of the two reaches it
// first: the check returns a refusedCancelError (an ErrExecutionCancelled), which handleExecution records
// with the login_required text of cancelledMessage. Every other lock (the 24
// hours without monoes.me, a clock rollback, an unknown key) ends it FAILED, with
// a *nodeRefusedError. Require reads the guard's cached verdict: a node pays no
// network call, and a stat of session.json at most once every 5 seconds.
func requireAccountForNode(ctx context.Context) error {
	err := account.Require(ctx)
	if err == nil {
		return nil
	}
	var lr *account.LoginRequiredError
	if errors.As(err, &lr) && refusalCancels(lr.Status) {
		return refusedCancelError{}
	}
	return &nodeRefusedError{err: err}
}

// nodeRefusedError ends a run at a node that a locked account refuses. Its text is
// what the execution records: "login_required: " and the first line of the
// refusal (the reason line stays in the wrapped error). It unwraps to the
// *account.LoginRequiredError, so account.IsLoginRequired sees it.
type nodeRefusedError struct{ err error }

func (e *nodeRefusedError) Error() string {
	first, _, _ := strings.Cut(e.err.Error(), "\n")
	return "login_required: " + first
}

func (e *nodeRefusedError) Unwrap() error { return e.err }
