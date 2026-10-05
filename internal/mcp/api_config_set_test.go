package mcp

// api_config_set saves and removes the settings of the API's server through apiconfig.Apply, so it
// is held to the rules of `api config set|unset`: the same checks, the same canonical spellings, the
// same document. These tests are about what the tool does with its two arguments; the exposure gate
// is in api_config_set_gate_test.go and what an error may repeat in api_config_set_echo_test.go.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

// set is the arguments of a call that saves the given settings.
func set(kv map[string]any) map[string]any { return map[string]any{"set": kv} }

// unset is the arguments of a call that removes the given settings (a list of keys, or "all").
func unset(what any) map[string]any { return map[string]any{"unset": what} }

func TestAPIConfigSetSavesAndReportsInTheCanonicalSpelling(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	text := f.mustCall("api_config_set", set(map[string]any{"max_concurrent": "8", "turn_timeout": "900s", "image_runtimes": "agy, Codex"}))

	r := decodeChangeResult(t, text)
	if !r.Applied || !reflect.DeepEqual(r.Changed, []string{"max_concurrent", "turn_timeout", "image_runtimes"}) || len(r.Widening) != 0 {
		t.Errorf("applied %v, changed %v, widening %+v", r.Applied, r.Changed, r.Widening)
	}
	// The document is the state after the change, made in this server's environment.
	if r.Environment != "mcp" || len(r.Settings) != 10 || rowOf(t, r.ConfigReport, "max_concurrent").Saved != "8" || rowOf(t, r.ConfigReport, "turn_timeout").Saved != "15m" {
		t.Errorf("the document after the change: %s", settingsOf(r.ConfigReport))
	}
	saved := f.saved()
	if saved.MaxConcurrent != "8" || saved.TurnTimeout != "15m" || saved.ImageRuntimes != "antigravity,codex" {
		t.Errorf("saved %+v: values are stored in the canonical spelling", saved)
	}
	// It saves; it restarts nothing, and without a daemon there is nothing to restart.
	if r.RestartNeeded || f.Installer.count("restart") != 0 || f.Installer.count("start") != 0 || f.Installer.count("install") != 0 {
		t.Errorf("restart needed %v; the service manager was asked %q", r.RestartNeeded, f.Installer.everything())
	}
}

// A number is what a model sends for a number; it is the text the CLI would have been given.
func TestAPIConfigSetTakesANumberForMaxConcurrentAndRefusesOneThatIsNotWhole(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	if r := decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"max_concurrent": 8}))); !reflect.DeepEqual(r.Changed, []string{"max_concurrent"}) || rowOf(t, r.ConfigReport, "max_concurrent").Saved != "8" {
		t.Errorf("max_concurrent as a number: %+v", r.Changed)
	}
	for _, bad := range []any{8.5, 0, 65, -1} {
		if _, err := f.call("api_config_set", set(map[string]any{"max_concurrent": bad})); err == nil || !strings.Contains(err.Error(), "max_concurrent must be an integer from 1 to 64") {
			t.Errorf("max_concurrent %v: %v, want the rule of the setting", bad, err)
		}
	}
	if got := f.saved().MaxConcurrent; got != "8" {
		t.Errorf("a refused change must leave what was saved: %q", got)
	}
}

// Only what is given changes: the other settings keep what they had, and the same value again is
// not a change.
func TestAPIConfigSetChangesOnlyWhatItIsGiven(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.mustCall("api_config_set", set(map[string]any{"max_concurrent": "8", "turn_timeout": "20m"}))

	r := decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"turn_timeout": "30m"})))
	if !reflect.DeepEqual(r.Changed, []string{"turn_timeout"}) {
		t.Errorf("changed %v", r.Changed)
	}
	if s := f.saved(); s.MaxConcurrent != "8" || s.TurnTimeout != "30m" {
		t.Errorf("saved %+v", s)
	}
	r = decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"turn_timeout": "30m"})))
	if !r.Applied || len(r.Changed) != 0 {
		t.Errorf("the same value again: applied %v, changed %v", r.Applied, r.Changed)
	}
	// A value that spells the default is saved, as the user said it, and is not exposure.
	r = decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"context_confinement": "chat-only", "max_concurrent": "4"})))
	if !reflect.DeepEqual(r.Changed, []string{"context_confinement", "max_concurrent"}) || len(r.Widening) != 0 {
		t.Errorf("a value that equals the default: changed %v, widening %+v", r.Changed, r.Widening)
	}
}

