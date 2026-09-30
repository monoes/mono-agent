package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
)

// monomind advertises no capability for signed org definitions, so the
// gate is the version (2.21.0, monomind#502).
func TestOrgSigningGate(t *testing.T) {
	for v, want := range map[string]bool{"2.21.0": true, "2.21.3": true, "2.22.0": true, "3.0.0": true, "2.20.9": false, "2.21.0-rc.1": true, "": false} {
		if got := NewCapabilitySet(v, "org-json-v1").OrgSigning(); got != want {
			t.Errorf("OrgSigning(%q) = %v, want %v", v, got, want)
		}
	}
	var nilSet *CapabilitySet
	if nilSet.OrgSigning() {
		t.Error("nil set")
	}
}

func TestSignatureRefusalClassified(t *testing.T) {
	for text, want := range map[string]string{
		"org growth: the definition has no operator signature — run `monomind org sign growth`":            orgsign.StateUnsigned,
		"org growth is not signed (changed)":                                                               orgsign.StateChanged,
		"org growth: the definition changed since the operator signed it (policy, roles, runtime, skills)": orgsign.StateChanged,
		"org growth: the definition has an operator signature that does not verify (no operator key)":      orgsign.StateInvalid,
		"org growth: the definition holds a forbidden key (roles[0].__proto__) — remove it":                orgsign.StateForbiddenKey,
		"org growth: budget exceeded": "",
	} {
		if got := signatureRefusalReason(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	err := asSignatureRefusal("growth", errors.New("monomind org run growth --yes: org growth is not signed (unsigned)"))
	se, ok := AsOrgSignatureError(err)
	if !ok || se.Org != "growth" || se.Status.State != orgsign.StateUnsigned {
		t.Fatalf("err = %v", err)
	}
	plain := errors.New("boom")
	if got := asSignatureRefusal("growth", plain); got != plain {
		t.Fatalf("other errors pass through, got %v", got)
	}
}

// fakeCheckMonomind answers the handshake as version and `org sign <org>
// --check --format json` with body (exit 1), logging each call's cwd+argv.
func fakeCheckMonomind(t *testing.T, version, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "monomind")
	script := "#!/bin/sh\necho \"$(pwd) $*\" >> '" + log + "'\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi` + "\n" +
		"cat <<'JSON'\n" + body + "\nJSON\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
	return log
}

func TestOrgSignCheck(t *testing.T) {
	root := t.TempDir()
	log := fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"changed","signedAt":"2026-09-30T12:00:00Z","message":"org growth changed"}]}`)
	st, ok := OrgSignCheck(context.Background(), root, "growth")
	if !ok || st.State != orgsign.StateChanged || st.Detail != "org growth changed" {
		t.Fatalf("check = %+v, %v", st, ok)
	}
	calls, _ := os.ReadFile(log)
	real, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(string(calls), real+" org sign growth --check --format json") {
		t.Fatalf("calls:\n%s", calls)
	}

	// A bad HMAC is invalid-signature; "invalid" is a broken definition.
	fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"invalid-signature","message":"bad sig"}]}`)
	if st, ok := OrgSignCheck(context.Background(), root, "growth"); !ok || st.State != orgsign.StateInvalid || !st.Refused() {
		t.Fatalf("invalid-signature = %+v, %v", st, ok)
	}
	fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"invalid","message":"not JSON"}]}`)
	if st, ok := OrgSignCheck(context.Background(), root, "growth"); !ok || st.State != orgsign.StateInvalidDefinition || st.Refused() {
		t.Fatalf("invalid = %+v, %v", st, ok)
	}
	// Anything else — including a 2.22 that ships without --check or with
	// another shape — is no answer, so the Go check is used.
	for _, body := range []string{
		`{"orgs":[{"org":"growth","state":"not-found"}]}`, `not json`, `{"orgs":[{"org":"other","state":"signed"}]}`,
		`[ERROR] Unknown option: --check`, `{"growth":"signed"}`, `{"orgs":[{"org":"growth","state":"verified"}]}`,
	} {
		fakeCheckMonomind(t, "2.22.0", body)
		if _, ok := OrgSignCheck(context.Background(), root, "growth"); ok {
			t.Fatalf("%s: want no verdict", body)
		}
	}
	// 2.21 has no --check: never run.
	log = fakeCheckMonomind(t, "2.21.0", `{"orgs":[{"org":"growth","state":"signed"}]}`)
	if _, ok := OrgSignCheck(context.Background(), root, "growth"); ok {
		t.Fatal("used --check on 2.21")
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "--check") {
		t.Fatalf("ran --check on 2.21:\n%s", calls)
	}
}

func TestOrgSignCheckAll(t *testing.T) {
	root := t.TempDir()
	log := fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"a","state":"signed"},{"org":"b","state":"changed"},{"org":"c","state":"not-found"}]}`)
	got, ok := OrgSignCheckAll(context.Background(), root)
	if !ok || len(got) != 2 || got["a"].State != orgsign.StateSigned || got["b"].State != orgsign.StateChanged {
		t.Fatalf("all = %+v, %v", got, ok)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "org sign --all --check --format json") {
		t.Fatalf("calls:\n%s", calls)
	}
}

