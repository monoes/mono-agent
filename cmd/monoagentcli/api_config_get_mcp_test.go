package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

// `api config show --json` and the tool api_config_get are one document, which says the environment
// it was made in: "shell" for the command, "mcp" for the tool. Apart from that word the two are the
// same bytes, whatever is saved, set or running.
func TestAPIConfigGetIsTheDocumentOfAPIConfigShowJSON(t *testing.T) {
	heartbeat := func(settings map[string]daemonhb.APISetting) *daemonhb.Heartbeat {
		return &daemonhb.Heartbeat{PID: os.Getpid(), APISettings: settings}
	}
	cases := []struct {
		name       string
		saved      []string
		env        map[string]string
		daemon     *daemonhb.Heartbeat
		registered bool
		state      map[string]string // the case exercises what it names: setting -> state
		source     map[string]string // setting -> where this process's value comes from
		problems   int
	}{
		{name: "nothing saved, no daemon", registered: true, state: map[string]string{"max_concurrent": "not_running"}},
		{name: "a setting saved", saved: []string{"max_concurrent=8", "v1_addr=0.0.0.0:9443", "image_runtimes=agy, Codex"}, state: map[string]string{"max_concurrent": "not_running"}, source: map[string]string{"max_concurrent": "saved"}},
		{
			name: "a setting overridden by this process's environment", saved: []string{"confinement=sandboxed"}, env: map[string]string{"MONOAGENT_API_CONFINEMENT": "chat-only"},
			source: map[string]string{"confinement": "env"},
		},
		{
			name: "pending restart", saved: []string{"max_concurrent=8"}, registered: true,
			daemon: heartbeat(map[string]daemonhb.APISetting{"max_concurrent": {Value: "4", Source: "default"}}),
			state:  map[string]string{"max_concurrent": "pending_restart", "v1_addr": "unknown"},
		},
		{
			name: "applied", saved: []string{"max_concurrent=8"}, registered: true,
			daemon: heartbeat(map[string]daemonhb.APISetting{"max_concurrent": {Value: "8", Source: "saved"}}),
			state:  map[string]string{"max_concurrent": "applied"},
		},
		{
			name: "overridden by the daemon's own flag and variable", saved: []string{"max_concurrent=8", "turn_timeout=20m"}, registered: true,
			daemon: heartbeat(map[string]daemonhb.APISetting{"max_concurrent": {Value: "6", Source: "flag"}, "turn_timeout": {Value: "5m", Source: "env"}}),
			state:  map[string]string{"max_concurrent": "overridden", "turn_timeout": "overridden"},
		},
		{name: "a daemon that predates the report", saved: []string{"max_concurrent=8"}, registered: true, daemon: heartbeat(nil), state: map[string]string{"max_concurrent": "unknown"}},
		{
			name: "pending restart and the daemon cannot be restarted", saved: []string{"max_concurrent=8"},
			daemon: heartbeat(map[string]daemonhb.APISetting{"max_concurrent": {Value: "4", Source: "default"}}),
			state:  map[string]string{"max_concurrent": "pending_restart"},
		},
		{name: "a saved value that fails its rule", saved: []string{"confinement=everything"}, problems: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := configTest(t)
			useInstaller(t, &fakeAutostart{installed: c.registered})
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			saveAt(t, db, c.saved...)
			if c.daemon != nil {
				t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
				if err := daemonhb.Write(*c.daemon); err != nil {
					t.Fatal(err)
				}
			}

			cli, _, err := runAPI(t, db, "default", true, "config", "show")
			if err != nil {
				t.Fatal(err)
			}
			tool, isErr := mcpOnce(t, mcpOptions(t, db, "default", false), "api_config_get", map[string]any{})
			if isErr {
				t.Fatalf("api_config_get failed: %s", tool)
			}

			// The case exercises what it names.
			doc := decodeConfig(t, cli)
			rows := map[string]apiconfig.SettingReport{}
			for _, row := range doc.Settings {
				rows[row.Key] = row
			}
			for key, want := range c.state {
				if rows[key].State != want {
					t.Errorf("the case does not exercise what it names: %s is %s, want %s", key, rows[key].State, want)
				}
			}
			for key, want := range c.source {
				if rows[key].Source != want {
					t.Errorf("the case does not exercise what it names: %s comes from %s, want %s", key, rows[key].Source, want)
				}
			}
			if doc.Environment != "shell" || doc.Daemon.Autostart != c.registered || len(doc.Problems) != c.problems || len(doc.Settings) != 10 {
				t.Errorf("the case does not exercise what it names: %+v", doc)
			}

			if want := asMCPDoc(cli); tool != want {
				t.Errorf("api_config_get is not the document of api config show --json: %s", firstDifference(want, tool))
			}
		})
	}
}
