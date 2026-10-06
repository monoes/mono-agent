package main

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
)

// newOrgSectionsRuntimesCmd prints which runtimes a sections org refuses or
// only warns about, with monomind's reasons (the Org Designer's runtime
// picker renders it).
func newOrgSectionsRuntimesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sections-runtimes",
		Short: "List the runtimes a sections org refuses or flags as unverified, with monomind's reasons",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			policy, err := monomind.SectionsRuntimes(cmd.Context())
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			return enc.Encode(policy)
		},
	}
}
