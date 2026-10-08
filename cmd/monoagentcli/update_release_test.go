package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/release"
	"github.com/zalando/go-keyring"
)

// signedLatest serves a signed manifest over TLS and points releaseClient at it;
// a plain-HTTP legacy endpoint answers latestReleaseURL with v1.0.0.
func signedLatest(t *testing.T, tamper bool) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m, _ := json.Marshal(release.Manifest{Schema: 1, Version: "v2.0.0", ReleasedAt: "2026-10-08T10:00:00Z", NotesURL: "https://monoes.me/n"})
	sig := []byte(release.KeyID(pub) + " " + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m)))
	if tamper {
		m = bytes.Replace(m, []byte("v2.0.0"), []byte("v9.0.0"), 1)
	}
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) { w.Write(m) })
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, _ *http.Request) { w.Write(sig) })
	oldClient := releaseClient
	releaseClient = func() *release.Client {
		return &release.Client{HTTP: srv.Client(), Keys: []release.Key{{ID: release.KeyID(pub), Public: pub}},
			ManifestURLs: []string{srv.URL + "/manifest.json"}, Hosts: []string{"127.0.0.1"}, RedirectHosts: []string{"127.0.0.1"}}
	}
	t.Cleanup(func() { releaseClient = oldClient })
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":"v1.0.0","html_url":"https://example.test/legacy"}`))
	}))
	t.Cleanup(legacy.Close)
	oldURL := latestReleaseURL
	latestReleaseURL = legacy.URL
	t.Cleanup(func() { latestReleaseURL = oldURL })
}

func TestFetchLatestPrefersSignedManifest(t *testing.T) {
	signedLatest(t, false)
	info, err := fetchLatest(context.Background())
	if err != nil || info.Tag != "v2.0.0" || info.Manifest == nil {
		t.Fatalf("got %+v, %v", info, err)
	}
}

func TestFetchLatestTamperedManifestDoesNotFallBack(t *testing.T) {
	signedLatest(t, true)
	info, err := fetchLatest(context.Background())
	if err == nil || info != nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("tampered manifest must fail, not fall back: %+v, %v", info, err)
	}
}

func TestFetchLatestNoPinnedKeyFallsBackToLegacy(t *testing.T) {
	signedLatest(t, false)
	releaseClient = func() *release.Client { return &release.Client{} }
	info, err := fetchLatest(context.Background())
	if err != nil || info.Tag != "v1.0.0" || info.Legacy == nil {
		t.Fatalf("got %+v, %v", info, err)
	}
	release.AllowLegacyGitHubUpdates = false
	t.Cleanup(func() { release.AllowLegacyGitHubUpdates = true })
	if _, err := fetchLatest(context.Background()); err == nil {
		t.Fatal("legacy disabled and no key must fail")
	}
}

func TestReleaseKeygenAndVerify(t *testing.T) {
	keyring.MockInit()
	var out bytes.Buffer
	cmd := newReleaseCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"keygen"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	i := strings.Index(out.String(), "\t\"")
	if i < 0 {
		t.Fatalf("no key line in %q", out.String())
	}
	line, err := strconv.Unquote(strings.TrimSuffix(strings.TrimSpace(out.String()[i:]), ","))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("private key printed")
	}
	// A second keygen refuses to replace the key.
	cmd = newReleaseCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"keygen"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("keygen replaced an existing key")
	}

	// verify with an explicit key: sign with a fresh pair (keygen's private key stays in the keychain).
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	mf := filepath.Join(dir, "manifest.json")
	sf := mf + ".sig"
	m := []byte(`{"schema":1,"version":"v3.0.0","released_at":"2026-10-08T10:00:00Z","assets":[]}`)
	os.WriteFile(mf, m, 0o600)
	os.WriteFile(sf, []byte(release.KeyID(pub)+" "+base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m))), 0o600)
	run := func(key string) (string, error) {
		var o bytes.Buffer
		c := newReleaseCmd()
		c.SetOut(&o)
		c.SetArgs([]string{"verify", mf, sf, "--pubkey", key})
		err := c.Execute()
		return o.String(), err
	}
	if o, err := run(release.KeyLine(pub)); err != nil || !strings.Contains(o, "v3.0.0") {
		t.Fatalf("verify: %q %v", o, err)
	}
	if _, err := run(line); err == nil {
		t.Fatal("verified against the wrong key")
	}
	os.WriteFile(mf, append(m, ' '), 0o600)
	if _, err := run(release.KeyLine(pub)); err == nil {
		t.Fatal("verified a tampered manifest")
	}
}
