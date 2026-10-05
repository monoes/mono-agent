package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

// The document `api status --json` prints is the one the MCP tool api_status returns, so its
// types and the rules that fill them live in internal/apiconfig.
type (
	apiListenerJSON = apiconfig.ListenerReport
	apiStatusJSON   = apiconfig.StatusReport
)

func newAPIStatusCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where the OpenAI-compatible API listens and whether it answers",
		Long: "Reports the profile's active key count, the running daemon (from its heartbeat) and each listener: " +
			"the main HTTP API listener (which serves /v1 only on loopback) and the dedicated --v1-addr listener. " +
			"A running daemon's heartbeat is believed about what the daemon serves. Anything it does not report, such as a " +
			"standalone `httpapi`, is taken from MONOAGENT_HTTPAPI_ADDR and MONOAGENT_API_V1_ADDR (or the v1_addr saved with " +
			"`api config`), else the default 127.0.0.1:9322, " +
			"and is listed next to a running daemon only when it answers. A listener is reachable when GET /health answers within 2 s, and answers /v1 when " +
			"GET /v1/models sent without a key is refused with 401, which only the API does: a server that predates it " +
			"answers 404 and must be restarted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			st, err := apiconfig.BuildStatus(cmd.Context(), db.DB, apiconfig.Env{}, cfg.ProfileID)
			if err != nil {
				return asCLIError(err)
			}

			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), st)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Profile %s: %d active API key(s)\n", st.Profile, st.Keys.Active)
			fmt.Fprintf(w, "Auto model: %s\n", autoNote(st.Auto))
			if st.Daemon.Running {
				fmt.Fprintln(w, "Daemon: running")
			} else {
				fmt.Fprintln(w, "Daemon: not running")
			}
			for _, l := range st.Listeners {
				fmt.Fprintf(w, "  %-4s %s  %s\n", l.Name, l.Addr, listenerNote(l))
			}
			if st.Daemon.Running {
				if !hasListener(st.Listeners, "main") {
					fmt.Fprintln(w, "  main none: the daemon serves no HTTP API (see --api and --api-addr), and nothing else answers at the usual address")
				}
				if !hasListener(st.Listeners, "v1") {
					fmt.Fprintln(w, "  v1   none: the daemon reports no dedicated /v1 listener (it was started without --v1-addr, or could not start it: see its log)")
				}
			}
			return nil
		},
	}
}

// hasListener reports whether the status lists a listener of that name.
func hasListener(ls []apiListenerJSON, name string) bool {
	for _, l := range ls {
		if l.Name == name {
			return true
		}
	}
	return false
}

// listenerNote says in a sentence what `api status` found at a listener.
func listenerNote(l apiListenerJSON) string {
	switch {
	case !l.Reachable:
		return "not reachable: nothing answers /health there"
	case l.DaemonSaysNoV1:
		return "reachable, but the daemon does not serve /v1 on it: another process may own the API's working folders (see the daemon's log)"
	case !l.V1:
		return "reachable, but does not serve /v1 (bound off-loopback; use --v1-addr)"
	case l.V1Answers:
		note := "serves /v1 over " + l.Scheme + ", confinement " + l.Confinement + ", keys created with --context: " + l.ContextConfinement + ", auto picks up to: " + l.AutoConfinement
		if l.ConfinementSource != "daemon" {
			note += " (assumed from this shell's environment and the saved settings: a server started with --confinement, --context-confinement or --auto-confinement may differ)"
		}
		return note
	}
	return "answers /health but not /v1: a server that predates the API may still be running, restart it"
}

// The probes are internal/apiconfig's, which the MCP tool api_status uses too.
var probeSchemes = apiconfig.ProbeSchemes

// probeAddr probes a listener over HTTP and then HTTPS, as `api status` does.
func probeAddr(addr string, loopback, wantV1 bool) (scheme string, reachable, v1Answers bool) {
	return apiconfig.ProbeAddr(addr, loopback, wantV1, nil)
}
