package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"

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

// orgCheckResult is `org sign --check --format json` (monomind 2.22).
type orgCheckResult struct {
	Orgs []struct {
		Org     string `json:"org"`
		State   string `json:"state"`
		Message string `json:"message"`
	} `json:"orgs"`
}

// checkState maps a --check state; ok is false for not-found or a state
// this client doesn't know (the Go check is used then).
func checkState(state, message string) (orgsign.Status, bool) {
	switch state {
	case orgsign.StateSigned, orgsign.StateChanged, orgsign.StateUnsigned, orgsign.StateForbiddenKey, orgsign.StateInvalid:
		return orgsign.Status{State: state, Detail: message}, true
	case "invalid":
		// Unreadable or schema-invalid JSON, not a bad signature.
		return orgsign.Status{State: orgsign.StateInvalidDefinition, Detail: message}, true
	}
	return orgsign.Status{}, false
}

// runOrgCheck runs `monomind org sign <args> --check --format json` on a
// monomind that has it (2.22+). Any other shape — no --check, a usage
// error, JSON this client doesn't expect — gives ok=false, never an error:
// callers fall back to the Go check.
func runOrgCheck(ctx context.Context, projectRoot string, args ...string) (orgCheckResult, bool) {
	var res orgCheckResult
	set, err := Capabilities(ctx)
	if err != nil || !versionAtLeast(set.Version, orgSignCheckMinVersion) {
		return res, false
	}
	bin, _, err := Ensure(ctx)
	if err != nil {
		return res, false
	}
	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	full := append(append([]string{"org", "sign"}, args...), "--check", "--format", "json")
	cmd := exec.CommandContext(cctx, bin, full...)
	cmd.Dir = projectRoot
	out, _ := cmd.Output() // exit 1 means "not all signed", with the same JSON
	if json.Unmarshal(lastJSONDocument(out), &res) != nil || res.Orgs == nil {
		return res, false
	}
	return res, true
}

// OrgSignCheck asks monomind whether org's definition in projectRoot
// verifies (`org sign <org> --check --format json`, 2.22+; read-only, no
// prompt). ok is false when monomind can't answer; callers then check in
// Go (internal/orgsign).
func OrgSignCheck(ctx context.Context, projectRoot, name string) (orgsign.Status, bool) {
	res, ok := runOrgCheck(ctx, projectRoot, name)
	if !ok {
		return orgsign.Status{}, false
	}
	for _, o := range res.Orgs {
		if o.Org == name {
			return checkState(o.State, o.Message)
		}
	}
	return orgsign.Status{}, false
}

// OrgSignCheckAll is OrgSignCheck for every org of the project in one
// monomind run (`--check --all`): the orgs it could answer for.
func OrgSignCheckAll(ctx context.Context, projectRoot string) (map[string]orgsign.Status, bool) {
	res, ok := runOrgCheck(ctx, projectRoot, "--all")
	if !ok {
		return nil, false
	}
	out := map[string]orgsign.Status{}
	for _, o := range res.Orgs {
		if st, ok := checkState(o.State, o.Message); ok {
			out[o.Org] = st
		}
	}
	return out, true
}

// OrgSign signs an org definition as the operator: `monomind org sign
// <name> --yes` in projectRoot. monomind itself refuses inside an org role
// or agent turn. hash is the projection hash the signature must cover;
// when this monomind's `org sign` takes --expect-hash (asked of monomind
// for 2.22) it is passed, so monomind refuses to sign anything else.
func OrgSign(ctx context.Context, projectRoot, name, hash string) (string, error) {
	args := []string{"sign", name, "--yes"}
	if hash != "" && orgSignExpectsHash(ctx) {
		args = append(args, "--expect-hash", hash)
	}
	return runOrgText(ctx, projectRoot, args...)
}

var expectHashCache struct {
	sync.Mutex
	bin string
	ok  bool
}

// orgSignExpectsHash reports whether the installed monomind's `org sign`
// has --expect-hash: 2.22+ whose help lists it (cached per binary).
func orgSignExpectsHash(ctx context.Context) bool {
	set, err := Capabilities(ctx)
	if err != nil || !versionAtLeast(set.Version, orgSignCheckMinVersion) {
		return false
	}
	bin, err := Find()
	if err != nil {
		return false
	}
	expectHashCache.Lock()
	defer expectHashCache.Unlock()
	if expectHashCache.bin != bin {
		cctx, cancel := context.WithTimeout(ctx, orgTimeout)
		defer cancel()
		out, _ := exec.CommandContext(cctx, bin, "org", "sign", "--help").CombinedOutput()
		expectHashCache.bin, expectHashCache.ok = bin, strings.Contains(string(out), "--expect-hash")
	}
	return expectHashCache.ok
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
