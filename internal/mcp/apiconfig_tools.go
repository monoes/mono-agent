package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// apiConfigTools are the tools over the OpenAI-compatible API's server: where it listens, the
// settings saved for it, and what makes them take effect. Their documents are internal/apiconfig's,
// the same code `monoagentcli api status` and `api config` run, so that each tool answers exactly
// what its command answers (`--json`), and the pipe tests in cmd/monoagentcli compare the two.
func apiConfigTools() []tool {
	tools := []tool{
		{
			name: "api_status",
			description: "Show where the OpenAI-compatible API (/v1) listens and whether it answers: the document of `monoagentcli api status --json`. " +
				"It has the active profile's count of active API keys, whether a daemon is running and the addresses it reports, whether the auto model works for the profile and what it is missing, " +
				"and each listener (the main HTTP API listener, which serves /v1 only while it is bound to loopback, and the dedicated v1_addr listener): its address, whether it is bound to loopback, " +
				"the confinement class it serves and the classes a key created with context and the auto model are held to on it, where that came from (confinement_source is daemon when the running daemon reported it, " +
				"and environment when it is worked out from this MCP server's environment, the settings saved with api_config_set and the defaults, which a running server started with flags of its own may not share), " +
				"the scheme that answered (http or https), whether GET /health answers and whether /v1 does. " +
				"It probes each listener over HTTP, which takes a few seconds when nothing answers. It changes nothing and returns no key: " +
				"api_config_get says, setting by setting, what is saved and what the running daemon started with. " + damagedRowNote,
			schema:      objSchema(nil),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIStatus,
		},
		{
			name: "api_config_get",
			description: "Show the settings of the OpenAI-compatible API's server and where each stands: the document of `monoagentcli api config show --json`. " +
				"There are ten: v1_addr (the dedicated /v1 listener), tls_cert_file and tls_key_file (paths, never contents), confinement, context_confinement and auto_confinement " +
				"(the strongest runtime class the server serves, a key created with context may use and the auto model may pick), max_concurrent, turn_timeout, image_runtimes and tool_runtimes. " +
				"For each: what is saved (api_config_set saves it), what a server started from this MCP server's environment would use and where that comes from (env, saved or default: flag, then environment variable, then saved, then default), " +
				"and, when a daemon is running and reports it, the value the daemon started with and where that came from (flag, env, saved or default), with a state: " +
				"applied; pending_restart (saved since the daemon started: api_config_apply restarts it); overridden (the daemon was given a flag or a variable of its own, so a saved value has no effect until that is removed); " +
				"not_serving (only v1_addr and the TLS files: the daemon took the value, but its dedicated listener is not up, because it could not bind the address or load the certificate: its log says which, and a restart does not cure it until the setting is corrected); " +
				"not_running; or unknown (a daemon that predates the report). Also daemon.autostart (whether api_config_apply can restart the daemon), restart_needed, " +
				"and problems (a saved value that fails its rule, which api_config_set can replace or remove). It changes nothing. " + damagedRowNote,
			schema:      objSchema(nil),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIConfigGet,
		},
		apiConfigSetTool(),
		{
			name: "api_config_apply",
			description: "Restart the daemon so that it reads the settings saved with api_config_set: what `monoagentcli daemon restart` does, through the same function and the service manager the daemon is registered with " +
				"(a LaunchAgent on macOS, a systemd --user unit on Linux, a Scheduled Task on Windows; `monoagentcli daemon install` registers it). " +
				"The result is the document of `daemon restart --json`: {restarted, via}, via being launchd, systemd or schtasks. " +
				"It interrupts whatever the daemon is running (workflows, org runs). How the daemon ends is the service manager's, and nothing here promises that it finishes what it is doing first. " +
				"Use it when api_config_get says restart_needed (some setting is pending_restart) and the user is ready for that. It saves no setting and takes no argument. " +
				"It reads the saved settings first and restarts nothing when they cannot be used (a saved value that fails its rule, or damaged settings), because a daemon that cannot use them starts without the API: it fails with the message api_config_get gives. " +
				"A daemon that is not registered for auto-start (daemon.autostart is false in api_config_get) is not restarted by anything: the call fails and says that the user can stop it and start `monoagentcli daemon` again, " +
				"or run `monoagentcli daemon install` to have the system manage it. A daemon started by hand while the service is registered has to be stopped first. " +
				"Afterwards api_config_get shows what the daemon started with.",
			schema:      objSchema(nil),
			annotations: map[string]bool{"readOnlyHint": false, "destructiveHint": true},
			mutating:    true,
			handler:     toolAPIConfigApply,
		},
	}
	return append(tools, apiAutoTools()...)
}

