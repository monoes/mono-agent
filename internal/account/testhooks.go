package account

import (
	"testing"
	"time"
)

// The hooks below are test seams. Each panics unless the binary is a test
// binary (testing.Testing), takes globalsMu, and restores what it changed when
// the test ends. A test that uses one must not call t.Parallel().

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
