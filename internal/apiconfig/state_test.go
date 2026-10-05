package apiconfig

import (
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

func heartbeatOf(settings map[string]daemonhb.APISetting) *daemonhb.Heartbeat {
	return &daemonhb.Heartbeat{PID: 1, APISettings: settings}
}

// Per setting, the saved layer against what the live daemon says it started with.
func TestState(t *testing.T) {
	run := func(value, source string) map[string]daemonhb.APISetting {
		return map[string]daemonhb.APISetting{"max_concurrent": {Value: value, Source: source}}
	}
	for _, c := range []struct {
		name  string
		saved string // max_concurrent
		hb    *daemonhb.Heartbeat
		want  string
	}{
		{"no live daemon", "8", nil, StateNotRunning},
		{"no live daemon, nothing saved", "", nil, StateNotRunning},
		{"a daemon that predates the report", "8", heartbeatOf(nil), StateUnknown},
		{"a daemon that reports other settings but not this one", "8", heartbeatOf(map[string]daemonhb.APISetting{"v1_addr": {Source: "default"}}), StateUnknown},

		// the daemon was given it by a flag or the environment: a saved value has no effect (P4)
		{"a flag, with a value saved", "8", heartbeatOf(run("2", "flag")), StateOverridden},
		{"a flag, with the same value saved", "2", heartbeatOf(run("2", "flag")), StateOverridden},
		{"a flag, with nothing saved", "", heartbeatOf(run("2", "flag")), StateOverridden},
		{"the environment", "8", heartbeatOf(run("2", "env")), StateOverridden},

		// it took the saved value or the default: what a restart would resolve is the saved value, else the default
		{"saved and running agree", "8", heartbeatOf(run("8", "saved")), StateApplied},
		{"saved changed", "9", heartbeatOf(run("8", "saved")), StatePendingRestart},
		{"saved was removed", "", heartbeatOf(run("8", "saved")), StatePendingRestart},
		{"saved since the daemon started", "8", heartbeatOf(run("4", "default")), StatePendingRestart},
		{"nothing saved, the default runs", "", heartbeatOf(run("4", "default")), StateApplied},
		{"a value that spells the default is no change", "4", heartbeatOf(run("4", "default")), StateApplied},
		{"saved in another spelling of the same value", "+8", heartbeatOf(run("8", "saved")), StateApplied},
		{"an invalid saved value is never what runs", "many", heartbeatOf(run("8", "saved")), StatePendingRestart},
		{"a source this binary does not know is compared like a saved one", "8", heartbeatOf(run("8", "somewhere")), StateApplied},
	} {
		t.Run(c.name, func(t *testing.T) {
			var saved Settings
			if c.saved != "" {
				_ = saved.Set("max_concurrent", c.saved)
			}
			if got := State(saved, "max_concurrent", c.hb); got != c.want {
				t.Errorf("State = %s, want %s", got, c.want)
			}
		})
	}
}

// What a restart would resolve is the saved layer alone: a daemon started by the login service
// does not read this process's environment (P5 of the plan).
func TestStateIgnoresThisProcessesEnvironment(t *testing.T) {
	t.Setenv("MONOAGENT_API_MAX_CONCURRENT", "50")
	var saved Settings
	_ = saved.Set("max_concurrent", "8")
	hb := heartbeatOf(map[string]daemonhb.APISetting{"max_concurrent": {Value: "8", Source: "saved"}})
	if got := State(saved, "max_concurrent", hb); got != StateApplied {
		t.Errorf("State = %s: this shell's variable must not decide what the daemon's restart would do", got)
	}
}

// The states are compared in the canonical spelling, whatever the setting.
func TestStateComparesCanonicalSpellings(t *testing.T) {
	for _, c := range []struct {
		key, saved, running, source, want string
	}{
		{"turn_timeout", "900s", "15m", "saved", StateApplied},
		{"turn_timeout", "20m", "15m", "saved", StatePendingRestart},
		{"image_runtimes", "agy, Codex", "antigravity,codex", "saved", StateApplied},
		{"image_runtimes", "none", "codex,antigravity", "default", StatePendingRestart},
		{"image_runtimes", "", "codex,antigravity", "default", StateApplied},
		{"tool_runtimes", "", "claude,codex", "default", StateApplied},
		{"context_confinement", "chat-only", "chat-only", "default", StateApplied},
		{"context_confinement", "any", "chat-only", "default", StatePendingRestart},
		{"v1_addr", "0.0.0.0:9443", "0.0.0.0:9443", "saved", StateApplied},
		{"v1_addr", "", "0.0.0.0:9443", "saved", StatePendingRestart},
		{"v1_addr", "0.0.0.0:9443", "", "default", StatePendingRestart},
		{"tls_cert_file", "/c.pem", "/c.pem", "saved", StateApplied},
		{"tls_cert_file", "/c.pem", "/e/c.pem", "env", StateOverridden},
		// confinement has no default value, so a saved value that spells the default of a
		// loopback listener is pending until the daemon restarts, though the policy is the same.
		{"confinement", "any", "", "default", StatePendingRestart},
		{"confinement", "", "", "default", StateApplied},
	} {
		var saved Settings
		if c.saved != "" {
			_ = saved.Set(c.key, c.saved)
		}
		hb := heartbeatOf(map[string]daemonhb.APISetting{c.key: {Value: c.running, Source: c.source}})
		if c.key == "v1_addr" {
			hb.V1Addr = c.running // the listener it names is up
		}
		if got := State(saved, c.key, hb); got != c.want {
			t.Errorf("%s saved %q, running %q from %s: %s, want %s", c.key, c.saved, c.running, c.source, got, c.want)
		}
	}
}

// The settings of the dedicated listener say applied only when the listener is up (C1 of the correctness review
// of phase 6): a daemon that took the address and then could not bind it or load the certificate logs it and
// reports no address, and `show` used to say applied for all three while the app said "the daemon is back".
func TestStateOfTheDedicatedListenersSettingsWhenItIsNotUp(t *testing.T) {
	settings := func(v1 daemonhb.APISetting) map[string]daemonhb.APISetting {
		return map[string]daemonhb.APISetting{
			"v1_addr": v1, "tls_cert_file": {Value: "/c.pem", Source: "saved"}, "tls_key_file": {Value: "/k.pem", Source: "saved"},
			"max_concurrent": {Value: "4", Source: "default"},
		}
	}
	saved := doc(t, "v1_addr=0.0.0.0:9443", "tls_cert_file=/c.pem", "tls_key_file=/k.pem")
	configured := settings(daemonhb.APISetting{Value: "0.0.0.0:9443", Source: "saved"})
	down := &daemonhb.Heartbeat{PID: 1, APISettings: configured} // it took the settings and no listener came up
	up := &daemonhb.Heartbeat{PID: 1, APISettings: configured, V1Addr: "[::]:9443"}
	for _, key := range []string{"v1_addr", "tls_cert_file", "tls_key_file"} {
		if got := State(saved, key, down); got != StateNotServing {
			t.Errorf("%s with the listener down: %s, want %s", key, got, StateNotServing)
		}
		if got := State(saved, key, up); got != StateApplied {
			t.Errorf("%s with the listener up: %s, want %s", key, got, StateApplied)
		}
	}
	// Only the listener's settings are about the listener.
	if got := State(saved, "max_concurrent", down); got != StateApplied {
		t.Errorf("max_concurrent with the listener down: %s, want %s", got, StateApplied)
	}
	// A restart that would change the setting is what the person needs first.
	if got := State(doc(t, "v1_addr=0.0.0.0:9444", "tls_cert_file=/c.pem", "tls_key_file=/k.pem"), "v1_addr", down); got != StatePendingRestart {
		t.Errorf("a saved address that differs from the running one: %s, want %s", got, StatePendingRestart)
	}
	// The daemon's own flag or variable is still what the person has to deal with.
	flagged := &daemonhb.Heartbeat{PID: 1, APISettings: settings(daemonhb.APISetting{Value: "0.0.0.0:9443", Source: "flag"})}
	if got := State(saved, "v1_addr", flagged); got != StateOverridden {
		t.Errorf("an address from the daemon's flag: %s, want %s", got, StateOverridden)
	}
	// No dedicated listener was asked for: none is down, and the files saved for it do nothing.
	none := &daemonhb.Heartbeat{PID: 1, APISettings: settings(daemonhb.APISetting{Value: "", Source: "default"})}
	for _, key := range []string{"v1_addr", "tls_cert_file", "tls_key_file"} {
		if got := State(doc(t, "tls_cert_file=/c.pem", "tls_key_file=/k.pem"), key, none); got != StateApplied {
			t.Errorf("%s with no dedicated listener: %s, want %s", key, got, StateApplied)
		}
	}
}
