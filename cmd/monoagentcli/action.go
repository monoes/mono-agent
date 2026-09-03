package main

import (
	"github.com/spf13/cobra"
)

func newActionCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action",
		Short: "Manage action templates",
		Long:  "Capture, install, and list action templates for new platforms.",
	}

	cmd.AddCommand(
		newActionTemplateCmd(cfg),
	)

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
