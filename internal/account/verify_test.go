package account_test

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// reasonOf returns the reason of a *VerifyError, or "" for a nil error and
// "other" for anything else.
func reasonOf(err error) account.Reason {
	if err == nil {
		return ""
	}
	var ve *account.VerifyError
	if !errors.As(err, &ve) {
		return "other"
	}
	return ve.Reason
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": account.Issuer, "aud": account.Audience, "azp": account.ClientID,
		"sub": "user-1", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	r, err := account.Verify(f.Token(accounttest.TokenOptions{Sub: "user-7", Plan: "pro"}), now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Sub != "user-7" || r.Plan != "pro" || r.KID != f.Key.KID || !r.IssuedAt.Equal(now) || !r.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("receipt = %+v", r)
	}
	if r, err := account.Verify(f.Token(accounttest.TokenOptions{}), now); err != nil || r.Plan != "free" {
		t.Fatalf("a token without plan: receipt %+v, err %v, want plan free", r, err)
	}
}

func TestVerifyKeepsAnExpiredTokenForTheGrace(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	tok := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Hour)})
	r, err := account.Verify(tok, now)
	if err != nil || !r.ExpiresAt.Before(now) {
		t.Fatalf("Verify of an expired token: %+v, %v; it must return the receipt, the grace needs it", r, err)
	}
}

func TestVerifyRejectsOneWrongClaimAtATime(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	set := func(k string, v any) func(map[string]any) { return func(m map[string]any) { m[k] = v } }
	del := func(k string) func(map[string]any) { return func(m map[string]any) { delete(m, k) } }
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong issuer", set("iss", "https://evil.example/api/auth")},
		{"issuer missing", del("iss")},
		{"issuer of the wrong type", set("iss", 7)},
		{"wrong audience", set("aud", "https://monoes.me/api/other")},
		{"audience array without ours", set("aud", []string{"a", "b"})},
		{"audience missing", del("aud")},
		{"audience of the wrong type", set("aud", 5)},
		{"audience array with a non-string", set("aud", []any{account.Audience, 5})},
		{"wrong client", set("azp", "someone-else")},
		{"client missing", del("azp")},
		{"azp ours but client_id not", set("client_id", "someone-else")},
		{"sub missing", del("sub")},
		{"sub empty", set("sub", "")},
		{"sub of the wrong type", set("sub", 12)},
		{"iat missing", del("iat")},
		{"iat a string", set("iat", "1")},
		{"iat null", set("iat", nil)},
		{"iat negative", set("iat", -1)},
		{"exp missing", del("exp")},
		{"exp before iat", set("exp", now.Add(-time.Hour).Unix())},
		{"exp equal to iat", set("exp", now.Unix())},
		{"lifetime a second over 24h", set("exp", now.Add(account.MaxTokenLife+time.Second).Unix())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := goodClaims(now)
			c.mutate(claims)
			_, err := account.Verify(f.Sign(header, claims), now)
			if got := reasonOf(err); got != account.ReasonInvalid {
				t.Fatalf("reason = %q, want invalid", got)
			}
		})
	}
}

