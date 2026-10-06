package accounttest

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// A fixture changes process-wide state through the seams of the account package (the
// trusted key, the enforcement date, strictness, the installed guard), and those fail
// at once when the test also calls t.Parallel, before or after (see
// TestEveryGlobalStateSeamPanicsWhenMixedWithTParallel in internal/account), so that a
// gate-site test that parallelizes breaks loudly instead of flaking on test order.
func TestFixturesPanicWhenMixedWithTParallel(t *testing.T) {
	fixtures := []struct {
		name string
		use  func(testing.TB)
	}{
		{"Install", func(t testing.TB) { Install(t, SignedIn) }},
		{"InstallWithFixture", func(t testing.TB) { InstallWithFixture(t, Dormant) }},
		{"New", func(t testing.TB) { New(t) }},
	}
	for _, f := range fixtures {
		t.Run(f.name+" then t.Parallel", func(t *testing.T) {
			f.use(t)
			expectParallelConflict(t, "t.Parallel after "+f.name, panicOf(t.Parallel))
		})
	}
	// One parallel subtest, so that a fixture that failed to panic could not race another
	// test, and the state to leave alone is one a default build does not have: a fixture
	// that panics after a write leaves the write in place.
	t.Run("t.Parallel then each fixture", func(t *testing.T) {
		g := Install(t, SignedIn)
		date, keys := account.EnforceDate(), kids(account.TrustedKeys())
		t.Run("in parallel", func(t *testing.T) {
			t.Parallel()
			for _, f := range fixtures {
				expectParallelConflict(t, f.name+" after t.Parallel", panicOf(func() { f.use(t) }))
				if got := account.EnforceDate(); !got.Equal(date) {
					t.Errorf("%s left the enforcement date at %v, want %v", f.name, got, date)
				}
				if got := kids(account.TrustedKeys()); !slices.Equal(got, keys) {
					t.Errorf("%s left the trusted keys %v, want %v", f.name, got, keys)
				}
				if account.Current() != g {
					t.Errorf("%s left the installed guard replaced", f.name)
				}
				if t.Failed() {
					return // the state is polluted now: what the other fixtures would report is noise
				}
			}
		})
	})
}

// panicOf is what fn panics with, or nil.
func panicOf(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

func expectParallelConflict(t *testing.T, what string, v any) {
	t.Helper()
	if msg := fmt.Sprint(v); v == nil || !strings.Contains(msg, "t.Setenv") || !strings.Contains(msg, "t.Parallel") {
		t.Errorf("%s: panicked with %v, want the testing package's refusal to combine t.Setenv with t.Parallel", what, v)
	}
}
