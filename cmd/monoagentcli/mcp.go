package main

import (
	"github.com/monoes/mono-agent/internal/mcp"
	"github.com/spf13/cobra"
)

// newMCPCmd returns the `mcp` cobra command: a stdio JSON-RPC MCP server
// exposing workflows, nodes, the HIL queue, and reference docs as tools
// for AI agents. stdout is the protocol channel; logs go to stderr only.
func newMCPCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server (stdio) for AI agents",
		Long: `Run a Model Context Protocol server over stdin/stdout
(newline-delimited JSON-RPC 2.0).

Register this command as a stdio MCP server in any MCP client. Exposed tools:
workflow_list, workflow_get, workflow_validate, workflow_run,
workflow_status, node_list, node_schema, hil_list, hil_approve,
hil_reject, docs.

Honors the global --profile flag (or the MONOAGENT_PROFILE environment
variable) and --db-path, exactly like every other command.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.Run(mcp.Options{
				DBPath:  cfg.DBPath,
				Profile: cfg.ProfileID,
				Version: version,
			})
		},
	}
}
