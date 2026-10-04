package main

// `api models --json` and the MCP tool api_models_list are one document, made by the
// same code in internal/openaiapi. Each of them gathers its own inputs, though: flags
// or arguments, this shell's environment or the MCP server's, the profile, the
// models. That is where they could come apart. These tests drive the tool as a host
// does, over a pipe to a server, and compare its answer with the CLI's byte for byte:
// they differ in policy.source, which says who evaluated the policy, and in nothing
// else.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/mcp"
)

// modelsFlagOf is the flag of `api models` that stands for each argument of the tool.
var modelsFlagOf = map[string]string{
	"for":                 "--for",
	"confinement":         "--confinement",
	"context_confinement": "--context-confinement",
	"auto_confinement":    "--auto-confinement",
}

const codexOnly = `{"id":"codex","installed":true,"binary":"/usr/local/bin/codex","version":null,"install_hint":"","native_sandbox":"full","sandbox_modes":["read-only","workspace-write","full"]}`

// mcpModelsList is what api_models_list answers to a server of its own, asked over a
// pipe the way a host asks. The server has the profile and database of the CLI.
func mcpModelsList(t *testing.T, dbPath, profile string, args map[string]string) string {
	t.Helper()
	if args == nil {
		args = map[string]string{}
	}
	s := mcp.NewServer(mcp.Options{DBPath: dbPath, Profile: profile, WorkflowsDir: filepath.Join(t.TempDir(), "workflows"), Version: "test"})
	in, toServer := io.Pipe()
	fromServer, out := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- s.Serve(context.Background(), in, out)
		out.Close()
	}()

	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "api_models_list", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toServer.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}
	type reply struct {
		line []byte
		err  error
	}
	replied := make(chan reply, 1)
	go func() {
		line, err := bufio.NewReader(fromServer).ReadBytes('\n')
		replied <- reply{line, err}
	}()
	var r reply
	select {
	case r = <-replied:
	case <-time.After(30 * time.Second):
		t.Fatal("the MCP server did not answer api_models_list within 30 s")
	}
	toServer.Close() // the host is done: the server stops
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("the server stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("the MCP server did not stop when its input ended")
	}
	if r.err != nil {
		t.Fatalf("no answer from the MCP server: %v", r.err)
	}

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.line, &resp); err != nil {
		t.Fatalf("not a JSON-RPC answer: %v\n%s", err, r.line)
	}
	if resp.Error != nil || resp.Result.IsError || len(resp.Result.Content) != 1 {
		t.Fatalf("api_models_list failed: %+v", resp)
	}
	return resp.Result.Content[0].Text
}

// firstDifference says where two documents part.
func firstDifference(cli, tool string) string {
	c, m := strings.Split(cli, "\n"), strings.Split(tool, "\n")
	for i := 0; i < len(c) || i < len(m); i++ {
		var cl, ml string
		if i < len(c) {
			cl = c[i]
		}
		if i < len(m) {
			ml = m[i]
		}
		if cl != ml {
			return fmt.Sprintf("line %d: the CLI has %q, the tool has %q", i+1, cl, ml)
		}
	}
	return "none"
}

// autoState names what a report says about the auto model.
func autoState(a apiAutoJSON) string {
	switch {
	case a.Available:
		return "available"
	case strings.Contains(a.Missing, "jev enable api_auto"):
		return "surface off"
	case strings.Contains(a.Missing, "Jev key"):
		return "no key"
	case strings.Contains(a.Missing, "--auto-confinement"):
		return "all held back"
	case strings.Contains(a.Missing, "at least one model"):
		return "no model served"
	}
	return "unexpected: " + a.Missing
}

