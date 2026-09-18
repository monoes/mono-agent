package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/storage"
)

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
	// FileInputNodes are nodes whose file paths can come from the run's
	// input (C-46); the grant dialog warns about them.
	FileInputNodes []orggrant.FileInputNode `json:"file_input_nodes"`
}

func viewAutomation(ctx context.Context, db *storage.Database, profileID string, a orgdesign.AutomationRef) automationView {
	v := automationView{WorkflowID: a.WorkflowID, Alias: a.Alias, Owned: a.Owned, OutboundNodes: []string{}, FileInputNodes: []orggrant.FileInputNode{}}
	if wf, err := orgWorkflowIn(ctx, db, profileID, a.WorkflowID); err == nil {
		v.Exists = true
		v.WorkflowName = wf.Name
		if fi := orggrant.FileInputNodes(wf); len(fi) > 0 {
			v.FileInputNodes = fi
		}
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
