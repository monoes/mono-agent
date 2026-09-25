package main

import "github.com/spf13/cobra"

// newAutomationCmd builds `monoagentcli automation …` (browser automation
// packages). Owned by the cli builder; see the build contracts doc.
func newAutomationCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "automation",
		Short: "Manage browser automation packages",
	}
	return cmd
}
