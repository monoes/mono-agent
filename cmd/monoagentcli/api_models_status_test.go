package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// fakeAPIMonomind points monomind at a script that lists three runtimes (the
// shapes a real monomind 2.22 reports): claude (chat-only), codex
// (sandboxed) and antigravity (unconfined).
func fakeAPIMonomind(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake monomind is a shell script")
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.22.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models","agent-exec-sandbox"]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then
  echo '{"v":1,"agents":[
    {"id":"claude","installed":true,"binary":"/usr/local/bin/claude","version":"2.1.0","install_hint":"","native_sandbox":"monomind","sandbox_modes":["read-only","workspace-write","full"]},
    {"id":"codex","installed":true,"binary":"/usr/local/bin/codex","version":null,"install_hint":"","native_sandbox":"full","sandbox_modes":["read-only","workspace-write","full"]},
    {"id":"antigravity","installed":true,"binary":"/usr/local/bin/agy","version":"1.2.14","install_hint":"","native_sandbox":"none","sandbox_modes":["restricted","full"]}]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "models" ]; then
  case "$4" in
    claude) echo '{"v":1,"runtime":"claude","supported":true,"models":[{"id":"default","label":"Default"}]}' ;;
    codex) echo '{"v":1,"runtime":"codex","supported":true,"models":[{"id":"gpt-6-astra","label":"GPT-6-Astra"}]}' ;;
    antigravity) echo '{"v":1,"runtime":"antigravity","supported":true,"models":[{"id":"gemini-3.8-flash-high","label":"Gemini 3.8 Flash"}]}' ;;
  esac
  exit 0
