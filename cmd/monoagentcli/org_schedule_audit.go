package main

import (
	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// newOrgScheduleAuditCmd prints the scheduled ticks of an org that did not
// start a run (refused, skipped, coalesced) from monomind's
// schedule-audit.jsonl. Read-only; monomind is not started.
func newOrgScheduleAuditCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "schedule-audit <org>",
		Short: "Scheduled ticks that did not start a run: refused, skipped, coalesced (read-only)",
		Long: "Reads <org>/schedule-audit.jsonl (monomind 2.24+) and prints the entries as JSON, newest first. " +
			"An org with no file prints no entries. A refused start of an unsigned scheduled org leaves no line " +
			"here; monomind only reports that on `org serve`'s own output.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !orgdesign.ValidOrgName(args[0]) {
				return errInvalidInput("invalid org name %q", args[0])
			}
			v, err := orgbridge.ReadScheduleAudit(env.Root(), args[0])
			if err != nil {
				return errInvalidInput("%s", err.Error())
			}
			return printJSONValue(v)
		},
	}
}