func TestAPIConfigSetUnsetRemovesWhatIsSaved(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("max_concurrent=8", "turn_timeout=20m", "context_confinement=sandboxed")

	// Keys, in either spelling; one that is not saved is no error.
	r := decodeChangeResult(t, f.mustCall("api_config_set", unset([]string{"turn_timeout", "max-concurrent", "v1_addr"})))
	if !r.Applied || !reflect.DeepEqual(r.Changed, []string{"max_concurrent", "turn_timeout"}) {
		t.Errorf("applied %v, changed %v", r.Applied, r.Changed)
	}
	if s := f.saved(); s.MaxConcurrent != "" || s.TurnTimeout != "" || s.ContextConfinement != "sandboxed" {
		t.Errorf("saved %+v", s)
	}
	if r = decodeChangeResult(t, f.mustCall("api_config_set", unset([]string{"max_concurrent"}))); len(r.Changed) != 0 {
		t.Errorf("an unset of what is not saved changed %v", r.Changed)
	}

	// The word all removes every setting.
	f.save("max_concurrent=8", "turn_timeout=20m")
	r = decodeChangeResult(t, f.mustCall("api_config_set", unset("all")))
	if !reflect.DeepEqual(r.Changed, []string{"context_confinement", "max_concurrent", "turn_timeout"}) || !f.saved().IsEmpty() {
		t.Errorf("unset all: changed %v, still saved %+v", r.Changed, f.saved())
	}
}

// Set and unset in one call, as the command takes them in two: the settings that are set and the
// ones that are removed together, and a key may not be both.
func TestAPIConfigSetSetsAndUnsetsInOneCall(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("max_concurrent=8", "turn_timeout=20m")
	r := decodeChangeResult(t, f.mustCall("api_config_set", map[string]any{"set": map[string]any{"max_concurrent": "9"}, "unset": []string{"turn_timeout"}}))
	if !reflect.DeepEqual(r.Changed, []string{"max_concurrent", "turn_timeout"}) {
		t.Errorf("changed %v", r.Changed)
	}
	if s := f.saved(); s.MaxConcurrent != "9" || s.TurnTimeout != "" {
		t.Errorf("saved %+v", s)
	}
}

