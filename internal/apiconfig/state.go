package apiconfig

import "github.com/monoes/mono-agent/internal/daemonhb"

// Where a setting stands against the running daemon (D37).
const (
	// StateApplied: the daemon took the saved value, or the default where none is saved, and
	// that is what a restart would resolve.
	StateApplied = "applied"
	// StatePendingRestart: the daemon took the saved value or the default, and a restart would
	// resolve something else.
	StatePendingRestart = "pending_restart"
	// StateOverridden: the daemon was given the value by a flag or an environment variable of
	// its own, so a saved value has no effect until that is removed.
	StateOverridden = "overridden"
	// StateNotRunning: no daemon is live.
	StateNotRunning = "not_running"
	// StateUnknown: a daemon is live but its heartbeat says nothing of this setting (an older
	// daemon).
	StateUnknown = "unknown"
)

// State says where one setting stands. hb is the live daemon's heartbeat, nil when no daemon is
// live. The comparison is of what the daemon started with and what a restart would resolve
// from the saved layer alone (the saved value, else the default), in the canonical spelling:
// the daemon is typically started by the login service, which does not read this process's
// environment, and where the daemon took a flag or a variable the saved layer is not asked.
// One conservative case: confinement has no default value, so a saved value that spells the
// default of a loopback listener is pending until the daemon restarts, though the policy is
// the same.
func State(saved Settings, key string, hb *daemonhb.Heartbeat) string {
	if hb == nil {
		return StateNotRunning
	}
	run, ok := hb.APISettings[key]
	if !ok {
		return StateUnknown
	}
	if run.Source == SourceFlag || run.Source == SourceEnv {
		return StateOverridden
	}
	if run.Value == resolvedWithoutOverrides(saved, key) {
		return StateApplied
	}
	return StatePendingRestart
}

// resolvedWithoutOverrides is what a daemon started now, with no flag and no variable, would
// use for a setting: the saved value, else the default, in the canonical spelling.
func resolvedWithoutOverrides(saved Settings, key string) string {
	return ResolveKey(key, "", func(string) string { return "" }, saved).Text
}
