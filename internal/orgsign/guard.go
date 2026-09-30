package orgsign

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Signer signs an org definition as the operator: `monomind org sign <org>
// --yes` run in the project root (internal/monomind.OrgSign).
type Signer interface {
	Sign(ctx context.Context, root, org string) error
}

// roleContextMarkers are the env vars monomind sets on an org role's or an
// `agent exec` turn's process tree (org-signature.ts ROLE_CONTEXT_MARKERS).
// monomind refuses to sign under any of them; mono-agent does not even ask.
var roleContextMarkers = []string{
	"MONOMIND_ORG_ROLE", "MONOMIND_SDK_AGENT", "MONOMIND_AGENT_EXEC", "MONOMIND_CLINE_TURN", "MONOMIND_AIDER",
}

// RoleContextMarker names the marker set in this process's env, or "".
func RoleContextMarker() string {
	for _, k := range roleContextMarkers {
		if os.Getenv(k) != "" {
			return k
		}
	}
	return ""
}

// Pre is an org file's state just before mono-agent writes it.
type Pre struct {
	root, org string
	eligible  bool
	why       string
}

// Eligible reports whether the coming write may be re-signed.
func (p Pre) Eligible() bool { return p.eligible }

// Before records whether the write mono-agent is about to make to org
// (the file the document came from; a rename's old name) may be re-signed
// afterwards. It may when the file on disk verifies now AND the document
// was loaded from exactly those bytes (loadedSHA, orgdesign.Doc.LoadedSHA)
// — then the only change a signature approves is mono-agent's own. A
// missing file is eligible only when signNew says the new definition is
// mono-agent's own content (a user's `org create`), not an import.
func Before(root, org, loadedSHA string, signNew bool) Pre {
	p := Pre{root: root, org: org}
	if m := RoleContextMarker(); m != "" {
		p.why = fmt.Sprintf("%s is set: an org role or agent turn made this change, and only the operator signs", m)
		return p
	}
	raw, sha, err := ReadFile(root, org)
	if errors.Is(err, os.ErrNotExist) {
		p.eligible = signNew
		if !signNew {
			p.why = "it was installed from outside mono-agent"
		}
		return p
	}
	if err != nil {
		p.why = err.Error()
		return p
	}
	st := Verify(root, org, raw)
	switch {
	case !st.OK():
		p.why = st.Describe()
	case loadedSHA == "" || loadedSHA != sha:
		p.why = "the change was not made to the signed file as it is on disk"
	default:
		p.eligible = true
	}
	return p
}

// Outcome is what happened to a write's signature.
type Outcome struct {
	Signed bool   `json:"signed"`
	State  string `json:"state"`
	// Notice is set when the org is left for the operator to review.
	Notice string `json:"notice,omitempty"`
}

// After re-signs org (the name just written; a rename's new name) when p
// allowed it and the file still holds exactly the bytes mono-agent wrote
// (sha, orgdesign.Save's return value). Otherwise the org is left unsigned
// with a notice saying why and how to review and sign it.
func (p Pre) After(ctx context.Context, s Signer, org, sha string) Outcome {
	if !p.eligible {
		st := Status{State: StateUnknown}
		if raw, _, err := ReadFile(p.root, org); err == nil {
			st = Verify(p.root, org, raw)
		}
		out := Outcome{Signed: st.OK(), State: st.State}
		if !st.OK() {
			out.Notice = fmt.Sprintf("org %s is not signed for this change (%s) — review it, then sign it: monoagentcli org sign %s", org, p.why, org)
		}
		return out
	}
	return SignExact(ctx, s, p.root, org, sha)
}

// SignExact signs org only if its file holds the bytes whose sha256 is
// sha, and confirms afterwards that monomind signed those bytes: if the
// file changed between the check and the signature, the signature is
// withdrawn. A definition that already verifies (only unsigned fields
// changed) needs no new signature.
func SignExact(ctx context.Context, s Signer, root, org, sha string) Outcome {
	notice := func(state, why string) Outcome {
		return Outcome{State: state, Notice: fmt.Sprintf("org %s is not signed: %s — review it, then sign it: monoagentcli org sign %s", org, why, org)}
	}
	if m := RoleContextMarker(); m != "" {
		return notice(StateUnknown, m+" is set, and only the operator signs")
	}
	raw, got, err := ReadFile(root, org)
	if err != nil {
		return notice(StateUnknown, err.Error())
	}
	if got != sha {
		return notice(Verify(root, org, raw).State, "the file changed after it was written or reviewed")
	}
	if st := Verify(root, org, raw); st.OK() {
		return Outcome{Signed: true, State: StateSigned}
	} else if st.State == StateForbiddenKey {
		return notice(st.State, Message(org, st))
	}
	want, err := Hash(root, raw)
	if err != nil {
		return notice(StateUnknown, err.Error())
	}
	if err := s.Sign(ctx, root, org); err != nil {
		st := Verify(root, org, raw)
		return notice(st.State, "monomind did not sign it: "+err.Error())
	}
	if signed, ok := SignedHash(root, org); !ok || signed != want {
		_ = Withdraw(root, org)
		return notice(StateUnsigned, "the file changed while it was being signed, so the signature was withdrawn")
	}
	return Outcome{Signed: true, State: StateSigned}
}
