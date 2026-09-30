package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orgsign"
)

// Signed org definitions (#288, monomind#502). monomind 2.21 runs an org
// only when the operator signed its definition. mono-agent keeps that
// meaningful: a write it makes is re-signed only when the file verified
// before the write and the document came from exactly those bytes
// (orgsign.Before/After); everything else — an outside edit, an imported
// org, a chat assistant's change — is left for the operator to review and
// sign with `org sign`, which shows monomind's own review first.

// orgSigningOn reports whether the installed monomind enforces signatures.
// A var so tests can switch it without a monomind.
var orgSigningOn = monomind.OrgSigningEnforced

type monomindOrgSigner struct{}

func (monomindOrgSigner) Sign(ctx context.Context, root, org string) error {
	_, err := monomind.OrgSign(ctx, root, org)
	return err
}

// orgSigner signs through monomind; a var so tests can stand one in.
var orgSigner orgsign.Signer = monomindOrgSigner{}

// saveOrgSigned writes doc like orgdesign.Save and keeps its signature:
// from is the org file the document replaces (a rename's old name), and
// signNew says a brand-new org is mono-agent's own content (not an import).
// The outcome is nil when monomind does not enforce signatures.
func saveOrgSigned(ctx context.Context, root, from string, doc *orgdesign.Doc, signNew bool) (string, *orgsign.Outcome, error) {
	if !orgSigningOn(ctx) {
		sha, err := orgdesign.Save(root, doc)
		return sha, nil, err
	}
	pre := orgsign.Before(root, from, doc.LoadedSHA(), signNew)
	sha, err := orgdesign.Save(root, doc)
	if err != nil {
		return "", nil, err
	}
	out := pre.After(ctx, orgSigner, doc.Name, sha)
	return sha, &out, nil
}

// saveOrgDoc is saveOrgSigned for an edit of an existing org, reporting on
// stderr when the org is left for the operator to review.
func saveOrgDoc(ctx context.Context, root string, doc *orgdesign.Doc) (string, error) {
	sha, out, err := saveOrgSigned(ctx, root, doc.Name, doc, false)
	warnOrgSignature(out)
	return sha, err
}

func warnOrgSignature(out *orgsign.Outcome) {
	if out != nil && out.Notice != "" {
		fmt.Fprintln(os.Stderr, "warning: "+out.Notice)
	}
}

