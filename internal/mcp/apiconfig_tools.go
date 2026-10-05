package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// apiConfigTools are the tools over the OpenAI-compatible API's server: where it listens, the
// settings saved for it, and what makes them take effect. Their documents are internal/apiconfig's,
// the same code `monoagentcli api status` and `api config` run, so that each tool answers exactly
// what its command answers (`--json`), and the pipe tests in cmd/monoagentcli compare the two.
func apiConfigTools() []tool {
	return []tool{
		{
			name: "api_status",
			description: "Show where the OpenAI-compatible API (/v1) listens and whether it answers: the document of `monoagentcli api status --json`. " +
				"It has the active profile's count of active API keys, whether a daemon is running and the addresses it reports, whether the auto model works for the profile and what it is missing, " +
				"and each listener (the main HTTP API listener, which serves /v1 only while it is bound to loopback, and the dedicated v1_addr listener): its address, whether it is bound to loopback, " +
				"the confinement class it serves and the classes a key created with context and the auto model are held to on it, where that came from (confinement_source is daemon when the running daemon reported it, " +
				"and environment when it is worked out from this MCP server's environment, the settings saved with api_config_set and the defaults, which a running server started with flags of its own may not share), " +
				"the scheme that answered (http or https), whether GET /health answers and whether /v1 does. " +
				"It probes each listener over HTTP, which takes a few seconds when nothing answers. It changes nothing and returns no key: " +
				"api_config_get says, setting by setting, what is saved and what the running daemon started with.",
			schema:      objSchema(nil),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIStatus,
		},
	}
}

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

// errBadEnvironment is the answer to a value in this server's environment that fails its rule.
var errBadEnvironment = errors.New("MONOAGENT_API_CONFINEMENT, MONOAGENT_API_CONTEXT_CONFINEMENT and MONOAGENT_API_AUTO_CONFINEMENT in this MCP server's environment must each be chat-only, sandboxed or any: one of them is not (api_models_list says which)")
