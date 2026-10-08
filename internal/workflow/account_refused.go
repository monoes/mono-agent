package workflow

import "github.com/monoes/mono-agent/internal/account"

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

// cancelledMessage is what a CANCELLED run records. When monoes.me has refused
// the account the run says so (spec section 12: the run records login_required):
// the cancel came from the guard, not from a person.
func cancelledMessage(runErr error) string {
	if refusalCancels(account.CurrentStatus()) {
		return "login_required: monoes.me refused this account: " + runErr.Error()
	}
	return runErr.Error()
}
