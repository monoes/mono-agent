package accounttest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

func TestClock(t *testing.T) {
	c := NewClock(DefaultNow)
	if !c.Now().Equal(DefaultNow) {
		t.Fatalf("Now = %v, want %v", c.Now(), DefaultNow)
	}
	c.Advance(90 * time.Minute)
	if !c.Now().Equal(DefaultNow.Add(90 * time.Minute)) {
		t.Fatalf("after Advance, Now = %v", c.Now())
	}
	c.Set(DefaultNow.Add(-time.Hour))
	if !c.Now().Equal(DefaultNow.Add(-time.Hour)) {
		t.Fatalf("after Set back, Now = %v", c.Now())
	}
	var wg sync.WaitGroup
	start := make(chan struct{}) // released together, so the goroutines overlap
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				c.Advance(time.Second)
				_ = c.Now()
			}
		}()
	}
	close(start)
	wg.Wait()
	// Every Advance must land: a read-then-Set Advance is free of data races yet loses updates.
	if want := DefaultNow.Add(-time.Hour + 800*time.Second); !c.Now().Equal(want) {
		t.Fatalf("after 800 concurrent Advance(1s) calls, Now = %v, want %v", c.Now(), want)
	}
}

func TestNewTrustsOnlyItsKeyAndEnforces(t *testing.T) {
	keysBefore, dateBefore := len(account.TrustedKeys()), account.EnforceDate()
	var f *Fixture
	t.Run("inside", func(t *testing.T) {
		f = New(t)
		keys := account.TrustedKeys()
		if len(keys) != 1 || keys[0].KID != f.Key.KID {
			t.Fatalf("trusted keys = %+v, want only the fixture key", keys)
		}
		if !account.Enforced(f.Clock.Now(), time.Time{}) {
			t.Fatal("New must leave the gate enforced")
		}
		if _, err := account.Verify(f.Token(TokenOptions{}), f.Clock.Now()); err != nil {
			t.Fatalf("a default token must verify: %v", err)
		}
	})
	if len(account.TrustedKeys()) != keysBefore || !account.EnforceDate().Equal(dateBefore) {
		t.Fatalf("New leaked out of its test: %d keys (was %d), date %v (was %v)", len(account.TrustedKeys()), keysBefore, account.EnforceDate(), dateBefore)
	}
}

func claimsOf(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestTokenDefaultsAndOverrides(t *testing.T) {
	f := New(t)
	def := claimsOf(t, f.Token(TokenOptions{}))
	if def["iss"] != account.Issuer || def["aud"] != account.Audience || def["azp"] != account.ClientID || def["sub"] != "user-1" {
		t.Fatalf("default claims = %v", def)
	}
	if _, has := def["plan"]; has {
		t.Fatal("a token without Plan must carry no plan claim")
	}
	if def["iat"] != float64(f.Clock.Now().Unix()) || def["exp"] != float64(f.Clock.Now().Add(time.Hour).Unix()) {
		t.Fatalf("default times = %v / %v", def["iat"], def["exp"])
	}
	two := claimsOf(t, f.Token(TokenOptions{Audience: []string{"a", account.Audience}, Plan: "pro", Lifetime: 2 * time.Hour}))
	if aud, ok := two["aud"].([]any); !ok || len(aud) != 2 || two["plan"] != "pro" || two["exp"].(float64)-two["iat"].(float64) != 7200 {
		t.Fatalf("overridden claims = %v", two)
	}
}

func TestTokenIssuerClientAndSingleAudienceOptions(t *testing.T) {
	f := New(t)
	claims := claimsOf(t, f.Token(TokenOptions{Issuer: "https://evil.example/api/auth", ClientClaim: "someone-else", Audience: []string{"https://monoes.me/api/other"}}))
	if claims["iss"] != "https://evil.example/api/auth" {
		t.Errorf("the Issuer option was ignored: iss = %v", claims["iss"])
	}
	if claims["azp"] != "someone-else" {
		t.Errorf("the ClientClaim option was ignored: azp = %v", claims["azp"])
	}
	if aud, ok := claims["aud"].(string); !ok || aud != "https://monoes.me/api/other" {
		t.Errorf("a one-element Audience must be written as a string, got %#v", claims["aud"])
	}
}

// A token built from a value that cannot be marshalled would silently lose a
// segment and be refused as "not a compact JWS", so a test of a refusal would
// pass for the wrong reason. Sign fails loudly instead.
func TestSignPanicsOnAValueThatCannotBeMarshalled(t *testing.T) {
	f := New(t)
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	claims := map[string]any{"sub": "x"}
	cases := []struct {
		name           string
		header, claims map[string]any
		part           string
	}{
		{"a channel in the header", map[string]any{"alg": "EdDSA", "x": make(chan int)}, claims, "header"},
		{"infinity in the header", map[string]any{"alg": "EdDSA", "x": math.Inf(1)}, claims, "header"},
		{"a function in the claims", header, map[string]any{"sub": func() {}}, "claims"},
		{"NaN in the claims", header, map[string]any{"iat": math.NaN()}, "claims"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("Sign returned a token for a value that cannot be marshalled")
				}
				err, ok := r.(error)
				if !ok || !strings.Contains(err.Error(), "accounttest: Sign") || !strings.Contains(err.Error(), c.part) || errors.Unwrap(err) == nil {
					t.Fatalf("Sign panicked with %v, want an error that names Sign and the %s and wraps the marshalling error", r, c.part)
				}
			}()
			f.Sign(c.header, c.claims)
		})
	}
}

func TestSignKeepsARealSignatureWhateverTheHeaderSays(t *testing.T) {
	f := New(t)
	tok := f.Sign(map[string]any{"alg": "HS256", "kid": f.Key.KID}, map[string]any{"sub": "x"})
	parts := strings.Split(tok, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(f.Key.Public, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatalf("Sign must always sign with the fixture key (err %v)", err)
	}
}

func TestDevKeyPairIsFixed(t *testing.T) {
	const wantPublic = "b2f5cb7a39788e27efd2b9c4f96873b495b61933f28c9c42904be76cd4328cff"
	pub, priv := DevKeyPair()
	if got := hex.EncodeToString(pub); got != wantPublic {
		t.Fatalf("the development public key changed to %s; keys_devaccount.go (B1b) pins %s", got, wantPublic)
	}
	msg := []byte("dev key check")
	if !ed25519.Verify(pub, msg, ed25519.Sign(priv, msg)) {
		t.Fatal("the development pair does not sign and verify")
	}
	pub2, _ := DevKeyPair()
	if !pub.Equal(pub2) || DevKID != "monoagent-dev-1" {
		t.Fatal("DevKeyPair must return the same pair every time")
	}
}