func TestAPIModelsListIsTheDocumentOfAPIModelsJSON(t *testing.T) {
	cases := []struct {
		name     string
		runtimes string // "" the three of fakeAPIMonomind, "codex" a scan of codex alone, "none" a scan of nothing
		args     map[string]string
		env      map[string]string
		surface  bool // the api_auto surface is on for the profile
		key      bool // TYPESAFE_API_KEY is set
		auto     string
		models   int // how many models the document lists
	}{
		{name: "nothing given", auto: "surface off", models: 5},
		{name: "a network listener", args: map[string]string{"for": "network"}, auto: "surface off", models: 5},
		{name: "a stronger listener policy", args: map[string]string{"confinement": "sandboxed"}, auto: "surface off", models: 5},
		{name: "a context key raised", args: map[string]string{"context_confinement": "sandboxed"}, auto: "surface off", models: 5},
		{name: "every argument", args: map[string]string{"for": "network", "confinement": "any", "context_confinement": "sandboxed", "auto_confinement": "sandboxed"}, auto: "surface off", models: 5},
		{
			name: "the environment alone",
			env: map[string]string{
				"MONOAGENT_API_CONFINEMENT": "sandboxed", "MONOAGENT_API_CONTEXT_CONFINEMENT": "sandboxed", "MONOAGENT_API_AUTO_CONFINEMENT": "sandboxed",
			},
			auto: "surface off", models: 5,
		},
		{
			name: "an argument over the environment",
			args: map[string]string{"confinement": "any", "auto_confinement": "chat-only"},
			env:  map[string]string{"MONOAGENT_API_CONFINEMENT": "chat-only", "MONOAGENT_API_AUTO_CONFINEMENT": "any"},
			auto: "surface off", models: 5,
		},
		{name: "auto on, no key", surface: true, auto: "no key", models: 5},
		{name: "auto on with a key", surface: true, key: true, auto: "available", models: 5},
		{name: "auto raised to any", args: map[string]string{"auto_confinement": "any"}, surface: true, key: true, auto: "available", models: 5},
		{name: "auto on a network listener", args: map[string]string{"for": "network", "auto_confinement": "any"}, surface: true, key: true, auto: "available", models: 5},
		{name: "auto with everything held back", runtimes: "codex", surface: true, key: true, auto: "all held back", models: 1},
		{name: "auto with nothing served", runtimes: "codex", args: map[string]string{"for": "network"}, surface: true, key: true, auto: "no model served", models: 1},
		{name: "no runtime installed", runtimes: "none", surface: true, key: true, auto: "no model served", models: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newAPITestDB(t)
			switch c.runtimes {
			case "":
				fakeAPIMonomind(t)
			case "codex":
				fakeMonomindWithAgents(t, codexOnly)
			case "none":
				fakeMonomindWithAgents(t, "")
			}
			for _, v := range []string{"MONOAGENT_API_CONFINEMENT", "MONOAGENT_API_CONTEXT_CONFINEMENT", "MONOAGENT_API_AUTO_CONFINEMENT", "MONOAGENT_API_IMAGE_RUNTIMES", "TYPESAFE_API_KEY"} {
				t.Setenv(v, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.key {
				t.Setenv("TYPESAFE_API_KEY", "test-key")
			}
			if c.surface {
				enableAPIAuto(t, db, "default")
			}

			names := make([]string, 0, len(c.args))
			for name := range c.args {
				names = append(names, name)
			}
			sort.Strings(names)
			cliArgs := []string{"models"}
			for _, name := range names {
				cliArgs = append(cliArgs, modelsFlagOf[name], c.args[name])
			}
			cli, _, err := runAPI(t, db, "default", true, cliArgs...)
			if err != nil {
				t.Fatal(err)
			}
			tool := mcpModelsList(t, db, "default", c.args)

			// Each says who evaluated the policy; apart from that the two are the same.
			doc := decodeModels(t, cli)
			if doc.Policy.Source != "shell" || decodeModels(t, tool).Policy.Source != "mcp" {
				t.Errorf("policy.source is %q for the CLI and %q for the tool, want shell and mcp", doc.Policy.Source, decodeModels(t, tool).Policy.Source)
			}
			if got := autoState(doc.Auto); got != c.auto || len(doc.Models) != c.models {
				t.Errorf("the case does not exercise what it names: auto is %q (want %q) over %d models (want %d)", got, c.auto, len(doc.Models), c.models)
			}
			asTool := strings.Replace(strings.TrimSuffix(cli, "\n"), `"source": "shell"`, `"source": "mcp"`, 1)
			if tool != asTool {
				t.Errorf("api_models_list is not the document of api models --json: %s", firstDifference(asTool, tool))
			}
		})
	}
}

// Both say which models make images, from the environment each of them runs in, and
// say the same: the capability is part of the one document.
func TestAPIModelsListSaysWhichModelsMakeImagesLikeAPIModels(t *testing.T) {
	for _, c := range []struct {
		name   string
		env    string // MONOAGENT_API_IMAGE_RUNTIMES
		images string // the models that make images, sorted
	}{
		{"the default list", "", "antigravity/default,antigravity/gemini-3.8-flash-high,codex/default,codex/gpt-6-astra"},
		{"antigravity alone", "agy", "antigravity/default,antigravity/gemini-3.8-flash-high"},
		{"codex alone", "codex", "codex/default,codex/gpt-6-astra"},
		{"switched off", "none", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := newAPITestDB(t)
			fakeAPIMonomind(t)
			for _, v := range []string{"MONOAGENT_API_CONFINEMENT", "MONOAGENT_API_CONTEXT_CONFINEMENT", "MONOAGENT_API_AUTO_CONFINEMENT", "TYPESAFE_API_KEY"} {
				t.Setenv(v, "")
			}
			t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", c.env)

			cli, _, err := runAPI(t, db, "default", true, "models")
			if err != nil {
				t.Fatal(err)
			}
			tool := mcpModelsList(t, db, "default", nil)

			var images []string
			for _, m := range decodeModels(t, cli).Models {
				if slices.Contains(m.Capabilities, "image") {
					images = append(images, m.ID)
				}
			}
			sort.Strings(images)
			if got := strings.Join(images, ","); got != c.images {
				t.Errorf("the models that make images: %s, want %s", got, c.images)
			}
			asTool := strings.Replace(strings.TrimSuffix(cli, "\n"), `"source": "shell"`, `"source": "mcp"`, 1)
			if tool != asTool {
				t.Errorf("api_models_list is not the document of api models --json: %s", firstDifference(asTool, tool))
			}
		})
	}
}
