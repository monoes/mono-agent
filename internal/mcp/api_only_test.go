package mcp

// --api-only serves a model the OpenAI-compatible API's tools and none other (the owner's decision of 2026-10-05,
// after the security review of phase 6 found that --allow-mutations, which the API's mutating tools need, also
// serves workflow tools that can run a command as the OS user, so that the operator's --allow-api-exposure guarded
// one tool and not the machine). With it the model has no workflow, vault, secret, person, org or documentation
// tool, and the exposure flag is a boundary for what that model can reach through this server.

import (
	"sort"
	"strings"
	"testing"
)

// the tools of the API, written out so that a tool added to the family without a decision shows as a failure here.
var apiReadOnlyTools = []string{"api_key_list", "api_models_list", "api_status", "api_config_get"}
var apiMutatingTools = []string{"api_key_create", "api_key_update", "api_key_revoke", "api_config_set", "api_config_apply", "api_auto_set"}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAPIOnlyServesTheAPIToolsAndNoOther(t *testing.T) {
	for name, c := range map[string]struct {
		setup configSetup
		want  []string
	}{
		"read-only":              {configSetup{readOnly: true, apiOnly: true}, apiReadOnlyTools},
		"with --allow-mutations": {configSetup{apiOnly: true}, append(append([]string(nil), apiReadOnlyTools...), apiMutatingTools...)},
	} {
		got := sortedKeys(toolsListNames(t, newConfigFixture(t, c.setup).Server))
		if want := sortedCopy(c.want); !equalStrings(got, want) {
			t.Errorf("%s: tools/list = %v, want exactly %v", name, got, want)
		}
	}
	// And a server without it serves them among the others, as it always did.
	all := toolsListNames(t, newConfigFixture(t, configSetup{}).Server)
	for _, name := range []string{"api_status", "api_config_set", "workflow_run", "workflow_node_add", "workflow_set_active", "docs", "secret_list"} {
		if !all[name] {
			t.Errorf("a server without --api-only does not list %s", name)
		}
	}
}

// Every tool that is not one of the API's is refused by name, mutating or not and with --allow-mutations given,
// before its handler runs: the model that was given the API's tools has nothing else to call.
func TestAPIOnlyRefusesEveryOtherToolByName(t *testing.T) {
	f := newConfigFixture(t, configSetup{apiOnly: true, allowExposure: true})
	api := map[string]bool{}
	for _, n := range append(append([]string(nil), apiReadOnlyTools...), apiMutatingTools...) {
		api[n] = true
	}
	refused := 0
	for _, tl := range allTools() {
		if api[tl.name] {
			continue
		}
		_, err := f.call(tl.name, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "--api-only") || !strings.Contains(err.Error(), tl.name) || strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%s: %v, want a refusal that says this server serves only the API's tools (--api-only)", tl.name, err)
		}
		refused++
	}
	if refused < 20 {
		t.Errorf("only %d other tools were refused: is the table of tools read?", refused)
	}
	// A name that is nobody's stays unknown, as it was.
	if _, err := f.call("nonsense_tool", nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("an unknown tool: %v", err)
	}
}

// The API's tools work as they did: --api-only takes tools away and changes none of those that stay.
func TestAPIOnlyLeavesTheAPIToolsAsTheyAre(t *testing.T) {
	f := newConfigFixture(t, configSetup{apiOnly: true})
	if text := f.mustCall("api_config_get", nil); !strings.Contains(text, `"environment": "mcp"`) {
		t.Errorf("api_config_get: %s", text)
	}
	// The gate of api_config_set is still the operator's flag, which this server was not given.
	_, err := f.call("api_config_set", set(map[string]any{"v1_addr": "0.0.0.0:9443"}))
	if err == nil || !strings.Contains(err.Error(), "--allow-api-exposure") {
		t.Errorf("a change that reaches further on an --api-only server without the exposure flag: %v", err)
	}
	// And the mutating tools still need --allow-mutations.
	ro := newConfigFixture(t, configSetup{apiOnly: true, readOnly: true})
	if _, err := ro.call("api_config_set", set(map[string]any{"max_concurrent": "8"})); err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
		t.Errorf("api_config_set on a read-only --api-only server: %v", err)
	}
}

// The family and the filter agree: every tool called api_* is one of the API's, and every tool of the API is
// called api_*. A tool added to one without the other would be served to a model that was to have the API alone,
// or taken away from it.
func TestTheAPIToolsAreTheToolsCalledAPI(t *testing.T) {
	names := apiToolNames()
	for _, tl := range allTools() {
		if strings.HasPrefix(tl.name, "api_") != names[tl.name] {
			t.Errorf("%s: called api_*: %v, one of the API's tools: %v", tl.name, strings.HasPrefix(tl.name, "api_"), names[tl.name])
		}
	}
	want := append(append([]string(nil), apiReadOnlyTools...), apiMutatingTools...)
	if got := sortedKeys(names); !equalStrings(got, sortedCopy(want)) {
		t.Errorf("the API's tools are %v, want %v", got, sortedCopy(want))
	}
}

// The operator can set it in the environment, as the other two switches of the server.
func TestAPIOnlyCanBeSetInTheEnvironment(t *testing.T) {
	t.Setenv("MONOAGENT_MCP_API_ONLY", "1")
	f := newConfigFixture(t, configSetup{})
	names := toolsListNames(t, f.Server)
	if names["workflow_run"] || names["docs"] || !names["api_status"] {
		t.Errorf("MONOAGENT_MCP_API_ONLY=1: tools/list = %v", sortedKeys(names))
	}
}

// What the server says of itself when it starts says what it serves.
func TestAPIOnlyServerSaysWhatItServes(t *testing.T) {
	f := newConfigFixture(t, configSetup{apiOnly: true})
	if got := f.Server.instructions(); !strings.Contains(got, "OpenAI-compatible API") || strings.Contains(got, "workflow_list") {
		t.Errorf("instructions: %q", got)
	}
}
