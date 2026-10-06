package account_test

import (
	"context"
	"crypto/ed25519"
	"slices"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

func trustedKIDs() []string {
	var out []string
	for _, k := range account.TrustedKeys() {
		out = append(out, k.KID)
	}
	return out
}

// Each seam puts back what was there before it, not the default: a gate-site test
// that sets one, whose subtests each set it again (every subtest installs a fixture),
// must find the outer value again when a subtest ends. A seam that restores the
// default would pass every test that sets it once, since the value before the first
// is the default, and would quietly switch the outer test's setting off, strictness
// included, which is the setting a test of the refusal depends on.
func TestEverySeamPutsBackThePreviousValueNotTheDefault(t *testing.T) {
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	outer, inner := &recTB{TB: t}, &recTB{TB: t}
	t.Cleanup(outer.end) // backstops, in case a check below ends the test early
	t.Cleanup(inner.end)

	t.Run("SetTrustedKeysForTest", func(t *testing.T) {
		before := trustedKIDs()
		account.SetTrustedKeysForTest(outer, []account.Key{{KID: "outer", Public: pub}})
		account.SetTrustedKeysForTest(inner, []account.Key{{KID: "inner", Public: pub}})
		inner.end()
		if got := trustedKIDs(); !slices.Equal(got, []string{"outer"}) {
			t.Fatalf("after the inner seam ended the trusted keys are %v, want the outer one", got)
		}
		outer.end()
		if got := trustedKIDs(); !slices.Equal(got, before) {
			t.Fatalf("after both ended the trusted keys are %v, want what they were: %v", got, before)
		}
	})

	t.Run("SetEnforceFromForTest", func(t *testing.T) {
		before := account.EnforceDate()
		first, second := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
		account.SetEnforceFromForTest(outer, first)
		account.SetEnforceFromForTest(inner, second)
		inner.end()
		if got := account.EnforceDate(); !got.Equal(first) {
			t.Fatalf("after the inner seam ended the enforcement date is %v, want the outer one, %v", got, first)
		}
		outer.end()
		if got := account.EnforceDate(); !got.Equal(before) {
			t.Fatalf("after both ended the enforcement date is %v, want what it was: %v", got, before)
		}
	})

	t.Run("StrictForTest", func(t *testing.T) {
		// Strictness is what Require tells with no guard installed and the gate enforced.
		account.InstallForTest(t, nil)
		account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
		ctx := context.Background()
		account.StrictForTest(outer)
		account.StrictForTest(inner)
		inner.end()
		if err := account.Require(ctx); !account.IsLoginRequired(err) {
			t.Fatalf("after the inner seam ended Require = %v, want the outer strictness still on", err)
		}
		outer.end()
		if err := account.Require(ctx); err != nil {
			t.Fatalf("after both ended Require = %v, want nil: a test binary that did not ask for strictness is let through", err)
		}
	})
}
