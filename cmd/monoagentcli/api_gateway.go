package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/openaiapi"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// Environment variables of the dedicated /v1 listener's TLS certificate.
const (
	apiTLSCertEnv = "MONOAGENT_API_TLS_CERT"
	apiTLSKeyEnv  = "MONOAGENT_API_TLS_KEY"
)

// apiFlags are the options `httpapi` and `daemon` share for the
// OpenAI-compatible API (/v1).
type apiFlags struct {
	v1Addr             string
	confinement        string
	contextConfinement string
	maxConcurrent      int
}

func (f *apiFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.v1Addr, "v1-addr", "",
		"Dedicated listener for the OpenAI-compatible API (serves only /v1 and /health). Off-loopback it requires TLS: "+
			apiTLSCertEnv+"/"+apiTLSKeyEnv+", else a self-signed certificate. Also MONOAGENT_API_V1_ADDR")
	cmd.Flags().StringVar(&f.confinement, "confinement", "",
		"Strongest runtime class the API serves: chat-only, sandboxed or any (default: any on loopback, chat-only off-loopback). Also MONOAGENT_API_CONFINEMENT")
	cmd.Flags().StringVar(&f.contextConfinement, "context-confinement", "",
		"Strongest runtime class a key created with --context may use: chat-only, sandboxed or any (default chat-only, never above --confinement). "+
			"Its requests carry excerpts of the profile's knowledge, which includes captured web pages nobody vetted. Also MONOAGENT_API_CONTEXT_CONFINEMENT")
	cmd.Flags().IntVar(&f.maxConcurrent, "max-concurrent", 0,
		"Maximum number of API turns running at once (default 4). Also MONOAGENT_API_MAX_CONCURRENT")
}

// apiRuntime is the gateway and how its listeners are set up.
type apiRuntime struct {
	gw       *openaiapi.Gateway
	override string // an explicit confinement; "" means each listener's default
	// contextMax is the strongest class a key created with --context may use.
	contextMax openaiapi.Class
	v1Addr     string
	logf       func(format string, args ...any)
	v1Done     chan struct{} // closed when the dedicated listener has stopped; nil without one
}

// newAPIRuntime builds the gateway from the flags, falling back to the
// environment. Bad values are invalid input (exit 3).
func newAPIRuntime(db *sql.DB, f apiFlags, logf func(format string, args ...any)) (*apiRuntime, error) {
	conf, err := openaiapi.ConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, errInvalidInput("%v", err)
	}
	if f.maxConcurrent < 0 {
		return nil, errInvalidInput("--max-concurrent must be a positive number, got %d", f.maxConcurrent)
	}
	if f.maxConcurrent > 0 {
		conf.MaxConcurrent = f.maxConcurrent
	}

	override := f.confinement
	if override == "" {
		override = os.Getenv("MONOAGENT_API_CONFINEMENT")
	}
	if override != "" {
		if _, err := openaiapi.ParsePolicy(override); err != nil {
			return nil, errInvalidInput("%v", err)
		}
	}
	contextMax, err := effectiveContextMax(f.contextConfinement, os.Getenv)
	if err != nil {
		return nil, err
	}
	v1 := f.v1Addr
	if v1 == "" {
		v1 = os.Getenv("MONOAGENT_API_V1_ADDR")
	}

	deps := openaiapi.DefaultDeps(db, getVersion())
	deps.Logf = logf
	gw, err := openaiapi.New(deps, conf)
	if err != nil {
		return nil, fmt.Errorf("starting the OpenAI-compatible API: %w", err)
	}
	return &apiRuntime{gw: gw, override: override, contextMax: contextMax, v1Addr: v1, logf: logf}, nil
}

// policy is the confinement policy of a listener bound to addr: what it
// serves, and what a key created with --context may use of that.
func (a *apiRuntime) policy(addr string) openaiapi.Policy {
	p := openaiapi.DefaultPolicy(addr)
	if a.override != "" {
		p, _ = openaiapi.ParsePolicy(a.override) // validated in newAPIRuntime
	}
	p.ContextMax = a.contextMax
	return p
}

