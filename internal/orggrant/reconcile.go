package orggrant

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// ManagedToolPrefix marks policy.approvalTools entries mono-agent owns.
const ManagedToolPrefix = orgdesign.ProviderName + "__"

// GenOptions carries what Reconcile needs to write generated blocks.
type GenOptions struct {
	ProfileID string
	CLIPath   string // absolute path of monoagentcli, the provider command
	APIAddr   string // host:port endpoint URLs point at
}

// Finding is one change or problem Reconcile reports.
type Finding struct {
	Kind   string `json:"kind"`
	Role   string `json:"role,omitempty"`
	Detail string `json:"detail"`
}

// Finding kinds.
const (
	FindingGrantRevoked         = "grant_revoked"
	FindingGrantSpecStripped    = "grant_spec_stripped"
	FindingGrantSpecUpdated     = "grant_spec_updated"
	FindingProviderStripped     = "provider_stripped"
	FindingProviderWritten      = "provider_written"
	FindingApprovalToolsUpdated = "approval_tools_updated"
	FindingOrgToolsNarrowed     = "org_tools_narrowed"
	FindingEndpointRevoked      = "endpoint_revoked"
	FindingEndpointUnregistered = "endpoint_unregistered"
	FindingEndpointURLUpdated   = "endpoint_url_updated"
)

// Report is the outcome of one Reconcile.
type Report struct {
	Org      string    `json:"org"`
	Changed  bool      `json:"changed"` // the doc was modified and needs saving
	Findings []Finding `json:"findings"`
}

func (r *Report) add(kind, role, format string, args ...interface{}) {
	r.Findings = append(r.Findings, Finding{Kind: kind, Role: role, Detail: fmt.Sprintf(format, args...)})
}

// Reconcile brings doc and the enforcement rows into agreement without ever
// creating power from the JSON (C-3):
//   - a live grant whose role, membership entry, or display copy vanished
//     from doc is revoked;
//   - a display copy with no live grant is stripped, and a kept one is
//     rewritten from its row;
//   - the generated "monoagent" tool provider and the managed
//     policy.approvalTools entries are regenerated from live rows;
//   - any other provider that runs monoagentcli or passes --grant is
//     stripped;
//   - endpoint rows whose role is gone (or no longer an automation role)
//     are revoked, and automation roles' URLs are pointed at their live row.
//
// Reconcile mutates doc in memory; callers save it when Report.Changed.
func Reconcile(ctx context.Context, s *Store, doc *orgdesign.Doc, opts GenOptions) (*Report, error) {
	rep := &Report{Org: doc.Name, Findings: []Finding{}}
	before, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	grants, err := s.ListGrants(ctx, opts.ProfileID, doc.Name, "")
	if err != nil {
		return nil, err
	}

	// 1. Revoke rows the JSON no longer backs.
	live := map[string][]Grant{} // role -> live grants
	for _, g := range grants {
		role, _ := doc.FindRole(g.RoleID)
		t := g.Automation()
		reason := ""
		switch {
		case role == nil:
			reason = "role no longer exists"
		case role.IsEndpoint():
			reason = "role is now an automation role"
		case t != nil && doc.FindAutomation(t.Alias) == nil:
			reason = fmt.Sprintf("automation %q was removed from the org", t.Alias)
		case t != nil && doc.FindAutomation(t.Alias).WorkflowID != t.WorkflowID:
			reason = fmt.Sprintf("automation %q now points at a different workflow", t.Alias)
		case t != nil && role.FindGrantSpec(t.Alias) == nil:
			reason = fmt.Sprintf("grant of %q was removed from the role", t.Alias)
		}
		if reason != "" {
			if err := s.revokeByID(ctx, g.ID); err != nil {
				return nil, err
			}
			rep.add(FindingGrantRevoked, g.RoleID, "revoked %s: %s", g.ID, reason)
			continue
		}
		live[g.RoleID] = append(live[g.RoleID], g)
	}

	// 2. Narrow org-tool scopes to the orgs doc still names.
	if err := narrowOrgToolScope(ctx, s, rep, doc, opts, live); err != nil {
		return nil, err
	}

	// 3–5. Per role: display copies, provider block, approvalTools.
	for i := range doc.Roles {
		r := &doc.Roles[i]
		roleGrants := live[r.ID]
		syncGrantSpecs(rep, r, roleGrants)
		syncProviders(rep, r, roleGrants, opts)
		syncApprovalTools(rep, r, roleGrants)
	}

	// 6. Endpoints.
	if err := syncEndpoints(ctx, s, rep, doc, opts); err != nil {
		return nil, err
	}

	after, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	rep.Changed = string(before) != string(after)
	return rep, nil
}

