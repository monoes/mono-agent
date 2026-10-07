package action

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// TestExecuteDefGate: a locked account is refused with the typed error before
// the first step runs; every other state runs the action.
func TestExecuteDefGate(t *testing.T) {
	for name, c := range map[string]struct {
		set     func(t *testing.T)
		refused bool
	}{
		"strict, no guard installed": {func(t *testing.T) {
			account.InstallForTest(t, nil)
			account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
			account.StrictForTest(t)
		}, true},
		"locked, not logged in": {func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
		"dormant":               {func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
	} {
		t.Run(name, func(t *testing.T) {
			c.set(t)
			ae := newPkgExecutor(nil, nil)
			def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
				{ID: "save", Type: "set_variable", Variable: "saved", Value: "yes", SideEffect: true},
			}}

			res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)

			_, ran := ae.execCtx.GetVariable("saved")
			if c.refused && (!account.IsLoginRequired(err) || res != nil || ran) {
				t.Fatalf("res %v, err %v, first step ran %v: want nil, *account.LoginRequiredError and no step", res, err, ran)
			}
			if !c.refused && (err != nil || !ran) {
				t.Fatalf("an admitted action: err %v, first step ran %v", err, ran)
			}
		})
	}
}
