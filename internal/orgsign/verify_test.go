package orgsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
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

// monomind's documented signable hash (doc/commands/org.md "The signable
// hash", monomind#568), pinned there by org-sign-expect-hash.test.ts: the
// worked example's canonical JSON and both fixture hashes, with and
// without an instructions file.
func TestMonomindDocumentedHashVectors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "boss.md"), []byte("Be the boss.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const fixture = `{"name":"fx","goal":"ship it","roles":[
  {"id":"boss","type":"boss","reports_to":null,"title":"CEO","instructions_file":"boss.md"},
  {"reports_to":"boss","id":"dev","responsibilities":["code"],"policy":{"git":"read"}}]}`
	const bare = `{"name":"fx","goal":"ship it","roles":[
  {"id":"boss","type":"boss","reports_to":null,"title":"CEO"},
  {"reports_to":"boss","id":"dev","responsibilities":["code"],"policy":{"git":"read"}}]}`
	for _, c := range []struct{ name, raw, canonical, hash string }{
		{"with instructions", fixture,
			`{"definition":{"name":"fx","roles":[{"id":"boss","instructions_file":"boss.md","reports_to":null,"type":"boss"},{"id":"dev","policy":{"git":"read"},"reports_to":"boss"}]},"instructions":{"role:boss":"sha256:272f6cf685a554faa4bc04a7890d434994aefcf98e9bfdfd339de87234e512cc"}}`,
			"a895d86cd63d1374360add64ce590de7591895b523e53512f5a2d9257ddf7125"},
		{"bare", bare,
			`{"name":"fx","roles":[{"id":"boss","reports_to":null,"type":"boss"},{"id":"dev","policy":{"git":"read"},"reports_to":"boss"}]}`,
			"2bb0a6ad90aa73e34b175333c401695c88079fb8faee131a43e27030689f247c"},
	} {
		v, err := parseOrgJSON([]byte(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		proj := projection(v)
		digests, err := instructionsDigests(v, readDigests(root).file)
		if err != nil {
			t.Fatal(err)
		}
		if len(digests) > 0 {
			proj = map[string]interface{}{"definition": proj, "instructions": digests}
		}
		if got := stringify(proj); got != c.canonical {
			t.Errorf("%s: canonical JSON\n got %s\nwant %s", c.name, got, c.canonical)
		}
		if got, err := Hash(root, []byte(c.raw)); err != nil || got != c.hash {
			t.Errorf("%s: hash %s (%v), monomind %s", c.name, got, err, c.hash)
		}
	}
}

// Key order and negative zero, with hashes from monomind's own
// computeOrgDefHash (2.21.0, and #568's documented vectors): array-index
// keys ("0" to "4294967294", no sign or leading zero) come first in
// numeric order, the rest by UTF-16 code units; JSON.stringify writes -0
// as 0 (Go's strconv would write "-0").
func TestMonomindKeyOrderAndNegativeZeroVectors(t *testing.T) {
	for raw, want := range map[string]string{
		`{"9":1,"10":1,"a":1,"b":1}`:                                "7730e4e01fd746abc3ff48aba427bd2f794534d137ff7ad01463a347faadf6e9",
		`{"10":1,"4294967294":1,"01":1,"4294967295":1,"a":1,"b":1}`: "42003f4c98a3d30c373a90b3fc40179cf7c58411c477f1b11c83285a78ba8113",
		`{"x":-0}`: "5bff452c5ed93f2e87a23984db5a15050c6477335fdec955b70063bb2d692bf1",
		`{"name":"z","roles":[],"run_config":{"n":-0,"m":[-0,-0.0,0]}}`: "b930cc4e090bd65320ce63d85698fe54b676027aa90d4739afa0a9b9e0aee831",
	} {
		if got, err := Hash(t.TempDir(), []byte(raw)); err != nil || got != want {
			t.Errorf("%s: %s (%v), monomind %s", raw, got, err, want)
		}
	}
	if got := jsNumber(math.Copysign(0, -1)); got != "0" {
		t.Errorf("-0 prints %q", got)
	}
}
