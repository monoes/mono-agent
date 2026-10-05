package main

// The `mcp` command hands the operator's two switches to the server and refuses to mix them with
// grant mode. The server itself is never started here: runMCP is replaced.

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/mcp"
)

// runMCPCommand runs `mcp <args>` with runMCP replaced, and returns the options the server would
// have been started with (nil when the command refused before starting it) and the error.
func runMCPCommand(t *testing.T, args ...string) (*mcp.Options, error) {
	t.Helper()
	t.Setenv("MONOMIND_ORG_NAME", "")
	var got *mcp.Options
	was := runMCP
	runMCP = func(o mcp.Options) error { got = &o; return nil }
	t.Cleanup(func() { runMCP = was })

	cmd := newMCPCmd(&globalConfig{DBPath: "x.db", ProfileID: "default"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	return got, err
}

func TestMCPCommandHandsTheOperatorsSwitchesToTheServer(t *testing.T) {
	for _, c := range []struct {
		args                []string
		mutations, exposure bool
	}{
		{nil, false, false},
		{[]string{"--allow-mutations"}, true, false},
		{[]string{"--allow-api-exposure"}, false, true},
		{[]string{"--allow-mutations", "--allow-api-exposure"}, true, true},
	} {
		got, err := runMCPCommand(t, c.args...)
		if err != nil || got == nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got.AllowMutations != c.mutations || got.AllowAPIExposure != c.exposure {
			t.Errorf("%v: AllowMutations %v, AllowAPIExposure %v; want %v and %v", c.args, got.AllowMutations, got.AllowAPIExposure, c.mutations, c.exposure)
		}
	}
}

// Grant mode serves one role's automations and none of the API tools, so a flag that only the API
// tools read is a mistake in its configuration, as --allow-mutations is.
func TestMCPCommandRefusesGrantModeWithTheExposureFlag(t *testing.T) {
	got, err := runMCPCommand(t, "--grant", "grt_x", "--allow-api-exposure")
	if err == nil || got != nil || !strings.Contains(err.Error(), "--allow-api-exposure") {
		t.Errorf("--grant with --allow-api-exposure: options %v, error %v", got, err)
	}
	if got, err := runMCPCommand(t, "--grant", "grt_x"); err != nil || got == nil || got.Grant != "grt_x" {
		t.Errorf("--grant alone: %v, %v", got, err)
	}
}

// --api-only is the operator's third switch: it takes every tool but the API's away from the server (the
// owner's decision of 2026-10-05). The command hands it over and, as the other two, refuses it in grant mode.
func TestMCPCommandHandsAPIOnlyToTheServer(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--api-only"}, true},
		{[]string{"--api-only", "--allow-mutations", "--allow-api-exposure"}, true},
		{[]string{"--allow-mutations"}, false},
	} {
		got, err := runMCPCommand(t, c.args...)
		if err != nil || got == nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got.APIOnly != c.want {
			t.Errorf("%v: APIOnly %v, want %v", c.args, got.APIOnly, c.want)
		}
	}
}

func TestMCPCommandRefusesGrantModeWithAPIOnly(t *testing.T) {
	got, err := runMCPCommand(t, "--grant", "grt_x", "--api-only")
	if err == nil || got != nil || !strings.Contains(err.Error(), "--api-only does not apply") {
		t.Errorf("--grant with --api-only: options %v, error %v", got, err)
	}
}

func TestMCPCommandDocumentsAPIOnly(t *testing.T) {
	cmd := newMCPCmd(&globalConfig{})
	fl := cmd.Flags().Lookup("api-only")
	if fl == nil {
		t.Fatal("`mcp` has no --api-only")
	}
	for _, want := range []string{"MONOAGENT_MCP_API_ONLY", "api_*", "workflow"} {
		if !strings.Contains(fl.Usage, want) {
			t.Errorf("the usage of --api-only must mention %s: %q", want, fl.Usage)
		}
	}
	long := oneLine(cmd.Long)
	for _, want := range []string{"--api-only", "MONOAGENT_MCP_API_ONLY", "no workflow, vault, secret, person, org or documentation tool"} {
		if !strings.Contains(long, want) {
			t.Errorf("the help of `mcp` does not say %q", want)
		}
	}
	ref := captureStdout(t, func() { c := refAPICmd(); c.Run(c, nil) })
	if from := strings.Index(ref, "From MCP (monoagentcli mcp)"); from < 0 || !strings.Contains(ref[from:], "--api-only") {
		t.Error("the paragraph of `ref api` on `mcp` does not mention --api-only")
	}
}

// slashList matches the shorthand the help uses for a family of tools: api_key_create/update/revoke.
var slashList = regexp.MustCompile(`([a-z_]*_)([a-z]+)((?:/[a-z_]+)+)`)

// expandToolLists spells the shorthand out: api_key_create/update/revoke is api_key_create,
// api_key_update and api_key_revoke.
func expandToolLists(help string) string {
	return slashList.ReplaceAllStringFunc(help, func(m string) string {
		parts := slashList.FindStringSubmatch(m)
		names := []string{parts[1] + parts[2]}
		for _, rest := range strings.Split(strings.TrimPrefix(parts[3], "/"), "/") {
			names = append(names, parts[1]+rest)
		}
		return strings.Join(names, " ")
	})
}

// apiToolNames are the tools of the OpenAI-compatible API a server lists, with mutations allowed or
// not.
func apiToolNames(t *testing.T, db string, allowMutations bool) []string {
	t.Helper()
	o := mcpOptions(t, db, "default", false)
	o.AllowMutations = allowMutations
	var names []string
	for _, name := range newMCPSession(t, o).toolNames() {
		if strings.HasPrefix(name, "api_") {
			names = append(names, name)
		}
	}
	return names
}

// The help of the command lists the tools. Every tool of the OpenAI-compatible API that the server
// really serves is named in it, those that need --allow-mutations in the paragraph that says which
// do and the others before it, so that a tool added later cannot be left out of what an operator
// reads before they register the server, or filed under the wrong switch.
func TestMCPCommandHelpNamesEveryAPITool(t *testing.T) {
	db := newAPITestDB(t)
	served := apiToolNames(t, db, true)
	if len(served) < 10 {
		t.Fatalf("the server lists %d API tools: %v", len(served), served)
	}
	readOnly := map[string]bool{}
	for _, name := range apiToolNames(t, db, false) {
		readOnly[name] = true
	}
	long := newMCPCmd(&globalConfig{}).Long
	named := expandToolLists(long)
	start := strings.Index(named, "Mutating tools (")
	end := start + strings.Index(named[max(start, 0):], ") are only") // the help wraps its lines after "only"
	if start < 0 || end < start {
		t.Fatalf("the help has no paragraph that lists the mutating tools:\n%s", long)
	}
	for _, name := range served {
		switch {
		case !strings.Contains(named, name):
			t.Errorf("the help of `mcp` does not name %s", name)
		case readOnly[name] && !strings.Contains(named[:start], name):
			t.Errorf("%s needs no --allow-mutations, and the help does not list it with the tools that are always exposed", name)
		case !readOnly[name] && !strings.Contains(named[start:end], name):
			t.Errorf("%s needs --allow-mutations, and the help does not list it with the mutating tools", name)
		}
	}
	// What the tool does with a change that reaches further is said next to the flag that allows it.
	for _, want := range []string{"api_config_apply", "interrupts", "acknowledge_egress"} {
		if !strings.Contains(long, want) {
			t.Errorf("the help of `mcp` does not say %q", want)
		}
	}
}

// finalWideningRules are the cases of a change that reaches further, in the words of SECURITY.md and of
// the plan's Widening section. The three places that tell an operator what api_config_set refuses (the
// help of `mcp`, the usage of --allow-api-exposure and `ref api`) say them the same way, so that none
// is left saying less than the gate does.
const finalWideningRules = "a dedicated listener that reaches further than the saved one (beyond this machine, another host beyond it, " +
	"or every interface where it was one host), a higher confinement class, a runtime list that gains a runtime it did not have, " +
	"none left (tool calling or image generation switched on again), and removing a saved row that cannot be read (saved_settings)"

// oneLine is a text as one line: the help wraps its lines, and a phrase may be wrapped inside.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestMCPCommandDocumentsTheExposureFlag(t *testing.T) {
	cmd := newMCPCmd(&globalConfig{})
	fl := cmd.Flags().Lookup("allow-api-exposure")
	if fl == nil {
		t.Fatal("`mcp` has no --allow-api-exposure")
	}
	for _, want := range []string{"MONOAGENT_MCP_ALLOW_API_EXPOSURE", "api_config_set"} {
		if !strings.Contains(fl.Usage, want) {
			t.Errorf("the usage of --allow-api-exposure must mention %s: %q", want, fl.Usage)
		}
	}
	if !strings.Contains(cmd.Long, "--allow-api-exposure") {
		t.Error("the help of `mcp` does not explain --allow-api-exposure next to --allow-mutations")
	}

	// What the flag allows is what the gate refuses: each text says every case, in the same words.
	ref := captureStdout(t, func() { c := refAPICmd(); c.Run(c, nil) })
	fromMCP := strings.Index(ref, "From MCP (monoagentcli mcp)")
	if fromMCP < 0 {
		t.Fatal("`ref api` has no paragraph on the tools of `mcp`")
	}
	for name, text := range map[string]string{
		"the usage of --allow-api-exposure":   fl.Usage,
		"the help of `mcp`":                   cmd.Long,
		"the paragraph of `ref api` on `mcp`": ref[fromMCP:],
	} {
		if !strings.Contains(oneLine(text), finalWideningRules) {
			t.Errorf("%s does not say what api_config_set refuses in the words of the gate:\nwant %s", name, finalWideningRules)
		}
	}
}
