package mcp

// api_config_get is the document of `monoagentcli api config show --json`, built by apiconfig.Show from
// this MCP server's environment, the database, the daemon's heartbeat and the service manager.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

func TestAPIConfigGetIsTheShowDocumentOfThisServer(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	text := f.mustCall("api_config_get", nil)
	asked := f.Installer.everything() // before the expected document below asks too
	r := decodeConfigReport(t, text)

	if r.V != 1 || r.Environment != "mcp" || len(r.Settings) != 10 || r.RestartNeeded || len(r.Problems) != 0 {
		t.Fatalf("report %+v", r)
	}
	var keys []string
	for _, row := range r.Settings {
		keys = append(keys, row.Key)
		if row.State != apiconfig.StateNotRunning || row.Saved != "" || row.Source != apiconfig.SourceDefault || row.Effective != row.Default || row.Running != nil {
			t.Errorf("%s: nothing saved, nothing set and no daemon: %+v", row.Key, row)
		}
	}
	if !reflect.DeepEqual(keys, apiconfig.Keys()) {
		t.Errorf("the settings are %v, want the ten, in the order of every document: %v", keys, apiconfig.Keys())
	}
	if r.Daemon.Running || r.Daemon.ReportsSettings || !r.Daemon.Autostart {
		t.Errorf("no daemon is running, and one is registered for auto-start: %+v", r.Daemon)
	}

	// It is the document Show builds from the same inputs, byte for byte, and nothing else.
	want, err := apiconfig.Show(context.Background(), f.Side.DB, apiconfig.Env{Environment: "mcp", Installer: f.Installer, Heartbeat: f.heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	wantText, _ := json.MarshalIndent(want, "", "  ")
	if text != string(wantText) {
		t.Errorf("the tool's document is not Show's:\n%s\nvs\n%s", text, wantText)
	}
	// It only asks the service manager whether the daemon is registered.
	if asked != "status" {
		t.Errorf("the service manager was asked %q: a read asks Status and nothing else", asked)
	}
}

// What is saved, what this server's environment says over it, and where each comes from.
func TestAPIConfigGetSaysWhatIsSavedAndWhatTakesEffect(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("max_concurrent=8", "v1_addr=0.0.0.0:9443", "image_runtimes=agy, Codex")

	r := decodeConfigReport(t, f.mustCall("api_config_get", nil))
	if row := rowOf(t, r, "max_concurrent"); row.Saved != "8" || row.Effective != "8" || row.Source != "saved" || row.Default != "4" {
		t.Errorf("max_concurrent: %+v", row)
	}
	if row := rowOf(t, r, "v1_addr"); row.Saved != "0.0.0.0:9443" || row.Effective != "0.0.0.0:9443" || row.Source != "saved" || row.ServerFlag != "--v1-addr" || row.Env != "MONOAGENT_API_V1_ADDR" {
		t.Errorf("v1_addr: %+v", row)
	}
	// What is saved is shown in its canonical spelling.
	if row := rowOf(t, r, "image_runtimes"); row.Saved != "antigravity,codex" {
		t.Errorf("image_runtimes: %+v", row)
	}

	// This MCP server's own environment is over what is saved, and the saved value stays what it was.
	t.Setenv("MONOAGENT_API_MAX_CONCURRENT", "12")
	r = decodeConfigReport(t, f.mustCall("api_config_get", nil))
	if row := rowOf(t, r, "max_concurrent"); row.Saved != "8" || row.Effective != "12" || row.Source != "env" {
		t.Errorf("max_concurrent with a variable set: %+v", row)
	}
}

// Against a live daemon: what it started with, where that came from, and where each setting stands.
func TestAPIConfigGetSaysWhereEachSettingStandsAgainstTheRunningDaemon(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	f.daemon(daemonhb.Heartbeat{APISettings: map[string]daemonhb.APISetting{
		"max_concurrent": {Value: "4", Source: "default"},
		"turn_timeout":   {Value: "5m", Source: "env"},
		"image_runtimes": {Value: "codex", Source: "saved"},
	}})
	f.save("max_concurrent=8", "turn_timeout=20m", "image_runtimes=codex")

	r := decodeConfigReport(t, f.mustCall("api_config_get", nil))
	states := map[string]string{}
	for _, row := range r.Settings {
		states[row.Key] = row.State
	}
	if states["max_concurrent"] != "pending_restart" || states["turn_timeout"] != "overridden" || states["image_runtimes"] != "applied" || states["v1_addr"] != "unknown" {
		t.Errorf("states %v", states)
	}
	if !r.Daemon.Running || !r.Daemon.ReportsSettings || !r.Daemon.Autostart || !r.RestartNeeded {
		t.Errorf("daemon %+v, restart needed %v", r.Daemon, r.RestartNeeded)
	}
	if row := rowOf(t, r, "turn_timeout"); row.Running == nil || *row.Running != "5m" || row.RunningSource != "env" {
		t.Errorf("turn_timeout: the daemon runs 5m from its environment: %+v", row)
	}
	// An older daemon says nothing of its settings.
	f.daemon(daemonhb.Heartbeat{})
	r = decodeConfigReport(t, f.mustCall("api_config_get", nil))
	if !r.Daemon.Running || r.Daemon.ReportsSettings || rowOf(t, r, "max_concurrent").State != "unknown" || r.RestartNeeded {
		t.Errorf("a daemon that predates the report: %+v, states %s", r.Daemon, rowOf(t, r, "max_concurrent").State)
	}
}

// A saved value that fails its rule is how a hand edit looks: it is listed as a problem, so that it
// can be found and removed, and it does not make the read an error.
func TestAPIConfigGetListsASavedValueThatFailsItsRule(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("confinement=everything-supersecret")
	r := decodeConfigReport(t, f.mustCall("api_config_get", nil))
	if len(r.Problems) != 1 || r.Problems[0].Key != "confinement" || strings.Contains(r.Problems[0].Message, "supersecret") {
		t.Errorf("problems %+v: the setting and the rule, and not the value", r.Problems)
	}
}

func TestAPIConfigGetOfADocumentItCannotReadIsAnError(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	if _, err := f.Side.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, apiconfig.Row, `{"v":2}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call("api_config_get", nil); err == nil || !strings.Contains(err.Error(), apiconfig.Row) {
		t.Errorf("a document in a newer format: %v, want an error that names the row", err)
	}
}

// It takes no argument and changes nothing: a model that sends a change to the reader gets the
// document, and the change is not made.
func TestAPIConfigGetIgnoresWhatItIsSentAndChangesNothing(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	text := f.mustCall("api_config_get", map[string]any{"set": map[string]any{"max_concurrent": "9"}, "unset": "all"})
	if !f.saved().IsEmpty() {
		t.Errorf("a read saved something: %+v", f.saved())
	}
	if r := decodeConfigReport(t, text); settingsOf(r) != "" {
		t.Errorf("the document shows saved settings: %s", settingsOf(r))
	}
}
