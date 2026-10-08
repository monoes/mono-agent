package account_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// attacker is an Ed25519 key that no build pins, and every way a JOSE header can
// hand a verifier that key: embedded in the header (jwk, x5c) or named by URL
// (jku, x5u). The URLs point at a real HTTP server that serves the key and
// counts the requests it gets.
type attacker struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	jwk  map[string]any
	x5c  []string
	jku  string
	x5u  string
	srv  *httptest.Server
	hits atomic.Int32
}

func newAttacker(t *testing.T) *attacker {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the attacker's key: %v", err)
	}
	a := &attacker{pub: pub, priv: priv, jwk: map[string]any{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "attacker.example"},
		NotBefore:    accounttest.DefaultNow.Add(-time.Hour),
		NotAfter:     accounttest.DefaultNow.Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatalf("self-signing the attacker's certificate: %v", err)
	}
	a.x5c = []string{base64.StdEncoding.EncodeToString(der)}
	jwks, err := json.Marshal(map[string]any{"keys": []any{a.jwk}})
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) { a.hits.Add(1); _, _ = w.Write(jwks) })
	mux.HandleFunc("/cert", func(w http.ResponseWriter, _ *http.Request) { a.hits.Add(1); _, _ = w.Write(certPEM) })
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	a.jku, a.x5u = a.srv.URL+"/jwks", a.srv.URL+"/cert"
	return a
}

// serves proves the server really hands out the attacker's key at both URLs, so
// that "no request arrived" means Verify never asked, not that there was
// nothing worth asking for. It leaves the request count at zero.
func (a *attacker) serves(t *testing.T) {
	t.Helper()
	get := func(url string) []byte {
		resp, err := a.srv.Client().Get(url)
		if err != nil {
			t.Fatalf("GET of the attacker's server: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, read error %v", url, resp.StatusCode, err)
		}
		return body
	}
	var jwks struct{ Keys []struct{ X string } }
	if err := json.Unmarshal(get(a.jku), &jwks); err != nil || len(jwks.Keys) != 1 || jwks.Keys[0].X != a.jwk["x"] {
		t.Fatalf("the jku URL does not serve the attacker's JWKS (err %v)", err)
	}
	block, _ := pem.Decode(get(a.x5u))
	if block == nil {
		t.Fatal("the x5u URL does not serve a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("the x5u certificate: %v", err)
	}
	if pub, ok := cert.PublicKey.(ed25519.PublicKey); !ok || !pub.Equal(a.pub) {
		t.Fatal("the x5u certificate does not carry the attacker's key")
	}
	a.hits.Store(0)
}

// sign signs header and claims with the attacker's key.
func (a *attacker) sign(header, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	return signed + "." + enc.EncodeToString(ed25519.Sign(a.priv, []byte(signed)))
}

// The key that verifies a token is one the build pins, chosen by kid, and
// nothing in the token can add to that set (spec D13, the embedded-key class of
// CVE-2018-0114). A token signed by a key that is not pinned must be refused
// whichever way its headers present that key, whatever its kid says, and
// Verify must not fetch the jku or x5u document it names.
func TestVerifyNeverTrustsKeyMaterialCarriedByTheToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	atk := newAttacker(t)
	atk.serves(t)

	carriers := []struct {
		name    string
		members map[string]any
	}{
		{"jwk", map[string]any{"jwk": atk.jwk}},
		{"x5c", map[string]any{"x5c": atk.x5c}},
		{"jku", map[string]any{"jku": atk.jku}},
		{"x5u", map[string]any{"x5u": atk.x5u}},
		{"all four", map[string]any{"jwk": atk.jwk, "x5c": atk.x5c, "jku": atk.jku, "x5u": atk.x5u}},
	}
	header := func(members map[string]any, kid string) map[string]any {
		h := map[string]any{"alg": "EdDSA"}
		maps.Copy(h, members)
		if kid != "" {
			h["kid"] = kid
		}
		return h
	}
	kids := []struct {
		name string
		kid  string
		want account.Reason
	}{
		{"an unpinned kid", "attacker-1", account.ReasonKeyUnknown},
		{"no kid", "", account.ReasonInvalid},
		{"the pinned kid", f.Key.KID, account.ReasonInvalid},
	}
	for _, c := range carriers {
		for _, k := range kids {
			t.Run("signed by the attacker, "+c.name+" header, "+k.name, func(t *testing.T) {
				_, err := account.Verify(atk.sign(header(c.members, k.kid), goodClaims(now)), now)
				if got := reasonOf(err); got != k.want {
					t.Fatalf("reason = %q, want %q", got, k.want)
				}
			})
		}
		t.Run("signed by the pinned key, "+c.name+" header", func(t *testing.T) {
			if _, err := account.Verify(f.Sign(header(c.members, f.Key.KID), goodClaims(now)), now); err != nil {
				t.Fatalf("the extra headers must be ignored, not refused: %v", err)
			}
		})
	}
	if n := atk.hits.Load(); n != 0 {
		t.Fatalf("Verify sent %d HTTP requests to the jku and x5u URLs of the tokens it was given; it must never fetch key material", n)
	}
}