// narrowOrgToolScope drops every org from a role's org-tool grant that doc
// no longer connects the role to. A role is only ever given org tools over
// its own org (a boss decider) or over a child of its holding org (the
// Initiator, a parent decider), so anything else is left over from a config
// that has since changed: nothing else shrinks these grants — MergeOrgTools
// only adds — so removing a child from `children` otherwise left the
// Initiator holding org_stop, org_status and org_report over it for good.
// It also drops the grant entirely once nothing is left in scope.
func narrowOrgToolScope(ctx context.Context, s *Store, rep *Report, doc *orgdesign.Doc, opts GenOptions, live map[string][]Grant) error {
	named := map[string]bool{doc.Name: true}
	for _, c := range doc.ChildOrgs {
		named[c.Org] = true
	}
	for roleID, grants := range live {
		kept := grants[:0:0]
		var dropped []string
		for _, g := range grants {
			if len(g.OrgTools) == 0 {
				kept = append(kept, g)
				continue
			}
			var tools []OrgTool
			var gone []string
			for _, ot := range g.OrgTools {
				narrowed := OrgTool{Tool: ot.Tool}
				for _, o := range ot.Orgs {
					if named[o] {
						narrowed.Orgs = append(narrowed.Orgs, o)
						continue
					}
					gone = append(gone, ot.Tool+" over "+o)
				}
				if len(narrowed.Orgs) > 0 {
					tools = append(tools, narrowed)
				}
			}
			if len(gone) == 0 {
				kept = append(kept, g)
				continue
			}
			dropped = append(dropped, gone...)
			if _, err := s.SetOrgTools(ctx, opts.ProfileID, doc.Name, roleID, tools); err != nil {
				return err
			}
			if len(tools) > 0 {
				g.OrgTools = tools
				kept = append(kept, g)
			}
		}
		if len(dropped) == 0 {
			continue
		}
		sort.Strings(dropped)
		rep.add(FindingOrgToolsNarrowed, roleID, "dropped %s: %s no longer names that org as itself or a child", strings.Join(uniqueSorted(dropped), ", "), doc.Name)
		live[roleID] = kept
	}
	return nil
}

func syncGrantSpecs(rep *Report, r *orgdesign.Role, grants []Grant) {
	byAlias := map[string]*Tool{}
	for i := range grants {
		if t := grants[i].Automation(); t != nil {
			byAlias[t.Alias] = t
		}
	}
	kept := r.Automations[:0:0]
	for _, spec := range r.Automations {
		t, ok := byAlias[spec.Alias]
		if !ok {
			rep.add(FindingGrantSpecStripped, r.ID, "stripped automation %q: no live grant", spec.Alias)
			continue
		}
		want := SpecFromTool(*t, spec)
		if !reflect.DeepEqual(want, spec) {
			rep.add(FindingGrantSpecUpdated, r.ID, "rewrote automation %q from its grant", spec.Alias)
		}
		kept = append(kept, want)
	}
	if len(kept) == 0 {
		kept = nil
	}
	r.Automations = kept
}

// SpecFromTool renders a grant row as its JSON display copy, keeping the
// previous copy's input_schema and unknown keys.
func SpecFromTool(t Tool, prev orgdesign.GrantSpec) orgdesign.GrantSpec {
	wait := t.Wait
	return orgdesign.GrantSpec{
		Alias:          t.Alias,
		Mode:           t.Mode,
		Wait:           &wait,
		TimeoutSeconds: t.Timeout,
		Approval:       t.Approval,
		InputSchema:    prev.InputSchema,
		MaxCallsPerRun: t.MaxCallsPerRun,
		MaxCallsPerDay: t.MaxCallsPerDay,
		MaxOutputBytes: t.MaxOutputBytes,
		Extra:          prev.Extra,
	}
}

// ProviderToolNames lists the MCP tool names a role's grants expose, as
// the provider's allow list (bare names, without the monoagent__ prefix).
func ProviderToolNames(grants []Grant) []string {
	var names []string
	hasAutomation := false
	for _, g := range grants {
		if t := g.Automation(); t != nil {
			names = append(names, t.Tool)
			hasAutomation = true
		}
		for _, ot := range g.OrgTools {
			names = append(names, ot.Tool)
		}
	}
	if hasAutomation {
		names = append(names, "automation_status", "automation_output")
	}
	sort.Strings(names)
	return uniqueSorted(names)
}

