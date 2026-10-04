package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

// enableAPIAuto switches the api_auto surface on for a profile in the test database.
func enableAPIAuto(t *testing.T, dbPath, profile string) {
	t.Helper()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := jevconf.SetEnabled(db.DB, profile, jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
}

// fakeMonomindWithAgents is a monomind whose scan lists exactly the given agents
// (the JSON of the "agents" array) and whose model lists are empty.
func fakeMonomindWithAgents(t *testing.T, agents string) {
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
  echo '{"v":1,"agents":[` + agents + `]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "models" ]; then
  echo '{"v":1,"runtime":"'"$4"'","supported":true,"models":[]}'
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

// `api models` says whether the auto model works for the profile and what is
// missing when it does not, and whose key it is when it comes from the environment.
func TestAPIModelsSaysWhetherAutoWorks(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "")
	t.Setenv("TYPESAFE_API_KEY", "")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeModels(t, out); m.Auto.Available || m.Auto.Candidates != 0 || !strings.Contains(m.Auto.Missing, "jev enable api_auto") {
		t.Errorf("surface off: %+v", m.Auto)
	}

	enableAPIAuto(t, db, "default")
	out, _, _ = runAPI(t, db, "default", true, "models")
	if m := decodeModels(t, out); m.Auto.Available || !strings.Contains(m.Auto.Missing, "Jev key") {
		t.Errorf("surface on, no key: %+v", m.Auto)
	}

	t.Setenv("TYPESAFE_API_KEY", "test-key")
	out, _, _ = runAPI(t, db, "default", true, "models", "--auto-confinement", "any")
	m := decodeModels(t, out)
	allowed := 0
	for _, x := range m.Models {
		if x.Allowed {
			allowed++
		}
	}
	if !m.Auto.Available || m.Auto.Missing != "" || m.Auto.Candidates != allowed || allowed < 2 || m.Auto.KeySource != "env" {
		t.Errorf("surface on, a key and nothing held back: %+v with %d models allowed", m.Auto, allowed)
	}

	// A key from the environment is this shell's: a running server reads its own.
	text, _, err := runAPI(t, db, "default", false, "models", "--auto-confinement", "any")
	if err != nil || !strings.Contains(text, "auto: available") || !strings.Contains(text, "this shell's TYPESAFE_API_KEY") {
		t.Errorf("the table must say it too, and whose key it is: %q, %v", text, err)
	}
}

// `api models` says what auto may pick: the models the listener serves that are
// within --auto-confinement (chat-only unless raised), which is what Jev is offered,
// and how many are held back.
func TestAPIModelsSaysWhatAutoMayPick(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "")
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	enableAPIAuto(t, db, "default")

	models := func(args ...string) apiModelsJSON {
		t.Helper()
		out, _, err := runAPI(t, db, "default", true, append([]string{"models"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return decodeModels(t, out)
	}
	autoAllowed := func(m apiModelsJSON) map[string]bool {
		got := map[string]bool{}
		for _, x := range m.Models {
			got[x.ID] = x.AutoAllowed
		}
		return got
	}

	// Nothing granted: claude's model only, though a loopback listener serves five.
	m := models()
	if m.Policy.AutoConfinement != "chat-only" || m.Auto.Confinement != "chat-only" || m.Auto.Candidates != 1 || m.Auto.HeldBack != 4 {
		t.Errorf("nothing granted: %+v %+v", m.Policy, m.Auto)
	}
	if a := autoAllowed(m); !a["claude/default"] || a["codex/default"] || a["codex/gpt-6-astra"] || a["antigravity/default"] {
		t.Errorf("by default auto may pick claude/default only: %v", a)
	}

	m = models("--auto-confinement", "sandboxed")
	if m.Policy.AutoConfinement != "sandboxed" || m.Auto.Confinement != "sandboxed" || m.Auto.Candidates != 3 || m.Auto.HeldBack != 2 {
		t.Errorf("sandboxed: %+v %+v", m.Policy, m.Auto)
	}
	if a := autoAllowed(m); !a["claude/default"] || !a["codex/default"] || !a["codex/gpt-6-astra"] || a["antigravity/default"] || a["antigravity/gemini-3.8-flash-high"] {
		t.Errorf("sandboxed: auto may pick claude and codex: %v", a)
	}

	m = models("--auto-confinement", "any")
	if m.Auto.Confinement != "unconfined" || m.Auto.Candidates != 5 || m.Auto.HeldBack != 0 {
		t.Errorf("any: %+v", m.Auto)
	}

	// Never above what the listener serves.
	m = models("--for", "network", "--auto-confinement", "any")
	if m.Policy.AutoConfinement != "chat-only" || m.Auto.Confinement != "chat-only" || m.Auto.Candidates != 1 || m.Auto.HeldBack != 0 {
		t.Errorf("a network listener serves chat-only whatever auto is granted: %+v %+v", m.Policy, m.Auto)
	}

	// The environment is read when the flag is not given, and the flag beats it.
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "sandboxed")
	if m = models(); m.Auto.Candidates != 3 {
		t.Errorf("MONOAGENT_API_AUTO_CONFINEMENT=sandboxed: %+v", m.Auto)
	}
	if m = models("--auto-confinement", "chat-only"); m.Auto.Candidates != 1 {
		t.Errorf("the flag beats the environment: %+v", m.Auto)
	}

	// The table says it too, and what to do about the models that are held back.
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "")
	text, _, err := runAPI(t, db, "default", false, "models")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AUTO", "auto: available", "up to chat-only", "4 more", "--auto-confinement"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table lacks %q:\n%s", want, text)
		}
	}
}

