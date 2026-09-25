package action

// Package-era executor configuration and accessors. Owned by the action-core
// builder.

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// SafeStop reports where safe-mode verification stopped: the first step
// marked sideEffect, which was NOT executed.
type SafeStop struct {
	StepID      string `json:"stepId"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// SetPackage attaches the automation package the action belongs to.
func (ae *ActionExecutor) SetPackage(p PackageContext) { ae.pkg = p }

// Package returns the attached package context (nil for legacy actions).
func (ae *ActionExecutor) Package() PackageContext { return ae.pkg }

// SetSelectorObserver installs the selector-health observer (may be nil).
func (ae *ActionExecutor) SetSelectorObserver(o SelectorObserver) { ae.selObs = o }

// SetSafeMode makes Execute stop, successfully, right before the first step
// with SideEffect set. SafeStopped reports where.
func (ae *ActionExecutor) SetSafeMode(on bool) { ae.safeMode = on }

// SafeStopped returns the step safe mode stopped at, or nil.
func (ae *ActionExecutor) SafeStopped() *SafeStop { return ae.safeStop }

// SetDownloadsAllowed reflects manifest.permissions.downloads; the download
// step refuses to run unless it is set.
func (ae *ActionExecutor) SetDownloadsAllowed(on bool) { ae.downloadsAllowed = on }

// DownloadsAllowed reports whether download steps may run.
func (ae *ActionExecutor) DownloadsAllowed() bool { return ae.downloadsAllowed }

// Resolver returns the executor's template resolver.
func (ae *ActionExecutor) Resolver() *VariableResolver { return ae.resolver }

// ExecContext returns the mutable state of the current run.
func (ae *ActionExecutor) ExecContext() *ExecutionContext { return ae.execCtx }

// Page returns the browser page the executor drives (may be nil).
func (ae *ActionExecutor) Page() browser.PageInterface { return ae.page }

// Logger returns the executor's logger.
func (ae *ActionExecutor) Logger() zerolog.Logger { return ae.logger }

// RunSteps executes steps with the executor's full step semantics (template
// resolution, onError, safe mode, domain checks). It is the entry point for
// nested bodies outside Execute; a nil action/definition gets an empty
// placeholder so condition steps and events work.
func (ae *ActionExecutor) RunSteps(ctx context.Context, steps []StepDef) error {
	if ae.action == nil {
		ae.action = &StorageAction{}
	}
	if ae.actionDef == nil {
		ae.actionDef = &ActionDef{Steps: steps}
	}
	if ctx == nil {
		ctx = ae.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return ae.executeSteps(ctx, steps)
}

// prepareRun is the package-era part of ExecuteDef: attach the package from
// the DefSource, reset halt state, validate, and seed package variables.
func (ae *ActionExecutor) prepareRun(platform string, def *ActionDef) error {
	ae.resetRunState()
	if isNilPackage(ae.pkg) {
		ae.pkg = nil
		if src := CurrentDefSource(); src != nil {
			if p := src.Package(strings.ToLower(strings.TrimSpace(platform))); !isNilPackage(p) {
				ae.pkg = p
			}
		}
	}

	issues := Validate(def, ae.pkg)
	if HasErrors(issues) {
		verr := &ValidationError{Action: platform + "/" + def.ActionType, Issues: issues}
		ae.logger.Error().Err(verr).Msg("action definition failed validation")
		return verr
	}

	// An imported package's write-level actions run for real only after the
	// user confirmed once (automation trust <id> --live); safe-mode
	// verification is unaffected.
	if err := ae.checkLiveRun(ae.pkg, def); err != nil {
		return err
	}

	if ae.pkg != nil {
		if _, ok := ae.execCtx.GetVariable("site"); !ok {
			ae.execCtx.SetVariable("site", map[string]interface{}{
				"startUrl": ae.pkg.StartURL(),
				"domains":  ae.pkg.Domains(),
			})
		}
		if _, ok := ae.execCtx.GetVariable("automation"); !ok {
			ae.execCtx.SetVariable("automation", ae.pkg.ID())
		}
	}
	return nil
}

// isNilPackage treats a typed-nil PackageContext as nil.
func isNilPackage(p PackageContext) bool {
	if p == nil {
		return true
	}
	v := reflect.ValueOf(p)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func:
		return v.IsNil()
	}
	return false
}

// untilSteps are the step types whose "until" is a post-step outcome; for
// wait_for it is the step's own condition (handled by its handler).
var untilSteps = map[string]bool{"click": true, "type": true, "submit": true, "press_key": true, "select_option": true}

// applyUntil waits for step.Until after a successful click/type-like step;
// a timeout turns the step into a failure (then subject to onError).
func (ae *ActionExecutor) applyUntil(ctx context.Context, step StepDef, result *StepResult, err error) (*StepResult, error) {
	if step.Until == nil || !untilSteps[step.Type] || err != nil || result == nil || !result.Success {
		return result, err
	}
	if werr := ae.waitUntil(ctx, step.Until, stepTimeout(step, 10)); werr != nil {
		return &StepResult{Success: false, StepID: step.ID, Element: result.Element,
			Error: fmt.Errorf("step %s: until: %w", step.ID, werr)}, nil
	}
	return result, nil
}
