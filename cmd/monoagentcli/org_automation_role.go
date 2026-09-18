package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

func newOrgAutomationRoleCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "automation-role",
		Short: "Put an automation in the org chart as a role agents message",
		Long: "An automation role is an org role backed by a workflow instead of an agent. Messages to it " +
			"start the workflow (needs `monoagentcli daemon`), and the workflow's result comes back as a reply. " +
			"Needs monomind with capability org-endpoint-roles.",
	}
	cmd.AddCommand(newOrgAutomationRoleAddCmd(env), newOrgAutomationRoleRemoveCmd(env), newOrgAutomationRoleRotateCmd(env))
	return cmd
}

func newOrgAutomationRoleAddCmd(env *orgEnv) *cobra.Command {
	var alias, reportsTo, title, reply string
	c := &cobra.Command{
		Use:   "add <org>",
		Short: "Add an automation role for one of the org's automations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			ref := doc.FindAutomation(alias)
			if ref == nil {
				return errInvalidInput("org %q has no automation %q; add it first with `org automation add`", doc.Name, alias)
			}
			parent, _ := doc.FindRole(reportsTo)
			if parent == nil {
				return errInvalidInput("org %q has no role %q to report to", doc.Name, reportsTo)
			}
			if parent.IsEndpoint() {
				return errInvalidInput("an automation role cannot report to another automation role")
			}
			if reply != "" && reply != "last_node" && !strings.HasPrefix(reply, "node:") {
				return errInvalidInput("--reply must be last_node or node:<name>")
			}
			wf, err := orgWorkflowIn(ctx, db, profileID, ref.WorkflowID)
			if err != nil {
				return err
			}
			for _, r := range doc.Roles {
				if r.IsEndpoint() && r.Automation != nil && r.Automation.WorkflowID == ref.WorkflowID {
					return errInvalidInput("automation role %q already runs this workflow", r.ID)
				}
			}
			if title == "" {
				title = wf.Name + " (automation)"
			}
			id := automationRoleID(doc, alias)
			ep, err := orggrant.NewStore(db.DB).CreateEndpoint(ctx, profileID, doc.Name, id, ref.WorkflowID)
			if err != nil {
				return err
			}
			parentID := parent.ID
			if reply == "" {
				reply = "last_node"
			}
			doc.Roles = append(doc.Roles, orgdesign.Role{
				ID: id, Title: title, Type: "automation", ReportsTo: &parentID, Responsibilities: []string{},
				Kind:       orgdesign.RoleKindEndpoint,
				Endpoint:   &orgdesign.Endpoint{URL: orggrant.EndpointURL(orgAPIAddr(db), ep.ID), InputHint: firstSentence(wf.Description)},
				Automation: &orgdesign.EndpointAutomation{WorkflowID: ref.WorkflowID, Reply: reply, Alias: alias},
			})
			prefillFence(doc)
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				_ = orggrant.NewStore(db.DB).RevokeEndpoint(ctx, profileID, doc.Name, id)
				return err
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "org": doc.Name,
				"role":         map[string]interface{}{"id": id, "title": title, "reports_to": parentID, "workflow_id": ref.WorkflowID},
				"endpoint_url": orggrant.EndpointURL(orgAPIAddr(db), ep.ID),
				"warnings":     nonNilStrings(capabilityWarnings(ctx, monomind.CapOrgEndpointRoles, "automation roles")),
			})
		},
	}
	c.Flags().StringVar(&alias, "alias", "", "Automation alias")
	c.Flags().StringVar(&reportsTo, "reports-to", "", "Role the automation role reports to")
	c.Flags().StringVar(&title, "title", "", "Role title (default: the workflow name)")
	c.Flags().StringVar(&reply, "reply", "last_node", "What the reply carries: last_node | node:<name>")
	_ = c.MarkFlagRequired("alias")
	_ = c.MarkFlagRequired("reports-to")
	return c
}

// automationRoleID derives the role id for an automation role from its
// alias. Role ids and automation aliases share one namespace (C-15), so an
// alias with no underscore ("formatter") gets a "-bot" suffix, and any id an
// existing role or alias already uses is numbered.
func automationRoleID(doc *orgdesign.Doc, alias string) string {
	base := strings.ReplaceAll(alias, "_", "-")
	if base == alias {
		base += "-bot"
	}
	id := orgdesign.UniqueRoleID(doc, base)
	for n := 2; doc.FindAutomation(id) != nil; n++ {
		id = orgdesign.UniqueRoleID(doc, fmt.Sprintf("%s-%d", base, n))
	}
	return id
}

func firstSentence(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func automationRole(doc *orgdesign.Doc, roleID string) (*orgdesign.Role, error) {
	r, _ := doc.FindRole(roleID)
	if r == nil {
		return nil, errInvalidInput("org %q has no role %q", doc.Name, roleID)
	}
	if !r.IsEndpoint() {
		return nil, errInvalidInput("role %q is not an automation role", roleID)
	}
	return r, nil
}

func newOrgAutomationRoleRemoveCmd(env *orgEnv) *cobra.Command {
	var roleID string
	c := &cobra.Command{
		Use:   "remove <org>",
		Short: "Remove an automation role and revoke its endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			if _, err := automationRole(doc, roleID); err != nil {
				return err
			}
			if err := orggrant.NewStore(db.DB).RevokeEndpoint(ctx, profileID, doc.Name, roleID); err != nil {
				return err
			}
			if _, err := doc.RemoveRole(roleID, orgdesign.Reparent); err != nil {
				return err
			}
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "removed": true, "role": roleID})
		},
	}
	c.Flags().StringVar(&roleID, "role", "", "Automation role id")
	_ = c.MarkFlagRequired("role")
	return c
}

func newOrgAutomationRoleRotateCmd(env *orgEnv) *cobra.Command {
	var roleID string
	c := &cobra.Command{
		Use:   "rotate <org>",
		Short: "Give an automation role a new endpoint URL; the old one stops working after 5 minutes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			if _, err := automationRole(doc, roleID); err != nil {
				return err
			}
			ep, err := orggrant.NewStore(db.DB).RotateEndpoint(ctx, profileID, doc.Name, roleID)
			if err != nil {
				return fmt.Errorf("rotate endpoint of %q: %w", roleID, err)
			}
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			applies := "next org start"
			if set, err := monomind.Capabilities(ctx); err == nil && set.Has(monomind.CapOrgToolProviders) {
				// M1 hot-reloads endpoint changes on existing roles (C-37).
				if _, err := monomind.OrgReload(ctx, root, doc.Name); err == nil {
					applies = "now"
				}
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "org": doc.Name, "role": roleID,
				"endpoint_url": orggrant.EndpointURL(orgAPIAddr(db), ep.ID), "applies": applies,
			})
		},
	}
	c.Flags().StringVar(&roleID, "role", "", "Automation role id")
	_ = c.MarkFlagRequired("role")
	return c
}
