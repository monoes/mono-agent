package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// ─── shared helpers ──────────────────────────────────────────────────────

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// docCarriesEnforcedKeys reports whether saving d needs the grant store:
// anything reconcile would have to back with a row.
func docCarriesEnforcedKeys(d *orgdesign.Doc) bool {
	for _, r := range d.Roles {
		if len(r.Automations) > 0 || len(r.ToolProviders) > 0 || r.IsEndpoint() {
			return true
		}
	}
	return false
}

// orgWorkflowIn loads a workflow and checks it belongs to profileID.
// Workflows without a profile (legacy, file-store) count as the default
// profile's.
func orgWorkflowIn(ctx context.Context, db *storage.Database, profileID, id string) (*workflow.Workflow, error) {
	wf, err := newHybridStore(db).GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, fmt.Errorf("workflow %q not found", id)
	}
	owner := wf.ProfileID
	if owner == "" {
		owner = "default"
	}
	if owner != profileID {
		return nil, fmt.Errorf("workflow %q belongs to profile %q, not %q (v1 grants stay inside one profile)", id, owner, profileID)
	}
	return wf, nil
}

func loadOrgForEdit(env *orgEnv, name string) (*storage.Database, string, string, *orgdesign.Doc, error) {
	db, profileID, root, err := env.Profile()
	if err != nil {
		return nil, "", "", nil, err
	}
	doc, err := orgdesign.Load(root, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", "", nil, fmt.Errorf("org %q not found under %s", name, orgdesign.OrgsDir(root))
		}
		return nil, "", "", nil, err
	}
	return db, profileID, root, doc, nil
}

// grantCapabilityWarnings explains, without failing the command, when the
// installed monomind cannot yet deliver what was just configured.
func capabilityWarnings(ctx context.Context, cap, feature string) []string {
	set, err := monomind.Capabilities(ctx)
	if err != nil {
		return []string{fmt.Sprintf("%s: monomind unavailable (%v)", feature, err)}
	}
	if err := set.Require(cap, feature); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// ─── org automation ──────────────────────────────────────────────────────

func newOrgAutomationCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "automation",
		Short: "Manage the workflows that belong to an org (its automations)",
	}
	cmd.AddCommand(newOrgAutomationListCmd(env), newOrgAutomationUnassignedCmd(env),
		newOrgAutomationAddCmd(env), newOrgAutomationRemoveCmd(env))
	return cmd
}

type automationView struct {
	WorkflowID       string   `json:"workflow_id"`
	Alias            string   `json:"alias"`
	Owned            bool     `json:"owned"`
	WorkflowName     string   `json:"workflow_name"`
	HasOutboundNodes bool     `json:"has_outbound_nodes"`
	OutboundNodes    []string `json:"outbound_nodes"`
	Exists           bool     `json:"exists"`
}

func viewAutomation(ctx context.Context, db *storage.Database, profileID string, a orgdesign.AutomationRef) automationView {
	v := automationView{WorkflowID: a.WorkflowID, Alias: a.Alias, Owned: a.Owned, OutboundNodes: []string{}}
	if wf, err := orgWorkflowIn(ctx, db, profileID, a.WorkflowID); err == nil {
		v.Exists = true
		v.WorkflowName = wf.Name
		if out := orggrant.OutboundNodes(wf); len(out) > 0 {
			v.OutboundNodes = out
			v.HasOutboundNodes = true
		}
	}
	return v
}

func newOrgAutomationListCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "list <org>",
		Short: "List an org's automations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, _, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			items := []automationView{}
			for _, a := range doc.Automations {
				items = append(items, viewAutomation(cmd.Context(), db, profileID, a))
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "automations": items})
		},
	}
}

func newOrgAutomationUnassignedCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "unassigned",
		Short: "List workflows in this profile that belong to no org",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			docs, _, err := orgdesign.LoadAll(root)
			if err != nil {
				return err
			}
			member := map[string]bool{}
			for _, d := range docs {
				for _, a := range d.Automations {
					member[a.WorkflowID] = true
				}
			}
			wfs, err := newHybridStore(db).ListWorkflows(cmd.Context(), profileID)
			if err != nil {
				return err
			}
			type item struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				IsActive bool   `json:"is_active"`
			}
			out := []item{}
			for _, wf := range wfs {
				owner := wf.ProfileID
				if owner == "" {
					owner = "default"
				}
				if owner != profileID || member[wf.ID] {
					continue
				}
				out = append(out, item{ID: wf.ID, Name: wf.Name, IsActive: wf.IsActive})
			}
			sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
			return printJSONValue(map[string]interface{}{"v": 1, "workflows": out})
		},
	}
}

func newOrgAutomationAddCmd(env *orgEnv) *cobra.Command {
	var workflowID, alias string
	var owned bool
	c := &cobra.Command{
		Use:   "add <org>",
		Short: "Add a workflow to an org as an automation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			if !orgdesign.ValidAlias(alias) {
				return fmt.Errorf("alias %q must be lowercase letters, digits, and underscores, start with a letter, and not be human, workflow, status, or output", alias)
			}
			if doc.FindAutomation(alias) != nil {
				return fmt.Errorf("org %q already has an automation called %q", doc.Name, alias)
			}
			if r, _ := doc.FindRole(alias); r != nil {
				return fmt.Errorf("alias %q is already a role id in org %q", alias, doc.Name)
			}
			if _, err := orgWorkflowIn(ctx, db, profileID, workflowID); err != nil {
				return err
			}
			for _, a := range doc.Automations {
				if a.WorkflowID == workflowID {
					return fmt.Errorf("workflow %q is already automation %q of org %q", workflowID, a.Alias, doc.Name)
				}
			}
			if owned {
				docs, _, err := orgdesign.LoadAll(root)
				if err != nil {
					return err
				}
				for _, other := range docs {
					if other.Name == doc.Name {
						continue
					}
					for _, a := range other.Automations {
						if a.WorkflowID == workflowID && a.Owned {
							return fmt.Errorf("workflow %q is already owned by org %q; an automation has at most one owner", workflowID, other.Name)
						}
					}
				}
			}
			ref := orgdesign.AutomationRef{WorkflowID: workflowID, Alias: alias, Owned: owned}
			doc.Automations = append(doc.Automations, ref)
			prefillFence(doc)
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "automation": viewAutomation(ctx, db, profileID, ref)})
		},
	}
	c.Flags().StringVar(&workflowID, "workflow", "", "Workflow id")
	c.Flags().StringVar(&alias, "alias", "", "Name the org uses for it (becomes the tool automation_<alias>)")
	c.Flags().BoolVar(&owned, "owned", false, "This org owns the workflow's lifecycle")
	_ = c.MarkFlagRequired("workflow")
	_ = c.MarkFlagRequired("alias")
	return c
}

// prefillFence turns on monomind's message fence for an org the first time
// it gains an automation: automation replies are untrusted content (§8
// layer 7). An operator who removes the fence key keeps it removed — this
// only fills a missing key.
func prefillFence(doc *orgdesign.Doc) {
	if _, ok := doc.Extra["fence"]; ok {
		return
	}
	if doc.Extra == nil {
		doc.Extra = map[string]json.RawMessage{}
	}
	doc.Extra["fence"] = json.RawMessage(`{"enabled":true,"scanMessages":true}`)
}

