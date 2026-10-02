package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// fakeReadOnlyMonomind is fakeAPIMonomind for a monomind that can run claude and
// codex read-only (agent-exec-access-read, and "read" among their access modes),
// which is what a tool leg on codex needs.
func fakeReadOnlyMonomind(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake monomind is a shell script")
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.22.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models","agent-exec-sandbox","agent-exec-access-read"]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then
  echo '{"v":1,"agents":[
    {"id":"claude","installed":true,"binary":"/usr/local/bin/claude","version":"2.1.0","install_hint":"","native_sandbox":"monomind","sandbox_modes":["read-only","workspace-write","full"],"access_modes":["scoped","read","full"]},
    {"id":"codex","installed":true,"binary":"/usr/local/bin/codex","version":null,"install_hint":"","native_sandbox":"full","sandbox_modes":["read-only","workspace-write","full"],"access_modes":["scoped","read","full"]},
    {"id":"antigravity","installed":true,"binary":"/usr/local/bin/agy","version":"1.2.14","install_hint":"","native_sandbox":"none","sandbox_modes":["restricted","full"],"access_modes":["scoped","full"]}]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "models" ]; then
  case "$4" in
    claude) echo '{"v":1,"runtime":"claude","supported":true,"models":[{"id":"default","label":"Default"}]}' ;;
    codex) echo '{"v":1,"runtime":"codex","supported":true,"models":[{"id":"gpt-6-astra","label":"GPT-6-Astra"}]}' ;;
    antigravity) echo '{"v":1,"runtime":"antigravity","supported":true,"models":[{"id":"gemini-3.8-flash-high","label":"Gemini 3.8 Flash"}]}' ;;
  esac
  exit 0
fi
exit 2
`
	bin := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
}

// `api models` says which models call tools, from the shell's environment like
// everything else it evaluates: the runtimes of MONOAGENT_API_TOOL_RUNTIMES, as far
// as monomind can run them read-only, and claude, whose own tools monomind gates.
func TestAPIModelsShowsWhichModelsCallTools(t *testing.T) {
	db := newAPITestDB(t)
	fakeReadOnlyMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "")
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	got := capabilitiesByID(decodeModels(t, out))
	for id, want := range map[string][]string{
		"claude/default":                    {"text", "tools"},
		"codex/gpt-6-astra":                 {"text", "image", "tools"},
		"antigravity/gemini-3.8-flash-high": {"text", "image"}, // not in the list, and cannot run read-only
	} {
		if !slices.Equal(got[id], want) {
			t.Errorf("%s: capabilities %v, want %v", id, got[id], want)
		}
	}

	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "codex")
	out, _, err = runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	got = capabilitiesByID(decodeModels(t, out))
	if slices.Contains(got["claude/default"], "tools") || !slices.Contains(got["codex/gpt-6-astra"], "tools") {
		t.Errorf("MONOAGENT_API_TOOL_RUNTIMES=codex: %v", got)
	}
}

func TestAPIModelsRefusesABadToolRuntimeList(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "co dex")
	_, _, err := runAPI(t, db, "default", true, "models")
	if exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_TOOL_RUNTIMES") {
		t.Fatalf("a bad list is invalid input (exit 3) that names the variable: %v", err)
	}
}

// withoutTools is a capability list without tools: what the tests of the image
// capability are about.
func withoutTools(caps []string) []string {
	return slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return c == "tools" })
}

// none switches tool calling off, as it does for the gateway: no model calls tools.
func TestAPIModelsShowsToolCallingSwitchedOff(t *testing.T) {
	db := newAPITestDB(t)
	fakeReadOnlyMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "")
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "none")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatalf("MONOAGENT_API_TOOL_RUNTIMES=none: %v", err)
	}
	for id, caps := range capabilitiesByID(decodeModels(t, out)) {
		if slices.Contains(caps, "tools") {
			t.Errorf("MONOAGENT_API_TOOL_RUNTIMES=none: %s has capabilities %v", id, caps)
		}
	}
	// Images are another list: still there.
	if got := capabilitiesByID(decodeModels(t, out))["codex/gpt-6-astra"]; !slices.Equal(got, []string{"text", "image"}) {
		t.Errorf("tool calling off must not touch the images: %v", got)
	}

	// A list that has none and a runtime is not a list.
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "none,codex")
	if _, _, err := runAPI(t, db, "default", true, "models"); exitCodeFor(err) != 3 || err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_TOOL_RUNTIMES") {
		t.Fatalf("none with others is invalid input (exit 3) that names the variable: %v", err)
	}
}

// The table has a column for it, after the IMAGES one, from the same report.
func TestAPIModelsTableHasAToolsColumn(t *testing.T) {
	db := newAPITestDB(t)
	fakeReadOnlyMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_IMAGE_RUNTIMES", "")
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "")

	column := func() (string, map[string]string) {
		out, _, err := runAPI(t, db, "default", false, "models")
		if err != nil {
			t.Fatal(err)
		}
		var header string
		tools := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			switch {
			case strings.HasPrefix(line, "MODEL"):
				header = line
			case len(f) > 0 && (strings.HasPrefix(f[0], "claude/") || strings.HasPrefix(f[0], "codex/") || strings.HasPrefix(f[0], "antigravity/")):
				tools[f[0]] = f[len(f)-1]
			}
		}
		return header, tools
	}
	header, tools := column()
	if !strings.HasSuffix(strings.TrimSpace(header), "IMAGES  TOOLS") {
		t.Fatalf("the TOOLS column goes last: %q", header)
	}
	for id, want := range map[string]string{"claude/default": "yes", "codex/gpt-6-astra": "yes", "antigravity/default": "no"} {
		if tools[id] != want {
			t.Errorf("%s: the TOOLS column says %q, want %s", id, tools[id], want)
		}
	}
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "none")
	if _, tools = column(); tools["claude/default"] != "no" || tools["codex/gpt-6-astra"] != "no" {
		t.Errorf("tool calling switched off: %v", tools)
	}
}

// Which models serve tools depends on more than the list of runtimes: every turn with tools
// requires monomind's sandbox, and a key created with --context is refused tools unless
// --context-confinement is above chat-only. The help says both, and so does one line under
// the table (the JSON document is the one the MCP tool returns, and has no field for it).
func TestAPIModelsSaysWhatToolCallingNeedsBeyondTheListOfRuntimes(t *testing.T) {
	db := newAPITestDB(t)
	fakeReadOnlyMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_API_TOOL_RUNTIMES", "")

	help, _, err := runAPI(t, db, "default", false, "models", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(help), " ")
	for _, want := range []string{"sandbox", "agent-exec-sandbox", "refused tools", "--context-confinement"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the help of `api models` does not say %q:\n%s", want, help)
		}
	}

	out, _, err := runAPI(t, db, "default", false, "models")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "tools: ") || !strings.Contains(last, "sandbox") || !strings.Contains(last, "--context-confinement") {
		t.Errorf("the last line under the table must say what tool calling needs, got %q", last)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "tools: ") && line != last {
			t.Errorf("one line, not two: %q", line)
		}
	}
}