func TestVerifyAcceptedClaimShapes(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"audience array that contains ours", func(m map[string]any) { m["aud"] = []string{"other", account.Audience} }},
		{"client_id instead of azp", func(m map[string]any) { delete(m, "azp"); m["client_id"] = account.ClientID }},
		{"azp and client_id both ours", func(m map[string]any) { m["client_id"] = account.ClientID }},
		{"lifetime of exactly 24h", func(m map[string]any) { m["exp"] = now.Add(account.MaxTokenLife).Unix() }},
		{"fractional NumericDate", func(m map[string]any) { m["iat"] = float64(now.Unix()) + 0.5 }},
		{"plan of the wrong type is free", func(m map[string]any) { m["plan"] = 3 }},
		{"unknown extra claims", func(m map[string]any) { m["scope"] = "openid library:read"; m["jti"] = "x" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := goodClaims(now)
			c.mutate(claims)
			if _, err := account.Verify(f.Sign(header, claims), now); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestVerifyClockSkew(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	if _, err := account.Verify(f.Token(accounttest.TokenOptions{IssuedAt: now.Add(account.ClockSkew)}), now); err != nil {
		t.Fatalf("iat exactly %v ahead must pass: %v", account.ClockSkew, err)
	}
	_, err := account.Verify(f.Token(accounttest.TokenOptions{IssuedAt: now.Add(account.ClockSkew + time.Second)}), now)
	if got := reasonOf(err); got != account.ReasonClockSkew {
		t.Fatalf("reason = %q, want clock_skew", got)
	}
}

func TestVerifyAlgorithmConfusion(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	enc := base64.RawURLEncoding
	signed := func(header map[string]any) string {
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(goodClaims(now))
		return enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	}
	junk := enc.EncodeToString(make([]byte, ed25519.SignatureSize))
	cases := map[string]string{
		"HS256 signed with the public key as secret": hs256(f.Key.Public, signed(map[string]any{"alg": "HS256", "kid": f.Key.KID})),
		"alg none with no signature":                 signed(map[string]any{"alg": "none", "kid": f.Key.KID}) + ".",
		"alg none with a junk signature":             signed(map[string]any{"alg": "none", "kid": f.Key.KID}) + "." + junk,
		"a real EdDSA signature under alg HS256":     f.Token(accounttest.TokenOptions{Alg: "HS256"}),
		"a real EdDSA signature under alg ES256":     f.Token(accounttest.TokenOptions{Alg: "ES256"}),
		"alg in the wrong case":                      f.Token(accounttest.TokenOptions{Alg: "eddsa"}),
		"alg missing":                                f.Sign(map[string]any{"kid": f.Key.KID}, goodClaims(now)),
		"alg of the wrong type":                      f.Sign(map[string]any{"alg": []string{"EdDSA"}, "kid": f.Key.KID}, goodClaims(now)),
		"critical header":                            f.Sign(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "crit": []string{"exp"}}, goodClaims(now)),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
				t.Fatalf("reason = %q (%v), want invalid", reasonOf(err), err)
			}
		})
	}
}

