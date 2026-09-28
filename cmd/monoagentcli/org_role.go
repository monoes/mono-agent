package main

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
)

func newOrgRoleCmd(root func() string) *cobra.Command {
	cmd := &cobra.Command{Use: "role", Short: "Manage one role of an org"}
	var yes bool
	setAccess := &cobra.Command{
		Use:   "set-access <org> <role> <full|scoped>",
		Short: "Grant or revoke a role's full access (granting is human-only)",
		Long: "full lets the role run like a coder chat: any command, any file, no approval gate " +
			"(budgets still apply). Granting needs --yes-i-understand and is refused when an agent runs " +
			"this command. scoped revokes it. The org's status shows each full-access role's state: " +
			"active, suspended (its config changed since the grant, so grant it again), or " +
			"unattended-blocked.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			org, role, access := args[0], args[1], args[2]
			if access != monomind.AccessFull && access != monomind.AccessScoped {
				return errInvalidInput("access must be full or scoped, got %q", access)
			}
			if access == monomind.AccessFull {
				if m := monomind.AgentContextMarker(); m != "" {
					return errInvalidInput("granting full access is human-only and this looks like an agent (%s is set); run it yourself from a terminal or the app", m)
				}
				if !yes {
					return errInvalidInput("full access lets role %q run any command and change any file without asking; pass --yes-i-understand to grant it", role)
				}
				set, err := capabilityProbe(cmd)
				if err != nil {
					return err
				}
				if err := set.Require(monomind.CapOrgRoleFullAccess, "full access for org roles"); err != nil {
					return err
				}
			}
			out, err := monomind.OrgRoleSetAccess(cmd.Context(), root(), org, role, access)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"org": org, "role": role, "access": access, "message": out})
		},
	}
	setAccess.Flags().BoolVar(&yes, "yes-i-understand", false, "Confirm a full-access grant")
	cmd.AddCommand(setAccess)
	return cmd
}
