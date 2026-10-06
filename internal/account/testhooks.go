package account

import (
	"testing"
	"time"
)

// The hooks below are test seams. Each panics unless the binary is a test
// binary (testing.Testing), marks the test with t.Setenv (testStateEnv), takes
// globalsMu, and restores what it changed when the test ends. A test that uses one
// must not call t.Parallel(): the marker makes the testing package panic if it does.

// testStateEnv is the name of a marker that every seam that changes process-wide
// state sets with t.Setenv, and that nothing reads. The testing package refuses to
// combine t.Setenv with t.Parallel, before or after, so a test that mixes a seam with
// t.Parallel fails at once instead of flaking on test order. A seam sets it right
// after requireTestBinary, which comes first and before which nothing runs, and
// before it touches a global.
const testStateEnv = "MONOAGENT_ACCOUNT_TEST_STATE"

// requireTestBinary panics unless the running binary is a test binary.
func requireTestBinary(name string) { mustBeTestBinary(testing.Testing(), name) }

func mustBeTestBinary(isTest bool, name string) {
	if !isTest {
		panic("account." + name + " is a test seam and may only be used from a test binary")
	}
}

// SetTrustedKeysForTest replaces the whole trusted key set (the pinned keys
// and, under devaccount, the development key) for the rest of the test.
func SetTrustedKeysForTest(t testing.TB, keys []Key) {
	t.Helper()
	requireTestBinary("SetTrustedKeysForTest")
	t.Setenv(testStateEnv, "1")
	globalsMu.Lock()
	prevKeys, prevSet := keysOverride, keysOverridden
	keysOverride, keysOverridden = cloneKeys(keys), true
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		keysOverride, keysOverridden = prevKeys, prevSet
		globalsMu.Unlock()
	})
}

// SetEnforceFromForTest sets the enforcement date for the rest of the test;
// the zero time makes the package dormant.
func SetEnforceFromForTest(t testing.TB, at time.Time) {
	t.Helper()
	requireTestBinary("SetEnforceFromForTest")
	t.Setenv(testStateEnv, "1")
	globalsMu.Lock()
	prev := enforceFrom
	enforceFrom = at
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		enforceFrom = prev
		globalsMu.Unlock()
	})
}

// StrictForTest turns off the test-binary exception of Require for the rest of
// the test: with no guard installed Require then judges the process as not
// logged in, as a release binary does. While the package is dormant that still
// allows everything, so a strict test also calls SetEnforceFromForTest.
func StrictForTest(t testing.TB) {
	t.Helper()
	requireTestBinary("StrictForTest")
	t.Setenv(testStateEnv, "1")
	globalsMu.Lock()
	prev := strict
	strict = true
	globalsMu.Unlock()
	t.Cleanup(func() {
		globalsMu.Lock()
		strict = prev
		globalsMu.Unlock()
	})
}
