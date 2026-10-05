package accounttest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// Fixture is a throwaway signing key that the account package trusts for the
// rest of the test, and a clock to judge tokens by.
type Fixture struct {
	Private ed25519.PrivateKey
	Key     account.Key
	Clock   *Clock
}

// TokenOptions shapes a token. Every zero field takes the value of a valid
// token, so a test sets only the one thing it wants wrong.
type TokenOptions struct {
	Sub, Plan, Issuer, ClientClaim string
	Audience                       []string      // zero: [account.Audience]; one element is written as a string
	IssuedAt                       time.Time     // zero: Clock.Now()
	Lifetime                       time.Duration // zero: 1 hour
	KID                            string        // zero: the fixture key's kid
	Alg                            string        // zero: EdDSA
}

// New generates a throwaway key, makes the account package trust exactly that
// key and sets the enforcement date a day before the clock's start, so the gate
// is enforced. Both are restored when the test ends. A test that uses it must
// not call t.Parallel().
func New(t testing.TB) *Fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("accounttest: generating a key: %v", err)
	}
	// The kid is derived from the key, so two fixtures in one test never share a kid.
	f := &Fixture{Private: priv, Key: account.Key{KID: "accounttest-" + hex.EncodeToString(pub[:4]), Public: pub}, Clock: NewClock(DefaultNow)}
	account.SetTrustedKeysForTest(t, []account.Key{f.Key})
	account.SetEnforceFromForTest(t, f.Clock.Now().Add(-24*time.Hour))
	return f
}

// Token signs a JWT valid at Clock.Now() unless o says otherwise.
func (f *Fixture) Token(o TokenOptions) string {
	iat := o.IssuedAt
	if iat.IsZero() {
		iat = f.Clock.Now()
	}
	life := o.Lifetime
	if life == 0 {
		life = time.Hour
	}
	var aud any = o.Audience
	switch len(o.Audience) {
	case 0:
		aud = account.Audience
	case 1:
		aud = o.Audience[0]
	}
	claims := map[string]any{
		"iss": orDefault(o.Issuer, account.Issuer),
		"aud": aud,
		"azp": orDefault(o.ClientClaim, account.ClientID),
		"sub": orDefault(o.Sub, "user-1"),
		"iat": iat.Unix(),
		"exp": iat.Add(life).Unix(),
	}
	if o.Plan != "" {
		claims["plan"] = o.Plan
	}
	header := map[string]any{"alg": orDefault(o.Alg, "EdDSA"), "kid": orDefault(o.KID, f.Key.KID), "typ": "JWT"}
	return f.Sign(header, claims)
}

// Sign signs any header and claims with the fixture key, whatever alg the
// header names. It is how a test builds a token that is wrong in a way
// TokenOptions cannot say (a missing claim, a claim of the wrong type, extra
// headers). It panics when the header or the claims cannot be marshalled to
// JSON, so that no test builds a token that silently lacks a segment.
func (f *Fixture) Sign(header, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, err := json.Marshal(header)
	if err != nil {
		panic(fmt.Errorf("accounttest: Sign: the header cannot be marshalled: %w", err))
	}
	c, err := json.Marshal(claims)
	if err != nil {
		panic(fmt.Errorf("accounttest: Sign: the claims cannot be marshalled: %w", err))
	}
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	return signed + "." + enc.EncodeToString(ed25519.Sign(f.Private, []byte(signed)))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