fi
exit 2
`
	bin := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
}

func decodeModels(t *testing.T, out string) apiModelsJSON {
	t.Helper()
	var got apiModelsJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not an api models document: %v\n%s", err, out)
	}
	return got
}

func allowedByID(m apiModelsJSON) map[string]bool {
	out := map[string]bool{}
	for _, x := range m.Models {
		out[x.ID] = x.Allowed
	}
	return out
}

func contextAllowedByID(m apiModelsJSON) map[string]bool {
	out := map[string]bool{}
	for _, x := range m.Models {
		out[x.ID] = x.ContextAllowed
	}
	return out
}

func TestAPIModelsMarksWhatEachPolicyAllows(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	loopback := decodeModels(t, out)
	if loopback.Policy.For != "loopback" || loopback.Policy.Confinement != "any" {
		t.Errorf("loopback policy: %+v", loopback.Policy)
	}
	// It evaluates this shell's flags and environment, not a running server: say so.
	if loopback.Policy.Source != "shell" {
		t.Errorf("policy.source = %q, want shell", loopback.Policy.Source)
	}
	classes := map[string]string{}
	for _, m := range loopback.Models {
		classes[m.ID] = m.Confinement
		if !m.Allowed {
			t.Errorf("%s must be allowed on a loopback listener", m.ID)
		}
	}
	if classes["claude/default"] != "chat-only" || classes["codex/gpt-6-astra"] != "sandboxed" ||
		classes["antigravity/default"] != "unconfined" || classes["codex/default"] != "sandboxed" {
		t.Errorf("classes: %v", classes)
	}

	// A key created with --context is held to chat-only unless raised.
	if loopback.Policy.ContextConfinement != "chat-only" {
		t.Errorf("context confinement: %+v", loopback.Policy)
	}
	if c := contextAllowedByID(loopback); !c["claude/default"] || c["codex/gpt-6-astra"] || c["antigravity/default"] {
		t.Errorf("models a context key may use by default: %v", c)
	}
	out, _, _ = runAPI(t, db, "default", true, "models", "--context-confinement", "sandboxed")
	raised := decodeModels(t, out)
	if c := contextAllowedByID(raised); !c["claude/default"] || !c["codex/gpt-6-astra"] || c["antigravity/default"] || raised.Policy.ContextConfinement != "sandboxed" {
		t.Errorf("--context-confinement sandboxed: %v %+v", c, raised.Policy)
	}
	// ... and never above what the listener serves.
	out, _, _ = runAPI(t, db, "default", true, "models", "--for", "network", "--context-confinement", "any")
	if capped := decodeModels(t, out); capped.Policy.ContextConfinement != "chat-only" || contextAllowedByID(capped)["codex/gpt-6-astra"] {
		t.Errorf("a network listener serves chat-only whatever the context maximum: %+v", capped.Policy)
	}

	out, _, _ = runAPI(t, db, "default", true, "models", "--for", "network")
	network := decodeModels(t, out)
	if network.Policy.Confinement != "chat-only" {
		t.Errorf("network policy: %+v", network.Policy)
	}
	if a := allowedByID(network); !a["claude/default"] || a["codex/gpt-6-astra"] || a["antigravity/default"] {
		t.Errorf("network allowed: %v", a)
	}

	out, _, _ = runAPI(t, db, "default", true, "models", "--for", "network", "--confinement", "sandboxed")
	if a := allowedByID(decodeModels(t, out)); !a["codex/gpt-6-astra"] || a["antigravity/default"] {
		t.Errorf("sandboxed override: %v", a)
	}

	// The environment sets the policy too, and the flag beats it.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	out, _, _ = runAPI(t, db, "default", true, "models")
	if a := allowedByID(decodeModels(t, out)); a["codex/gpt-6-astra"] {
		t.Errorf("MONOAGENT_API_CONFINEMENT=chat-only ignored: %v", a)
	}
	out, _, _ = runAPI(t, db, "default", true, "models", "--confinement", "any")
	if a := allowedByID(decodeModels(t, out)); !a["antigravity/default"] {
		t.Errorf("--confinement any must beat the environment: %v", a)
	}
}

func TestAPIModelsRejectsBadValuesWithExit3(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	for _, args := range [][]string{{"models", "--for", "moon"}, {"models", "--confinement", "everything"}, {"models", "--context-confinement", "everything"}} {
		if _, _, err := runAPI(t, db, "default", true, args...); exitCode(err) != 3 {
			t.Errorf("%v: exit %d (%v), want 3", args, exitCode(err), err)
		}
	}
}

func TestAPIModelsHumanTable(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	out, _, err := runAPI(t, db, "default", false, "models", "--for", "network")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MODEL", "CONFINEMENT", "CONTEXT KEY", "claude/default", "chat-only", "no (policy)", "yes", "this shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

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

// A running daemon serves what it reports: one started with --api=false and no
// --v1-addr serves no /v1 at all, whatever this shell's environment says.
func TestAPIStatusListsNothingWhenTheDaemonServesNoAPI(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", apiServer(t, true))                       // this shell's environment says there is a main listener...
	t.Setenv("MONOAGENT_API_V1_ADDR", apiServer(t, true))                        // ...and a dedicated one
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid()}); err != nil { // the daemon reports neither
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	st := decodeStatus(t, out)
	if !st.Daemon.Running || len(st.Listeners) != 0 {
		t.Fatalf("a daemon that serves no API has no listener, whatever the shell's environment says: %+v", st)
	}
	human, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(human, "serves no") {
		t.Errorf("the output must say so:\n%s", human)
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
	for _, l := range decodeStatus(t, out).Listeners {
		if l.Name == "v1" && (!l.Reachable || !l.V1Answers) {
			t.Errorf("a loopback listener that speaks TLS must be found: %+v", l)
		}
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

func TestEffectiveContextMaxPrecedence(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, c := range []struct {
		flag, env string
		want      openaiapi.Class
	}{
		{"", "", openaiapi.ChatOnly}, // nothing set: chat-only
		{"", "sandboxed", openaiapi.Sandboxed},
		{"any", "chat-only", openaiapi.Unconfined}, // the flag beats the environment
	} {
		got, err := effectiveContextMax(c.flag, env(c.env))
		if err != nil || got != c.want {
			t.Errorf("effectiveContextMax(%q, env=%q) = %v, %v; want %v", c.flag, c.env, got, err, c.want)
		}
	}
	if _, err := effectiveContextMax("nope", env("")); exitCode(err) != 3 {
		t.Errorf("a bad value must be invalid input, got exit %d", exitCode(err))
	}
}

func TestEffectivePolicyPrecedence(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, c := range []struct {
		addr, flag, env, want string
	}{
		{"127.0.0.1:1", "", "", "any"},
		{"0.0.0.0:1", "", "", "chat-only"},
		{"127.0.0.1:1", "", "chat-only", "chat-only"},
		{"0.0.0.0:1", "any", "chat-only", "any"}, // the flag beats the environment
	} {
		got, err := effectivePolicy(c.addr, c.flag, env(c.env))
		if err != nil || got.String() != c.want {
			t.Errorf("effectivePolicy(%q, %q, env=%q) = %s, %v; want %s", c.addr, c.flag, c.env, got, err, c.want)
		}
	}
	if _, err := effectivePolicy("127.0.0.1:1", "nope", env("")); exitCode(err) != 3 {
		t.Errorf("a bad value must be invalid input, got exit %d", exitCode(err))
	}
}
