package main

import (
	"github.com/spf13/cobra"
)

func newActionCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action",
		Short: "Export, import and template single actions",
		Long:  "Move single actions between automations and machines, and capture action templates for new sites.",
	}

	cmd.AddCommand(
		newActionTemplateCmd(cfg),
		newActionExportCmd(cfg),
		newActionImportCmd(cfg),
	)
	for _, sub := range cmd.Commands() {
		withJSONErrors(cfg, sub)
	}

	return cmd
}

// truncateStr shortens s to at most n characters, appending "..." if truncated.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