// --expect-hash is passed only when this monomind's `org sign --help`
// lists it (monomind#568, 2.22); otherwise the plain --yes, with
// mono-agent's own post-check.
func TestOrgSignExpectHashOnlyWhenOffered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	for _, c := range []struct {
		version, help string
		want          bool
	}{
		{"2.22.0", "  --expect-hash <hex>  Sign only if the signable hash is <hex>", true},
		{"2.22.0", "  --yes  Skip the confirmation", false},
		{"2.21.0", "  --expect-hash <hex>", false},
	} {
		dir := t.TempDir()
		log := filepath.Join(dir, "calls")
		bin := filepath.Join(dir, "monomind")
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + c.version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi` + "\n" +
			`if [ "$3" = "--help" ]; then echo '` + c.help + `'; exit 0; fi` + "\n" +
			"echo signed\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvOverride, bin)
		ResetCapabilityCache()
		if _, err := OrgSign(context.Background(), t.TempDir(), "growth", "abc123"); err != nil {
			t.Fatal(err)
		}
		calls, _ := os.ReadFile(log)
		got := strings.Contains(string(calls), "org sign growth --yes --expect-hash abc123")
		if got != c.want || !strings.Contains(string(calls), "org sign growth --yes") {
			t.Errorf("%s %q: calls\n%s", c.version, c.help, calls)
		}
	}
	ResetCapabilityCache()
}

// 2.22's review JSON: the reviewText and the hash of what was reviewed.
// Any other shape, or an older monomind, gives the human review instead.
func TestOrgSignReviewJSON(t *testing.T) {
	root := t.TempDir()
	h := strings.Repeat("ab", 32)
	for _, c := range []struct {
		version, jsonOut, wantHash, wantText string
		wantErr                              bool
	}{
		{"2.22.0", `{"org":"growth","state":"changed","hash":"` + strings.ToUpper(h) + `","review":{"authority":[]},"reviewText":"org growth (changed):"}`, h, "org growth (changed):", false},
		{"2.22.0", `{"org":"growth","error":"org not found: growth"}`, "", "", true},
		{"2.22.0", `{"org":"growth","state":"changed","reviewText":"no hash here"}`, "", "text review", false},
		{"2.22.0", `{"org":"growth","hash":"xyz","reviewText":"bad hash"}`, "", "text review", false},
		{"2.21.0", `{"org":"growth","hash":"` + h + `","reviewText":"never read"}`, "", "text review", false},
	} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "monomind")
		script := "#!/bin/sh\n" +
			`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + c.version + `","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi` + "\n" +
			`if [ "$4" = "--format" ]; then echo '` + c.jsonOut + `'; exit 0; fi` + "\n" +
			"echo 'text review'\necho 'Not signed. Review the above, then sign it yourself in a terminal: monomind org sign <org> (or pass --yes).'\nexit 1\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvOverride, bin)
		ResetCapabilityCache()
		rv, err := OrgSignReview(context.Background(), root, "growth")
		if (err != nil) != c.wantErr || rv.Hash != c.wantHash || rv.Text != c.wantText {
			t.Errorf("%s %s: %+v, %v", c.version, c.jsonOut, rv, err)
		}
	}
	ResetCapabilityCache()
}

// A version shim (mise, asdf) picks the monomind per project: the
// --expect-hash detection runs in the project root, as the sign does, and
// is cached per root. An advertised org-sign-expect-hash capability
// decides without reading help.
func TestOrgSignExpectsHashPerProjectRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "monomind")
	// A shim: in a folder with .tool-versions it is 2.21 (no flag);
	// with .caps it is a 2.21 that advertises the capability; else 2.22
	// whose help lists --expect-hash.
	script := "#!/bin/sh\n" +
		`v=2.22.0; caps='"agent-exec","agent-scan","org-json-v1"'; help='  --expect-hash <hex>'` + "\n" +
		`if [ -f .tool-versions ]; then v=2.21.0; help='  --yes'; fi` + "\n" +
		`if [ -f .caps ]; then v=2.21.0; help='  --yes'; caps="$caps,\"org-sign-expect-hash\""; fi` + "\n" +
		`if [ "$1" = "--version" ]; then echo "{\"v\":1,\"version\":\"$v\",\"min_caller\":\"1.0.0\",\"capabilities\":[$caps]}"; exit 0; fi` + "\n" +
		`if [ "$3" = "--help" ]; then echo "$help"; exit 0; fi` + "\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)

	pinned, plain, advertised := t.TempDir(), t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(pinned, ".tool-versions"), []byte("monomind 2.21.0\n"), 0o644)
	_ = os.WriteFile(filepath.Join(advertised, ".caps"), []byte("x"), 0o644)
	ctx := context.Background()
	if OrgSignExpectsHash(ctx, pinned) {
		t.Error("claimed --expect-hash where the shim picks 2.21")
	}
	if !OrgSignExpectsHash(ctx, plain) {
		t.Error("missed --expect-hash on 2.22")
	}
	if !OrgSignExpectsHash(ctx, advertised) {
		t.Error("missed the advertised capability")
	}
	// Cached per root: the answer for pinned doesn't leak to plain.
	if OrgSignExpectsHash(ctx, pinned) || !OrgSignExpectsHash(ctx, plain) {
		t.Error("cache mixed the roots up")
	}
}