// damagedRowNote is what the description of every tool that reads the saved settings tells a model of a
// row that cannot be read: the message starts the same way for every kind of damage, the user has a
// command for it, and the operator can allow api_config_set to do it. A row that a newer version saved
// is not damaged, and nothing removes it.
const damagedRowNote = "If the saved settings are damaged (a row that cannot be read) it fails with a message that starts `the saved settings are damaged`: " +
	"tell the user, who can run `monoagentcli api config unset --all --yes`, or the operator can allow api_config_set to remove the row (unset all, with --allow-api-exposure); " +
	"a row that a newer version saved fails too, and nothing here removes it."

// apiEnv is what the documents of the tools read of this process: Options.APIEnv, whose zero value
// is the process's own. Whose environment they describe is always this MCP server's.
func (s *Server) apiEnv() apiconfig.Env {
	env := s.opts.APIEnv
	env.Environment = openaiapi.ReportSourceMCP
	return env
}

func toolAPIStatus(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	st, err := apiconfig.BuildStatus(ctx, rt.db.DB, s.apiEnv(), rt.profileID)
	if err != nil {
		var input *apiconfig.InputError
		if errors.As(err, &input) {
			// The shared parsers quote the value they refuse, which suits a command line: what this
			// server's environment holds is not repeated to a model. api_models_list names which
			// variable it is.
			return nil, errBadEnvironment
		}
		return nil, err
	}
	return st, nil
}

func toolAPIConfigGet(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	return apiconfig.Show(ctx, rt.db.DB, s.apiEnv())
}

// getenv reads this server's environment: Options.APIEnv's, else the process's own.
func (s *Server) getenv() func(string) string {
	if g := s.opts.APIEnv.Getenv; g != nil {
		return g
	}
	return os.Getenv
}

// installer is the service manager: Options.APIEnv's, else this system's own.
func (s *Server) installer() autostart.Installer {
	if in := s.opts.APIEnv.Installer; in != nil {
		return in
	}
	return autostart.New()
}

// toolAPIConfigApply restarts the daemon as `daemon restart` does, and answers with the words of
// that command: its document, and its errors (settings that cannot be used, a daemon that is not
// registered, a service manager that fails). It takes no argument: what it applies is what is saved,
// and it reads that first: a daemon that cannot use the saved settings starts without the API, so a
// restart would replace one that serves it with one that does not. The error is the one of every tool
// that reads them, and nothing is restarted.
func toolAPIConfigApply(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	if _, err := apiconfig.LoadValid(ctx, rt.db.DB); err != nil {
		return nil, err
	}
	res, err := autostart.RestartRegistered(ctx, s.installer())
	if err != nil {
		var notRegistered *autostart.NotRegisteredError
		if errors.As(err, &notRegistered) {
			return nil, err
		}
		return nil, fmt.Errorf("restart: %w", err)
	}
	return res, nil
}

// errBadEnvironment is the answer to a value in this server's environment that fails its rule.
var errBadEnvironment = errors.New("MONOAGENT_API_CONFINEMENT, MONOAGENT_API_CONTEXT_CONFINEMENT and MONOAGENT_API_AUTO_CONFINEMENT in this MCP server's environment must each be chat-only, sandboxed or any: one of them is not (api_models_list says which)")