// With nothing for Jev to pick among the gateway does not serve auto, and the CLI
// says so instead of promising it: nothing installed, or only runtimes above what
// auto may pick (codex alone, with auto held to chat-only unless raised).
func TestAPIModelsSaysAutoNeedsAModel(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_AUTO_CONFINEMENT", "")
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	enableAPIAuto(t, db, "default")

	fakeMonomindWithAgents(t, ``)
	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeModels(t, out); m.Auto.Available || m.Auto.Candidates != 0 || !strings.Contains(m.Auto.Missing, "model") {
		t.Errorf("nothing installed: %+v", m.Auto)
	}

	fakeMonomindWithAgents(t, `{"id":"codex","installed":true,"binary":"/usr/local/bin/codex","version":null,"install_hint":"","native_sandbox":"full","sandbox_modes":["read-only","workspace-write","full"]}`)
	out, _, _ = runAPI(t, db, "default", true, "models")
	if m := decodeModels(t, out); m.Auto.Available || m.Auto.Candidates != 0 || !strings.Contains(m.Auto.Missing, "--auto-confinement") {
		t.Errorf("only a sandboxed runtime, auto held to chat-only: %+v", m.Auto)
	}
	out, _, _ = runAPI(t, db, "default", true, "models", "--auto-confinement", "sandboxed")
	if m := decodeModels(t, out); !m.Auto.Available || m.Auto.Candidates != 1 || m.Auto.Confinement != "sandboxed" {
		t.Errorf("only a sandboxed runtime, auto raised to sandboxed: %+v", m.Auto)
	}
}

func TestAPIStatusSaysWhetherAutoWorks(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "127.0.0.1:1")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	t.Setenv("TYPESAFE_API_KEY", "")

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	if st := decodeStatus(t, out); st.Auto.Available || !strings.Contains(st.Auto.Missing, "jev enable api_auto") {
		t.Errorf("surface off: %+v", st.Auto)
	}
	text, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(text, "Auto model: off") || !strings.Contains(text, "jev enable api_auto") {
		t.Errorf("the text must say what to set up:\n%s", text)
	}

	enableAPIAuto(t, db, "default")
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	out, _, _ = runAPI(t, db, "default", true, "status")
	if st := decodeStatus(t, out); !st.Auto.Available || st.Auto.KeySource != "env" {
		t.Errorf("surface on and a key: %+v", st.Auto)
	}
	if text, _, _ := runAPI(t, db, "default", false, "status"); !strings.Contains(text, "Auto model: available") || !strings.Contains(text, "this shell's TYPESAFE_API_KEY") {
		t.Errorf("the text must say it works, and whose key it is:\n%s", text)
	}
}
