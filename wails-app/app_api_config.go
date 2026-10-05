package main

// The server settings of the OpenAI-compatible API (Settings → "OpenAI-compatible API" →
// "Server settings") and the restart that applies them. Like app_api.go, every method shells
// out to `monoagentcli … --json` through runAPICLI. This file never opens the database, reads
// a certificate or a key, or judges anything: what a value means, whether a change makes the
// server reach further, and where each setting stands against the running daemon are the
// CLI's to say (internal/apiconfig), and the page shows it as it said it. What is passed is
// the ten settings' keys, a short text for each, and the paths of the two TLS files, each
// value attached to its flag so that it can never be read as another one.
//
// What the CLI does not send stays absent in what the page receives (a pointer, or
// omitempty): a daemon that does not report a setting has no running value, which is not the
// same as one that runs it with no value. A list is a list, never null.

import (
	"errors"
	"slices"
	"strconv"
	"strings"
)

// apiConfigKeys are the ten settings, in the order every document of the CLI lists them. A key
// that is not one of them is refused before the CLI runs: a positional of `unset` that starts
// with a dash would be a flag, and a name that carries a space would be two arguments.
var apiConfigKeys = []string{
	"v1_addr", "tls_cert_file", "tls_key_file", "confinement", "context_confinement", "auto_confinement",
	"max_concurrent", "turn_timeout", "image_runtimes", "tool_runtimes",
}

// APIConfigSetting is one setting of `api config show --json`.
type APIConfigSetting struct {
	Key           string  `json:"key"`
	ServerFlag    string  `json:"server_flag"`     // "" for the five settings the server has no flag for
	Env           string  `json:"env"`             // the environment variable that names it
	Saved         string  `json:"saved,omitempty"` // absent when nothing is saved for it
	Default       string  `json:"default"`         // may be ""
	Effective     string  `json:"effective"`       // what a server started from the CLI's environment would use; may be ""
	Source        string  `json:"source"`          // env | saved | default
	Running       *string `json:"running,omitempty"`
	RunningSource string  `json:"running_source,omitempty"` // flag | env | saved | default, present exactly when Running is
	State         string  `json:"state"`                    // applied | pending_restart | overridden | not_running | unknown
}

// APIConfigDaemon is the daemon, as far as the settings go.
type APIConfigDaemon struct {
	Running         bool `json:"running"`
	ReportsSettings bool `json:"reports_settings"` // false for a daemon that predates the report
	Autostart       bool `json:"autostart"`        // registered with the service manager: it can be restarted
}

// APIConfigProblem is something wrong with the saved settings: a value that fails its rule, one
// TLS file without the other. Key is "" for a problem of the document as a whole (the CLI sends none
// today: a saved row that cannot be read is an error, not a problem).
type APIConfigProblem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

// APIConfigInfo mirrors `api config show --json`.
type APIConfigInfo struct {
	V             int                `json:"v"`
	Environment   string             `json:"environment"` // shell | mcp
	Settings      []APIConfigSetting `json:"settings"`
	Daemon        APIConfigDaemon    `json:"daemon"`
	RestartNeeded bool               `json:"restart_needed"`
	Problems      []APIConfigProblem `json:"problems"`
}

// APIWidening is one way a change makes the server reach further than it did: a stable key
// and one sentence, which the page shows as it is.
type APIWidening struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// APIConfigChange mirrors `api config set|unset --json`: the document of the state after the
// change, plus whether it was applied (false for a dry run, whose document is the state the
// change would give), which settings it changed and how it widens.
type APIConfigChange struct {
	APIConfigInfo
	Applied  bool          `json:"applied"`
	Changed  []string      `json:"changed"`
	Widening []APIWidening `json:"widening"`
	// RemovedUnreadableRow is sent by the CLI only when true: `unset --all` found a saved row it cannot
	// read and removed it (a dry run: would remove it). Absent for every other change.
	RemovedUnreadableRow bool `json:"removed_unreadable_row,omitempty"`
}

// DaemonRestartResult mirrors `daemon restart --json`.
type DaemonRestartResult struct {
	Restarted bool   `json:"restarted"`
	Via       string `json:"via"` // launchd | systemd | schtasks
}

func (i *APIConfigInfo) fill() {
	if i.Settings == nil {
		i.Settings = []APIConfigSetting{}
	}
	if i.Problems == nil {
		i.Problems = []APIConfigProblem{}
	}
}

func (c *APIConfigChange) fill() {
	c.APIConfigInfo.fill()
	if c.Changed == nil {
		c.Changed = []string{}
	}
	if c.Widening == nil {
		c.Widening = []APIWidening{}
	}
}

