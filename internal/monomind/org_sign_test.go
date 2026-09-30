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

	fakeCheckMonomind(t, "2.22.0", `{"orgs":[{"org":"growth","state":"invalid","message":"bad sig"}]}`)
	if st, ok := OrgSignCheck(context.Background(), root, "growth"); !ok || st.State != orgsign.StateInvalid {
		t.Fatalf("invalid = %+v, %v", st, ok)
	}
	for _, body := range []string{`{"orgs":[{"org":"growth","state":"not-found"}]}`, `not json`, `{"orgs":[{"org":"other","state":"signed"}]}`} {
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