// hs256 signs signedPart the way an alg-confusion attacker would: an HMAC
// keyed with the public key, which the attacker can read.
func hs256(secret []byte, signedPart string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signedPart))
	return signedPart + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyKeys(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	attackerPub, attackerPriv, _ := ed25519.GenerateKey(rand.Reader)
	enc := base64.RawURLEncoding
	forge := func(header map[string]any) string {
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(goodClaims(now))
		signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
		return signed + "." + enc.EncodeToString(ed25519.Sign(attackerPriv, []byte(signed)))
	}
	jwk := map[string]any{"kty": "OKP", "crv": "Ed25519", "x": enc.EncodeToString(attackerPub)}

	t.Run("a kid that is not pinned", func(t *testing.T) {
		_, err := account.Verify(f.Token(accounttest.TokenOptions{KID: "rotated-away"}), now)
		if reasonOf(err) != account.ReasonKeyUnknown {
			t.Fatalf("reason = %q, want key_unknown", reasonOf(err))
		}
	})
	t.Run("no kid", func(t *testing.T) {
		_, err := account.Verify(f.Sign(map[string]any{"alg": "EdDSA"}, goodClaims(now)), now)
		if reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
	t.Run("signed by another key under the pinned kid", func(t *testing.T) {
		_, err := account.Verify(forge(map[string]any{"alg": "EdDSA", "kid": f.Key.KID}), now)
		if reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
	t.Run("jwk, jku and x5u headers are ignored, not trusted", func(t *testing.T) {
		tok := forge(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "jwk": jwk, "jku": "https://evil.example/jwks", "x5u": "https://evil.example/cert"})
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("a token signed by the key its own jwk header names was not refused: %v", err)
		}
		own := f.Sign(map[string]any{"alg": "EdDSA", "kid": f.Key.KID, "jwk": jwk, "jku": "https://evil.example/jwks"}, goodClaims(now))
		if _, err := account.Verify(own, now); err != nil {
			t.Fatalf("the extra headers must be ignored, not refused: %v", err)
		}
	})
	t.Run("an empty trusted set", func(t *testing.T) {
		tok := f.Token(accounttest.TokenOptions{})
		account.SetTrustedKeysForTest(t, nil)
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonKeyUnknown {
			t.Fatalf("reason = %q, want key_unknown", reasonOf(err))
		}
	})
	t.Run("a malformed pinned key does not panic", func(t *testing.T) {
		tok := f.Token(accounttest.TokenOptions{})
		account.SetTrustedKeysForTest(t, []account.Key{{KID: f.Key.KID, Public: f.Key.Public[:10]}})
		if _, err := account.Verify(tok, now); reasonOf(err) != account.ReasonInvalid {
			t.Fatalf("reason = %q, want invalid", reasonOf(err))
		}
	})
}

// The pinned set holds the current signing key and the next one (spec §4.7), so
// a rotation never strands a client: either key verifies, and a later release
// that drops the old key stops trusting its tokens.
func TestVerifyDuringAKeyRotation(t *testing.T) {
	current := accounttest.New(t)
	next := accounttest.New(t)
	account.SetTrustedKeysForTest(t, []account.Key{current.Key, next.Key})
	now := current.Clock.Now()
	for name, f := range map[string]*accounttest.Fixture{"the current key": current, "the next key": next} {
		r, err := account.Verify(f.Token(accounttest.TokenOptions{}), now)
		if err != nil || r.KID != f.Key.KID {
			t.Errorf("a token signed by %s: receipt %+v, err %v", name, r, err)
		}
	}
	account.SetTrustedKeysForTest(t, []account.Key{next.Key}) // a later release drops the old key
	if _, err := account.Verify(current.Token(accounttest.TokenOptions{}), now); reasonOf(err) != account.ReasonKeyUnknown {
		t.Fatalf("a token signed by the dropped key: reason %q, want key_unknown", reasonOf(err))
	}
	if _, err := account.Verify(next.Token(accounttest.TokenOptions{}), now); err != nil {
		t.Fatalf("a token signed by the remaining key: %v", err)
	}
}

func TestVerifyTruncatedAndGarbageInput(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	good := f.Token(accounttest.TokenOptions{})
	enc := base64.RawURLEncoding
	object := enc.EncodeToString([]byte(`{"alg":"EdDSA","kid":"` + f.Key.KID + `"}`))
	sig := enc.EncodeToString(make([]byte, ed25519.SignatureSize))
	// signedPayload has a real signature, so Verify gets past the signature
	// check: the payload p is refused by what comes after it (the payload
	// parser or, for null, the claim checks), never by the signature.
	signedPayload := func(p string) string {
		signed := object + "." + enc.EncodeToString([]byte(p))
		return signed + "." + enc.EncodeToString(ed25519.Sign(f.Private, []byte(signed)))
	}
	inputs := map[string]string{
		"empty":                    "",
		"one dot":                  "a.b",
		"four segments":            good + ".x",
		"empty segments":           "..",
		"empty signature":          strings.Join(strings.Split(good, ".")[:2], ".") + ".",
		"empty payload":            strings.Split(good, ".")[0] + ".." + strings.Split(good, ".")[2],
		"padding in a segment":     strings.Replace(good, ".", "=.", 1),
		"a space":                  good[:20] + " " + good[20:],
		"a newline at the end":     good + "\n",
		"a bad character":          good[:20] + "!" + good[21:],
		"standard base64 alphabet": good + "+/",
		"header not JSON":          enc.EncodeToString([]byte("not json")) + "." + object + "." + sig,
		"payload not JSON":         signedPayload("not json"),
		"payload a JSON array":     signedPayload(`[1,2]`),
		"payload JSON null":        signedPayload(`null`),
		"short signature":          object + "." + object + "." + enc.EncodeToString([]byte("short")),
		"far too large":            strings.Repeat("a", 9<<10),
	}
	for i := 10; i < len(good); i += 37 {
		inputs[fmt.Sprintf("truncated at %d", i)] = good[:i]
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			_, err := account.Verify(in, now)
			if reasonOf(err) != account.ReasonInvalid {
				t.Fatalf("reason = %q (%v), want invalid", reasonOf(err), err)
			}
			if len(in) > 12 && strings.Contains(err.Error(), in) {
				t.Fatal("the error message carries the token")
			}
		})
	}
}

func TestVerifyErrorMessagesNeverCarryTheToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	for name, tok := range map[string]string{
		"key_unknown": f.Token(accounttest.TokenOptions{KID: "rotated-away"}),
		"clock_skew":  f.Token(accounttest.TokenOptions{IssuedAt: now.Add(time.Hour)}),
		"invalid":     f.Token(accounttest.TokenOptions{Audience: []string{"x"}}),
	} {
		_, err := account.Verify(tok, now)
		if err == nil || strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), strings.Split(tok, ".")[2]) {
			t.Errorf("%s: the error is missing or carries token material", name)
		}
	}
}
