package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime" // the package has a type named runtime
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// fakeAPIMonomind points monomind at a script that lists three runtimes, the shapes
// a real monomind 2.22 reports: claude (chat-only), codex (sandboxed) and
// antigravity (unconfined). The CLI's tests use the same script; it is a test
// helper of package main and cannot be imported.
func fakeAPIMonomind(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
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

// pinAPIEnv clears everything api_models_list reads from the environment of the
// MCP server, so a test sets exactly what it means to.
func pinAPIEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"MONOAGENT_API_CONFINEMENT", "MONOAGENT_API_CONTEXT_CONFINEMENT", "MONOAGENT_API_AUTO_CONFINEMENT", "TYPESAFE_API_KEY"} {
		t.Setenv(v, "")
	}
}

func modelsReport(t *testing.T, s *Server, args map[string]any) openaiapi.ModelsReport {
	t.Helper()
	text := mustCall(t, s, "api_models_list", args)
	var r openaiapi.ModelsReport
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("not an api models document: %v\n%s", err, text)
	}
	return r
}

func classesOf(r openaiapi.ModelsReport) map[string]string {
	out := map[string]string{}
	for _, m := range r.Models {
		out[m.ID] = m.Confinement
	}
	return out
}

func allowedOf(r openaiapi.ModelsReport) map[string]bool {
	out := map[string]bool{}
	for _, m := range r.Models {
		if m.Allowed {
			out[m.ID] = true
		}
	}
	return out
}

func TestAPIModelsListReturnsEachModelWithItsConfinementClass(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, _ := newAPIKeyServer(t, false)

	r := modelsReport(t, s, nil)
	wantClasses := map[string]string{
		"claude/default": "chat-only", "codex/default": "sandboxed", "codex/gpt-6-astra": "sandboxed",
		"antigravity/default": "unconfined", "antigravity/gemini-3.8-flash-high": "unconfined",
	}
	if got := classesOf(r); !reflect.DeepEqual(got, wantClasses) {
		t.Errorf("classes %v, want %v", got, wantClasses)
	}
	if len(allowedOf(r)) != 5 {
		t.Errorf("a loopback listener serves every runtime: %v", allowedOf(r))
	}
	// The policy is the MCP server's own: say so, not "shell".
	if p := r.Policy; r.V != 1 || p.For != "loopback" || p.Confinement != "any" || p.ContextConfinement != "chat-only" ||
		p.AutoConfinement != "chat-only" || p.Source != "mcp" {
		t.Errorf("policy %+v", p)
	}
	// The surface is off for the profile, and the document says what to do about it.
	if r.Auto.Available || !strings.Contains(r.Auto.Missing, "jev enable api_auto") {
		t.Errorf("auto %+v", r.Auto)
	}
}

func TestAPIModelsListTakesTheFlagsOfApiModels(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, _ := newAPIKeyServer(t, false)

	network := modelsReport(t, s, map[string]any{"for": "network"})
	if network.Policy.For != "network" || network.Policy.Confinement != "chat-only" {
		t.Errorf("a network listener serves chat-only by default: %+v", network.Policy)
	}
	if got := allowedOf(network); !reflect.DeepEqual(got, map[string]bool{"claude/default": true}) {
		t.Errorf("served on a network listener: %v", got)
	}

	// A key created with context and the auto model are each held to their own cap,
	// and neither goes above what the listener serves.
	raised := modelsReport(t, s, map[string]any{"context_confinement": "sandboxed", "auto_confinement": "any"})
	if raised.Policy.ContextConfinement != "sandboxed" || raised.Policy.AutoConfinement != "any" {
		t.Errorf("caps %+v", raised.Policy)
	}
	for _, m := range raised.Models {
		wantContext := m.Confinement != "unconfined"
		if m.ContextAllowed != wantContext || !m.AutoAllowed {
			t.Errorf("%s (%s): context_allowed %v auto_allowed %v", m.ID, m.Confinement, m.ContextAllowed, m.AutoAllowed)
		}
	}
	capped := modelsReport(t, s, map[string]any{"for": "network", "context_confinement": "any", "auto_confinement": "any"})
	if capped.Policy.ContextConfinement != "chat-only" || capped.Policy.AutoConfinement != "chat-only" {
		t.Errorf("a network listener serves chat-only whatever the caps say: %+v", capped.Policy)
	}

	sandboxed := modelsReport(t, s, map[string]any{"confinement": "sandboxed"})
	if got := allowedOf(sandboxed); len(got) != 3 || got["antigravity/default"] {
		t.Errorf("--confinement sandboxed serves claude and codex only: %v", got)
	}
	// Empty strings are what some clients send for an argument they leave out.
	if blank := modelsReport(t, s, map[string]any{"for": "", "confinement": "", "context_confinement": "", "auto_confinement": ""}); blank.Policy != modelsReport(t, s, nil).Policy {
		t.Errorf("empty arguments must mean unset: %+v", blank.Policy)
	}
}

