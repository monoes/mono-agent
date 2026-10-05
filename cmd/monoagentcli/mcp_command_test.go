package main

// The `mcp` command hands the operator's two switches to the server and refuses to mix them with
// grant mode. The server itself is never started here: runMCP is replaced.

import (
	"bytes"
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
}
