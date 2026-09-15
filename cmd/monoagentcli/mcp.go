package main

import (
	"github.com/monoes/mono-agent/internal/mcp"
	"github.com/spf13/cobra"
)

// newMCPCmd returns the `mcp` cobra command: a stdio JSON-RPC MCP server
// exposing workflows, nodes, the HIL queue, monoagent-domain tools (vault,
// secrets, people, orgs — the same surface the chat feature uses), and
// reference docs as tools for AI agents. stdout is the protocol channel;
// logs go to stderr only.
func newMCPCmd(cfg *globalConfig) *cobra.Command {
	var allowMutations bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server (stdio) for AI agents",
		Long: `Run a Model Context Protocol server over stdin/stdout
(newline-delimited JSON-RPC 2.0).

Register this command as a stdio MCP server in any MCP client. Read-only
tools are always exposed: workflow_list, workflow_get, workflow_validate,
workflow_status, node_list, node_schema, hil_list, vault_item_list,
vault_item_get_path, profile_document_search, secret_list, person_list,
person_get, message_list, message_get, social_list_list, template_list,
org_list, org_get, org_validate, docs.

Mutating tools (workflow_run, workflow_create/delete/set_active/node_add,
hil_approve, hil_reject, secret_add/update/delete, person_upsert/delete,
org_create and every org_role_*/org_reload tool) are only exposed with
--allow-mutations or MONOAGENT_MCP_ALLOW_MUTATIONS=1 — without it they are
omitted from tools/list and refuse with an explanatory error if called by
name. This includes workflow_run/hil_approve/hil_reject, which were exposed
unconditionally before this flag existed — add --allow-mutations to an
existing MCP client config that relies on them.

Honors the global --profile flag (or the MONOAGENT_PROFILE environment
variable) and --db-path, exactly like every other command.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.Run(mcp.Options{
				DBPath:         cfg.DBPath,
				Profile:        cfg.ProfileID,
				Version:        version,
				AllowMutations: allowMutations,
			})
		},
	}
	cmd.Flags().BoolVar(&allowMutations, "allow-mutations", false,
		"Serve mutating tools (workflow_run, hil_approve/reject, and create/update/delete-class workflow/secret/person/org tools); also settable via MONOAGENT_MCP_ALLOW_MUTATIONS=1")
	return cmd
}