// contextReport is the context maximum to record in the daemon's heartbeat.
func (a *apiRuntime) contextReport() string {
	return openaiapi.Policy{Max: a.contextMax}.String()
}

// mainMount returns the route registrar that serves /v1 on the main HTTP API
// listener, or nil when that listener is not loopback: /v1 is never served
// in plaintext off-loopback. Expose it with --v1-addr instead.
func (a *apiRuntime) mainMount(mainAddr string) func(*http.ServeMux) {
	if !tlsserve.IsLoopbackAddr(mainAddr) {
		a.logf("the HTTP API listener %s is not loopback, so it does not serve /v1: give the OpenAI-compatible API its own listener with --v1-addr", mainAddr)
		return nil
	}
	p := a.policy(mainAddr)
	return func(mux *http.ServeMux) { a.gw.Mount(mux, p) }
}

// startV1 binds the dedicated /v1 listener, when one is configured, and
// serves it until ctx ends. It returns the bound address, "" when there is
// none. Off-loopback the listener is TLS only.
func (a *apiRuntime) startV1(ctx context.Context) (string, error) {
	if a.v1Addr == "" {
		return "", nil
	}
	tlsCfg, err := tlsserve.Resolve(tlsserve.Config{
		Addr: a.v1Addr, CertEnv: apiTLSCertEnv, KeyEnv: apiTLSKeyEnv,
		CacheDir: "api-tls", CommonName: "monoagentcli API server (self-signed)", Label: "API server",
		Warn: func(msg string) { a.logf("%s", msg) },
	})
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", a.v1Addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", a.v1Addr, err)
	}
	p := a.policy(a.v1Addr)
	a.v1Done = make(chan struct{})
	go func() {
		defer close(a.v1Done)
		if err := a.gw.Serve(ctx, ln, p, tlsCfg); err != nil && ctx.Err() == nil {
			a.logf("the /v1 listener stopped: %v", err)
		}
	}()
	return ln.Addr().String(), nil
}

// apiDrainWait is how long a stopping command waits for the API's turns to be
// killed. The listeners' own graceful shutdown has already had its share.
const apiDrainWait = 30 * time.Second

// drain ends every turn of the OpenAI-compatible API that is still running and
// waits for them to be gone, then for the dedicated listener to stop. A command
// calls it after its servers were told to stop, so that no agent CLI outlives
// the process that was supposed to supervise it.
func (a *apiRuntime) drain() {
	if !a.gw.Shutdown(apiDrainWait) {
		a.logf("turns of the OpenAI-compatible API were still running %s after the server stopped", apiDrainWait)
	}
	if a.v1Done != nil {
		select {
		case <-a.v1Done:
		case <-time.After(apiDrainWait):
			a.logf("the /v1 listener did not stop within %s", apiDrainWait)
		}
	}
}

// confinementReport is the policy to record in the daemon's heartbeat for a
// listener at addr: "" when there is no such listener, or when it does not
// serve /v1 (the main listener serves it only on loopback).
func (a *apiRuntime) confinementReport(addr string, dedicated bool) string {
	if addr == "" || (!dedicated && !tlsserve.IsLoopbackAddr(addr)) {
		return ""
	}
	return a.policy(addr).String()
}

// daemonRoutes is what the daemon's HTTP API server mounts through its single
// ExtraRoutes slot: the org endpoint receiver and, on a loopback bind, the
// OpenAI-compatible API. Mounting only one of them would silently lose the
// other.
func daemonRoutes(orgReceiver func(*http.ServeMux), api *apiRuntime, addr string) func(*http.ServeMux) {
	return composeRoutes(orgReceiver, api.mainMount(httpapi.ResolveAddr(addr)))
}

// composeRoutes mounts several route registrars, in order, on one mux. nil
// registrars are skipped. httpapi.Options has a single ExtraRoutes slot.
func composeRoutes(fns ...func(*http.ServeMux)) func(*http.ServeMux) {
	return func(mux *http.ServeMux) {
		for _, f := range fns {
			if f != nil {
				f(mux)
			}
		}
	}
}
