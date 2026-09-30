// Package orgsigntest signs org definitions the way monomind 2.21's
// signOrgDef does, for tests that stand in for `monomind org sign`.
// mono-agent itself never signs; only tests import this.
package orgsigntest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
)

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return string(bytes.TrimSpace(b.Bytes()))
}

func realRoot(root string) string {
	abs, _ := filepath.Abs(root)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// AsOperator clears every agent-context marker for the test, so mono-agent
// acts as it would in the operator's own terminal.
func AsOperator(t testing.TB) {
	t.Helper()
	for _, k := range orgsign.AgentContextMarkers() {
		t.Setenv(k, "")
	}
}

// Sign writes the operator key (if missing) and org's signature for the
// definition currently on disk under root, in orgsign.OperatorDir(root).
func Sign(t testing.TB, root, org string) {
	t.Helper()
	raw, _, err := orgsign.ReadFile(root, org)
	if err != nil {
		t.Fatal(err)
	}
	h, err := orgsign.Hash(root, raw)
	if err != nil {
		t.Fatal(err)
	}
	SignHash(t, root, org, h)
}

// SignHash writes org's signature for hash, as monomind does when it
// signs content whose hash this package can't compute (--expect-hash).
func SignHash(t testing.TB, root, org, h string) {
	t.Helper()
	dir := orgsign.OperatorDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "full-access-grant.key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		key = []byte("0123456789abcdef0123456789abcdef")
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	at, rr := "2026-09-30T12:00:00.000Z", realRoot(root)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(`{"kind":"org-def","v":1,"org":` + jsonString(org) + `,"root":` + jsonString(rr) +
		`,"hash":` + jsonString(h) + `,"at":` + jsonString(at) + `}`))
	rec, _ := json.Marshal(map[string]interface{}{"v": 1, "org": org, "root": rr, "hash": h, "at": at, "sig": hex.EncodeToString(mac.Sum(nil))})
	path, err := orgsign.SignaturePath(root, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rec, 0o600); err != nil {
		t.Fatal(err)
	}
}