// configFlag is the flag of `api config set` for a setting: its key with dashes.
func configFlag(key string) string { return "--" + strings.ReplaceAll(key, "_", "-") }

// confirmFlags are what a change adds to its arguments: --yes (the widening gate is open: only
// the page's confirmation dialog passes it) and --dry-run (say what the change would do, and
// whether it widens, and save nothing).
func confirmFlags(confirm, dryRun bool) []string {
	var flags []string
	if confirm {
		flags = append(flags, "--yes")
	}
	if dryRun {
		flags = append(flags, "--dry-run")
	}
	return flags
}

// APIConfigShow says, for each setting, what is saved, what the running daemon started with and
// where that came from, and where the setting stands (`api config show`).
func (a *App) APIConfigShow() (APIConfigInfo, error) {
	var info APIConfigInfo
	if err := a.runAPICLI(&info, "api", "config", "show"); err != nil {
		return APIConfigInfo{}, err
	}
	info.fill()
	return info, nil
}

// APIConfigSet saves the settings given (key to text, in the syntax of the setting's environment
// variable) and leaves the others as they are (`api config set`). confirm opens the widening gate:
// the page passes it only after its dialog listed what the change widens, which it learns from a
// call with dryRun, whose document is the state the change would give and which saves nothing. The
// CLI refuses a widening change without confirm, and a value that fails its rule, with exit 3.
func (a *App) APIConfigSet(values map[string]string, confirm, dryRun bool) (APIConfigChange, error) {
	if len(values) == 0 {
		return APIConfigChange{}, errors.New("nothing to change")
	}
	for key := range values {
		if !slices.Contains(apiConfigKeys, key) {
			return APIConfigChange{}, errors.New("not a setting: " + strconv.Quote(key))
		}
	}
	args := []string{"api", "config", "set"}
	for _, key := range apiConfigKeys {
		if v, ok := values[key]; ok {
			args = append(args, configFlag(key)+"="+strings.TrimSpace(v))
		}
	}
	return a.runConfigChange(append(args, confirmFlags(confirm, dryRun)...))
}

// APIConfigUnset removes the saved value of each setting named, so that the server uses the
// environment or the default (`api config unset`). Removing a value that held the server below its
// default is a widening change like any other: see APIConfigSet for confirm and dryRun.
func (a *App) APIConfigUnset(keys []string, confirm, dryRun bool) (APIConfigChange, error) {
	if len(keys) == 0 {
		return APIConfigChange{}, errors.New("nothing to change")
	}
	for _, key := range keys {
		if !slices.Contains(apiConfigKeys, key) {
			return APIConfigChange{}, errors.New("not a setting: " + strconv.Quote(key))
		}
	}
	args := []string{"api", "config", "unset"}
	for _, key := range apiConfigKeys {
		if slices.Contains(keys, key) {
			args = append(args, key)
		}
	}
	return a.runConfigChange(append(args, confirmFlags(confirm, dryRun)...))
}

// APIConfigReset removes every saved setting (`api config unset --all`), and a saved row that cannot
// be read with them. A row that cannot be read is an error of every other call (exit 3, a message that
// starts "the saved settings are damaged", which reaches the page as `invalid_input: …`; one in a newer
// format is exit 1 and is never removed), and removing it is a widening of unknown size: the CLI refuses
// it without confirm, and a dry run says so, with the reason, and whether the row is unreadable
// (`removed_unreadable_row`). See APIConfigSet for confirm and dryRun.
func (a *App) APIConfigReset(confirm, dryRun bool) (APIConfigChange, error) {
	return a.runConfigChange(append([]string{"api", "config", "unset", "--all"}, confirmFlags(confirm, dryRun)...))
}

func (a *App) runConfigChange(args []string) (APIConfigChange, error) {
	var change APIConfigChange
	if err := a.runAPICLI(&change, args...); err != nil {
		return APIConfigChange{}, err
	}
	change.fill()
	return change, nil
}

// DaemonRestart restarts the daemon through the auto-start service it is registered as
// (`daemon restart`), so that it reads the saved settings. It interrupts whatever the daemon is
// running (workflows, org runs): the page asks first. A daemon that is not registered cannot be
// restarted by it (exit 3).
func (a *App) DaemonRestart() (DaemonRestartResult, error) {
	var res DaemonRestartResult
	if err := a.runAPICLI(&res, "daemon", "restart"); err != nil {
		return DaemonRestartResult{}, err
	}
	return res, nil
}