func newOrgAutomationRemoveCmd(env *orgEnv) *cobra.Command {
	var alias string
	c := &cobra.Command{
		Use:   "remove <org>",
		Short: "Remove an automation from an org, revoking every grant to it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			ref := doc.FindAutomation(alias)
			if ref == nil {
				return fmt.Errorf("org %q has no automation %q", doc.Name, alias)
			}
			for _, r := range doc.Roles {
				if r.IsEndpoint() && r.Automation != nil && r.Automation.WorkflowID == ref.WorkflowID {
					return fmt.Errorf("automation role %q runs this workflow; remove it first with `org automation-role remove %s --role %s`", r.ID, doc.Name, r.ID)
				}
			}
			kept := doc.Automations[:0:0]
			for _, a := range doc.Automations {
				if a.Alias != alias {
					kept = append(kept, a)
				}
			}
			doc.Automations = kept
			for i := range doc.Roles {
				specs := doc.Roles[i].Automations[:0:0]
				for _, g := range doc.Roles[i].Automations {
					if g.Alias != alias {
						specs = append(specs, g)
					}
				}
				doc.Roles[i].Automations = specs
			}
			if _, err := orggrant.NewStore(db.DB).RevokeAlias(ctx, profileID, doc.Name, alias); err != nil {
				return err
			}
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "removed": true})
		},
	}
	c.Flags().StringVar(&alias, "alias", "", "Automation alias")
	_ = c.MarkFlagRequired("alias")
	return c
}

// ─── org grant ───────────────────────────────────────────────────────────

func newOrgGrantCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grant",
		Short: "Let org roles call automations as tools",
	}
	cmd.AddCommand(newOrgGrantListCmd(env), newOrgGrantAddCmd(env), newOrgGrantRemoveCmd(env))
	return cmd
}

