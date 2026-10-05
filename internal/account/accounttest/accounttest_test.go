package accounttest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Advance(time.Second)
				_ = c.Now()
			}
		}()
	}
	wg.Wait()
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
