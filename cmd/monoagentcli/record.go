package main

import "github.com/spf13/cobra"

// newRecordCmd builds `monoagentcli record …` (extension activity
// recordings). Owned by the ingest builder; analyze/verify/save come from
// addRecordAnalyzeCommands (record_analyze.go, analyze builder).
func newRecordCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Browser activity recordings",
	}
	addRecordAnalyzeCommands(cmd, cfg)
	return cmd
}
