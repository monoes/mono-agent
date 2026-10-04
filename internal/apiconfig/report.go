package apiconfig

import (
	"context"
	"database/sql"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// SettingReport is one setting of `api config show --json`: what is saved, what a server
// started from this process would use and where that comes from, and what the running daemon
// says it started with and what that means.
type SettingReport struct {
	Key string `json:"key"`
	// ServerFlag is the flag `httpapi` and `daemon` have for it, "" where it has none.
	ServerFlag string `json:"server_flag"`
	// Env is the environment variable that names it.
	Env string `json:"env"`
	// Saved is the saved value in the canonical spelling (an invalid one, from a document
	// edited by hand, as it is stored); omitted when nothing is saved.
	Saved string `json:"saved,omitempty"`
	// Default is the canonical text of the default, "" where there is no value.
	Default string `json:"default"`
	// Effective is what a server started from this process would use: its environment, else the
	// saved value, else the default; "" where there is no value.
	Effective string `json:"effective"`
	// Source is where Effective comes from: env, saved or default.
	Source string `json:"source"`
	// Running is the value the running daemon says it started with, and RunningSource where it
	// came from (flag, env, saved or default). Both are absent unless a live daemon reports the
	// setting; Running is "" when the daemon reports an empty value.
	Running       *string `json:"running,omitempty"`
	RunningSource string  `json:"running_source,omitempty"`
	// State is where the setting stands against the running daemon: applied, pending_restart,
	// overridden, not_running or unknown.
	State string `json:"state"`
}

// DaemonReport is the daemon, as far as the settings go.
type DaemonReport struct {
	// Running: a live heartbeat exists.
	Running bool `json:"running"`
	// ReportsSettings: the live heartbeat carries api_settings; false for an older daemon and
	// when none is running.
	ReportsSettings bool `json:"reports_settings"`
	// Autostart: the daemon is registered with the OS service manager, so `daemon restart` can
	// restart it.
	Autostart bool `json:"autostart"`
}

// ConfigReport is the document of `monoagentcli api config show --json` and of the MCP tool
// api_config_get.
type ConfigReport struct {
	V int `json:"v"`
	// Environment says whose environment Effective and a Source of env refer to:
	// openaiapi.ReportSourceShell or openaiapi.ReportSourceMCP.
	Environment string          `json:"environment"`
	Settings    []SettingReport `json:"settings"`
	Daemon      DaemonReport    `json:"daemon"`
	// RestartNeeded: some setting is pending_restart.
	RestartNeeded bool `json:"restart_needed"`
	// Problems are the saved values that fail their rules, and the TLS file that lacks the other.
	Problems []Problem `json:"problems"`
}

// Show reads the saved settings and says, for each setting, what is saved, what a server started
// from this process would use and where that comes from, and what the running daemon started
// with and what that means (State). An invalid saved value is a problem of the report, not an
// error: it is how it is found and removed. A saved document that cannot be read is the error of
// Load.
func Show(ctx context.Context, db *sql.DB, env Env) (ConfigReport, error) {
	saved, err := Load(ctx, db)
	if err != nil {
		return ConfigReport{}, err
	}
	return report(ctx, saved, env), nil
}

// report is the document for saved settings, whether they were just changed or not.
func report(ctx context.Context, saved Settings, env Env) ConfigReport {
	hbValue, live := env.heartbeat()
	var hb *daemonhb.Heartbeat
	if live {
		hb = &hbValue
	}
	registered, _ := env.installer().Status(ctx)

	r := ConfigReport{V: 1, Environment: env.environment(), Settings: make([]SettingReport, 0, len(Keys())), Problems: []Problem{}}
	r.Daemon = DaemonReport{Running: live, ReportsSettings: live && hb.APISettings != nil, Autostart: registered}
	getenv := env.getenv()
	for _, sp := range Specs() {
		res := ResolveKey(sp.Key, "", getenv, saved)
		row := SettingReport{
			Key: sp.Key, ServerFlag: sp.ServerFlag, Env: sp.Env, Saved: savedText(saved, sp.Key), Default: sp.Default,
			Effective: res.Text, Source: res.Source, State: State(saved, sp.Key, hb),
		}
		if hb != nil {
			if run, ok := hb.APISettings[sp.Key]; ok {
				value := run.Value
				row.Running, row.RunningSource = &value, run.Source
			}
		}
		if row.State == StatePendingRestart {
			r.RestartNeeded = true
		}
		r.Settings = append(r.Settings, row)
	}
	if problems := Validate(saved); len(problems) > 0 {
		r.Problems = problems
	}
	return r
}

// savedText is the saved value in the canonical spelling, as stored when that fails its rule.
func savedText(saved Settings, key string) string {
	text := saved.Get(key)
	if text == "" {
		return ""
	}
	if canon, err := Canonical(key, text); err == nil {
		return canon
	}
	return text
}
