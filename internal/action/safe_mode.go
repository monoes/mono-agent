package action

// Run-halting conditions shared by every nesting level of executeSteps:
// safe-mode stops (spec §8.5) and fatal step errors (off_domain, unknown
// step type). Both are recorded on the executor, so a nested body (condition
// branch, fragment, for_each, call_action) that stops also stops every
// enclosing executeSteps, whatever the nested handler did with the error.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/fsconfine"
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
	if cause != nil && isFatalCause(cause) {
		return ae.failRun(step.ID, cause)
	}
	// A call_action body ran under the callee's package, which checked its
	// own domains; the caller's next step re-checks the page.
	if step.Type == "call_action" {
		return nil
	}
	if derr := ae.checkPageDomain(); derr != nil {
		return ae.failRun(step.ID, fmt.Errorf("page left the allowed domains: %w", derr))
	}
	return nil
}

// safeStopRequired reports whether safe mode stops before step: a step
// flagged sideEffect, and the steps that act beyond the page or run
// arbitrary code — page_script, upload, download, a non-GET
// http_fetch_in_page, and a call_action whose target writes (or cannot be
// resolved, or does not declare its level).
func (ae *ActionExecutor) safeStopRequired(step StepDef) bool {
	if step.SideEffect {
		return true
	}
	switch step.Type {
	case "page_script", "upload", "download":
		return true
	case "http_fetch_in_page":
		m := strings.ToUpper(strings.TrimSpace(ae.resolver.Resolve(step.Method)))
		return m != "" && m != "GET"
	case "call_action":
		if ae.pkg == nil {
			return true
		}
		def, _, err := CheckCallAction(ae.pkg, step.Action)
		return err != nil || def.SideEffects == "" || atLeastWrite(def.SideEffects)
	}
	return false
}

// isFatalCause reports whether a step failure must end the run whatever
// the step's onError says: leaving the allowed domains, or a security
// refusal (ErrRefused, or a path outside the run's workdir).
func isFatalCause(err error) bool {
	return errors.Is(err, ErrOffDomain) || errors.Is(err, ErrRefused) || errors.Is(err, fsconfine.ErrOutsideWorkdir)
}

// defaultOnError is the policy for a failed step without "onError". A
// declarative package action (a package that is not legacy/local-* and has
// no native bot) aborts: a failure there must not report success. Legacy
// and native built-in actions keep master's default, continue.
func (ae *ActionExecutor) defaultOnError() *ErrorHandlerDef {
	if ae.pkg == nil || isLegacyPackage(ae.pkg) {
		return nil
	}
	if nb, ok := ae.pkg.(NativeBacked); ok && nb.Native() != "" {
		return nil
	}
	return &ErrorHandlerDef{Action: ErrorActionAbort}
}
