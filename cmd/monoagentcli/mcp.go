package main

import (
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/mcp"
	"github.com/spf13/cobra"
)

// newMCPCmd returns the `mcp` cobra command: a stdio JSON-RPC MCP server
// exposing workflows, nodes, the HIL queue, monoagent-domain tools (vault,
// secrets, people, orgs — the same surface the chat feature uses), and
// reference docs as tools for AI agents. stdout is the protocol channel;
// logs go to stderr only.
func newMCPCmd(cfg *globalConfig) *cobra.Command {
	var allowMutations, allowAPIExposure bool
	var grant string
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
org_list, org_get, org_validate, api_key_list, api_models_list, api_status,
api_config_get, docs.

Mutating tools (workflow_run, workflow_create/delete/set_active/node_add,
hil_approve, hil_reject, secret_add/update/delete, person_upsert/delete,
org_create and every org_role_*/org_reload tool, api_key_create/update/revoke,
which manage the OpenAI-compatible API's keys, api_config_set and
api_config_apply, which save its server's settings and restart the daemon that
reads them, and api_auto_set, which switches its auto model on or off) are only
exposed with --allow-mutations or MONOAGENT_MCP_ALLOW_MUTATIONS=1 — without it they are
omitted from tools/list and refuse with an explanatory error if called by
name. This includes workflow_run/hil_approve/hil_reject, which were exposed
unconditionally before this flag existed — add --allow-mutations to an
existing MCP client config that relies on them.

api_key_create returns the new API key once, in its result, which makes it part
of the host's transcript. monoagentcli api key create writes the key to its
stdout: run in your own terminal, and not through an agent's shell tool, it
stays out of that transcript.

api_config_set saves settings of the OpenAI-compatible API's server and refuses a
change that makes the server reach further than it did, and saves nothing, unless
this server was started with --allow-api-exposure or MONOAGENT_MCP_ALLOW_API_EXPOSURE=1.
What reaches further is: a dedicated listener that reaches further than the saved
one (beyond this machine, another host beyond it, or every interface where it was
one host), a higher confinement class, a runtime list that gains a runtime it did
not have, none left (tool calling or image generation switched on again), and
removing a saved row that cannot be read (saved_settings). That is the operator's
decision, made here: the arguments of a tool are set by the model, so none of them
can lift the refusal. It adds nothing without --allow-mutations, and it guards that
one tool only: --allow-mutations also serves workflow_node_add (which accepts the
node type system.execute_command), workflow_set_active and workflow_run, with which
a model can have a workflow of the profile run "monoagentcli api config set ... --yes"
as you. If a model must not be able to widen the server, do not give it
--allow-mutations.

api_config_apply restarts the daemon, as daemon restart does, and interrupts what
it is running (workflows, org runs). api_auto_set needs acknowledge_egress: true to
switch the auto model on, because prompts then go to TypeSafe; it never creates or
reads the Jev key.

Honors the global --profile flag (or the MONOAGENT_PROFILE environment
variable) and --db-path, exactly like every other command.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A process monomind spawned for an org role may only serve a
			// grant: a hand-added provider running plain `mcp` would give
			// the role every mutating tool (C-3).
			if grant == "" && os.Getenv("MONOMIND_ORG_NAME") != "" {
				return fmt.Errorf("refusing to serve the full MCP tool set to an org role (MONOMIND_ORG_NAME is set); org roles use `mcp --grant <id>`")
			}
			if grant != "" && allowMutations {
				return fmt.Errorf("--grant serves only the granted automations; --allow-mutations does not apply")
			}
			if grant != "" && allowAPIExposure {
				return fmt.Errorf("--grant serves only the granted automations; --allow-api-exposure does not apply")
			}
			return runMCP(mcp.Options{
				DBPath:           cfg.DBPath,
				Profile:          cfg.ProfileID,
				Version:          version,
				AllowMutations:   allowMutations,
				AllowAPIExposure: allowAPIExposure,
				Grant:            grant,
			})
		},
	}
	cmd.Flags().StringVar(&grant, "grant", "",
		"Grant mode: serve only the automations granted to one org role (monomind spawns this for role tool providers)")
	cmd.Flags().BoolVar(&allowMutations, "allow-mutations", false,
		"Serve mutating tools (workflow_run, hil_approve/reject, and create/update/delete-class workflow/secret/person/org/api-key tools, and api_config_set, api_config_apply and api_auto_set); also settable via MONOAGENT_MCP_ALLOW_MUTATIONS=1")
	cmd.Flags().BoolVar(&allowAPIExposure, "allow-api-exposure", false,
		"Let api_config_set save a change that makes the OpenAI-compatible API's server reach further, which it otherwise refuses: a dedicated listener that reaches further than the saved one (beyond this machine, another host beyond it, or every interface where it was one host), a higher confinement class, a runtime list that gains a runtime it did not have, none left (tool calling or image generation switched on again), and removing a saved row that cannot be read (saved_settings); needs --allow-mutations; it guards that tool only (--allow-mutations also serves workflow tools that can run a command as you); also settable via MONOAGENT_MCP_ALLOW_API_EXPOSURE=1")
	return cmd
}

// runMCP serves the MCP server on stdin and stdout. A test replaces it to see what the command
// would have started the server with.
var runMCP = mcp.Run
