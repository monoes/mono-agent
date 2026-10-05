package main

// `api models --json` and the MCP tool api_models_list read the settings saved with `api config` as the
// layer below the environment, and say the same. These tests drive the tool over a pipe, as
// api_models_mcp_test.go does, with settings saved in the database both of them open.

import (
	"slices"
	"strings"
	"testing"
)

func TestAPIModelsListReadsTheSavedSettingsLikeAPIModels(t *testing.T) {
	cases := []struct {
		name  string
		saved []string
		args  map[string]string
		env   map[string]string
		check func(t *testing.T, m apiModelsJSON) // the case exercises what it names
	}{
		{
			name:  "classes and lists saved",
			saved: []string{"confinement=sandboxed", "context_confinement=sandboxed", "auto_confinement=sandboxed", "image_runtimes=agy", "tool_runtimes=none"},
			check: func(t *testing.T, m apiModelsJSON) {
				if p := m.Policy; p.Confinement != "sandboxed" || p.ContextConfinement != "sandboxed" || p.AutoConfinement != "sandboxed" {
					t.Errorf("the saved classes: %+v", p)
				}
				if got := capabilitiesByID(m); !slices.Contains(got["antigravity/default"], "image") || slices.Contains(got["codex/default"], "image") || slices.Contains(got["claude/default"], "tools") {
					t.Errorf("the saved lists: %v", got)
				}
			},
		},
		{
			name:  "saved, and the environment over it",
			saved: []string{"confinement=sandboxed", "image_runtimes=none"},
			env:   map[string]string{"MONOAGENT_API_CONFINEMENT": "chat-only", "MONOAGENT_API_IMAGE_RUNTIMES": "codex"},
			check: func(t *testing.T, m apiModelsJSON) {
				if m.Policy.Confinement != "chat-only" || !slices.Contains(capabilitiesByID(m)["codex/default"], "image") {
					t.Errorf("the environment over the saved settings: %+v %v", m.Policy, capabilitiesByID(m)["codex/default"])
				}
			},
		},
		{
			name:  "saved, and an argument over it",
			saved: []string{"confinement=chat-only"},
			args:  map[string]string{"confinement": "any"},
			check: func(t *testing.T, m apiModelsJSON) {
				if m.Policy.Confinement != "any" {
					t.Errorf("the argument over the saved settings: %+v", m.Policy)
				}
			},
		},
		{
			name:  "saved, for a network listener",
			saved: []string{"confinement=sandboxed", "context_confinement=any", "auto_confinement=any"},
			args:  map[string]string{"for": "network"},
			check: func(t *testing.T, m apiModelsJSON) {
				if p := m.Policy; p.For != "network" || p.Confinement != "sandboxed" || p.ContextConfinement != "sandboxed" || p.AutoConfinement != "sandboxed" {
					t.Errorf("a saved confinement holds on a network listener, and caps the others: %+v", p)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newAPITestDB(t)
			fakeAPIMonomind(t)
			clearAPIEnv(t)
			t.Setenv("TYPESAFE_API_KEY", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			saveAt(t, db, c.saved...)

			names := make([]string, 0, len(c.args))
			for name := range c.args {
				names = append(names, name)
			}
			slices.Sort(names)
			cliArgs := []string{"models"}
			for _, name := range names {
				cliArgs = append(cliArgs, modelsFlagOf[name], c.args[name])
			}
			cli, _, err := runAPI(t, db, "default", true, cliArgs...)
			if err != nil {
				t.Fatal(err)
			}
			tool := mcpModelsList(t, db, "default", c.args)

			c.check(t, decodeModels(t, cli))
			asTool := strings.Replace(strings.TrimSuffix(cli, "\n"), `"source": "shell"`, `"source": "mcp"`, 1)
			if tool != asTool {
				t.Errorf("api_models_list is not the document of api models --json: %s", firstDifference(asTool, tool))
			}
		})
	}
}
