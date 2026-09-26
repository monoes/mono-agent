package nodes

import (
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// sideEffectRank orders an action's declared sideEffects. An undeclared or
// unknown value ranks as write: it can't be shown to be harmless.
func sideEffectRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return 0
	case "read":
		return 1
	case "message":
		return 3
	case "destructive":
		return 4
	default: // "write", "" and anything unrecognised
		return 2
	}
}

// checkLiveRun enforces the first-live-run confirmation (contract §8): an
// action of an imported package that writes (sideEffects ≥ write) refuses
// to run until the user has confirmed the package once. Built-in, local and
// recorded packages, and read-only actions, run as before.
func checkLiveRun(automationID, actionType string) error {
	id := strings.ToLower(automationID)
	var pkg action.PackageContext
	if src := action.CurrentDefSource(); src != nil {
		pkg = src.Package(id)
	}
	switch automationTrust(id) {
	case automation.SourceImported:
	case "":
		// A package whose trust can't be read counts as imported (fail
		// closed); an action with no package at all is a legacy one.
		if pkg == nil {
			return nil
		}
	default:
		return nil
	}
	def, err := action.GetLoader().Load(id, actionType)
	if err != nil {
		return nil // the run itself reports the missing action
	}
	if sideEffectRank(def.SideEffects) < sideEffectRank("write") {
		return nil
	}
	if c, ok := pkg.(interface{ LiveRunConfirmed() bool }); ok && c.LiveRunConfirmed() {
		return nil
	}
	return fmt.Errorf("%s.%s changes data on the site (sideEffects %q) and %s is an imported automation that hasn't been confirmed for live runs yet — run `monoagentcli automation trust %s --live` or confirm it in Connections",
		id, actionType, def.SideEffects, id, id)
}