type grantView struct {
	ID              string `json:"id"`
	Org             string `json:"org"`
	Role            string `json:"role"`
	Alias           string `json:"alias"`
	WorkflowID      string `json:"workflow_id"`
	WorkflowName    string `json:"workflow_name"`
	Mode            string `json:"mode"`
	Wait            bool   `json:"wait"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Approval        string `json:"approval"`
	Tier            string `json:"tier"`
	MaxCallsPerRun  int    `json:"max_calls_per_run"`
	MaxCallsPerDay  int    `json:"max_calls_per_day"`
	MaxOutputBytes  int    `json:"max_output_bytes"`
	CreatedAt       string `json:"created_at"`
	WorkflowMissing bool   `json:"workflow_missing,omitempty"`
}

func viewGrant(ctx context.Context, db *storage.Database, g orggrant.Grant) grantView {
	t := g.Automation()
	v := grantView{
		ID: g.ID, Org: g.OrgName, Role: g.RoleID, Alias: t.Alias, WorkflowID: t.WorkflowID,
		Mode: t.Mode, Wait: t.Wait, TimeoutSeconds: t.Timeout, Approval: t.Approval, Tier: t.Tier,
		MaxCallsPerRun: t.MaxCallsPerRun, MaxCallsPerDay: t.MaxCallsPerDay, MaxOutputBytes: t.MaxOutputBytes,
		CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
	}
	// The tier follows the workflow as it is now, not as it was at grant
	// time: adding an email node to a granted workflow raises its calls to
	// irreversible immediately.
	if wf, err := orgWorkflowIn(ctx, db, g.ProfileID, t.WorkflowID); err == nil {
		v.WorkflowName = wf.Name
		v.Tier = orggrant.GrantTier(orggrant.OutboundNodes(wf))
	} else {
		v.WorkflowMissing = true
	}
	return v
}

func newOrgGrantListCmd(env *orgEnv) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "list <org>",
		Short: "List automation grants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, _, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			grants, err := orggrant.NewStore(db.DB).ListGrants(cmd.Context(), profileID, doc.Name, role)
			if err != nil {
				return err
			}
			out := []grantView{}
			for _, g := range grants {
				if g.Automation() != nil {
					out = append(out, viewGrant(cmd.Context(), db, g))
				}
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "grants": out})
		},
	}
	c.Flags().StringVar(&role, "role", "", "Only this role's grants")
	return c
}

func newOrgGrantAddCmd(env *orgEnv) *cobra.Command {
	var role, alias, mode, approval string
	var wait bool
	var timeout, maxRun, maxDay, maxOut int
	c := &cobra.Command{
		Use:   "add <org>",
		Short: "Grant a role an automation as a tool (updates the grant when it exists)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			r, _ := doc.FindRole(role)
			if r == nil {
				return fmt.Errorf("org %q has no role %q", doc.Name, role)
			}
			if r.IsEndpoint() {
				return fmt.Errorf("role %q is an automation role; only agent roles hold grants", role)
			}
			ref := doc.FindAutomation(alias)
			if ref == nil {
				return fmt.Errorf("org %q has no automation %q; add it first with `org automation add`", doc.Name, alias)
			}
			switch mode {
			case "run", "trigger", "status":
			default:
				return fmt.Errorf("--mode must be run, trigger, or status")
			}
			wf, err := orgWorkflowIn(ctx, db, profileID, ref.WorkflowID)
			if err != nil {
				return err
			}
			outbound := orggrant.OutboundNodes(wf)
			if !cmd.Flags().Changed("approval") {
				// C-34: side effects outside mono-agent need a decision by default.
				approval = "none"
				if len(outbound) > 0 {
					approval = "required"
				}
			}
			if approval != "none" && approval != "required" {
				return fmt.Errorf("--approval must be none or required")
			}
			store := orggrant.NewStore(db.DB)
			existing, _ := store.ListGrants(ctx, profileID, doc.Name, role)

			tool := orggrant.Tool{
				Alias: alias, WorkflowID: ref.WorkflowID, Mode: mode, Wait: wait, Timeout: timeout,
				Approval: approval, Tier: orggrant.GrantTier(outbound),
				MaxCallsPerRun: maxRun, MaxCallsPerDay: maxDay, MaxOutputBytes: maxOut,
			}
			// Display copy first, so reconcile finds the JSON counterpart of
			// the row it is about to see.
			if spec := r.FindGrantSpec(alias); spec == nil {
				r.Automations = append(r.Automations, orgdesign.GrantSpec{Alias: alias})
			}
			g, err := store.UpsertGrant(ctx, orggrant.GrantInput{ProfileID: profileID, OrgName: doc.Name, RoleID: role, Tool: tool})
			if err != nil {
				return err
			}
			if len(existing) == 0 {
				// Q2: a role's first grant pre-fills denyTools Bash, because
				// Bash can run monoagentcli and bypass every grant (C-2). The
				// operator may remove it; later grants do not re-add it.
				deny := r.PolicyStrings("denyTools")
				if !containsString(deny, "Bash") {
					r.SetPolicyStrings("denyTools", append(deny, "Bash"))
				}
			}
			rep, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID))
			if err != nil {
				return err
			}
			var warnings []string
			warnings = append(warnings, capabilityWarnings(ctx, monomind.CapOrgToolProviders, "role automation tools")...)
			if !containsString(r.PolicyStrings("denyTools"), "Bash") {
				warnings = append(warnings, fmt.Sprintf("role %q can use Bash, which can run monoagentcli directly and bypass its grants", role))
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "org": doc.Name, "grant": viewGrant(ctx, db, *g),
				"tool_name": orgdesign.ProviderName + "__automation_" + alias,
				"applies":   "next org start",
				"reconcile": rep.Findings,
				"warnings":  nonNilStrings(warnings),
			})
		},
	}
	c.Flags().StringVar(&role, "role", "", "Role id")
	c.Flags().StringVar(&alias, "automation", "", "Automation alias")
	c.Flags().StringVar(&mode, "mode", "run", "run | trigger | status")
	c.Flags().BoolVar(&wait, "wait", true, "Wait for the run to finish and return its output")
	c.Flags().IntVar(&timeout, "timeout", orggrant.DefaultTimeoutSeconds, "Seconds to wait when --wait")
	c.Flags().StringVar(&approval, "approval", "none", "none | required (default: required when the workflow has outbound nodes)")
	c.Flags().IntVar(&maxRun, "max-calls-per-run", orggrant.DefaultMaxCallsPerRun, "Calls allowed per org run")
	c.Flags().IntVar(&maxDay, "max-calls-per-day", orggrant.DefaultMaxCallsPerDay, "Calls allowed per day")
	c.Flags().IntVar(&maxOut, "max-output-bytes", orggrant.DefaultMaxOutputBytes, "Output bytes returned to the role per call")
	_ = c.MarkFlagRequired("role")
	_ = c.MarkFlagRequired("automation")
	return c
}

func newOrgGrantRemoveCmd(env *orgEnv) *cobra.Command {
	var role, alias string
	c := &cobra.Command{
		Use:   "remove <org>",
		Short: "Revoke a role's grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			store := orggrant.NewStore(db.DB)
			if err := store.RevokeGrant(ctx, profileID, doc.Name, role, alias); err != nil {
				if errors.Is(err, orggrant.ErrNotFound) {
					return fmt.Errorf("role %q of org %q has no grant for %q", role, doc.Name, alias)
				}
				return err
			}
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "removed": true})
		},
	}
	c.Flags().StringVar(&role, "role", "", "Role id")
	c.Flags().StringVar(&alias, "automation", "", "Automation alias")
	_ = c.MarkFlagRequired("role")
	_ = c.MarkFlagRequired("automation")
	return c
}

// ─── org effective-tools ─────────────────────────────────────────────────

// orgToolNames are the tools monomind's buildOrgTools gives every agent
// role (session.ts), and bossOrgTools the ones only the boss gets. Kept here
// as a display list; monomind remains the authority.
var orgToolNames = []string{
	"org_send", "ask_human", "org_gate", "org_task", "org_task_done", "org_tasks",
	"org_task_split", "org_task_merge", "org_task_cancel", "org_task_block", "org_plan_graph",
	"org_recall", "org_remember", "org_learn",
}
var bossOrgTools = []string{"org_complete", "org_respawn_role", "org_list_runtime_options"}

// builtinRuntimeTools are the runtime's own tools a role has unless policy
// denies them.
var builtinRuntimeTools = []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash", "WebFetch", "WebSearch"}

func newOrgEffectiveToolsCmd(env *orgEnv) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "effective-tools <org>",
		Short: "List the tools a role's model sees: org tools, granted automations, and runtime tools",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, _, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			r, _ := doc.FindRole(role)
			if r == nil {
				return fmt.Errorf("org %q has no role %q", doc.Name, role)
			}
			type tool struct {
				Name        string `json:"name"`
				Source      string `json:"source"`
				Description string `json:"description"`
			}
			tools := []tool{}
			if r.IsEndpoint() {
				return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "role": role, "tools": tools})
			}
			deny := map[string]bool{}
			for _, d := range r.PolicyStrings("denyTools") {
				deny[d] = true
			}
			allow := r.PolicyStrings("allowTools")
			for _, n := range orgToolNames {
				tools = append(tools, tool{Name: n, Source: "org", Description: "monomind org tool"})
			}
			if r.ReportsTo == nil {
				for _, n := range bossOrgTools {
					tools = append(tools, tool{Name: n, Source: "org", Description: "monomind org tool (boss only)"})
				}
			}
			grants, err := orggrant.NewStore(db.DB).ListGrants(cmd.Context(), profileID, doc.Name, role)
			if err != nil {
				return err
			}
			for _, name := range orggrant.ProviderToolNames(grants) {
				desc := "granted automation tool"
				for _, g := range grants {
					if t := g.Automation(); t != nil && t.Tool == name {
						desc = fmt.Sprintf("runs automation %q (%s, approval %s)", t.Alias, t.Mode, t.Approval)
					}
				}
				if deny[orgdesign.ProviderName+"__"+name] {
					continue
				}
				tools = append(tools, tool{Name: orgdesign.ProviderName + "__" + name, Source: "grant", Description: desc})
			}
			for _, n := range builtinRuntimeTools {
				if deny[n] || (len(allow) > 0 && !containsString(allow, n)) {
					continue
				}
				tools = append(tools, tool{Name: n, Source: "builtin", Description: "runtime tool"})
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "role": role, "tools": tools})
		},
	}
	c.Flags().StringVar(&role, "role", "", "Role id")
	_ = c.MarkFlagRequired("role")
	return c
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
