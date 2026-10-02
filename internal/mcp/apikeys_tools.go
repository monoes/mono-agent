package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// apiTools are the tools over the API keys of the OpenAI-compatible API (/v1) and
// the models it would serve (spec §8.3). Keys belong to the server's profile: a key
// of another profile is "not found". The rules (names, uniqueness, the context
// switch, the errors) are internal/apikeys', and what api_models_list computes is
// internal/openaiapi's, the same code `monoagentcli api` runs.
func apiTools() []tool {
	return []tool{
		{
			name:        "api_key_list",
			description: "List the API keys of the active profile, for the OpenAI-compatible API (/v1): [{id, profile_id, name, prefix, context, created_at, last_used_at, revoked_at}]. Metadata only: a key itself is never part of it, because only its SHA-256 is stored and it was shown once, when it was created. Revoked keys are included only with include_revoked.",
			schema: objSchema(map[string]interface{}{
				"include_revoked": boolParam("Also list revoked keys (default false)"),
			}),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIKeyList,
		},
		{
			name: "api_models_list",
			description: "List the models the OpenAI-compatible API (/v1) would serve, with each one's confinement class (chat-only, sandboxed or unconfined), " +
				"whether a listener of the given kind serves it, whether a key created with context may use it, whether the auto model may pick it, " +
				"and whether the auto model works for the active profile: the document of `monoagentcli api models --json`. " +
				"The policy comes from the arguments, else from MONOAGENT_API_CONFINEMENT, MONOAGENT_API_CONTEXT_CONFINEMENT and MONOAGENT_API_AUTO_CONFINEMENT " +
				"in this MCP server's environment, else the listener's defaults, so it can differ from what a running server applies (`monoagentcli api status` shows that). " +
				"It asks every installed agent runtime for its models, so it takes a few seconds.",
			schema: objSchema(map[string]interface{}{
				"for":                 strParam("loopback (default) or network: the kind of listener to evaluate"),
				"confinement":         strParam("Strongest class the listener serves: chat-only, sandboxed or any (default: the environment, else any on loopback and chat-only on a network listener)"),
				"context_confinement": strParam("Strongest class a key created with context may use: chat-only, sandboxed or any (default: the environment, else chat-only; never above the listener's)"),
				"auto_confinement":    strParam("Strongest class the auto model may pick: chat-only, sandboxed or any (default: the environment, else chat-only; never above the listener's)"),
			}),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIModelsList,
		},
	}
}

// apiKeys opens the key store of the server's profile.
func (s *Server) apiKeys() (store *apikeys.Store, profileID string, err error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, "", err
	}
	return apikeys.NewStore(rt.db.DB), rt.profileID, nil
}

func toolAPIKeyList(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		IncludeRevoked bool `json:"include_revoked"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	store, profileID, err := s.apiKeys()
	if err != nil {
		return nil, err
	}
	return store.List(ctx, profileID, a.IncludeRevoked)
}

func toolAPIModelsList(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		For                string `json:"for"`
		Confinement        string `json:"confinement"`
		ContextConfinement string `json:"context_confinement"`
		AutoConfinement    string `json:"auto_confinement"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.For == "" {
		a.For = "loopback"
	}
	addr, err := openaiapi.ListenerAddr(a.For)
	if err != nil {
		return nil, fmt.Errorf("for %v", err)
	}
	policy, err := openaiapi.EffectivePolicy(addr, a.Confinement, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("confinement (MONOAGENT_API_CONFINEMENT): %v", err)
	}
	if policy.ContextMax, err = openaiapi.EffectiveContextMax(a.ContextConfinement, os.Getenv); err != nil {
		return nil, fmt.Errorf("context_confinement (MONOAGENT_API_CONTEXT_CONFINEMENT): %v", err)
	}
	if policy.AutoMax, err = openaiapi.EffectiveAutoMax(a.AutoConfinement, os.Getenv); err != nil {
		return nil, fmt.Errorf("auto_confinement (MONOAGENT_API_AUTO_CONFINEMENT): %v", err)
	}
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	models, err := openaiapi.LoadModels(ctx, rt.db.DB)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	return openaiapi.NewModelsReport(openaiapi.ModelsReportInput{
		For: a.For, Policy: policy, Source: openaiapi.ReportSourceMCP, Models: models,
		Auto: openaiapi.DefaultAuto(rt.db.DB).Status(ctx, rt.profileID),
	}), nil
}
