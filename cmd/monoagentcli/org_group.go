package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/orggroup"
	"github.com/monoes/mono-agent/internal/storage"
)

func newOrgGroupCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Run a holding org and its child orgs as a group",
		Long: "A holding org (kind \"holding\") lists child orgs. `group init` gives its root role — the " +
			"Initiator — the org_start, org_stop, org_status, and org_report tools over its children and adds a " +
			"report-up line to each child's boss. run_config.group_budget_usd caps the group's spend; each " +
			"child's budget_share is its fraction of that cap.",
	}
	cmd.AddCommand(newOrgGroupInitCmd(env), newOrgGroupStartStopCmd(env, true), newOrgGroupStartStopCmd(env, false), newOrgGroupStatusCmd(env))
	return cmd
}

// validateGroup applies the cross-org holding rules (C-39, cycles, missing
// children).
func validateGroup(root string) ([]*orgdesign.Doc, error) {
	docs, _, err := orgdesign.LoadAll(root)
	if err != nil {
		return nil, err
	}
	if errs := orgdesign.ValidateProfileOrgs(docs); len(errs) > 0 {
		return nil, errInvalidInput("%s", strings.Join(errs, "; "))
	}
	return docs, nil
}

// saveReportUp writes the managed report-up lines into child bosses.
func saveReportUp(ctx context.Context, env *orgEnv, db *storage.Database, profileID, root string, docs []*orgdesign.Doc) error {
	for _, d := range orggroup.ApplyReportUp(docs) {
		if _, err := saveOrgReconciled(ctx, db, profileID, root, d, env.genOptions(profileID)); err != nil {
			return err
		}
	}
	return nil
}

func newOrgGroupInitCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "init <holding>",
		Short: "Grant the Initiator its org tools over the children and add report-up lines",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			genv := orggroup.NewEnv(db.DB, profileID, root)
			h, err := genv.LoadHolding(args[0])
			if err != nil {
				return errInvalidInput("%v", err)
			}
			docs, err := validateGroup(root)
			if err != nil {
				return err
			}
			initiator, ok := h.RootRole()
			if !ok {
				return errInvalidInput("holding org %q needs exactly one root role", h.Name)
			}
			// Scope, not merge: init passes every Initiator tool over the
			// holding org's current children, so a child removed from the
			// config loses the Initiator on the next init.
			if _, err := orggrant.NewStore(db.DB).SetOrgToolScope(ctx, profileID, h.Name, initiator.ID, orggroup.InitiatorOrgTools(h)); err != nil {
				return err
			}
			if err := saveReportUp(ctx, env, db, profileID, root, docs); err != nil {
				return err
			}
			h, _ = orgdesign.Load(root, h.Name)
			if _, err := saveOrgReconciled(ctx, db, profileID, root, h, env.genOptions(profileID)); err != nil {
				return err
			}
			children := []string{}
			for _, c := range h.ChildOrgs {
				children = append(children, c.Org)
			}
			return printJSONValue(map[string]interface{}{
				"v": 1, "holding": h.Name, "initiator": initiator.ID, "children": children,
				"tools":    orggroup.InitiatorTools,
				"warnings": nonNilStrings(capabilityWarnings(ctx, monomind.CapOrgToolProviders, "Initiator org tools")),
			})
		},
	}
}

func newOrgGroupStartStopCmd(env *orgEnv, start bool) *cobra.Command {
	var task string
	use, short := "stop <holding>", "Stop the holding org and its with_parent children"
	if start {
		use, short = "start <holding>", "Start the holding org and its with_parent children"
	}
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			genv := orggroup.NewEnv(db.DB, profileID, root)
			h, err := genv.LoadHolding(args[0])
			if err != nil {
				return errInvalidInput("%v", err)
			}
			if start {
				if _, err := validateGroup(root); err != nil {
					return err
				}
				if err := monomind.OrgRunStart(ctx, root, h.Name, task); err != nil {
					return err
				}
				for _, c := range h.ChildOrgs {
					if c.Start == "with_parent" {
						if err := genv.CheckStart(ctx, h.Name, c.Org); err != nil {
							return errInvalidInput("%v", err)
						}
						if err := monomind.OrgRunStart(ctx, root, c.Org, ""); err != nil {
							return err
						}
					}
				}
			} else {
				if _, err := monomind.OrgStop(ctx, root, h.Name); err != nil {
					return err
				}
				for _, c := range h.ChildOrgs {
					if c.Start == "with_parent" {
						_, _ = monomind.OrgStop(ctx, root, c.Org)
					}
				}
			}
			st, err := genv.GroupStatus(ctx, h.Name)
			if err != nil {
				return err
			}
			return printJSONValue(st)
		},
	}
	if start {
		c.Flags().StringVar(&task, "task", "", "Task for the holding org's run")
	}
	return c
}

func newOrgGroupStatusCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "status <holding>",
		Short: "Status and cost roll-up of a holding org and its children",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			st, err := orggroup.NewEnv(db.DB, profileID, root).GroupStatus(cmd.Context(), args[0])
			if err != nil {
				return errInvalidInput("%v", err)
			}
			return printJSONValue(st)
		},
	}
}

// grantDeciderTools gives a boss or parent decider role its decision tools
// and rewrites that role's org file so monomind serves them.
func grantDeciderTools(ctx context.Context, env *orgEnv, db *storage.Database, profileID, root string, doc *orgdesign.Doc, a *orgdecide.Autonomy) error {
	if a.Decider.Kind != orgdesign.DeciderBoss && a.Decider.Kind != orgdesign.DeciderParent {
		return nil
	}
	all, _, err := orgdesign.LoadAll(root)
	if err != nil {
		return err
	}
	dOrg, dRole, err := orgdecide.DeciderRole(a, doc, all)
	if err != nil {
		return errInvalidInput("%v", err)
	}
	if err := orgdecide.GrantDecisionTools(ctx, orggrant.NewStore(db.DB), profileID, dOrg, dRole, doc.Name); err != nil {
		return err
	}
	if dOrg == doc.Name {
		return nil // the caller saves doc
	}
	deciderDoc, err := orgdesign.Load(root, dOrg)
	if err != nil {
		return err
	}
	_, err = saveOrgReconciled(ctx, db, profileID, root, deciderDoc, env.genOptions(profileID))
	return err
}
