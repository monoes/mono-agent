package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/openaiapi"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

type apiListenerJSON struct {
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Loopback    bool   `json:"loopback"`
	V1          bool   `json:"v1"` // the listener is meant to serve /v1
	Confinement string `json:"confinement"`
	// daemonSaysNoV1: the daemon knows /v1 and reports no policy for this
	// listener, which means it did not mount it here (another process owns the
	// API's working folders, say).
	daemonSaysNoV1 bool
	// ContextConfinement is the strongest class a key created with --context
	// may use here: the context maximum, never above Confinement.
	ContextConfinement string `json:"context_confinement"`
	// ConfinementSource is "daemon" when the running daemon reported the
	// policy, "environment" when it is worked out from this shell's
	// environment and the defaults, which a server started with
	// --confinement may not share.
	ConfinementSource string `json:"confinement_source"`
	Reachable         bool   `json:"reachable"`  // GET /health answers 200
	V1Answers         bool   `json:"v1_answers"` // GET /v1/models without a key answers 401: the gateway is mounted
}

type apiStatusJSON struct {
	V       int    `json:"v"`
	Profile string `json:"profile"`
	Keys    struct {
		Active int `json:"active"`
	} `json:"keys"`
	Daemon struct {
		Running bool   `json:"running"`
		APIAddr string `json:"api_addr,omitempty"`
		V1Addr  string `json:"v1_addr,omitempty"`
	} `json:"daemon"`
	Listeners []apiListenerJSON `json:"listeners"`
}

func newAPIStatusCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where the OpenAI-compatible API listens and whether it answers",
		Long: "Reports the profile's active key count, the running daemon (from its heartbeat) and each listener: " +
			"the main HTTP API listener (which serves /v1 only on loopback) and the dedicated --v1-addr listener. " +
			"A running daemon's heartbeat is believed about what the daemon serves. Anything it does not report, such as a " +
			"standalone `httpapi`, is taken from MONOAGENT_HTTPAPI_ADDR and MONOAGENT_API_V1_ADDR, else the default 127.0.0.1:9322, " +
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
			active, err := apikeys.NewStore(db.DB).CountActive(cmd.Context(), cfg.ProfileID)
			if err != nil {
				return err
			}

			st := apiStatusJSON{V: 1, Profile: cfg.ProfileID, Listeners: []apiListenerJSON{}}
			st.Keys.Active = active
			hb, live := daemonhb.Read()
			mainAddr, v1Addr := httpapi.ResolveAddr(""), os.Getenv("MONOAGENT_API_V1_ADDR")
			mainFromDaemon, v1FromDaemon := false, false
			if live {
				st.Daemon.Running, st.Daemon.APIAddr, st.Daemon.V1Addr = true, hb.APIAddr, hb.V1Addr
				// A running daemon is believed about what it serves: one started
				// with --api=false and no --v1-addr serves no /v1, whatever this
				// shell's environment says. What the environment names is then
				// only listed when something answers there, as a standalone
				// `httpapi` does.
				if hb.APIAddr != "" {
					mainAddr, mainFromDaemon = hb.APIAddr, true
				}
				if hb.V1Addr != "" {
					v1Addr, v1FromDaemon = hb.V1Addr, true
				}
			}
			add := func(l apiListenerJSON, fromDaemon bool) {
				if live && !fromDaemon && !l.Reachable {
					return
				}
				st.Listeners = append(st.Listeners, l)
			}
			override := os.Getenv
			// The context maximum is the daemon's when it reported one.
			contextMax, err := effectiveContextMax("", os.Getenv)
			if err != nil {
				return err
			}
			if live && hb.ContextConfinement != "" {
				if p, perr := openaiapi.ParsePolicy(hb.ContextConfinement); perr == nil {
					contextMax = p.Max
				}
			}
			{
				mainPolicy, err := effectivePolicy(mainAddr, "", override)
				if err != nil {
					return err
				}
				mainLoop := tlsserve.IsLoopbackAddr(mainAddr)
				main := apiListenerJSON{Name: "main", Addr: mainAddr, Loopback: mainLoop, V1: mainLoop, Confinement: mainPolicy.String(), ConfinementSource: "environment"}
				if mainFromDaemon && hb.APIConfinement != "" {
					main.Confinement, main.ConfinementSource = hb.APIConfinement, "daemon"
				}
				// The daemon knows /v1 but reports no policy for a loopback listener:
				// it did not mount the API there. (Off loopback the main listener
				// never serves /v1, and reports none for that reason.)
				if mainFromDaemon && mainLoop && hb.APIConfinement == "" && hb.ContextConfinement != "" {
					main.V1, main.daemonSaysNoV1 = false, true
				}
				main.ContextConfinement = contextConfinementFor(main.Confinement, contextMax)
				// The main listener is always plain HTTP: only the dedicated one has TLS.
				main.Reachable, main.V1Answers = probeListener("http://"+mainAddr, main.V1)
				add(main, mainFromDaemon)
			}
			if v1Addr != "" {
				v1Policy, err := effectivePolicy(v1Addr, "", override)
				if err != nil {
					return err
				}
				loop := tlsserve.IsLoopbackAddr(v1Addr)
				dedicated := apiListenerJSON{Name: "v1", Addr: v1Addr, Loopback: loop, V1: true, Confinement: v1Policy.String(), ConfinementSource: "environment"}
				if v1FromDaemon && hb.V1Confinement != "" {
					dedicated.Confinement, dedicated.ConfinementSource = hb.V1Confinement, "daemon"
				}
				dedicated.ContextConfinement = contextConfinementFor(dedicated.Confinement, contextMax)
				dedicated.Reachable, dedicated.V1Answers = probeAddr(v1Addr, loop, true)
				add(dedicated, v1FromDaemon)
			}

			if cfg.JSONOutput {
				return writeJSONTo(cmd.OutOrStdout(), st)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Profile %s: %d active API key(s)\n", st.Profile, st.Keys.Active)
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

// contextConfinementFor is what a key created with --context may use on a
// listener that serves confinement, given the context maximum.
func contextConfinementFor(confinement string, contextMax openaiapi.Class) string {
	p, err := openaiapi.ParsePolicy(confinement)
	if err != nil {
		return ""
	}
	p.ContextMax = contextMax
	return p.ForContextKey().String()
}

// listenerNote says in a sentence what `api status` found at a listener.
func listenerNote(l apiListenerJSON) string {
	switch {
	case !l.Reachable:
		return "not reachable: nothing answers /health there"
	case l.daemonSaysNoV1:
		return "reachable, but the daemon does not serve /v1 on it: another process may own the API's working folders (see the daemon's log)"
	case !l.V1:
		return "reachable, but does not serve /v1 (bound off-loopback; use --v1-addr)"
	case l.V1Answers:
		note := "serves /v1, confinement " + l.Confinement + ", keys created with --context: " + l.ContextConfinement
		if l.ConfinementSource != "daemon" {
			note += " (assumed from this shell's environment: a server started with --confinement or --context-confinement may differ)"
		}
		return note
	}
	return "answers /health but not /v1: a server that predates the API may still be running, restart it"
}

// probeSchemes is the order to try a listener's schemes in: the one it more
// likely speaks first. A listener off loopback is TLS only, and a plaintext
// request to it would put a handshake error in its log on every `api status`.
// Both are always tried: the certificate may be set only in the server's own
// environment, which makes a loopback listener speak TLS too.
func probeSchemes(loopback bool) []string {
	if loopback {
		return []string{"http://", "https://"}
	}
	return []string{"https://", "http://"}
}

// probeAddr probes a listener at addr without knowing whether it speaks TLS
// (see probeSchemes), and, when wantV1, whether it answers /v1.
func probeAddr(addr string, loopback, wantV1 bool) (reachable, v1Answers bool) {
	for _, scheme := range probeSchemes(loopback) {
		if reachable, v1Answers = probeListener(scheme+addr, wantV1); reachable {
			return reachable, v1Answers
		}
	}
	return false, false
}

// probeListener asks a listener, with a 2 s timeout each, whether GET /health
// answers 200 and, when wantV1, whether GET /v1/models sent without a key is
// refused with 401. Only the API answers 401 there; a server that predates it
// answers 404. No request carries a secret, so the self-signed certificate of
// the dedicated listener is not verified.
func probeListener(base string, wantV1 bool) (reachable, v1Answers bool) {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // no secret is sent
		// What answers is whatever is at the address: it is not followed anywhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	status := func(path string) int {
		resp, err := client.Get(base + path)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if status("/health") != http.StatusOK {
		return false, false
	}
	return true, wantV1 && status("/v1/models") == http.StatusUnauthorized
}
