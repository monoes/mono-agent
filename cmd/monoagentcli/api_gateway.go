package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/monomind"
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
	maxConcurrentSet   bool // --max-concurrent was given, so an explicit 0 is an error and not "the default"
}

// optInt is an int flag that remembers whether it was given.
type optInt struct {
	v   *int
	set *bool
}

func (o optInt) String() string {
	if o.v == nil {
		return "0"
	}
	return strconv.Itoa(*o.v)
}

func (o optInt) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*o.v, *o.set = n, true
	return nil
}

func (optInt) Type() string { return "int" }

func (f *apiFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.v1Addr, "v1-addr", "",
		"Dedicated listener for the OpenAI-compatible API (serves only /v1 and /health). Off-loopback it requires TLS: "+
			apiTLSCertEnv+"/"+apiTLSKeyEnv+", else a self-signed certificate. Also MONOAGENT_API_V1_ADDR")
	cmd.Flags().StringVar(&f.confinement, "confinement", "",
		"Strongest runtime class the API serves: chat-only, sandboxed or any (default: any on loopback, chat-only off-loopback). Also MONOAGENT_API_CONFINEMENT")
	cmd.Flags().StringVar(&f.contextConfinement, "context-confinement", "",
		"Strongest runtime class a key created with --context may use: chat-only, sandboxed or any (default chat-only, never above --confinement). "+
			"Its requests carry excerpts of the profile's knowledge, which includes captured web pages nobody vetted. Also MONOAGENT_API_CONTEXT_CONFINEMENT")
	cmd.Flags().Var(optInt{&f.maxConcurrent, &f.maxConcurrentSet}, "max-concurrent",
		fmt.Sprintf("Maximum number of API turns running at once, 1 to %d (default 4). Also MONOAGENT_API_MAX_CONCURRENT", openaiapi.MaxConcurrentLimit))
}

// apiRuntime is the gateway's settings and how its listeners are set up. The
// gateway itself is built when something is going to serve it: building it
// takes the lock on the working folders and empties them, which a process that
// serves no /v1 (a daemon started with --api=false, a second server over the
// same home) must not do to one that does.
type apiRuntime struct {
	deps     openaiapi.Deps
	conf     openaiapi.Config
	override string // an explicit confinement; "" means each listener's default
	// contextMax is the strongest class a key created with --context may use.
	contextMax openaiapi.Class
	v1Addr     string
	logf       func(format string, args ...any)

	mu       sync.Mutex
	gw       *openaiapi.Gateway // nil until built, and when it could not be
	gwErr    error
	v1Done   chan struct{}      // closed when the dedicated listener has stopped; nil without one
	cancelV1 context.CancelFunc // stops the dedicated listener
}

// newAPIRuntime reads the flags, falling back to the environment. Bad values
// are invalid input (exit 3).
func newAPIRuntime(db *sql.DB, f apiFlags, logf func(format string, args ...any)) (*apiRuntime, error) {
	conf, err := openaiapi.ConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, errInvalidInput("%v", err)
	}
	if f.maxConcurrent < 0 || (f.maxConcurrentSet && f.maxConcurrent == 0) || f.maxConcurrent > openaiapi.MaxConcurrentLimit {
		return nil, errInvalidInput("--max-concurrent must be a number from 1 to %d, got %d", openaiapi.MaxConcurrentLimit, f.maxConcurrent)
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
	if v1 != "" {
		if err := validListenAddr(v1); err != nil {
			return nil, errInvalidInput("--v1-addr (MONOAGENT_API_V1_ADDR) must be host:port, such as 127.0.0.1:9443 or :9443: %v", err)
		}
	}

	deps := openaiapi.DefaultDeps(db, getVersion())
	deps.Logf = logf
	return &apiRuntime{deps: deps, conf: conf, override: override, contextMax: contextMax, v1Addr: v1, logf: logf}, nil
}

// validListenAddr checks the shape of a listen address: host:port with a
// numeric port. The host may be empty, as in :9443.
func validListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q is not a port number", port)
	}
	return nil
}

