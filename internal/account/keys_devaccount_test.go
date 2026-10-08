//go:build devaccount

package account_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// A devaccount build trusts the development key, and a token signed with its
// private half verifies end to end.
func TestDevAccountBuildTrustsTheDevelopmentKey(t *testing.T) {
	pub, priv := accounttest.DevKeyPair()
	trusted := false
	for _, k := range account.TrustedKeys() {
		if k.KID == accounttest.DevKID && k.Public.Equal(pub) {
			trusted = true
		}
	}
	if !trusted {
		t.Fatal("a devaccount build does not trust the development key")
	}
	enc := base64.RawURLEncoding
	now := time.Now()
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": accounttest.DevKID})
	claims, _ := json.Marshal(map[string]any{
		"iss": account.Issuer, "aud": account.Audience, "azp": account.ClientID,
		"sub": "dev-user", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	signed := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	issued := signed + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(signed)))
	if _, err := account.Verify(issued, now); err != nil {
		t.Fatalf("a token signed with the development key does not verify: %v", err)
	}
}

// TrustedKeys hands out a copy, so what a caller does to the keys it was given, the slice and the
// bytes of every public key in it, never changes what the next call trusts. Only a devaccount build
// has an extra key to merge into the pinned set, which is why this is pinned here and not in the
// default build's tests.
func TestDevaccountTrustedKeysReturnsACopy(t *testing.T) {
	pub, _ := accounttest.DevKeyPair()
	holdsDevKey := func(keys []account.Key) bool {
		for _, k := range keys {
			if k.KID == accounttest.DevKID && k.Public.Equal(pub) {
				return true
			}
		}
		return false
	}
	keys := account.TrustedKeys()
	if !holdsDevKey(keys) {
		t.Fatal("a devaccount build does not trust the development key")
	}
	for i := range keys {
		keys[i].KID = "scribbled"
		for j := range keys[i].Public {
			keys[i].Public[j] ^= 0xff
		}
	}
	if !holdsDevKey(account.TrustedKeys()) {
		t.Fatal("changing the keys TrustedKeys returned changed the keys it trusts next")
	}
}

// With no test hook at all, a devaccount binary verifies what libraryfake signs.
func TestDevaccountVerifiesTheFakesTokens(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	ts, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := account.Verify(ts.AccessToken, time.Now()); err != nil || rec.Sub != "u-ada" {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDevaccountHostHonorsMonoesBaseURL(t *testing.T) {
	t.Setenv("MONOES_BASE_URL", " http://127.0.0.1:3100/ ")
	if got := account.Host(); got != "http://127.0.0.1:3100" {
		t.Fatalf("Host = %q", got)
	}
	t.Setenv("MONOES_BASE_URL", "")
	if got := account.Host(); got != account.HostURL {
		t.Fatalf("Host = %q", got)
	}
}
