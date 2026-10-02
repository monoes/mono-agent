package main

// api status: what it reports about a running daemon and about the listeners, and how it probes them.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

func decodeStatus(t *testing.T, out string) apiStatusJSON {
	t.Helper()
	var st apiStatusJSON
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("not an api status document: %v\n%s", err, out)
	}
	return st
}

// apiServer answers GET /health with 200 like the API listeners do and, when
// gateway is true, GET /v1/models with the 401 the gateway gives a request
// without a key. Otherwise /v1 is a 404, like a server that predates the API.
func apiServer(t *testing.T, gateway bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
		case gateway && r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}
func TestAPIStatusWithoutADaemon(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", apiServer(t, true))
	t.Setenv("MONOAGENT_API_V1_ADDR", "127.0.0.1:1") // nothing listens there
	for _, name := range []string{"one", "two"} {
		if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", name); err != nil {
			t.Fatal(err)
		}
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStatus(t, out)
	if st.Profile != "default" || st.Keys.Active != 2 || st.Daemon.Running {
		t.Fatalf("status: %+v", st)
	}
	if len(st.Listeners) != 2 {
		t.Fatalf("listeners: %+v", st.Listeners)
	}
	main, v1 := st.Listeners[0], st.Listeners[1]
	if main.Name != "main" || !main.Loopback || !main.V1 || !main.Reachable || !main.V1Answers || main.Confinement != "any" || main.ConfinementSource != "environment" || main.ContextConfinement != "chat-only" {
		t.Errorf("main listener: %+v", main)
	}
	if v1.Name != "v1" || v1.Addr != "127.0.0.1:1" || !v1.V1 || v1.Reachable || v1.V1Answers {
		t.Errorf("v1 listener: %+v", v1)
	}
}
func TestAPIStatusSaysWhenTheRunningServerDoesNotAnswerV1(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", apiServer(t, false)) // up, but it predates the API

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	if main := decodeStatus(t, out).Listeners[0]; !main.Reachable || main.V1Answers {
		t.Fatalf("a server that answers /health but not /v1: %+v", main)
	}
	human, _, err := runAPI(t, db, "default", false, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "not /v1") || !strings.Contains(human, "restart it") {
		t.Errorf("the output must say what to do:\n%s", human)
	}
}
func TestAPIStatusOffLoopbackMainListenerDoesNotServeV1(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "0.0.0.0:9")

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	main := decodeStatus(t, out).Listeners[0]
	if main.Loopback || main.V1 || main.Confinement != "chat-only" {
		t.Errorf("an off-loopback main listener must not serve /v1: %+v", main)
	}
}

// A running daemon is believed about what it serves: one started with
// --api=false and no --v1-addr serves no /v1, and the addresses this shell's
// environment names are not listed as if it did, unless something answers there.
func TestAPIStatusListsNothingWhenTheDaemonServesNoAPI(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "127.0.0.1:1")                            // the shell's environment names a main listener...
	t.Setenv("MONOAGENT_API_V1_ADDR", "127.0.0.1:1")                             // ...and a dedicated one, and nothing listens on either
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid()}); err != nil { // the daemon reports neither
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStatus(t, out)
	if !st.Daemon.Running || len(st.Listeners) != 0 {
		t.Fatalf("a daemon that serves no API, and nothing answering where the environment says, has no listener: %+v", st)
	}
	human, _, _ := runAPI(t, db, "default", false, "status")
	for _, want := range []string{"serves no HTTP API", "reports no dedicated /v1 listener"} {
		if !strings.Contains(human, want) {
			t.Errorf("the output must say %q:\n%s", want, human)
		}
	}
}

// A daemon started with --api=false is often paired with a standalone `httpapi`
// that serves /v1. What answers where this shell's environment says is real, so
// it is listed, as assumed, though the daemon reports nothing.
func TestAPIStatusFindsAStandaloneServerNextToADaemonWithoutTheAPI(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", apiServer(t, true))
	t.Setenv("MONOAGENT_API_V1_ADDR", apiServer(t, true))
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStatus(t, out)
	if !st.Daemon.Running || len(st.Listeners) != 2 {
		t.Fatalf("both servers answer, so both are listed: %+v", st)
	}
	for _, l := range st.Listeners {
		if !l.Reachable || !l.V1Answers || l.ConfinementSource != "environment" {
			t.Errorf("%s: a server found where the environment says is reachable and assumed: %+v", l.Name, l)
		}
	}
}

// The daemon knows /v1 (it reports a context maximum) but did not mount it on
// its main listener, because another process owns the working folders: telling
// the operator to restart a server that predates the API would be wrong.
func TestAPIStatusSaysWhenTheDaemonDoesNotServeV1OnItsListener(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: apiServer(t, false), ContextConfinement: "chat-only"}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	main := decodeStatus(t, out).Listeners[0]
	if main.V1 || main.V1Answers || !main.Reachable {
		t.Fatalf("a daemon that knows /v1 and did not mount it: %+v", main)
	}
	human, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(human, "does not serve /v1") || !strings.Contains(human, "log") || strings.Contains(human, "restart it") {
		t.Errorf("the output must point at the daemon's log, not at a restart:\n%s", human)
	}
}

