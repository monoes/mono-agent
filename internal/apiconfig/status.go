package apiconfig

import (
	"context"
	"database/sql"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/openaiapi"
	"github.com/monoes/mono-agent/internal/tlsserve"
)

// ListenerReport is one listener of `api status --json`.
type ListenerReport struct {
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Loopback    bool   `json:"loopback"`
	V1          bool   `json:"v1"` // the listener is meant to serve /v1
	Confinement string `json:"confinement"`
	// DaemonSaysNoV1: the daemon knows /v1 and reports no policy for this
	// listener, which means it did not mount it here (another process owns the
	// API's working folders, say).
	DaemonSaysNoV1 bool `json:"-"`
	// ContextConfinement is the strongest class a key created with --context
	// may use here: the context maximum, never above Confinement.
	ContextConfinement string `json:"context_confinement"`
	// AutoConfinement is the strongest class the auto model may pick here: the
	// auto maximum, never above Confinement.
	AutoConfinement string `json:"auto_confinement"`
	// ConfinementSource is "daemon" when the running daemon reported the
	// policy, "environment" when it is worked out by this process from its
	// environment, the settings saved with `api config` and the defaults,
	// which a server started with --confinement may not share.
	ConfinementSource string `json:"confinement_source"`
	// Scheme is "http" or "https": the one that answered the probe, so a client
	// does not have to guess whether the listener speaks TLS. Omitted when the
	// listener is not reachable.
	Scheme    string `json:"scheme,omitempty"`
	Reachable bool   `json:"reachable"`  // GET /health answers 200
	V1Answers bool   `json:"v1_answers"` // GET /v1/models without a key answers 401: the gateway is mounted
}

// StatusReport is the document of `monoagentcli api status --json` and of the MCP tool
// api_status.
type StatusReport struct {
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
	// Auto is the auto model for the profile: whether it works, and what is
	// missing when it does not.
	Auto      openaiapi.AutoReport `json:"auto"`
	Listeners []ListenerReport     `json:"listeners"`
}

// BuildStatus reports the profile's active key count, the running daemon (from its heartbeat)
// and each listener: the main HTTP API listener (which serves /v1 only on loopback) and the
// dedicated v1_addr listener. A running daemon's heartbeat is believed about what the daemon
// serves. Anything it does not report, such as a standalone `httpapi`, is taken from
// MONOAGENT_HTTPAPI_ADDR and the API's variables with the saved settings under them, else the
// default 127.0.0.1:9322, and is listed next to a running daemon only when it answers. A
// listener is reachable when GET /health answers within 2 s, and answers /v1 when GET
// /v1/models sent without a key is refused with 401, which only the API does: a server that
// predates it answers 404 and must be restarted.
//
// A bad value in the environment is an *InputError; saved settings that fail their rules are a
// *ValidationError (wrapped), a saved document that cannot be read the error of Load.
func BuildStatus(ctx context.Context, db *sql.DB, env Env, profileID string) (StatusReport, error) {
	// What a server started now would read: the process environment, with the saved settings under it.
	getenv, err := EnvWithSaved(ctx, db, env.getenv())
	if err != nil {
		return StatusReport{}, err
	}
	active, err := apikeys.NewStore(db).CountActive(ctx, profileID)
	if err != nil {
		return StatusReport{}, err
	}

	st := StatusReport{V: 1, Profile: profileID, Listeners: []ListenerReport{}}
	st.Keys.Active = active
	autoStatus := openaiapi.DefaultAuto(db).Status(ctx, profileID)
	st.Auto = openaiapi.AutoReport{Available: autoStatus.Available, Missing: autoStatus.Missing, KeySource: autoStatus.KeySource}
	hb, live := env.heartbeat()
	mainAddr, v1Addr := httpapi.ResolveAddr(""), getenv("MONOAGENT_API_V1_ADDR")
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
	add := func(l ListenerReport, fromDaemon bool) {
		if live && !fromDaemon && !l.Reachable {
			return
		}
		st.Listeners = append(st.Listeners, l)
	}
	// The context maximum is the daemon's when it reported one.
	contextMax, err := EffectiveContextMax("", getenv)
	if err != nil {
		return StatusReport{}, err
	}
	if live && hb.ContextConfinement != "" {
		if p, perr := openaiapi.ParsePolicy(hb.ContextConfinement); perr == nil {
			contextMax = p.Max
		}
	}
	// The auto maximum is the daemon's too. A daemon that predates the setting
	// reports none, and auto is chat-only there.
	autoMax, err := EffectiveAutoMax("", getenv)
	if err != nil {
		return StatusReport{}, err
	}
	if live {
		autoMax = openaiapi.ChatOnly
		if p, perr := openaiapi.ParsePolicy(hb.AutoConfinement); perr == nil {
			autoMax = p.Max
		}
	}
	probe := env.probe()
	{
		mainPolicy, err := EffectivePolicy(mainAddr, "", getenv)
		if err != nil {
			return StatusReport{}, err
		}
		mainLoop := tlsserve.IsLoopbackAddr(mainAddr)
		main := ListenerReport{Name: "main", Addr: mainAddr, Loopback: mainLoop, V1: mainLoop, Confinement: mainPolicy.String(), ConfinementSource: "environment"}
		if mainFromDaemon && hb.APIConfinement != "" {
			main.Confinement, main.ConfinementSource = hb.APIConfinement, "daemon"
		}
		// The daemon knows /v1 but reports no policy for a loopback listener:
		// it did not mount the API there. (Off loopback the main listener
		// never serves /v1, and reports none for that reason.)
		if mainFromDaemon && mainLoop && hb.APIConfinement == "" && hb.ContextConfinement != "" {
			main.V1, main.DaemonSaysNoV1 = false, true
		}
		main.ContextConfinement = contextConfinementFor(main.Confinement, contextMax)
		main.AutoConfinement = autoConfinementFor(main.Confinement, autoMax)
		// The main listener is always plain HTTP: only the dedicated one has TLS.
		main.Reachable, main.V1Answers = probe("http://"+mainAddr, main.V1)
		if main.Reachable {
			main.Scheme = "http"
		}
		add(main, mainFromDaemon)
	}
	if v1Addr != "" {
		v1Policy, err := EffectivePolicy(v1Addr, "", getenv)
		if err != nil {
			return StatusReport{}, err
		}
		loop := tlsserve.IsLoopbackAddr(v1Addr)
		dedicated := ListenerReport{Name: "v1", Addr: v1Addr, Loopback: loop, V1: true, Confinement: v1Policy.String(), ConfinementSource: "environment"}
		if v1FromDaemon && hb.V1Confinement != "" {
			dedicated.Confinement, dedicated.ConfinementSource = hb.V1Confinement, "daemon"
		}
		dedicated.ContextConfinement = contextConfinementFor(dedicated.Confinement, contextMax)
		dedicated.AutoConfinement = autoConfinementFor(dedicated.Confinement, autoMax)
		dedicated.Scheme, dedicated.Reachable, dedicated.V1Answers = ProbeAddr(v1Addr, loop, true, probe)
		add(dedicated, v1FromDaemon)
	}
	return st, nil
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

// autoConfinementFor is what the auto model may pick on a listener that serves
// confinement, given the auto maximum.
func autoConfinementFor(confinement string, autoMax openaiapi.Class) string {
	p, err := openaiapi.ParsePolicy(confinement)
	if err != nil {
		return ""
	}
	p.AutoMax = autoMax
	return p.ForAuto().String()
}