// orgSignResult is `org sign`'s JSON.
type orgSignResult struct {
	V         int    `json:"v"`
	Org       string `json:"org"`
	Supported bool   `json:"supported"`
	State     string `json:"state,omitempty"`
	Detail    string `json:"detail,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Review    string `json:"review,omitempty"`
	Signed    bool   `json:"signed,omitempty"` // this call signed it
	Message   string `json:"message,omitempty"`
}

func newOrgSignCmd(env *orgEnv) *cobra.Command {
	var yes, statusOnly bool
	var expect string
	c := &cobra.Command{
		Use:   "sign <org>",
		Short: "Review an org definition and sign it as the operator (monomind 2.21+)",
		Long: "monomind 2.21 runs an org only when the operator signed its definition (roles, policies, " +
			"runtimes, autonomy, automations; not the goal, status or role titles). mono-agent re-signs " +
			"its own edits of a signed org; an org changed any other way, or never signed, needs this.\n\n" +
			"Without --yes it prints monomind's review (state, each role's authority, what changed since " +
			"the last signature) and, on a terminal, asks before signing; otherwise it signs nothing. " +
			"--yes signs (with --expect-sha256, only the exact file that was reviewed). --status prints " +
			"the signature state alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name, root := args[0], env.Root()
			if !orgdesign.ValidOrgName(name) {
				return errInvalidInput("invalid org name %q", name)
			}
			raw, sha, err := orgsign.ReadFile(root, name)
			if errors.Is(err, os.ErrNotExist) {
				return errNotFound("org %q not found under %s", name, orgdesign.OrgsDir(root))
			} else if err != nil {
				return err
			}
			res := orgSignResult{V: 1, Org: name, SHA256: sha}
			if !orgSigningOn(ctx) {
				if statusOnly {
					res.Message = "the installed monomind does not require signed org definitions (2.21+)"
					return printJSONValue(res)
				}
				return errOrgSigningUnsupported
			}
			res.Supported = true
			st := orgsign.Verify(root, name, raw)
			res.State, res.Detail = st.State, st.Detail
			if statusOnly {
				if !st.OK() {
					res.Message = orgsign.Message(name, st)
				}
				return printJSONValue(res)
			}
			if !yes {
				review, err := monomind.OrgSignReview(ctx, root, name)
				if err != nil {
					return err
				}
				res.Review = review
				if env.cfg.JSONOutput || !stdinIsTerminal() {
					res.Message = "not signed: review the above, then run `monoagentcli org sign " + name + " --yes`"
					return printJSONValue(res)
				}
				if !confirmOrgSign(cmd.ErrOrStderr(), os.Stdin, name, review) {
					res.Message = "not signed (declined)"
					return printJSONValue(res)
				}
				expect = sha // sign exactly what was just reviewed
			}
			if m := orgsign.RoleContextMarker(); m != "" {
				return errInvalidInput("refusing to sign: %s is set — this is an org role or agent turn, and only the operator signs org definitions", m)
			}
			if expect == "" {
				expect = sha
			}
			out := orgsign.SignExact(ctx, orgSigner, root, name, expect)
			res.State, res.Detail, res.Signed = out.State, "", out.Signed
			if !out.Signed {
				return &orgSignFailed{org: name, out: out}
			}
			res.Message = "signed; a running org picks it up with `monoagentcli org reload " + name + "`"
			return printJSONValue(res)
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "Sign without asking (required when not on a terminal)")
	c.Flags().StringVar(&expect, "expect-sha256", "", "With --yes: sign only if the org file's sha256 is this (the reviewed file)")
	c.Flags().BoolVar(&statusOnly, "status", false, "Print the signature state only")
	return c
}

// errOrgSigningUnsupported: the installed monomind predates signed org
// definitions, so there is nothing to sign (the GUI ignores this code).
var errOrgSigningUnsupported error = &orgSigningUnsupported{}

type orgSigningUnsupported struct{}

func (*orgSigningUnsupported) Error() string {
	return "org signing needs monomind 2.21 or later (the installed monomind runs orgs unsigned)"
}

func (*orgSigningUnsupported) JSONErrorFields() map[string]any {
	return map[string]any{"code": "org_signing_unsupported"}
}

// orgSignFailed is an `org sign --yes` that did not sign: the notice says
// why (the file changed since the review, monomind refused, ...).
type orgSignFailed struct {
	org string
	out orgsign.Outcome
}

func (e *orgSignFailed) Error() string { return e.out.Notice }

func (e *orgSignFailed) JSONErrorFields() map[string]any {
	return map[string]any{"code": "org_not_signed", "org": e.org, "signature": e.out.State}
}

// confirmOrgSign shows the review on w and asks on in.
func confirmOrgSign(w io.Writer, in io.Reader, name, review string) bool {
	fmt.Fprintln(w, review)
	fmt.Fprintf(w, "\nSign org %q as the operator? [y/N] ", name)
	ans, _ := bufio.NewReader(in).ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	return ans == "y" || ans == "yes"
}

// withOrgSignature adds each org's signature state to `org status` JSON (a
// single {name,...} object or a {items:[...]} list) when monomind enforces
// signatures. The payload is returned unchanged on any decode problem.
func withOrgSignature(ctx context.Context, root string, payload json.RawMessage) json.RawMessage {
	if !orgSigningOn(ctx) {
		return payload
	}
	add := func(item map[string]interface{}) {
		name, _ := item["name"].(string)
		if !orgdesign.ValidOrgName(name) {
			return
		}
		if st, _, err := orgsign.VerifyFile(root, name); err == nil {
			item["signature"] = st
		}
	}
	var obj map[string]interface{}
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	if items, ok := obj["items"].([]interface{}); ok {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok {
				add(m)
			}
		}
	} else {
		add(obj)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return b
}
