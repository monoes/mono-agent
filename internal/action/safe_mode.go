package action

// Run-halting conditions shared by every nesting level of executeSteps:
// safe-mode stops (spec §8.5) and fatal step errors (off_domain, unknown
// step type). Both are recorded on the executor, so a nested body (condition
// branch, fragment, for_each, call_action) that stops also stops every
// enclosing executeSteps, whatever the nested handler did with the error.

import (
	"errors"
	"fmt"
	"time"
)

// errSafeStop unwinds executeSteps when safe mode stops before a
// side-effect step. Execute turns it into a successful result.
var errSafeStop = errors.New("safe mode: stopped before a side-effect step")

// stopBeforeSideEffect records the safe stop for step and returns
// errSafeStop. It is called only when safe mode is on and step.SideEffect.
func (ae *ActionExecutor) stopBeforeSideEffect(step StepDef) error {
	desc := step.Description
	if desc == "" && step.Intent != "" {
		desc = step.Intent
	}
	ae.safeStop = &SafeStop{StepID: step.ID, Type: step.Type, Description: desc}
	ae.logger.Info().Str("stepID", step.ID).Str("type", step.Type).
		Msg("safe mode: stopping before side-effect step")
	actionID := ""
	if ae.action != nil {
		actionID = ae.action.ID
	}
	ae.emitEvent(ExecutionEvent{
		Type:     "safe_stop",
		ActionID: actionID,
		StepID:   step.ID,
		Message:  fmt.Sprintf("Safe mode: stopped before %s (%s)", step.ID, step.Type),
	})
	return errSafeStop
}

// failRun records a fatal step failure and returns an error wrapping both
// ErrAbort and cause. Every enclosing executeSteps returns it as well.
func (ae *ActionExecutor) failRun(stepID string, cause error) error {
	ae.execCtx.AddFailedItem(FailedItem{StepID: stepID, Error: cause, Timestamp: time.Now()})
	ae.fatal = fmt.Errorf("%w: step %s: %w", ErrAbort, stepID, cause)
	return ae.fatal
}

// haltErr returns the error that must unwind the current executeSteps, or
// nil to keep going.
func (ae *ActionExecutor) haltErr() error {
	if ae.safeStop != nil {
		return errSafeStop
	}
	return ae.fatal
}

// resetRunState clears per-run halt state at the start of Execute.
func (ae *ActionExecutor) resetRunState() {
	ae.safeStop = nil
	ae.fatal = nil
}

// isHalt reports whether err must stop a loop or run immediately.
func isHalt(err error) bool {
	return errors.Is(err, ErrAbort) || errors.Is(err, errSafeStop)
}

// afterStep runs the checks every step shares once its handler returned:
// selector-marker cleanup, halts raised inside nested bodies, off_domain
// failures (which onError cannot swallow) and the post-step domain check.
func (ae *ActionExecutor) afterStep(step StepDef, result *StepResult, err error) error {
	ae.releaseSelectorMarkers()
	if h := ae.haltErr(); h != nil {
		return h
	}
	cause := err
	if cause == nil && result != nil && !result.Success {
		cause = result.Error
	}
	if cause != nil && errors.Is(cause, ErrOffDomain) {
		return ae.failRun(step.ID, cause)
	}
	if derr := ae.checkPageDomain(); derr != nil {
		return ae.failRun(step.ID, fmt.Errorf("page left the allowed domains: %w", derr))
	}
	return nil
}
