package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/monoes/mono-agent/internal/apiconfig"
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
				"its capabilities (text; image for a model of a runtime that makes images; tools for a model that serves tool calling: its runtime is in the list below, monomind can apply the sandbox every turn with tools requires, and for a runtime that is not chat-only run it read-only), " +
				"whether a listener of the given kind serves it, whether a key created with context may use it, whether the auto model may pick it, " +
				"and whether the auto model works for the active profile: the document of `monoagentcli api models --json`. " +
				"The policy comes from the arguments, else from MONOAGENT_API_CONFINEMENT, MONOAGENT_API_CONTEXT_CONFINEMENT and MONOAGENT_API_AUTO_CONFINEMENT " +
				"in this MCP server's environment, else from the settings saved for the server (api_config_get shows them, api_config_set changes them), else the listener's defaults. " +
				"The runtimes that make images come from MONOAGENT_API_IMAGE_RUNTIMES in this environment, else the saved image_runtimes, else codex and antigravity, " +
				"and the runtimes that serve tool calling from MONOAGENT_API_TOOL_RUNTIMES, else the saved tool_runtimes, else claude and codex (none switches either off). " +
				"That is what a server started now would apply, so it can differ from what a running one applies, which is what it read when it started: api_status and api_config_get say what it runs. " +
				"Loading the models asks every installed agent runtime for its list, which takes a few seconds: calls at once share one load, the list is reused for a minute, " +
				"and after that the previous one is served at once while a new one is loaded in the background, as the server's own /v1/models does. " + damagedRowNote,
			schema: objSchema(map[string]interface{}{
				"for":                 strParam("loopback (default) or network: the kind of listener to evaluate"),
				"confinement":         strParam("Strongest class the listener serves: chat-only, sandboxed or any (default: the environment, else the saved confinement, else any on loopback and chat-only on a network listener)"),
				"context_confinement": strParam("Strongest class a key created with context may use: chat-only, sandboxed or any (default: the environment, else the saved context_confinement, else chat-only; never above the listener's)"),
				"auto_confinement":    strParam("Strongest class the auto model may pick: chat-only, sandboxed or any (default: the environment, else the saved auto_confinement, else chat-only; never above the listener's)"),
			}),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolAPIModelsList,
		},
		{
			name: "api_key_create",
			description: "Create an API key for the active profile, for the OpenAI-compatible API (/v1). The key is returned ONCE, in the key field of the result: " +
				"only its SHA-256 is stored, so no tool can show it again. It authenticates /v1 requests as this profile and nothing else. " +
				"Treat it as a password and give it only to the user: it is now part of this conversation's transcript, which the host may keep " +
				"(`monoagentcli api key create` writes the key to stdout: run by the user in their own terminal it keeps the key out of any transcript, run by an agent through a shell tool it puts the key in that transcript too). " +
				"The name rule is the store's: " + apikeys.ErrInvalidName.Error() + ". A name must also be unique among the profile's active keys. " +
				"With context true, requests made with the key get excerpts of the profile's own knowledge added, and such a key is served only by chat-only models, and is refused tool calling, unless the server raises --context-confinement (and --confinement, on a chat-only listener) above chat-only: " +
				"the settings context_confinement and confinement, which api_config_get shows (saved, and as the running daemon started with them) and api_config_set changes, a change that reaches further and that the operator must have allowed.",
			schema: objSchema(map[string]interface{}{
				"name":    strParam("Key name, unique among the profile's active keys"),
				"context": boolParam("Add the profile's own knowledge (documents and captures) to requests made with this key (default false)"),
			}, "name"),
			annotations: map[string]bool{"readOnlyHint": false},
			mutating:    true,
			handler:     toolAPIKeyCreate,
		},
		{
			name: "api_key_update",
			description: "Rename an active API key of the active profile and/or switch its knowledge context on or off. Pass name, context or both: an argument left out is not changed, " +
				"and context false turns it off. The key itself is not changed and never returned.",
			schema: objSchema(map[string]interface{}{
				"id":      strParam("The key's id (key_…) or the name of an active key"),
				"name":    strParam("New name (omit to leave unchanged)"),
				"context": boolParam("true adds the profile's own knowledge to requests made with the key, false stops it (omit to leave unchanged)"),
			}, "id"),
			annotations: map[string]bool{"readOnlyHint": false},
			mutating:    true,
			handler:     toolAPIKeyUpdate,
		},
		{
			name: "api_key_revoke",
			description: "Revoke an API key of the active profile now: requests made with it are refused from the next request on (a turn already running finishes). " +
				"It cannot be undone. Revoking a revoked key by id succeeds.",
			schema: objSchema(map[string]interface{}{
				"id": strParam("The key's id (key_…) or the name of an active key"),
			}, "id"),
			annotations: map[string]bool{"readOnlyHint": false, "destructiveHint": true},
			mutating:    true,
			handler:     toolAPIKeyRevoke,
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

// createdAPIKey is what api_key_create returns: the key's metadata and the key
// itself, the one place the key is ever shown.
type createdAPIKey struct {
	apikeys.Key
	Secret string `json:"key"`
}

// The three handlers below pass the store's errors on as they are (ErrInvalidName,
// ErrNameTaken, ErrNotFound: static texts) and never put an argument in one, since a
// caller may have pasted the key itself where an id goes.

func toolAPIKeyCreate(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		Name    string `json:"name"`
		Context bool   `json:"context"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	store, profileID, err := s.apiKeys()
	if err != nil {
		return nil, err
	}
	key, secret, err := store.Create(ctx, profileID, a.Name, a.Context)
	if err != nil {
		return nil, err
	}
	return createdAPIKey{Key: key, Secret: secret}, nil
}

func toolAPIKeyUpdate(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		ID      string  `json:"id"`
		Name    *string `json:"name"`
		Context *bool   `json:"context"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	u := apikeys.Update{Name: a.Name, Context: a.Context}
	if u.IsEmpty() {
		return nil, fmt.Errorf("nothing to change: pass name or context")
	}
	store, profileID, err := s.apiKeys()
	if err != nil {
		return nil, err
	}
	return store.Update(ctx, profileID, a.ID, u)
}

func toolAPIKeyRevoke(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	store, profileID, err := s.apiKeys()
	if err != nil {
		return nil, err
	}
	return store.Revoke(ctx, profileID, a.ID)
}

// modelsTTL is how long api_models_list reuses the list of models it loaded. After
// it the previous list is served at once while a new one is loaded in the
// background, as the gateway's own catalog does.
const modelsTTL = time.Minute

// modelCatalog is the one catalog of models of this server. Calls at once share a
// load, and a list is reused for modelsTTL, so that a host that asks again does not
// start every installed runtime each time (a catalog made per call did: twenty
// calls started a hundred processes).
func (rt *runtime) modelCatalog() *openaiapi.Catalog {
	rt.catalogOnce.Do(func() { rt.catalog = openaiapi.NewModelCatalog(rt.db.DB, modelsTTL) })
	return rt.catalog
}

// maxEnumArgLen is far above the longest value any argument of api_models_list takes
// ("sandboxed", "loopback"): a longer one is refused before it is looked at.
const maxEnumArgLen = 32

// errBadClass is the refusal of a confinement class. It names the environment
// variable too, because that is what gives the value when the argument is left out.
func errBadClass(arg, env string) error {
	return fmt.Errorf("%s (or %s, when it is left out) must be chat-only, sandboxed or any", arg, env)
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
	// The refusals below are fixed texts, and a value is looked at only after its
	// length: a caller may have put anything, a key included, into an argument, and
	// what it sent does not come back. The shared parsers say more (they quote the
	// value, which suits a command line) and are left as they are.
	for _, arg := range []struct{ name, value string }{
		{"for", a.For}, {"confinement", a.Confinement}, {"context_confinement", a.ContextConfinement}, {"auto_confinement", a.AutoConfinement},
	} {
		if len(arg.value) > maxEnumArgLen {
			return nil, fmt.Errorf("%s is too long", arg.name)
		}
	}
	if a.For == "" {
		a.For = "loopback"
	}
	addr, err := openaiapi.ListenerAddr(a.For)
	if err != nil {
		return nil, errors.New("for must be loopback or network")
	}
	rt, err := s.runtime()
	if err != nil {
		return nil, err
	}
	// What a server started now would read: this server's environment, with the settings saved
	// with api_config_set under it. A saved setting that fails its rule stops such a server, so it
	// stops this call too, naming the setting and not repeating its value.
	getenv, err := apiconfig.EnvWithSaved(ctx, rt.db.DB, os.Getenv)
	if err != nil {
		return nil, err
	}
	policy, err := openaiapi.EffectivePolicy(addr, a.Confinement, getenv)
	if err != nil {
		return nil, errBadClass("confinement", "MONOAGENT_API_CONFINEMENT")
	}
	if policy.ContextMax, err = openaiapi.EffectiveContextMax(a.ContextConfinement, getenv); err != nil {
		return nil, errBadClass("context_confinement", "MONOAGENT_API_CONTEXT_CONFINEMENT")
	}
	if policy.AutoMax, err = openaiapi.EffectiveAutoMax(a.AutoConfinement, getenv); err != nil {
		return nil, errBadClass("auto_confinement", "MONOAGENT_API_AUTO_CONFINEMENT")
	}
	imageRuntimes, err := openaiapi.EffectiveImageRuntimes(getenv)
	if err != nil { // a fixed text, like the others: the shared parser quotes the value
		return nil, errors.New("MONOAGENT_API_IMAGE_RUNTIMES must be a comma-separated list of runtime ids, such as codex,antigravity")
	}
	toolRuntimes, err := openaiapi.EffectiveToolRuntimes(getenv)
	if err != nil { // a fixed text too
		return nil, errors.New("MONOAGENT_API_TOOL_RUNTIMES must be a comma-separated list of runtime ids, such as claude,codex")
	}
	// Bound to the call's context: it ends when the server stops its calls, and the
	// call does not wait for monomind's processes to close their pipes.
	models, err := rt.modelCatalog().ModelsBound(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	return openaiapi.NewModelsReport(openaiapi.ModelsReportInput{
		For: a.For, Policy: policy, Source: openaiapi.ReportSourceMCP, Models: models, ImageRuntimes: imageRuntimes, ToolRuntimes: toolRuntimes,
		Auto: openaiapi.DefaultAuto(rt.db.DB).Status(ctx, rt.profileID),
	}), nil
}