// The MCP server's own environment sets the policy, and an argument beats it.
func TestAPIModelsListReadsTheServersEnvironment(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, _ := newAPIKeyServer(t, false)

	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	t.Setenv("MONOAGENT_API_CONTEXT_CONFINEMENT", "chat-only")
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "sandboxed")
	r := modelsReport(t, s, nil)
	if got := allowedOf(r); len(got) != 1 || r.Policy.Confinement != "chat-only" {
		t.Errorf("MONOAGENT_API_CONFINEMENT=chat-only ignored: %v %+v", got, r.Policy)
	}
	// The auto cap is never above the listener, so it reads chat-only here.
	if r.Policy.AutoConfinement != "chat-only" {
		t.Errorf("auto_confinement %q, want chat-only: the listener serves nothing more", r.Policy.AutoConfinement)
	}

	r = modelsReport(t, s, map[string]any{"confinement": "any"})
	if r.Policy.Confinement != "any" || r.Policy.AutoConfinement != "sandboxed" {
		t.Errorf("the argument must beat the environment, and MONOAGENT_API_AUTO_CONFINEMENT=sandboxed applies: %+v", r.Policy)
	}
}

func TestAPIModelsListRefusesBadValues(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, _ := newAPIKeyServer(t, false)

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"for": "moon"}, `for must be loopback or network, got "moon"`},
		{map[string]any{"confinement": "everything"}, "confinement (MONOAGENT_API_CONFINEMENT): unknown confinement"},
		{map[string]any{"context_confinement": "everything"}, "context_confinement (MONOAGENT_API_CONTEXT_CONFINEMENT): unknown confinement"},
		{map[string]any{"auto_confinement": "everything"}, "auto_confinement (MONOAGENT_API_AUTO_CONFINEMENT): unknown confinement"},
		{map[string]any{"for": 3}, "invalid arguments"},
	} {
		text, err := callAPITool(t, s, "api_models_list", c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %q, %v; want an error containing %q", c.args, text, err, c.want)
		}
	}
}

// Whether auto works is a fact about the server's profile: the surface switched on
// for another profile does nothing here.
func TestAPIModelsListSaysWhetherAutoWorksForTheServersProfile(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	s, dbPath := newAPIKeyServer(t, false)
	side := sideDB(t, dbPath)

	if err := jevconf.SetEnabled(side.DB, "other-profile", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	if r := modelsReport(t, s, nil); r.Auto.Available || !strings.Contains(r.Auto.Missing, "jev enable api_auto") {
		t.Errorf("the surface is on for another profile only: %+v", r.Auto)
	}

	if err := jevconf.SetEnabled(side.DB, "default", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	r := modelsReport(t, s, nil)
	want := openaiapi.AutoReport{Available: true, KeySource: "env", Confinement: "chat-only", Candidates: 1, HeldBack: 4}
	if r.Auto != want {
		t.Errorf("surface on and a key: %+v, want %+v", r.Auto, want)
	}
	if r = modelsReport(t, s, map[string]any{"auto_confinement": "any"}); r.Auto.Candidates != 5 || r.Auto.HeldBack != 0 || r.Auto.Confinement != "unconfined" {
		t.Errorf("auto raised to any: %+v", r.Auto)
	}
}
