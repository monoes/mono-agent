package mcp

// api_models_list stands for a server started now, as `api models` does, so it reads what that server
// reads: the argument, then this MCP server's environment, then the settings saved with
// `api config set` / api_config_set, then the default. It used to read the environment alone, and
// said something a server started now would not do.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/storage"
)

// saveSettings saves settings in the database of a test, as `api config set` does, and without
// checking them (a value that fails its rule is how a hand edit looks).
func saveSettings(t *testing.T, db *storage.Database, words ...string) {
	t.Helper()
	if err := apiconfig.Update(context.Background(), db.DB, func(s *apiconfig.Settings) error {
		for _, w := range words {
			key, value, _ := strings.Cut(w, "=")
			if err := s.Set(key, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIModelsListReadsTheSavedSettings(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, dbPath := newAPIKeyServer(t, false)
	side := sideDB(t, dbPath)

	saveSettings(t, side, "confinement=sandboxed", "context_confinement=sandboxed", "auto_confinement=sandboxed", "image_runtimes=none", "tool_runtimes=none")
	r := modelsReport(t, s, nil)
	if p := r.Policy; p.Confinement != "sandboxed" || p.ContextConfinement != "sandboxed" || p.AutoConfinement != "sandboxed" {
		t.Errorf("the saved classes: %+v", p)
	}
	for id, caps := range capabilitiesOf(r) {
		if !slices.Equal(caps, []string{"text"}) {
			t.Errorf("%s: capabilities %v, want text alone: images and tools are saved as none", id, caps)
		}
	}
	if got := allowedOf(r); !got["claude/default"] || !got["codex/default"] || got["antigravity/default"] {
		t.Errorf("a saved confinement of sandboxed serves claude and codex and not antigravity: %v", got)
	}

	// The server reads the saved settings on every call, as a server started now would: a change
	// made after the first call is seen by the next.
	saveSettings(t, side, "confinement=chat-only", "image_runtimes=codex")
	r = modelsReport(t, s, nil)
	if got := allowedOf(r); !reflect.DeepEqual(got, map[string]bool{"claude/default": true}) {
		t.Errorf("a later save of confinement=chat-only: %v", got)
	}
	if !slices.Contains(capabilitiesOf(r)["codex/default"], "image") {
		t.Errorf("a later save of image_runtimes=codex: %v", capabilitiesOf(r)["codex/default"])
	}
}

// The layers are the server's: an argument beats the environment, and the environment beats what
// is saved, which beats the default.
func TestAPIModelsListOrdersTheArgumentTheEnvironmentTheSavedSettingsAndTheDefault(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, dbPath := newAPIKeyServer(t, false)
	saveSettings(t, sideDB(t, dbPath), "confinement=sandboxed", "tool_runtimes=none")

	// Saved only.
	if r := modelsReport(t, s, nil); r.Policy.Confinement != "sandboxed" {
		t.Errorf("saved only: %+v", r.Policy)
	}
	// The environment over what is saved. An empty variable is an unset one.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "claude")
	r := modelsReport(t, s, nil)
	if r.Policy.Confinement != "chat-only" || !slices.Contains(capabilitiesOf(r)["claude/default"], "tools") {
		t.Errorf("the environment over the saved settings: %+v %v", r.Policy, capabilitiesOf(r)["claude/default"])
	}
	// An argument over both.
	if r := modelsReport(t, s, map[string]any{"confinement": "any"}); r.Policy.Confinement != "any" {
		t.Errorf("an argument over the environment and the saved settings: %+v", r.Policy)
	}
	// With no variable and nothing saved for a setting, the default is what is left.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	if r := modelsReport(t, s, map[string]any{"for": "network"}); r.Policy.Confinement != "sandboxed" {
		t.Errorf("a saved confinement holds on a network listener too, since it is one value: %+v", r.Policy)
	}
	if err := apiconfig.Update(context.Background(), sideDB(t, dbPath).DB, func(st *apiconfig.Settings) error { st.Unset("confinement"); return nil }); err != nil {
		t.Fatal(err)
	}
	if r := modelsReport(t, s, map[string]any{"for": "network"}); r.Policy.Confinement != "chat-only" {
		t.Errorf("nothing saved: the default of a network listener is chat-only: %+v", r.Policy)
	}
}

// A saved setting that fails its rule is the operator's mistake, as a bad variable is: the answer
// names the setting and how to fix it and repeats nothing, and the call does not guess.
func TestAPIModelsListRefusesSavedSettingsThatFailTheirRules(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, dbPath := newAPIKeyServer(t, false)
	saveSettings(t, sideDB(t, dbPath), "confinement=everything-supersecret")

	_, err := callAPITool(t, s, "api_models_list", nil)
	if err == nil || !strings.Contains(err.Error(), "confinement must be chat-only, sandboxed or any") || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("a saved confinement that is not a class: %v, want the setting and the rule, and not the value", err)
	}
	if !strings.Contains(err.Error(), "api config") {
		t.Errorf("the answer must say how to fix it: %v", err)
	}
	// An argument cannot go around it: the server started now would not start.
	if _, err := callAPITool(t, s, "api_models_list", map[string]any{"confinement": "any"}); err == nil {
		t.Error("an argument must not hide a saved setting that would stop the server")
	}
}

func TestAPIModelsListRefusesASavedDocumentItCannotRead(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	s, dbPath := newAPIKeyServer(t, false)
	if _, err := sideDB(t, dbPath).DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, `not json at all`); err != nil {
		t.Fatal(err)
	}
	if _, err := callAPITool(t, s, "api_models_list", nil); err == nil {
		t.Error("a saved document that cannot be read must be an error, not an ignored one")
	}
}

// What a model has of the tool is its description: it must say that the saved settings are a layer,
// and where to see them.
func TestAPIModelsListDescriptionSaysThatSavedSettingsCount(t *testing.T) {
	var d string
	for _, tl := range apiTools() {
		if tl.name == "api_models_list" {
			d = tl.description
		}
	}
	// The classes, and each of the two lists, say where the saved layer stands, and where to see it.
	for _, want := range []string{"settings saved for the server", "saved image_runtimes", "saved tool_runtimes", "api_config_get", "api_status"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description of api_models_list does not mention %q: %s", want, d)
		}
	}
	if strings.Contains(d, "monoagentcli api status") {
		t.Errorf("the description sends a model to a command it cannot run, where it has api_status: %s", d)
	}
}