// A daemon's main listener off loopback never serves /v1, and reports no policy
// for it: that is not a daemon that failed to mount the API, and the operator
// must be told to use --v1-addr, not to look for another process.
func TestAPIStatusDoesNotBlameAnotherProcessForAnOffLoopbackMainListener(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	_, port, _ := strings.Cut(apiServer(t, false), ":") // a server that answers on every address of the machine
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: "0.0.0.0:" + port, ContextConfinement: "chat-only"}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	main := decodeStatus(t, out).Listeners[0]
	if main.Loopback || main.V1 || !main.Reachable {
		t.Fatalf("an off-loopback main listener: %+v", main)
	}
	human, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(human, "--v1-addr") || strings.Contains(human, "another process") {
		t.Errorf("the output must point at --v1-addr, not at another process:\n%s", human)
	}
}

// A daemon that cannot start the dedicated listener only warns, and runs on:
// the listener is then missing from `api status`, which has to say what that means.
func TestAPIStatusExplainsAMissingDedicatedListener(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "127.0.0.1:1")
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: apiServer(t, true), APIConfinement: "any", ContextConfinement: "chat-only"}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	if ls := decodeStatus(t, out).Listeners; len(ls) != 1 || ls[0].Name != "main" {
		t.Fatalf("only the main listener is up: %+v", ls)
	}
	human, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(human, "reports no dedicated /v1 listener") || !strings.Contains(human, "could not start") {
		t.Errorf("a missing dedicated listener must be explained:\n%s", human)
	}
}

// The scheme is not the shell's to guess: the daemon's certificate may be set
// only in its own environment, so a loopback listener can speak TLS.
func TestAPIStatusReachesAListenerWhicheverSchemeItSpeaks(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_TLS_CERT", "") // not set in this shell
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/models":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), V1Addr: addr, V1Confinement: "chat-only"}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range decodeStatus(t, out).Listeners {
		if l.Name == "v1" {
			found = true
			if !l.Reachable || !l.V1Answers {
				t.Errorf("a loopback listener that speaks TLS must be found: %+v", l)
			}
		}
	}
	if !found {
		t.Error("the dedicated listener the daemon reported is missing from the status")
	}
}

// A listener off loopback is TLS only. A plaintext probe of it would put a
// handshake error in the server's log on every `api status`, so it is probed
// over TLS first. Both schemes are always tried: the certificate may be set
// only in the server's own environment, which makes a loopback listener speak
// TLS too.
func TestProbeSchemesTryTLSFirstOffLoopback(t *testing.T) {
	if got := probeSchemes(false); len(got) != 2 || got[0] != "https://" {
		t.Errorf("a network listener is probed over TLS first, then in the clear: %v", got)
	}
	if got := probeSchemes(true); len(got) != 2 || got[0] != "http://" {
		t.Errorf("a loopback listener is probed in the clear first, then over TLS: %v", got)
	}
}

// Whether /v1 is asked is the caller's call: the main listener off loopback is
// not expected to serve it, so it is not probed for it.
func TestProbeAddrAsksForV1OnlyWhenWanted(t *testing.T) {
	addr := apiServer(t, true) // answers 401 on /v1/models
	if reachable, v1 := probeAddr(addr, true, false); !reachable || v1 {
		t.Errorf("a listener not expected to serve /v1 is not asked: reachable=%v v1=%v", reachable, v1)
	}
	if reachable, v1 := probeAddr(addr, true, true); !reachable || !v1 {
		t.Errorf("a listener expected to serve /v1 is asked: reachable=%v v1=%v", reachable, v1)
	}
}
func TestAPIStatusReadsTheDaemonHeartbeat(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	addr := apiServer(t, true)
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: addr, V1Addr: "127.0.0.1:2", APIConfinement: "sandboxed", V1Confinement: "chat-only", ContextConfinement: "sandboxed"}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStatus(t, out)
	if !st.Daemon.Running || st.Daemon.APIAddr != addr || st.Daemon.V1Addr != "127.0.0.1:2" {
		t.Fatalf("daemon: %+v", st.Daemon)
	}
	if st.Listeners[0].Addr != addr || !st.Listeners[0].Reachable || !st.Listeners[0].V1Answers || st.Listeners[1].Addr != "127.0.0.1:2" {
		t.Errorf("listeners from the heartbeat: %+v", st.Listeners)
	}
	// The policies are what the daemon applies, not what this shell's
	// environment would give (any on loopback, chat-only elsewhere).
	if l := st.Listeners[0]; l.Confinement != "sandboxed" || l.ConfinementSource != "daemon" || l.ContextConfinement != "sandboxed" {
		t.Errorf("main: %+v", l)
	}
	// The context maximum never goes above what the listener serves.
	if l := st.Listeners[1]; l.Confinement != "chat-only" || l.ConfinementSource != "daemon" || l.ContextConfinement != "chat-only" {
		t.Errorf("v1: %+v", l)
	}
}
