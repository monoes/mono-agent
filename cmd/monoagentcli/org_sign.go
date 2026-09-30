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
	"time"

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

// orgSigningOn reports whether the monomind that runs in a project root
// enforces signatures (its own handshake, run in that root).
// A var so tests can switch it without a monomind.
var orgSigningOn = monomind.OrgSigningEnforced

type monomindOrgSigner struct{}

func (monomindOrgSigner) Sign(ctx context.Context, root, org, hash string) error {
	_, err := monomind.OrgSign(ctx, root, org, hash)
	return err
}

// EnforcesHash: monomind's own --expect-hash (2.22) refuses other content.
func (monomindOrgSigner) EnforcesHash(ctx context.Context, root string) bool {
	return monomind.OrgSignExpectsHash(ctx, root)
}

// Check is monomind's own verdict where it has one (2.22 --check).
func (monomindOrgSigner) Check(ctx context.Context, root, org string) (orgsign.Status, bool) {
	return monomind.OrgSignCheck(ctx, root, org)
}

// orgSignStatus is org's signature state: monomind's own check where it
// has one, else the Go check.
func orgSignStatus(ctx context.Context, root, org string, raw []byte) orgsign.Status {
	if c, ok := orgSigner.(orgsign.Checker); ok {
		if st, ok := c.Check(ctx, root, org); ok {
			return st
		}
	}
	return orgsign.Verify(root, org, raw)
}

// orgSignCheckAll is monomind.OrgSignCheckAll; a var so tests can stand
// in for it.
var orgSignCheckAll = monomind.OrgSignCheckAll

// orgSigner signs through monomind; a var so tests can stand one in.
var orgSigner orgsign.Signer = monomindOrgSigner{}

