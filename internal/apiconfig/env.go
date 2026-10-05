package apiconfig

import (
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// Env is what the documents read of the process that builds them: the CLI, or the MCP server.
// A nil field is the process's own.
//
// Show and Apply ask Installer.Status, which runs the service manager (launchctl print and
// systemctl when a registration exists under the home, schtasks /query always on Windows), and
// BuildStatus probes the listeners over HTTP: every test sets Installer, Heartbeat and Probe to
// fakes, or it depends on the machine it runs on.
type Env struct {
	// Getenv is the process environment, without the saved layer (which the documents add).
	// nil: os.Getenv.
	Getenv func(string) string
	// Environment says whose environment that is: openaiapi.ReportSourceShell (the default) or
	// openaiapi.ReportSourceMCP.
	Environment string
	// Heartbeat reads the running daemon's heartbeat and whether it is live. nil: daemonhb.Read.
	Heartbeat func() (daemonhb.Heartbeat, bool)
	// Installer is the service manager. nil: autostart.New().
	Installer autostart.Installer
	// Probe asks a listener over HTTP (BuildStatus only). nil: ProbeListener.
	Probe ProbeFunc
}

func (e Env) getenv() func(string) string {
	if e.Getenv != nil {
		return e.Getenv
	}
	return os.Getenv
}

func (e Env) heartbeat() (daemonhb.Heartbeat, bool) {
	if e.Heartbeat != nil {
		return e.Heartbeat()
	}
	return daemonhb.Read()
}

func (e Env) installer() autostart.Installer {
	if e.Installer != nil {
		return e.Installer
	}
	return autostart.New()
}

func (e Env) environment() string {
	if e.Environment != "" {
		return e.Environment
	}
	return openaiapi.ReportSourceShell
}

func (e Env) probe() ProbeFunc {
	if e.Probe != nil {
		return e.Probe
	}
	return ProbeListener
}

// InputError is a flag or environment value that fails its rule: the caller's input and not a
// fault, which the CLI reports as invalid input (exit 3). Its message names the setting and the
// rule, as the flag or the variable always did.
type InputError struct{ Err error }

func (e *InputError) Error() string { return e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

// EffectivePolicy is the confinement policy of a listener bound to addr: the explicit value (a
// flag), else MONOAGENT_API_CONFINEMENT, else the default for that kind of bind. A bad value is
// an *InputError.
func EffectivePolicy(addr, explicit string, getenv func(string) string) (openaiapi.Policy, error) {
	p, err := openaiapi.EffectivePolicy(addr, explicit, getenv)
	if err != nil {
		return openaiapi.Policy{}, &InputError{err}
	}
	return p, nil
}

// EffectiveContextMax is the strongest class a key created with --context may use: the explicit
// value (a flag), else MONOAGENT_API_CONTEXT_CONFINEMENT, else chat-only. A bad value is an
// *InputError.
func EffectiveContextMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	c, err := openaiapi.EffectiveContextMax(explicit, getenv)
	if err != nil {
		return 0, &InputError{fmt.Errorf("--context-confinement (MONOAGENT_API_CONTEXT_CONFINEMENT): %w", err)}
	}
	return c, nil
}

// EffectiveAutoMax is the strongest class the auto model may pick: the explicit value (a flag),
// else MONOAGENT_API_AUTO_CONFINEMENT, else chat-only. A bad value is an *InputError.
func EffectiveAutoMax(explicit string, getenv func(string) string) (openaiapi.Class, error) {
	c, err := openaiapi.EffectiveAutoMax(explicit, getenv)
	if err != nil {
		return 0, &InputError{fmt.Errorf("--auto-confinement (MONOAGENT_API_AUTO_CONFINEMENT): %w", err)}
	}
	return c, nil
}
