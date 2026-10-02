package openaiapi

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestParseToolRuntimes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"", []string{"claude", "codex"}, false},
		{"   ", []string{"claude", "codex"}, false},
		{"claude", []string{"claude"}, false},
		{" Claude , CODEX ,claude", []string{"claude", "codex"}, false},
		{"agy,claude", []string{"antigravity", "claude"}, false},
		{"codex,", nil, true},
		{"claude;codex", nil, true},
		{"--help", nil, true},
		{"claude,../x", nil, true},
	}
	for _, c := range cases {
		got, err := ParseToolRuntimes(c.in)
		if (err != nil) != c.err || (err == nil && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("ParseToolRuntimes(%q) = %v, %v; want %v (error %v)", c.in, got, err, c.want, c.err)
		}
	}
}

func TestConfigFromEnvReadsTheToolRuntimes(t *testing.T) {
	conf, err := ConfigFromEnv(func(k string) string {
		if k == "MONOAGENT_API_TOOL_RUNTIMES" {
			return "codex"
		}
		return ""
	})
	if err != nil || !reflect.DeepEqual(conf.ToolRuntimeList(), []string{"codex"}) {
		t.Fatalf("conf %+v, err %v", conf, err)
	}
	if _, err := ConfigFromEnv(func(k string) string {
		if k == "MONOAGENT_API_TOOL_RUNTIMES" {
			return "not a runtime"
		}
		return ""
	}); err == nil {
		t.Error("a bad MONOAGENT_API_TOOL_RUNTIMES must be refused at start, not ignored")
	}
	if got := (Config{}).ToolRuntimeList(); !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Errorf("the default list = %v", got)
	}
}

// One predicate decides both whether a request for tools is refused and whether a
// model is listed as able to call them.
func TestServesToolsAndItsRefusalAgree(t *testing.T) {
	cfg := Config{}
	models := []struct {
		m      ModelInfo
		serves bool
	}{
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly}, true},
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly, ReadAccess: true}, true},
		{ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed, ReadAccess: true}, true},
		{ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed}, false}, // monomind cannot run it read-only
		{ModelInfo{ID: "antigravity/default", Runtime: "antigravity", Class: Unconfined, ReadAccess: true}, false},
		{ModelInfo{ID: "hermes/default", Runtime: "hermes", Class: Unconfined}, false},
	}
	for _, c := range models {
		e := cfg.toolsRefusal(c.m)
		if cfg.ServesTools(c.m) != c.serves || (e == nil) != c.serves {
			t.Errorf("%s (%s, read access %v): serves %v, refusal %v, want serves %v", c.m.ID, c.m.Class, c.m.ReadAccess, cfg.ServesTools(c.m), e, c.serves)
		}
		if e != nil && (e.Status != 400 || e.Code != "unsupported_parameter" || e.Param != "tools") {
			t.Errorf("%s: refusal %+v", c.m.ID, e)
		}
	}
	// An operator's list decides the runtime half.
	cfg.ToolRuntimes = []string{"antigravity"}
	if !cfg.ServesTools(ModelInfo{Runtime: "antigravity", Class: Unconfined, ReadAccess: true}) || cfg.ServesTools(ModelInfo{Runtime: "claude", Class: ChatOnly}) {
		t.Error("the configured runtimes are not what decides")
	}
}

func TestToolsRefusalSaysWhichRuntimesServeTools(t *testing.T) {
	e := (Config{}).toolsRefusal(ModelInfo{ID: "antigravity/default", Runtime: "antigravity", Class: Unconfined})
	if e == nil {
		t.Fatal("antigravity must be refused")
	}
	for _, want := range []string{"claude", "codex", "MONOAGENT_API_TOOL_RUNTIMES"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the refusal does not say %q: %s", want, e.Message)
		}
	}
	e = (Config{}).toolsRefusal(ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed})
	if e == nil || !strings.Contains(e.Message, "read") {
		t.Errorf("a runtime that cannot run read-only must say so: %+v", e)
	}
}

// The catalog says whether monomind can run a model's runtime with read access:
// the handshake has the capability and the runtime's scan entry lists the mode.
func TestCatalogMarksTheModelsThatCanRunReadOnly(t *testing.T) {
	read := func(caps ...string) func(*Deps, *Config) {
		return func(d *Deps, _ *Config) {
			d.Catalog.Caps = func(context.Context) (*monomind.CapabilitySet, error) {
				return monomind.NewCapabilitySet("2.22.0", caps...), nil
			}
		}
	}
	for name, c := range map[string]struct {
		mutate func(*Deps, *Config)
		want   map[string]bool // by runtime
	}{
		"with the capability": {read(monomind.CapAgentExecSandbox, monomind.CapAgentExecAccessRead), map[string]bool{"claude": true, "codex": true, "antigravity": false}},
		"without it":          {read(monomind.CapAgentExecSandbox), map[string]bool{"claude": false, "codex": false, "antigravity": false}},
	} {
		h := newHarness(t, okTurn("x"), c.mutate)
		models, err := h.g.catalog.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range models {
			if want, ok := c.want[m.Runtime]; ok && m.ReadAccess != want {
				t.Errorf("%s: %s has ReadAccess %v, want %v", name, m.ID, m.ReadAccess, want)
			}
		}
	}
}
