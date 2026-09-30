package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/monoes/mono-agent/internal/orgsign"
)

// Signed org definitions (monomind#502, 2.21.0): monomind refuses to start
// or reload an org whose definition the operator has not signed. monomind
// advertises no capability for it, so it is gated on the version.
const orgSigningMinVersion = "2.21.0"

// orgSignCheckMinVersion adds `org sign <org> --check --format json`: a
// read-only verdict from monomind itself (2.22).
const orgSignCheckMinVersion = "2.22.0"

// OrgSigning reports whether this monomind enforces signed org definitions.
func (c *CapabilitySet) OrgSigning() bool {
	return c != nil && versionAtLeast(c.Version, orgSigningMinVersion)
}

// OrgSigningEnforced runs (or reuses) the handshake and reports whether the
// installed monomind enforces signed org definitions. False when monomind
// is missing.
func OrgSigningEnforced(ctx context.Context) bool {
	set, err := Capabilities(ctx)
	return err == nil && set.OrgSigning()
}

// OrgSignCheck asks monomind whether org's definition in projectRoot
// verifies (`org sign <org> --check --format json`, 2.22+; read-only, no
// prompt). ok is false when the installed monomind has no --check or its
// answer can't be used; callers then check in Go (internal/orgsign).
func OrgSignCheck(ctx context.Context, projectRoot, name string) (st orgsign.Status, ok bool) {
	set, err := Capabilities(ctx)
	if err != nil || !versionAtLeast(set.Version, orgSignCheckMinVersion) {
		return st, false
	}
	bin, _, err := Ensure(ctx)
	if err != nil {
		return st, false
	}
	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "org", "sign", name, "--check", "--format", "json")
	cmd.Dir = projectRoot
	out, _ := cmd.Output() // exit 1 means "not signed", with the same JSON
	var res struct {
		Orgs []struct {
			Org     string `json:"org"`
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"orgs"`
	}
	if json.Unmarshal(lastJSONDocument(out), &res) != nil {
		return st, false
	}
	for _, o := range res.Orgs {
		if o.Org != name {
			continue
		}
		switch o.State {
		case orgsign.StateSigned, orgsign.StateChanged, orgsign.StateUnsigned, orgsign.StateForbiddenKey:
			return orgsign.Status{State: o.State, Detail: o.Message}, true
		case "invalid":
			return orgsign.Status{State: orgsign.StateInvalid, Detail: o.Message}, true
		}
	}
	return st, false // not-found, or a state this client doesn't know
}

// OrgSign signs an org definition as the operator: `monomind org sign
// <name> --yes` in projectRoot. monomind itself refuses inside an org role
// or agent turn.
func OrgSign(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "sign", name, "--yes")
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// OrgSignReview returns monomind's review of an org definition — its
// state, the authority every role gets, and what changed since the last
// signature — by running `monomind org sign <name>` without --yes and
// without a terminal, which prints the review and signs nothing.
func OrgSignReview(ctx context.Context, projectRoot, name string) (string, error) {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "org", "sign", name)
	cmd.Dir = projectRoot
	out, runErr := cmd.CombinedOutput()
	var kept []string
	for _, line := range strings.Split(ansiRe.ReplaceAllString(string(out), ""), "\n") {
		t := strings.TrimSpace(line)
		// The refusal footer is about monomind's own CLI, not the org.
		if strings.HasPrefix(t, "Not signed. Review the above") || strings.Contains(t, "confirmation required (--yes)") {
			continue
		}
		kept = append(kept, strings.TrimRight(line, " \r"))
	}
	review := strings.TrimSpace(strings.Join(kept, "\n"))
	if review == "" {
		if runErr != nil {
			return "", orgCommandError([]string{"sign", name}, runErr)
		}
		return "", fmt.Errorf("monomind org sign %s: empty review", name)
	}
	return review, nil
}

// OrgSignatureError is monomind refusing (or about to refuse) to start or
// reload an org because its definition is not signed.
type OrgSignatureError struct {
	Org    string
	Status orgsign.Status
}

func (e *OrgSignatureError) Error() string { return orgsign.Message(e.Org, e.Status) }

// AsOrgSignatureError finds an OrgSignatureError in err's chain.
func AsOrgSignatureError(err error) (*OrgSignatureError, bool) {
	var se *OrgSignatureError
	ok := errors.As(err, &se)
	return se, ok
}

// signatureRefusalReason names the reason in monomind's own refusal text
// (orgSignatureMessage and org run's "is not signed (<reason>)"), or "".
func signatureRefusalReason(text string) string {
	switch {
	case strings.Contains(text, "has no operator signature"), strings.Contains(text, "is not signed (unsigned)"):
		return orgsign.StateUnsigned
	case strings.Contains(text, "changed since the operator signed it"), strings.Contains(text, "is not signed (changed)"):
		return orgsign.StateChanged
	case strings.Contains(text, "operator signature that does not verify"), strings.Contains(text, "is not signed (invalid-signature)"):
		return orgsign.StateInvalid
	case strings.Contains(text, "holds a forbidden key"), strings.Contains(text, "is not signed (forbidden-key)"):
		return orgsign.StateForbiddenKey
	}
	return ""
}

// checkOrgSigned is the start/reload preflight: on a monomind that enforces
// signatures, an org whose definition would be refused gets an
// OrgSignatureError naming the sign command, instead of monomind's refusal
// in a log nobody reads (a detached start) or a bare exit status. An org
// this code can't judge (StateUnknown) is left to monomind.
func checkOrgSigned(ctx context.Context, projectRoot, name string) error {
	if !OrgSigningEnforced(ctx) {
		return nil
	}
	st, ok := OrgSignCheck(ctx, projectRoot, name)
	if !ok {
		var err error
		if st, _, err = orgsign.VerifyFile(projectRoot, name); err != nil {
			return nil
		}
	}
	if !st.Refused() {
		return nil
	}
	return &OrgSignatureError{Org: name, Status: st}
}

// asSignatureRefusal turns a failed org command whose text is monomind's
// signature refusal into an OrgSignatureError; other errors pass through.
func asSignatureRefusal(name string, err error) error {
	if err == nil {
		return nil
	}
	if reason := signatureRefusalReason(err.Error()); reason != "" {
		return fmt.Errorf("%w (monomind: %s)", &OrgSignatureError{Org: name, Status: orgsign.Status{State: reason}}, strings.TrimSpace(err.Error()))
	}
	return err
}

// JSONErrorFields gives `--json` callers the machine-readable refusal: the
// code, the org, its signature state and the command that signs it.
func (e *OrgSignatureError) JSONErrorFields() map[string]any {
	return map[string]any{
		"code": "org_not_signed", "org": e.Org, "signature": e.Status.State,
		"sign_command": "monoagentcli org sign " + e.Org,
	}
}
