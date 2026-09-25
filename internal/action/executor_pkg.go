package action

// Package-era executor configuration. Owned by the action-core builder.

// SafeStop reports where safe-mode verification stopped: the first step
// marked sideEffect, which was NOT executed.
type SafeStop struct {
	StepID      string `json:"stepId"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// SetPackage attaches the automation package the action belongs to.
func (ae *ActionExecutor) SetPackage(p PackageContext) { ae.pkg = p }

// SetSelectorObserver installs the selector-health observer (may be nil).
func (ae *ActionExecutor) SetSelectorObserver(o SelectorObserver) { ae.selObs = o }

// SetSafeMode makes Execute stop, successfully, right before the first step
// with SideEffect set. SafeStopped reports where.
func (ae *ActionExecutor) SetSafeMode(on bool) { ae.safeMode = on }

// SafeStopped returns the step safe mode stopped at, or nil.
func (ae *ActionExecutor) SafeStopped() *SafeStop { return ae.safeStop }
