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
		if got := State(saved, c.key, hb); got != c.want {
			t.Errorf("%s saved %q, running %q from %s: %s, want %s", c.key, c.saved, c.running, c.source, got, c.want)
		}
	}
}
