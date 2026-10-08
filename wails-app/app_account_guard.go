// wails-app/app_account_guard.go
//
// The app's own process judges nothing today: it calls none of the functions the
// account gate judges (monomind.Exec, the workflow engine, the action executor;
// TestDesktopGoSideCallsNoGatedFunction pins that), and runs its work as
// `monoagentcli` subprocesses, which the CLI gate judges. It still installs a
// guard, because once the gate is enforced account.Require fails closed in a
// process that has none, so a layer-2 call added here later would refuse a
// signed-in user. The guard has no Refresher and its sealer never opens the key
// store (a keychain prompt from the desktop is issue #54); refreshing is the
// CLI's job.
package main

import (
	"sync"

	"github.com/monoes/mono-agent/internal/account"
)

// readOnlySealer is the guard's sealer: the refresh token is never read here.
type readOnlySealer struct{}

func (readOnlySealer) Seal([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }
func (readOnlySealer) Open([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }

var accountGuard struct {
	mu sync.Mutex
	g  *account.Guard
}

// startAccountGuard installs the process guard over the machine's session
// store. A store with no session creates no file.
func (a *App) startAccountGuard() {
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore("", readOnlySealer{})})
	accountGuard.mu.Lock()
	prev := accountGuard.g
	accountGuard.g = g
	accountGuard.mu.Unlock()
	account.Install(g)
	if prev != nil {
		prev.Close()
	}
}

// stopAccountGuard closes the guard on shutdown. With no Refresher there is
// nothing for Close to stop; it is housekeeping, and a closed guard still answers.
func (a *App) stopAccountGuard() {
	accountGuard.mu.Lock()
	g := accountGuard.g
	accountGuard.g = nil
	accountGuard.mu.Unlock()
	if g != nil {
		g.Close()
	}
}
