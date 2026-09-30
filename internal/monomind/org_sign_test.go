package monomind

import (
	"errors"
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
