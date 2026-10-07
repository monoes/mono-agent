//go:build !devaccount

package account_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// A build without the devaccount tag must never trust the development key: its
// seed is in this repository, so trusting it would let anyone mint a session.
func TestDefaultBuildDoesNotTrustTheDevelopmentKey(t *testing.T) {
	pub, _ := accounttest.DevKeyPair()
	for _, k := range account.TrustedKeys() {
		if k.KID == accounttest.DevKID || k.Public.Equal(pub) {
			t.Fatalf("a build without the devaccount tag trusts the development key %q", k.KID)
		}
	}
}
