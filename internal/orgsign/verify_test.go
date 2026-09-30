package orgsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testdata/hash_golden.json holds computeOrgDefHash results from monomind
// 2.21.0 itself (dist/src/orgrt/org-signature.js) for definitions that
// exercise number printing, string escaping, key order, role shapes and
// instructions-file digests.
func TestHashMatchesMonomindGoldens(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "hash_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]struct {
		JSON string `json:"json"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompts", "lead.md"), []byte("You lead.\nünïcode ✓\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, c := range cases {
		got, err := Hash(root, []byte(c.JSON))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.Hash {
			t.Errorf("%s: hash %s, monomind %s", name, got, c.Hash)
		}
	}
}

func TestJSNumber(t *testing.T) {
	for in, want := range map[float64]string{
		1: "1", 100: "100", 0.1: "0.1", 1e21: "1e+21", 1e20: "100000000000000000000",
		1.5e-7: "1.5e-7", 1e-6: "0.000001", -314159: "-314159", 1.2345e-10: "1.2345e-10", 2.5e25: "2.5e+25",
	} {
		if got := jsNumber(in); got != want {
			t.Errorf("jsNumber(%v) = %s, want %s", in, got, want)
		}
	}
}

// signFixture writes an operator key and a sidecar for org the way
// monomind's signOrgDef does, returning the key.
func signFixture(t *testing.T, root, org string, raw []byte) []byte {
	t.Helper()
	dir := OperatorDir("")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dir, "full-access-grant.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Hash(root, raw)
	if err != nil {
		t.Fatal(err)
	}
	at := "2026-09-30T12:00:00.000Z"
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(hmacInput(json.Number("1"), true, org, realRoot(root), h, at)))
	rec, _ := json.Marshal(map[string]interface{}{"v": 1, "org": org, "root": realRoot(root), "hash": h, "at": at, "sig": hex.EncodeToString(mac.Sum(nil))})
	path, _ := SignaturePath(root, org)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rec, 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

func operatorDirForTest(t *testing.T) {
	t.Helper()
	t.Setenv("MONOMIND_ORGRT_OPERATOR_DIR", filepath.Join(t.TempDir(), "operator"))
	for _, k := range agentContextMarkers {
		t.Setenv(k, "")
	}
}

func TestVerifyStates(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	raw := []byte(`{"name":"growth","goal":"g","roles":[{"id":"lead","title":"Lead","policy":{"git":"read"}}]}`)

	if st := Verify(root, "growth", raw); st.State != StateUnsigned || !st.Refused() {
		t.Fatalf("no sidecar: %+v", st)
	}
	signFixture(t, root, "growth", raw)
	if st := Verify(root, "growth", raw); !st.OK() {
		t.Fatalf("signed: %+v", st)
	}
	// Unsigned fields: goal, status, role title/responsibilities/ui.
	cosmetic := []byte(`{"name":"growth","goal":"other","status":"x","roles":[{"id":"lead","title":"Boss","ui":{"x":3},"policy":{"git":"read"}}]}`)
	if st := Verify(root, "growth", cosmetic); !st.OK() {
		t.Fatalf("cosmetic edit should still verify: %+v", st)
	}
	widened := []byte(`{"name":"growth","goal":"g","roles":[{"id":"lead","title":"Lead","policy":{"git":"push"}}]}`)
	if st := Verify(root, "growth", widened); st.State != StateChanged {
		t.Fatalf("policy edit: %+v", st)
	}
	if st := Verify(root, "growth", []byte(`{"name":"growth","roles":[],"__proto__":{}}`)); st.State != StateForbiddenKey {
		t.Fatalf("forbidden key: %+v", st)
	}
	// The signature is bound to the org name and the project root.
	if st := Verify(root, "other", raw); st.State != StateUnsigned {
		t.Fatalf("other org: %+v", st)
	}
	if st := Verify(t.TempDir(), "growth", raw); st.State != StateUnsigned {
		t.Fatalf("other root: %+v", st)
	}
}

func TestVerifyRejectsForgedOrUntrustedSidecar(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	raw := []byte(`{"name":"growth","roles":[]}`)
	signFixture(t, root, "growth", raw)
	path, _ := SignaturePath(root, "growth")

	// A sidecar re-hashed for new content without the key does not verify.
	widened := []byte(`{"name":"growth","roles":[{"id":"x","policy":{"git":"push"}}]}`)
	h, _ := Hash(root, widened)
	b, _ := os.ReadFile(path)
	var rec map[string]interface{}
	_ = json.Unmarshal(b, &rec)
	rec["hash"] = h
	forged, _ := json.Marshal(rec)
	if err := os.WriteFile(path, forged, 0o600); err != nil {
		t.Fatal(err)
	}
	if st := Verify(root, "growth", widened); st.State != StateInvalid {
		t.Fatalf("forged hash: %+v", st)
	}

	if runtime.GOOS == "windows" {
		return
	}
	signFixture(t, root, "growth", raw)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if st := Verify(root, "growth", raw); st.State != StateInvalid || !strings.Contains(st.Detail, "mode") {
		t.Fatalf("world-readable sidecar: %+v", st)
	}
	signFixture(t, root, "growth", raw)
	if err := os.Chmod(filepath.Join(OperatorDir(""), "full-access-grant.key"), 0o640); err != nil {
		t.Fatal(err)
	}
	if st := Verify(root, "growth", raw); st.State != StateInvalid {
		t.Fatalf("group-readable key: %+v", st)
	}
}

func TestInstructionsFileOutsideProjectIsUnknown(t *testing.T) {
	operatorDirForTest(t)
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(outside, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"name":"g","roles":[{"id":"a","instructions_file":` + mustJSON(outside) + `}]}`)
	if _, err := Hash(root, raw); err == nil {
		t.Fatal("hashed an instructions file monomind would refuse")
	}
	signFixture(t, root, "g", []byte(`{"name":"g","roles":[]}`))
	if st := Verify(root, "g", raw); st.State != StateUnknown || st.Refused() {
		t.Fatalf("state %+v, want unknown (not a refusal)", st)
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
