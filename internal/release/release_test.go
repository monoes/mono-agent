package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rig struct {
	srv      *httptest.Server
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	asset    []byte
	manifest []byte
	sig      []byte
	client   *Client
	mux      *http.ServeMux
}

func sign(priv ed25519.PrivateKey, pub ed25519.PublicKey, m []byte) []byte {
	return []byte(KeyID(pub) + " " + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m)) + "\n")
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{pub: pub, priv: priv, asset: []byte("binary-bytes-v2"), mux: http.NewServeMux()}
	r.srv = httptest.NewTLSServer(r.mux)
	t.Cleanup(r.srv.Close)
	sum := sha256.Sum256(r.asset)
	m := Manifest{Schema: 1, Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z", NotesURL: "https://monoes.me/n",
		Assets: []Asset{{OS: "linux", Arch: "amd64", Name: "monoagentcli-linux-amd64", URL: r.srv.URL + "/asset",
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(r.asset)), Kind: "cli"}}}
	r.manifest, _ = json.Marshal(m)
	r.sig = sign(priv, pub, r.manifest)
	r.mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.manifest) })
	r.mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.sig) })
	r.mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.asset) })
	r.client = &Client{
		HTTP: r.srv.Client(), Keys: []Key{{ID: KeyID(pub), Public: pub}},
		ManifestURLs: []string{r.srv.URL + "/manifest.json"},
		Hosts:        []string{"127.0.0.1"}, RedirectHosts: []string{"127.0.0.1"},
	}
	return r
}

func TestValid(t *testing.T) {
	r := newRig(t)
	m, err := r.client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Select("linux", "amd64", "cli")
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.client.Download(context.Background(), a)
	if err != nil || string(data) != string(r.asset) {
		t.Fatalf("download: %v", err)
	}
}

func TestBadSignatureAndTamperedManifest(t *testing.T) {
	r := newRig(t)
	other, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	// Signed by a key the client does not pin, even claiming the pinned id.
	forged := []byte(KeyID(r.pub) + " " + base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, r.manifest)))
	_ = other
	if _, err := VerifyWith(r.client.Keys, r.manifest, forged); !errors.Is(err, ErrUntrusted) {
		t.Errorf("wrong-key signature: %v", err)
	}
	// Unknown key id.
	if _, err := VerifyWith(r.client.Keys, r.manifest, sign(otherPriv, other, r.manifest)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("unpinned key: %v", err)
	}
	// Manifest altered after signing (a different size).
	r.manifest = []byte(strings.Replace(string(r.manifest), `"v2.0.0"`, `"v9.0.0"`, 1))
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Errorf("tampered manifest: %v", err)
	}
	// Garbage signature.
	if _, err := VerifyWith(r.client.Keys, r.manifest, []byte("nonsense")); !errors.Is(err, ErrUntrusted) {
		t.Errorf("garbage sig: %v", err)
	}
}

func TestNoPinnedKeyIsUnavailableWithoutNetwork(t *testing.T) {
	c := &Client{ManifestURLs: []string{"https://monoes.me/never-fetched"}}
	_, err := c.Latest(context.Background())
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUntrusted) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if _, err := Verify([]byte("{}"), []byte("x y")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Verify with empty pinned list: %v", err)
	}
}

func TestUnreachableManifestIsUnavailable(t *testing.T) {
	r := newRig(t)
	r.mux = nil
	r.client.ManifestURLs = []string{r.srv.URL + "/missing.json"}
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("404 manifest: %v", err)
	}
}

func TestTamperedAsset(t *testing.T) {
	r := newRig(t)
	m, _ := r.client.Latest(context.Background())
	a, _ := m.Select("linux", "amd64", "cli")
	r.asset = []byte("binary-bytes-v3") // same length, other content
	if _, err := r.client.Download(context.Background(), a); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("tampered asset: %v", err)
	}
	r.asset = append([]byte("binary-bytes-v2"), 'x') // longer than declared
	if _, err := r.client.Download(context.Background(), a); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("oversize asset: %v", err)
	}
}

func TestOversizeManifestBody(t *testing.T) {
	r := newRig(t)
	r.manifest = []byte(strings.Repeat("a", maxManifestBytes+10))
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("oversize manifest: %v", err)
	}
	m := Asset{Name: "x", Size: 1 << 40}
	if _, err := r.client.Download(context.Background(), m); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("declared oversize: %v", err)
	}
}

