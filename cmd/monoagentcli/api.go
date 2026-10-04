package main

import "github.com/spf13/cobra"

// newAPICmd is the management side of the OpenAI-compatible HTTP API: API
// keys, the models it would serve and its status. The server itself runs in
// `monoagentcli httpapi` and `monoagentcli daemon`.
func newAPICmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage API keys and inspect the OpenAI-compatible HTTP API (/v1)",
		Long: "The OpenAI-compatible API serves the local agent runtimes (claude, codex, antigravity, …) through " +
			"/v1/models and /v1/chat/completions. It runs inside `monoagentcli httpapi` and `monoagentcli daemon`; " +
			"`--v1-addr` gives it a dedicated listener, which is how it is exposed beyond loopback (TLS required). " +
			"These commands manage its API keys, show what it serves, and show and save the settings of its server " +
			"(`api config`).",
	}
	cmd.AddCommand(newAPIKeyCmd(cfg), newAPIModelsCmd(cfg), newAPIStatusCmd(cfg), newAPIConfigCmd(cfg))
	return cmd
}
