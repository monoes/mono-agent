package account

import (
	"context"
	"testing"
	"time"
)

// installed is the process-wide guard; globalsMu guards it.
var installed *Guard

// Install makes g the process-wide guard that Require and CurrentStatus use.
// Install(nil) removes it. The command's main installs one for every command (B2
// writes that call; nothing outside this package imports account before it).
func Install(g *Guard) {
	globalsMu.Lock()
	installed = g
	globalsMu.Unlock()
}

// Current returns the installed guard, or nil when none is installed.
func Current() *Guard {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	return installed
}

// InstallForTest installs g for the rest of the test and puts the previous
// guard back when it ends; a nil g installs no guard for the rest of the test.
// A test that uses it must not call t.Parallel() (see testStateEnv: it panics).
func InstallForTest(t testing.TB, g *Guard) {
	t.Helper()
	requireTestBinary("InstallForTest")
	t.Setenv(testStateEnv, "1")
	globalsMu.Lock()
	prev := installed
	installed = g
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		installed = prev
		globalsMu.Unlock()
	})
}

// Require is the gate every layer calls. It uses the installed guard. With none
// installed it fails closed: the process is judged as not logged in, so once the
// gate is enforced it returns *LoginRequiredError. Two exceptions: while the
// package is dormant, or before the enforcement date, nothing locks (spec D22),
// so it returns nil; and inside a test binary (testing.Testing) it returns nil
// unless StrictForTest is active, so unit tests that never install a guard keep
// working (spec D24).
func Require(ctx context.Context) error {
	if g := Current(); g != nil {
		return g.Require(ctx)
	}
	return requireNoGuard(testing.Testing(), isStrict(), time.Now())
}

// requireNoGuard is Require for a process with no guard installed. isTest is
// testing.Testing(). It is a parameter so that a test can run what a release
// binary runs, which testing.Testing() makes impossible inside a test binary
// (the split of requireTestBinary and mustBeTestBinary in testhooks.go).
func requireNoGuard(isTest, strictNow bool, now time.Time) error {
	if isTest && !strictNow {
		return nil
	}
	if st := noGuardStatus(now); !st.Allowed() {
		return &LoginRequiredError{Status: st}
	}
	return nil
}

// CurrentStatus is nil-safe: the installed guard's Status, or, with none
// installed, locked(not_logged_in) with Enforced and EnforceFrom filled in.
// The doors and /health use it; a process that must react to a change (the
// daemon) polls it every PollInterval.
func CurrentStatus() Status {
	if g := Current(); g != nil {
		return g.Status()
	}
	return noGuardStatus(time.Now())
}

func noGuardStatus(now time.Time) Status { return judge(nil, nil, nil, now) }
