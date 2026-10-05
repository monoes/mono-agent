package account_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// An Ed25519 signature is 86 base64url characters: the last one carries 2 data
// bits and 4 padding bits that a canonical encoding leaves zero. A token whose
// signature differs only there decodes to the same bytes under a lenient
// decoder, so it is a second spelling of the same token and must be refused.
func TestVerifyRefusesNonCanonicalTrailingBitsInTheSignature(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	good := f.Token(accounttest.TokenOptions{})
	enc := base64.RawURLEncoding
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, good[len(good)-1])
	if last < 0 || last&0x0f != 0 {
		t.Fatalf("a canonical 64-byte signature ends in a character whose low 4 bits are zero, got index %d", last)
	}
	if _, err := account.Verify(good, now); err != nil {
		t.Fatalf("the canonical token must verify: %v", err)
	}
	canonical, err := enc.DecodeString(strings.Split(good, ".")[2])
	if err != nil {
		t.Fatal(err)
	}
	for bits := 1; bits < 16; bits++ {
		variant := good[:len(good)-1] + string(alphabet[last|bits])
		// The premise: a lenient decoder reads the variant as the same signature.
		if lenient, err := enc.DecodeString(strings.Split(variant, ".")[2]); err != nil || !bytes.Equal(lenient, canonical) {
			t.Fatalf("variant %d does not decode to the same signature under the lenient decoder (err %v)", bits, err)
		}
		if _, err := account.Verify(variant, now); reasonOf(err) != account.ReasonInvalid {
			t.Errorf("a signature with trailing bits %#x: reason %q, want invalid", bits, reasonOf(err))
		}
	}
}

// S and S+L are the same scalar modulo the group order L. RFC 8032 section
// 5.1.7 requires S < L and the standard library enforces it. A verifier that
// reduced S instead would accept a second signature for the same message.
func TestVerifyRefusesANonCanonicalSignatureScalar(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	good := f.Token(accounttest.TokenOptions{})
	parts := strings.Split(good, ".")
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		t.Fatalf("decoding the signature: %v", err)
	}
	// L = 2^252 + 27742317777372353535851937790883648493
	order := new(big.Int).Lsh(big.NewInt(1), 252)
	delta, _ := new(big.Int).SetString("27742317777372353535851937790883648493", 10)
	order.Add(order, delta)

	reverse := func(b []byte) []byte { // the scalar is little-endian
		out := make([]byte, len(b))
		for i := range b {
			out[len(b)-1-i] = b[i]
		}
		return out
	}
	s := new(big.Int).SetBytes(reverse(sig[32:]))
	if s.Cmp(order) >= 0 {
		t.Fatal("a signature made by ed25519.Sign has S < L")
	}
	s.Add(s, order) // below 2^254: it still fits in 32 bytes
	forged := append(append([]byte{}, sig[:32]...), reverse(s.FillBytes(make([]byte, 32)))...)

	if _, err := account.Verify(good, now); err != nil {
		t.Fatalf("the canonical token must verify: %v", err)
	}
	if _, err := account.Verify(parts[0]+"."+parts[1]+"."+enc.EncodeToString(forged), now); reasonOf(err) != account.ReasonInvalid {
		t.Fatalf("a signature with the scalar S+L: reason %q, want invalid", reasonOf(err))
	}
}

// maxTokenBytes of verify.go, written out. The test pins the value on purpose:
// a change to the limit has to be a change to this test as well.
const pinnedMaxTokenBytes = 8 << 10

// signedTokenOfLength returns a token the fixture key really signed, with
// exactly n bytes: a claim is padded, and a header member too when padding the
// claim alone cannot reach n (an unpadded base64url string never has a length
// that is 1 modulo 4).
func signedTokenOfLength(t *testing.T, f *accounttest.Fixture, now time.Time, n int) string {
	t.Helper()
	enc := base64.RawURLEncoding
	build := func(headerPad, claimPad int) (header, claims map[string]any, size int) {
		header = map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
		if headerPad > 0 {
			header["x"] = strings.Repeat("h", headerPad)
		}
		claims = goodClaims(now)
		claims["pad"] = strings.Repeat("c", claimPad)
		h, err := json.Marshal(header)
		if err != nil {
			t.Fatal(err)
		}
		c, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		return header, claims, enc.EncodedLen(len(h)) + 1 + enc.EncodedLen(len(c)) + 1 + enc.EncodedLen(ed25519.SignatureSize)
	}
	for headerPad := 0; headerPad < 4; headerPad++ {
		// The size only grows with the padding: take the smallest claim padding that reaches n.
		claimPad := sort.Search(n, func(p int) bool { _, _, size := build(headerPad, p); return size >= n })
		if header, claims, size := build(headerPad, claimPad); size == n {
			tok := f.Sign(header, claims)
			if len(tok) != n {
				t.Fatalf("built a token of %d bytes, wanted %d", len(tok), n)
			}
			return tok
		}
	}
	t.Fatalf("no padding of the claims and the header gives a token of %d bytes", n)
	return ""
}

// Both tokens are validly signed and differ in length by one byte, so only the
// size limit can tell them apart.
func TestVerifyEnforcesTheTokenSizeLimitExactly(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	atLimit := signedTokenOfLength(t, f, now, pinnedMaxTokenBytes)
	if _, err := account.Verify(atLimit, now); err != nil {
		t.Fatalf("a validly signed token of exactly %d bytes: %v", pinnedMaxTokenBytes, err)
	}
	over := signedTokenOfLength(t, f, now, pinnedMaxTokenBytes+1)
	_, err := account.Verify(over, now)
	if reasonOf(err) != account.ReasonInvalid || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a validly signed token of %d bytes: %v, want invalid and too large", pinnedMaxTokenBytes+1, err)
	}
}

// numericDate accepts 0 through 1e11 seconds and nothing else.
func TestVerifyNumericDateBounds(t *testing.T) {
	f := accounttest.New(t)
	header := map[string]any{"alg": "EdDSA", "kid": f.Key.KID}
	const limit = int64(1e11)
	atLimit := time.Unix(limit, 0)
	cases := []struct {
		name     string
		iat, exp any
		now      time.Time
		want     account.Reason // "" means Verify returns a receipt
	}{
		{"iat and exp both negative with a one-hour life", int64(-7200), int64(-3600), f.Clock.Now(), account.ReasonInvalid},
		{"exp exactly at the upper limit", limit - 3600, limit, atLimit, ""},
		{"exp a second over the upper limit", limit - 3599, limit + 1, atLimit, account.ReasonInvalid},
		{"iat over the upper limit", limit + 1, limit + 3601, atLimit, account.ReasonInvalid},
		{"2e11", int64(2e11) - 3600, int64(2e11), f.Clock.Now(), account.ReasonInvalid},
		{"iat too large for a float64", json.RawMessage("1e400"), limit, f.Clock.Now(), account.ReasonInvalid},
		{"exp too large for a float64", limit - 3600, json.RawMessage("1e400"), f.Clock.Now(), account.ReasonInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			claims := goodClaims(c.now)
			claims["iat"], claims["exp"] = c.iat, c.exp
			r, err := account.Verify(f.Sign(header, claims), c.now)
			if got := reasonOf(err); got != c.want {
				t.Fatalf("reason = %q (%v), want %q", got, err, c.want)
			}
			if c.want == "" && (!r.IssuedAt.Equal(time.Unix(limit-3600, 0)) || !r.ExpiresAt.Equal(atLimit)) {
				t.Fatalf("receipt times = %v / %v", r.IssuedAt, r.ExpiresAt)
			}
		})
	}
}
