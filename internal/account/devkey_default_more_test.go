//go:build !devaccount

package account_test

import (
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// The end-to-end form of acceptance 5. The development key's seed is in this
// repository, so anyone can mint a token as monoes.me would write it, signed
// with that key; a build without the devaccount tag must refuse it, and for its
// key. TestDefaultBuildDoesNotTrustTheDevelopmentKey reads the list of trusted
// keys; this one goes through Verify, so a path to the key that skips the list
// cannot hide.
func TestDefaultBuildRefusesATokenSignedWithTheDevelopmentKey(t *testing.T) {
	pub, priv := accounttest.DevKeyPair()
	// Built by hand: accounttest.New would make the account package trust its own
	// throwaway key, and this test must not trust anything.
	f := &accounttest.Fixture{
		Private: priv,
		Key:     account.Key{KID: accounttest.DevKID, Public: pub},
		Clock:   accounttest.NewClock(accounttest.DefaultNow),
	}
	token := f.Token(accounttest.TokenOptions{})

	_, err := account.Verify(token, f.Clock.Now())
	if err == nil {
		t.Fatal("a build without the devaccount tag accepted a token signed with the development key")
	}
	var verr *account.VerifyError
	if !errors.As(err, &verr) || verr.Reason != account.ReasonKeyUnknown {
		t.Fatalf("a token signed with the development key was refused, but not as key_unknown: %v", err)
	}

	// Verify checks the key before the signature and the claims, so the refusal
	// above would be the same for a token this test built badly. The control: the
	// same token verifies once its key is trusted, so the key alone was refused.
	t.Run("control: the token verifies once its key is trusted", func(t *testing.T) {
		account.SetTrustedKeysForTest(t, []account.Key{f.Key})
		if _, err := account.Verify(token, f.Clock.Now()); err != nil {
			t.Fatalf("the token does not verify even with its key trusted: %v", err)
		}
	})
}
