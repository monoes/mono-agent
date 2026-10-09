package monomind

import (
	"context"
	"encoding/json"
	"fmt"
)

// ResolveOptions attributes a resolution (capability
// org-decision-attribution, M5). On older monomind both fields are dropped
// and callers put "[decided by …]" into the resolution text instead.
type ResolveOptions struct {
	By        string // resolver, e.g. "model:claude-fable-5-1", "rule", "human"
	RequestID string // approvals only: resolve just this request
}

// attributionArgs returns the M5 flags when monomind supports them.
func attributionArgs(ctx context.Context, opts ResolveOptions, withRequest bool) []string {
	if opts.By == "" && opts.RequestID == "" {
		return nil
	}
	caps, err := Capabilities(ctx)
	if err != nil || !caps.Has(CapOrgDecisionAttribution) {
		return nil
	}
	var args []string
	if opts.By != "" {
		args = append(args, "--by", opts.By)
	}
	if withRequest && opts.RequestID != "" {
		args = append(args, "--request", opts.RequestID)
	}
	return args
}

// SupportsAttribution reports whether the installed monomind records
// resolvers itself (M5).
func SupportsAttribution(ctx context.Context) bool {
	caps, err := Capabilities(ctx)
	return err == nil && caps.Has(CapOrgDecisionAttribution)
}

// OrgApproveWith approves a pending approval, attributed.
func OrgApproveWith(ctx context.Context, projectRoot, name, role, action string, opts ResolveOptions) (json.RawMessage, error) {
	args := append([]string{"approve", name, role, action}, attributionArgs(ctx, opts, true)...)
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgDenyWith denies a pending approval, attributed.
func OrgDenyWith(ctx context.Context, projectRoot, name, role, action string, opts ResolveOptions) (json.RawMessage, error) {
	args := append([]string{"deny", name, role, action}, attributionArgs(ctx, opts, true)...)
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgAnswerWith answers a pending ask_human question, attributed.
func OrgAnswerWith(ctx context.Context, projectRoot, name, questionID, answer string, opts ResolveOptions) (json.RawMessage, error) {
	cmd := append([]string{"answer"}, attributionArgs(ctx, opts, false)...)
	return runOrgJSONText(ctx, projectRoot, cmd, name, questionID, answer)
}

// orgDismissMinVersion is the monomind that has `org questions dismiss`
// (#572). It advertises no capability for it, so it is gated on the version.
const orgDismissMinVersion = "2.21.0"

// OrgDismissQuestionWith closes a pending ask_human question without an
// answer, attributed (--by). reason may be empty; it is passed as
// --reason=<text> so a leading dash is not read as a flag. Refused before
// anything runs when this monomind predates the command.
func OrgDismissQuestionWith(ctx context.Context, projectRoot, name, questionID, reason string, opts ResolveOptions) (json.RawMessage, error) {
	caps, err := Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if !versionAtLeast(caps.Version, orgDismissMinVersion) {
		return nil, fmt.Errorf("monomind %s cannot dismiss questions (needs %s or newer)", caps.Version, orgDismissMinVersion)
	}
	cmd := []string{"questions", "dismiss"}
	if opts.By != "" {
		cmd = append(cmd, "--by", opts.By)
	}
	if reason != "" {
		cmd = append(cmd, "--reason="+reason)
	}
	return runOrgJSONText(ctx, projectRoot, cmd, name, questionID)
}

// OrgGateResolveWith approves or rejects a gate, attributed.
func OrgGateResolveWith(ctx context.Context, projectRoot, name, gateID string, approve bool, resolution string, opts ResolveOptions) (json.RawMessage, error) {
	sub := "gate-reject"
	if approve {
		sub = "gate-approve"
	}
	values := []string{name, gateID}
	if resolution != "" {
		values = append(values, resolution)
	}
	return runOrgJSONText(ctx, projectRoot, append([]string{sub}, attributionArgs(ctx, opts, false)...), values...)
}
