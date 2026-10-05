package mcp

// The daemon starts without the OpenAI-compatible API when it cannot use the settings saved with
// api_config_set, so a restart would replace a daemon that serves it with one that does not (C2 of the
// correctness review of phase 6). api_config_apply reads the saved settings first, as `daemon restart`
// does, fails with the words every tool that reads them has, and restarts nothing.

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

func TestAPIConfigApplyRefusesWhenTheSavedSettingsCannotBeUsed(t *testing.T) {
	for name, c := range map[string]struct{ row, contains string }{
		"a row that is not JSON":            {`not json`, "the saved settings are damaged"},
		"a saved value that fails its rule": {`{"v":1,"max_concurrent":99}`, "max_concurrent must be"},
		"a row in a newer format":           {`{"v":2}`, apiconfig.Row},
	} {
		t.Run(name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{registered: true})
			if _, err := f.Side.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, apiconfig.Row, c.row); err != nil {
				t.Fatal(err)
			}
			_, err := f.call("api_config_apply", nil)
			if err == nil || !strings.Contains(err.Error(), c.contains) {
				t.Fatalf("the call: %v, want an error that says %q", err, c.contains)
			}
			if got := f.Installer.everything(); got != "" {
				t.Errorf("a refused call asked the service manager: %q", got)
			}
		})
	}
}

// Settings that can be used, and nothing saved, are restarted as they were.
func TestAPIConfigApplyWithUsableSavedSettingsRestarts(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	f.save("max_concurrent=8", "confinement=sandboxed")
	f.mustCall("api_config_apply", nil)
	if got := f.Installer.everything(); got != "status,restart" {
		t.Errorf("the service manager was asked %q, want status and then restart", got)
	}
}
