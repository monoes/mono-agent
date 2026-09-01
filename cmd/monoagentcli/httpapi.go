package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/httpapi"
)

// newHTTPAPICmd runs the REST/JSON HTTP API server in the foreground,
// mirroring `monoagentcli daemon`'s signal handling. Unlike daemon (which
// keeps triggers alive across all profiles), the HTTP API is scoped to the
// active --profile, exactly like every other read/write command.
func newHTTPAPICmd(cfg *globalConfig) *cobra.Command {
	var addr string
	var allowMutations bool

	cmd := &cobra.Command{
		Use:   "httpapi",
		Short: "Run a REST/JSON HTTP API server for external agents (read-only by default)",
		Long: "Starts a REST/JSON HTTP server exposing workflows, node types, and the HIL queue " +
			"for external agents that cannot speak the stdio MCP protocol (see `monoagentcli mcp`).\n\n" +
			"Loopback-only by default (127.0.0.1:9322) — override with --addr or MONOAGENT_HTTPAPI_ADDR. " +
			"Every request requires `Authorization: Bearer <token>` except GET /health; the token is " +
			"generated on first start and stored in this profile's secrets vault (`monoagentcli secret " +
			"list`, name \"httpapi-token\").\n\n" +
			"Mutating endpoints (workflow run/activate/deactivate, hil approve/reject) are only served " +
			"when --allow-mutations or MONOAGENT_HTTPAPI_ALLOW_MUTATIONS=1 is set; without it, only the " +
			"read endpoints are registered (an unregistered mutating path 404s, not 403s). " +
			"Output items are redacted the same way as `workflow run --json` and the MCP tools " +
			"(credential-shaped keys masked); pass `X-Full-Outputs: 1` to opt out per request, " +
			"mirroring `workflow run --full-outputs`.\n\n" +
			"See internal/httpapi/openapi.yaml for the full endpoint list and examples/httpapi-quickstart.md " +
			"for a curl walkthrough.",
		Example: "  monoagentcli httpapi\n  monoagentcli httpapi --allow-mutations\n  MONOAGENT_HTTPAPI_ADDR=127.0.0.1:9999 monoagentcli httpapi",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := httpapi.NewServer(httpapi.Options{
				DBPath:         cfg.DBPath,
				Profile:        cfg.ProfileID,
				Addr:           addr,
				AllowMutations: allowMutations,
				Version:        version,
			})
			if err != nil {
				return fmt.Errorf("build httpapi server: %w", err)
			}
			defer srv.Close()

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Mirrors daemon.go: a second interrupt forces an immediate exit
			// instead of waiting out the graceful-shutdown window.
			shutdownDone := make(chan struct{})
			sigCh := make(chan os.Signal, 2)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigCh)
			go func() {
				<-sigCh
				select {
				case <-sigCh:
					fmt.Fprintln(os.Stderr, "Second interrupt received — forcing immediate exit.")
					os.Exit(130)
				case <-shutdownDone:
				}
			}()
			defer close(shutdownDone)

			fmt.Fprintf(os.Stdout, "HTTP API listening on %s (mutations %s). Press Ctrl+C to stop.\n",
				srv.Addr(), mutationsLabel(srv.AllowsMutations()))
			return srv.ListenAndServe(ctx)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default 127.0.0.1:9322, or MONOAGENT_HTTPAPI_ADDR)")
	cmd.Flags().BoolVar(&allowMutations, "allow-mutations", false,
		"Serve mutating endpoints (workflow run/activate/deactivate, hil approve/reject); also settable via MONOAGENT_HTTPAPI_ALLOW_MUTATIONS=1")
	return cmd
}

func mutationsLabel(allowed bool) string {
	if allowed {
		return "enabled"
	}
	return "disabled — read-only"
}
