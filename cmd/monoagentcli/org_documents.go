package main

import (
	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// newOrgDocumentsCmd prints the document channel of an org run: documents by
// section and type, their status, producer -> consumer, rework round of the
// cap, the lineage of each revise cycle and the deliverables. It reads
// monomind's per-run store (`.monomind/orgs/<org>/docs/<run>/`) and never
// writes it; monomind is not started.
func newOrgDocumentsCmd(env *orgEnv) *cobra.Command {
	var run string
	c := &cobra.Command{
		Use:   "documents <org>",
		Short: "Documents of an org run: status, producer -> consumer, rework rounds and caps (read-only)",
		Long: "Replays the run's document store the way monomind does (status is derived from the events) " +
			"and prints the view as JSON. Without --run it reads the newest run that has a store. " +
			"An org or run with no documents prints an empty view.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			org := args[0]
			if !orgdesign.ValidOrgName(org) {
				return errInvalidInput("invalid org name %q", org)
			}
			v, err := orgbridge.ReadDocView(env.Root(), org, run)
			if err != nil {
				return errInvalidInput("%s", err.Error())
			}
			return printJSONValue(v)
		},
	}
	c.Flags().StringVar(&run, "run", "", "Run id (default: the newest run with a document store)")
	return c
}