func uniqueSorted(in []string) []string {
	out := in[:0:0]
	for i, s := range in {
		if i > 0 && s == in[i-1] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// GenerateProvider builds the monoagent tool provider for a role's live
// grants (contracts §7), or nil when there are none.
func GenerateProvider(grants []Grant, opts GenOptions) *orgdesign.ToolProvider {
	if len(grants) == 0 {
		return nil
	}
	maxTimeout := 0
	for _, g := range grants {
		if t := g.Automation(); t != nil && t.Timeout > maxTimeout {
			maxTimeout = t.Timeout
		}
	}
	if maxTimeout == 0 {
		maxTimeout = DefaultTimeoutSeconds
	}
	return &orgdesign.ToolProvider{
		Kind:      "mcp-stdio",
		Name:      orgdesign.ProviderName,
		Command:   opts.CLIPath,
		Args:      []string{"mcp", "--grant", grants[0].ID, "--profile", opts.ProfileID},
		Allow:     ProviderToolNames(grants),
		Prefix:    orgdesign.ProviderName,
		TimeoutMS: maxTimeout*1000 + 30000,
	}
}

func runsMonoagent(p orgdesign.ToolProvider) bool {
	base := strings.ToLower(filepath.Base(p.Command))
	if strings.HasPrefix(base, "monoagentcli") {
		return true
	}
	for _, a := range p.Args {
		if a == "--grant" || strings.HasPrefix(a, "--grant=") || strings.HasPrefix(a, "grt_") {
			return true
		}
	}
	return false
}

func syncProviders(rep *Report, r *orgdesign.Role, grants []Grant, opts GenOptions) {
	gen := GenerateProvider(grants, opts)
	var out []orgdesign.ToolProvider
	var prevGen *orgdesign.ToolProvider
	for _, p := range r.ToolProviders {
		if p.Name == orgdesign.ProviderName {
			pp := p
			prevGen = &pp
			continue
		}
		if runsMonoagent(p) {
			rep.add(FindingProviderStripped, r.ID, "stripped tool provider %q: only mono-agent may add providers that run monoagentcli", p.Name)
			continue
		}
		out = append(out, p)
	}
	if gen != nil {
		out = append([]orgdesign.ToolProvider{*gen}, out...)
		if prevGen == nil || !reflect.DeepEqual(normalizeProvider(*prevGen), normalizeProvider(*gen)) {
			rep.add(FindingProviderWritten, r.ID, "wrote tool provider %q for %d grant(s)", orgdesign.ProviderName, len(grants))
		}
	} else if prevGen != nil {
		rep.add(FindingProviderStripped, r.ID, "stripped tool provider %q: role has no live grants", orgdesign.ProviderName)
	}
	if len(out) == 0 {
		out = nil
	}
	r.ToolProviders = out
}

func normalizeProvider(p orgdesign.ToolProvider) orgdesign.ToolProvider {
	p.Extra = nil
	if len(p.Env) == 0 {
		p.Env = nil
	}
	return p
}

func syncApprovalTools(rep *Report, r *orgdesign.Role, grants []Grant) {
	if r.IsEndpoint() {
		return
	}
	current := r.PolicyStrings("approvalTools")
	var want []string
	for _, name := range current {
		if !strings.HasPrefix(name, ManagedToolPrefix) {
			want = append(want, name)
		}
	}
	for _, g := range grants {
		if t := g.Automation(); t != nil && t.Approval == "required" {
			want = append(want, ManagedToolPrefix+t.Tool)
		}
		for _, ot := range g.OrgTools {
			if ot.Tool == "org_start" {
				want = append(want, ManagedToolPrefix+ot.Tool)
			}
		}
	}
	sort.Strings(want)
	want = uniqueSorted(want)
	sortedCurrent := append([]string(nil), current...)
	sort.Strings(sortedCurrent)
	if strings.Join(sortedCurrent, "\x00") == strings.Join(want, "\x00") {
		return
	}
	r.SetPolicyStrings("approvalTools", want)
	rep.add(FindingApprovalToolsUpdated, r.ID, "approvalTools now %v", want)
}

func syncEndpoints(ctx context.Context, s *Store, rep *Report, doc *orgdesign.Doc, opts GenOptions) error {
	rows, err := s.ListEndpoints(ctx, opts.ProfileID, doc.Name)
	if err != nil {
		return err
	}
	byRole := map[string]*EndpointRow{}
	for i := range rows {
		row := &rows[i]
		role, _ := doc.FindRole(row.RoleID)
		if role == nil || !role.IsEndpoint() {
			if err := s.RevokeEndpoint(ctx, opts.ProfileID, doc.Name, row.RoleID); err != nil {
				return err
			}
			rep.add(FindingEndpointRevoked, row.RoleID, "revoked endpoint of %q: no automation role with that id", row.RoleID)
			continue
		}
		byRole[row.RoleID] = row
	}
	for i := range doc.Roles {
		r := &doc.Roles[i]
		if !r.IsEndpoint() {
			continue
		}
		row := byRole[r.ID]
		if row == nil {
			rep.add(FindingEndpointUnregistered, r.ID, "automation role %q has no registered endpoint; deliveries to it are refused (recreate it with `org automation-role add`)", r.ID)
			continue
		}
		want := EndpointURL(opts.APIAddr, row.ID)
		if r.Endpoint == nil {
			r.Endpoint = &orgdesign.Endpoint{}
		}
		if r.Endpoint.URL != want {
			r.Endpoint.URL = want
			rep.add(FindingEndpointURLUpdated, r.ID, "pointed endpoint URL at its registered id")
		}
	}
	return nil
}