// Everything a call may get wrong is refused with a text that names the setting and the rule, and
// nothing is saved: not the part that was right either.
func TestAPIConfigSetRefusesWhatTheCommandRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"nothing at all", map[string]any{}, "nothing to change"},
		{"empty set and unset", map[string]any{"set": map[string]any{}, "unset": []string{}}, "nothing to change"},
		{"a setting that does not exist", set(map[string]any{"nonsense": "x"}), "not a setting"},
		{"a class that is not one", set(map[string]any{"confinement": "everything"}), "confinement must be chat-only, sandboxed or any"},
		{"a context class that is not one", set(map[string]any{"context_confinement": "Any"}), "context_confinement must be chat-only, sandboxed or any"},
		{"too many turns", set(map[string]any{"max_concurrent": "65"}), "max_concurrent must be an integer from 1 to 64"},
		{"a padded timeout", set(map[string]any{"turn_timeout": " 15m"}), "turn_timeout must be a duration of at least 10s"},
		{"a list with a space inside an id", set(map[string]any{"tool_runtimes": "co dex"}), "tool_runtimes must be a comma-separated list of runtime ids"},
		{"none among others", set(map[string]any{"image_runtimes": "none,codex"}), "image_runtimes must be a comma-separated list of runtime ids"},
		{"an address without a port", set(map[string]any{"v1_addr": "9443"}), "v1_addr must be host:port"},
		{"an empty value", set(map[string]any{"max_concurrent": ""}), "must not be empty"},
		{"a blank value", set(map[string]any{"v1_addr": "  "}), "must not be empty"},
		{"a null value", set(map[string]any{"max_concurrent": nil}), "must not be empty"},
		{"a certificate without its key", set(map[string]any{"tls_cert_file": "/c.pem"}), "tls_cert_file and tls_key_file must be set together"},
		{"a key file without its certificate", set(map[string]any{"tls_key_file": "/k.pem"}), "tls_cert_file and tls_key_file must be set together"},
		{"the same setting set and unset", map[string]any{"set": map[string]any{"max_concurrent": "8"}, "unset": []string{"max_concurrent"}}, "is both set and unset"},
		{"every setting removed and one set", map[string]any{"set": map[string]any{"max_concurrent": "8"}, "unset": "all"}, "cannot be combined"},
		{"an unset that names no setting", unset([]string{"nonsense"}), "not a setting"},
		{"set is not an object", map[string]any{"set": []string{"max_concurrent"}}, "invalid arguments"},
		{"a value that is not text", set(map[string]any{"v1_addr": true}), "v1_addr must be text"},
		{"a value that is an object", set(map[string]any{"confinement": map[string]any{"a": 1}}), "confinement must be text"},
		{"unset is a number", unset(5), "unset must be"},
		{"unset is a word that is not all", unset("everything"), "unset must be"},
		{"unset is a list with a number in it", unset([]any{"max_concurrent", 5}), "unset must be"},
		{"all inside a list", unset([]string{"all"}), "to remove every setting"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{})
			f.save("turn_timeout=20m")
			if _, err := f.call("api_config_set", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one containing %q", err, c.want)
			}
			if got := f.saved(); got.TurnTimeout != "20m" || got.MaxConcurrent != "" || got.Confinement != "" || got.TLSCertFile != "" || got.V1Addr != "" {
				t.Errorf("a refused call changed what is saved: %+v", got)
			}
			if f.Installer.everything() != "" {
				t.Errorf("a refused change must not ask the service manager: %q", f.Installer.everything())
			}
		})
	}
}

// Both TLS files together, or removed together; and they are not exposure.
func TestAPIConfigSetSavesTheTLSFilesAsAPair(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	r := decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"tls_cert_file": "/etc/ssl/api.pem", "tls_key_file": "/etc/ssl/api.key"})))
	if !reflect.DeepEqual(r.Changed, []string{"tls_cert_file", "tls_key_file"}) || len(r.Widening) != 0 {
		t.Errorf("changed %v, widening %+v: the files are not exposure", r.Changed, r.Widening)
	}
	if _, err := f.call("api_config_set", unset([]string{"tls_key_file"})); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Errorf("removing one file of the pair: %v", err)
	}
	if r := decodeChangeResult(t, f.mustCall("api_config_set", unset([]string{"tls_cert_file", "tls_key_file"}))); len(r.Changed) != 2 || !f.saved().IsEmpty() {
		t.Errorf("removing both: changed %v, saved %+v", r.Changed, f.saved())
	}
}

// The tool answers what the command answers about a setting a running daemon was given its own
// value of: the saved value is kept, and the document says it has no effect for now.
func TestAPIConfigSetShowsAPendingRestartAndAnOverride(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	f.daemon(daemonhb.Heartbeat{APISettings: map[string]daemonhb.APISetting{"max_concurrent": {Value: "4", Source: "default"}, "turn_timeout": {Value: "5m", Source: "flag"}}})

	r := decodeChangeResult(t, f.mustCall("api_config_set", set(map[string]any{"max_concurrent": "8", "turn_timeout": "20m"})))
	if rowOf(t, r.ConfigReport, "max_concurrent").State != apiconfig.StatePendingRestart || rowOf(t, r.ConfigReport, "turn_timeout").State != apiconfig.StateOverridden || !r.RestartNeeded || !r.Daemon.Autostart {
		t.Errorf("states %s and %s, restart needed %v, autostart %v", rowOf(t, r.ConfigReport, "max_concurrent").State, rowOf(t, r.ConfigReport, "turn_timeout").State, r.RestartNeeded, r.Daemon.Autostart)
	}
	if f.Installer.count("restart") != 0 {
		t.Error("saving a setting must not restart the daemon: api_config_apply does")
	}
}