func TestRedirectToOtherHostRefused(t *testing.T) {
	r := newRig(t)
	r.mux.HandleFunc("/redir.json", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "https://example.com/manifest.json", http.StatusFound)
	})
	r.client.ManifestURLs = []string{r.srv.URL + "/redir.json"}
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("redirect to other host: %v", err)
	}
	r.mux.HandleFunc("/redir-http", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://127.0.0.1/x", http.StatusFound)
	})
	r.client.ManifestURLs = []string{r.srv.URL + "/redir-http"}
	if _, err := r.client.Latest(context.Background()); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("redirect to http: %v", err)
	}
}

func TestURLsOutsideAllowList(t *testing.T) {
	for _, u := range []string{"http://monoes.me/a", "https://evil.example/a", "ftp://monoes.me/a", "monoes.me/a"} {
		if err := CheckURL(u, []string{"monoes.me"}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("%s accepted", u)
		}
	}
	if err := CheckURL("https://monoes.me/a", []string{"monoes.me"}); err != nil {
		t.Error(err)
	}
	r := newRig(t)
	a := Asset{Name: "x", URL: "https://evil.example/x", Size: 1}
	if _, err := r.client.Download(context.Background(), a); !errors.Is(err, ErrUntrusted) {
		t.Errorf("asset on foreign host: %v", err)
	}
}

func TestSchemaAndDowngrade(t *testing.T) {
	r := newRig(t)
	bad := []byte(`{"schema":2,"version":"v3.0.0","released_at":"2026-10-08T10:00:00Z","assets":[]}`)
	if _, err := VerifyWith(r.client.Keys, bad, sign(r.priv, r.pub, bad)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("unknown schema: %v", err)
	}
	if err := CheckDowngrade("v1.0.0", "v2.0.0", false); !errors.Is(err, ErrUntrusted) {
		t.Errorf("downgrade allowed: %v", err)
	}
	if err := CheckDowngrade("v1.0.0", "v2.0.0", true); err != nil {
		t.Errorf("--force: %v", err)
	}
	for _, c := range [][2]string{{"v2.0.0", "v2.0.0"}, {"v0.10.0", "v0.9.9"}, {"v1.0.0", "dev"}} {
		if err := CheckDowngrade(c[0], c[1], false); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
}

func TestSelect(t *testing.T) {
	m := &Manifest{Version: "v1", Assets: []Asset{
		{OS: "windows", Arch: "amd64", Name: "a.exe", Kind: "cli"},
		{OS: "windows", Arch: "amd64", Name: "b.exe", Kind: "cli"},
		{OS: "linux", Arch: "arm64", Name: "c", Kind: "app"},
	}}
	if _, err := m.Select("windows", "amd64", "cli"); err == nil {
		t.Error("ambiguous select should fail")
	}
	if _, err := m.Asset("b.exe", "windows", "amd64", "cli"); err != nil {
		t.Error(err)
	}
	if _, err := m.Select("linux", "amd64", "app"); err == nil {
		t.Error("missing arch should fail")
	}
	if a, err := m.Select("linux", "arm64", "app"); err != nil || a.Name != "c" {
		t.Errorf("%v %v", a, err)
	}
}

func TestPinnedKeyLine(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, err := ParseKey(KeyLine(pub))
	if err != nil || !pub.Equal(k.Public) || k.ID != KeyID(pub) {
		t.Fatalf("roundtrip: %v", err)
	}
	if _, err := ParseKey("only-one-field"); err == nil {
		t.Error("malformed line accepted")
	}
	if len(PinnedKeys()) != 0 {
		t.Error("this build must pin no key until the owner pastes one")
	}
}

// A manifest signed by a revoked key id is rejected even though the key is still pinned; the
// key that replaces it keeps working.
func TestRevokedKeyIsRejected(t *testing.T) {
	r := newRig(t)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []Key{{ID: KeyID(r.pub), Public: r.pub}, {ID: KeyID(newPub), Public: newPub}}
	if _, err := VerifyWith(keys, r.manifest, r.sig); err != nil {
		t.Fatalf("before revocation: %v", err)
	}
	revokedReleaseKeyIDs[KeyID(r.pub)] = true
	t.Cleanup(func() { delete(revokedReleaseKeyIDs, KeyID(r.pub)) })
	if _, err := VerifyWith(keys, r.manifest, r.sig); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("a revoked key's manifest: %v", err)
	}
	if _, err := VerifyWith(keys, r.manifest, sign(newPriv, newPub, r.manifest)); err != nil {
		t.Fatalf("the replacement key: %v", err)
	}
}