// gateway builds the gateway on first use. A process that cannot (another one
// owns the working folders) gets the error every time.
func (a *apiRuntime) gateway() (*openaiapi.Gateway, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gw == nil && a.gwErr == nil {
		a.gw, a.gwErr = openaiapi.New(a.deps, a.conf)
		if a.gwErr != nil && !errors.Is(a.gwErr, openaiapi.ErrScratchBusy) {
			a.gwErr = fmt.Errorf("starting the OpenAI-compatible API: %w", a.gwErr)
		}
	}
	return a.gw, a.gwErr
}

// built is the gateway if it has been built, nil otherwise.
func (a *apiRuntime) built() *openaiapi.Gateway {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gw
}

// releaseUnused gives the working folders back when no listener serves the
// gateway. The HTTP API server takes its routes when it is made, so the gateway
// exists before the listener it is mounted on is bound: a bind that then fails
// (a port in use) must not leave the home locked by a process that serves
// nothing. A gateway the dedicated listener serves stays.
func (a *apiRuntime) releaseUnused() {
	a.mu.Lock()
	if a.v1Done != nil {
		a.mu.Unlock()
		return
	}
	gw := a.gw
	a.gw, a.gwErr = nil, nil
	a.mu.Unlock()
	if gw != nil {
		gw.Shutdown(time.Second)
	}
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
	gw, err := a.gateway()
	if err != nil {
		a.logf("this process does not serve /v1: %v", err)
		return nil
	}
	p := a.policy(mainAddr)
	return func(mux *http.ServeMux) { gw.Mount(mux, p) }
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
	// Only now, with the address in hand: building the gateway takes the home's
	// working folders, and a listener that cannot bind must not keep them.
	gw, err := a.gateway()
	if err != nil {
		ln.Close()
		return "", err
	}
	p := a.policy(a.v1Addr)
	// The listener gets a context of its own: a command whose other listener
	// failed to bind never ends the one it was started with, and drain must
	// still be able to stop this one.
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.mu.Lock()
	a.v1Done, a.cancelV1 = done, cancel
	a.mu.Unlock()
	go func() {
		defer close(done)
		if err := gw.Serve(lctx, ln, p, tlsCfg); err != nil && lctx.Err() == nil {
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
	// The dedicated listener first: it gives its requests their grace, then
	// ends the turns it still has.
	a.mu.Lock()
	done, cancel := a.v1Done, a.cancelV1
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(apiDrainWait):
			a.logf("the /v1 listener did not stop within %s", apiDrainWait)
		}
	}
	if gw := a.built(); gw != nil && !gw.Shutdown(apiDrainWait) {
		a.logf("turns of the OpenAI-compatible API were still running %s after the server stopped", apiDrainWait)
	}
}

// killReapMargin is how long killTurns waits beyond monomind.KillGrace. A turn
// that ignores its cancel is ended by a group kill that Exec arms KillGrace
// after the cancel reaches it, so a wait of just the grace returns a moment
// before the kill.
const killReapMargin = 3 * time.Second

// killTurns ends the turns in flight and waits for their processes to be
// killed. A command about to exit at once (a second Ctrl+C) calls it first, so
// the agent CLIs do not outlive it: where a child gets no signal when its
// parent dies (macOS), one left behind runs on.
func (a *apiRuntime) killTurns() {
	if gw := a.built(); gw != nil {
		gw.Shutdown(monomind.KillGrace + killReapMargin)
	}
}

// confinementReport is the policy to record in the daemon's heartbeat for a
// listener at addr: "" when there is no such listener, or when it does not
// serve /v1 (the main listener serves it only on loopback, and nothing serves
// it in a process that could not build the gateway).
func (a *apiRuntime) confinementReport(addr string, dedicated bool) string {
	a.mu.Lock()
	failed := a.gwErr != nil
	a.mu.Unlock()
	if failed || addr == "" || (!dedicated && !tlsserve.IsLoopbackAddr(addr)) {
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