// saveOrgSigned writes doc like orgdesign.Save and keeps its signature:
// from is the org file the document replaces (a rename's old name). A new
// org is never signed here — a whole document can come from anyone, the
// chat assistant included — so the user reviews it with `org sign`.
// The outcome is nil when monomind does not enforce signatures.
func saveOrgSigned(ctx context.Context, root, from string, doc *orgdesign.Doc) (string, *orgsign.Outcome, error) {
	if !orgSigningOn(ctx, root) {
		sha, err := orgdesign.Save(root, doc)
		return sha, nil, err
	}
	pre := orgsign.Before(ctx, orgSigner, root, from, doc.LoadedSHA(), false)
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
	sha, out, err := saveOrgSigned(ctx, root, doc.Name, doc)
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
	// Hash is the reviewed definition's projection hash (instructions
	// files included): what --expect-hash signs.
	Hash string `json:"hash,omitempty"`
	// BlockedBy names the agent-context marker (CLAUDECODE, ...) this
	// process inherited with no terminal: it can't sign here (the app
	// started from an AI-agent shell), so callers say so up front.
	BlockedBy string `json:"blocked_by,omitempty"`
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
			"--yes signs (with --expect-hash, only the definition that was reviewed, instructions files " +
			"included). --status prints " +
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
			if !orgSigningOn(ctx, root) {
				if statusOnly {
					res.Message = "the installed monomind does not require signed org definitions (2.21+)"
					return printJSONValue(res)
				}
				return errOrgSigningUnsupported
			}
			res.Supported = true
			if !stdinIsTerminal() {
				res.BlockedBy = orgsign.AgentContextMarker()
			}
			st := orgSignStatus(ctx, root, name, raw)
			res.State, res.Detail = st.State, st.Detail
			if statusOnly {
				if !st.OK() {
					res.Message = orgsign.Message(name, st)
				}
				return printJSONValue(res)
			}
			var note string
			if !yes {
				res.Hash, res.Review, note, err = reviewOrg(ctx, root, name)
				if err != nil {
					return err
				}
				if env.cfg.JSONOutput || !stdinIsTerminal() {
					res.Message = "not signed: review the above, then run `monoagentcli org sign " + name + " --yes --expect-hash " + res.Hash + "`"
					if res.Hash == "" {
						res.Message = "not signed: " + note
					}
					return printJSONValue(res)
				}
				if !confirmOrgSign(cmd.ErrOrStderr(), os.Stdin, name, res.Review) {
					res.Message = "not signed (declined)"
					return printJSONValue(res)
				}
				if res.Hash == "" {
					return errInvalidInput("org %s: not signed: %s", name, note)
				}
				expect = res.Hash // sign exactly what was just reviewed
			}
			if m := orgsign.RoleContextMarker(); m != "" {
				return errInvalidInput("refusing to sign: %s is set — this is an org role or agent turn, and only the operator signs org definitions", m)
			}
			// A coding agent (the chat assistant included) runs commands with
			// no terminal: under its markers, --yes needs a person at a TTY.
			if m := orgsign.AgentContextMarker(); m != "" && yes && !stdinIsTerminal() {
				return &orgSignBlocked{marker: m, org: name}
			}
			if cmd.Flags().Changed("expect-hash") && expect == "" {
				// A review that couldn't vouch for its hash hands over "":
				// never read that as "sign whatever is there now".
				return errInvalidInput("org %s: --expect-hash is empty: the review could not tell what it showed — review it again", name)
			}
			if expect == "" {
				expect, _ = orgsign.Hash(root, raw)
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
	c.Flags().StringVar(&expect, "expect-hash", "", "With --yes: sign only if the definition's hash (the review's \"hash\", instructions files included) is this")
	c.Flags().BoolVar(&statusOnly, "status", false, "Print the signature state only")
	return c
}

// reviewOrg is monomind's review of org and the hash of exactly the
// definition it reviewed — or "" with a note saying why that can't be told
// (#295 review).
//   - monomind 2.22 reports that hash itself, from the same read as the
//     review; it is the signing target (its --expect-hash refuses anything
//     else).
//   - On 2.21 it is this package's hash, taken around monomind's read.
//
// The org JSON, every instructions file and the directories above them
// are stamped before and after the review and must not have moved: a file
// or folder swapped in for monomind's read and put back after would
// otherwise show one definition and sign another. Where the stamps can't
// vouch for the files (orgsign.Untrusted) only monomind's own reviewed
// hash, enforced by its --expect-hash, counts. A review during which only
// directory times moved (something writing into .monomind) is run again,
// up to twice; it counts only once it comes back unchanged.
func reviewOrg(ctx context.Context, root, name string) (hash, review, note string, err error) {
	const changed = "the definition changed during the review — review it again"
	for attempt := 0; ; attempt++ {
		before, stampErr := orgsign.StampDefinition(root, name)
		raw, _, readErr := orgsign.ReadFile(root, name)
		h, hashErr := orgsign.Hash(root, raw)
		rv, err := monomind.OrgSignReview(ctx, root, name)
		if err != nil {
			return "", "", "", err
		}
		after, stampErr2 := orgsign.StampDefinition(root, name)
		raw2, _, readErr2 := orgsign.ReadFile(root, name)
		h2, hashErr2 := orgsign.Hash(root, raw2)
		// Judged now, after this attempt's read: a folder turned into a
		// symlink between attempts must not ride on an earlier verdict.
		untrusted := orgsign.Untrusted(root, name)
		goAgrees := func(want string) bool {
			// Where Go can hash the definition, it must equal want at both
			// ends of the read.
			return (hashErr == nil) == (hashErr2 == nil) && (hashErr != nil || (h == want && h2 == want))
		}
		if untrusted != "" {
			if rv.Hash != "" && monomind.OrgSignExpectsHash(ctx, root) && goAgrees(rv.Hash) {
				return rv.Hash, rv.Text, "", nil
			}
			return "", rv.Text, untrusted, nil
		}
		if stampErr != nil || stampErr2 != nil || readErr != nil || readErr2 != nil || !before.Same(after) {
			busy := stampErr == nil && stampErr2 == nil && before.OnlyDirTimesMoved(after) &&
				hashErr == nil && hashErr2 == nil && h == h2
			if busy && attempt < reviewRetries {
				reviewSleep(time.Duration(attempt+1) * reviewRetryBackoff)
				continue
			}
			return "", rv.Text, changed, nil
		}
		if rv.Hash != "" {
			if !goAgrees(rv.Hash) {
				return "", rv.Text, changed, nil
			}
			return rv.Hash, rv.Text, "", nil
		}
		if hashErr != nil || hashErr2 != nil {
			return "", rv.Text, "its hash can't be computed here — sign it with `monomind org sign " + name + "` in a terminal", nil
		}
		if h != h2 {
			return "", rv.Text, changed, nil
		}
		return h, rv.Text, "", nil
	}
}

// reviewRetries and reviewRetryBackoff: a review whose only change was a
// directory time (a running org writing into .monomind) is run again up to
// reviewRetries times, waiting a little longer each time. vars for tests.
var (
	reviewRetries      = 2
	reviewRetryBackoff = 250 * time.Millisecond
	reviewSleep        = time.Sleep
)

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

// orgSignBlocked is `org sign --yes` refused under an agent-context marker
// with no terminal: a coding agent runs it, or the app was started from an
// AI-agent shell and inherited the marker (it is never stripped).
type orgSignBlocked struct{ marker, org string }

func (e *orgSignBlocked) Error() string {
	return fmt.Sprintf("refusing to sign: %s is set and there is no terminal, so an AI agent may be running this "+
		"(or the app was started from an AI-agent shell). Sign it yourself: `monoagentcli org sign %s` in a normal terminal, "+
		"or the app started normally", e.marker, e.org)
}

func (e *orgSignBlocked) JSONErrorFields() map[string]any {
	return map[string]any{"code": "org_sign_agent_context", "org": e.org, "blocked_by": e.marker}
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
	if !orgSigningOn(ctx, root) {
		return payload
	}
	// One monomind run answers for every org (2.22 --check --all).
	all, haveAll := orgSignCheckAll(ctx, root)
	add := func(item map[string]interface{}) {
		name, _ := item["name"].(string)
		if !orgdesign.ValidOrgName(name) {
			return
		}
		if st, ok := all[name]; haveAll && ok {
			item["signature"] = st
		} else if raw, _, err := orgsign.ReadFile(root, name); err == nil {
			item["signature"] = orgsign.Verify(root, name, raw)
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
