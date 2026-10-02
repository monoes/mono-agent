package openaiapi

import (
	"context"
	"reflect"
	"slices"
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
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly, Sandboxable: true}, true},
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly, Sandboxable: true, ReadAccess: true}, true},
		{ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed, Sandboxable: true, ReadAccess: true}, true},
		{ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed, Sandboxable: true}, false}, // monomind cannot run it read-only
		{ModelInfo{ID: "antigravity/default", Runtime: "antigravity", Class: Unconfined, Sandboxable: true, ReadAccess: true}, false},
		{ModelInfo{ID: "hermes/default", Runtime: "hermes", Class: Unconfined, Sandboxable: true}, false},
		// The sandbox every leg requires cannot be applied, whatever else is true.
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly}, false},
		{ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly, ReadAccess: true}, false},
		{ModelInfo{ID: "codex/default", Runtime: "codex", Class: Sandboxed, ReadAccess: true}, false},
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
	if !cfg.ServesTools(ModelInfo{Runtime: "antigravity", Class: Unconfined, Sandboxable: true, ReadAccess: true}) || cfg.ServesTools(ModelInfo{Runtime: "claude", Class: ChatOnly, Sandboxable: true}) {
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

// The catalog says whether monomind can apply the sandbox every leg requires to a
// model's runtime, by the rule Exec itself follows (monomind.SandboxArgs): the
// handshake has agent-exec-sandbox and the runtime's scan entry lists the mode a turn
// asks for.
func TestCatalogMarksTheModelsWhoseRuntimeCanBeSandboxed(t *testing.T) {
	for name, c := range map[string]struct {
		caps  []string
		modes map[string][]string // by runtime; the 2.22.0 fixture's own where left out
		want  map[string]bool
	}{
		"monomind 2.22": {[]string{monomind.CapAgentExecSandbox}, nil,
			map[string]bool{"claude": true, "codex": true, "antigravity": false}}, // antigravity lists restricted and full
		"no agent-exec-sandbox": {nil, nil,
			map[string]bool{"claude": false, "codex": true, "antigravity": false}}, // codex has the env path of monomind 2.11.1 and later
		"claude lists only full": {[]string{monomind.CapAgentExecSandbox}, map[string][]string{"claude": {"full"}},
			map[string]bool{"claude": false, "codex": true, "antigravity": false}},
		"claude lists no modes": {[]string{monomind.CapAgentExecSandbox}, map[string][]string{"claude": nil},
			map[string]bool{"claude": false, "codex": true, "antigravity": false}},
	} {
		h := newHarness(t, okTurn("x"), func(d *Deps, _ *Config) {
			d.Catalog.Caps = func(context.Context) (*monomind.CapabilitySet, error) {
				return monomind.NewCapabilitySet("2.22.0", c.caps...), nil
			}
			scan := d.Catalog.Scan
			d.Catalog.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
				res, err := scan(ctx)
				if err != nil {
					return nil, err
				}
				out := *res
				out.Agents = slices.Clone(res.Agents)
				for i := range out.Agents {
					if modes, ok := c.modes[out.Agents[i].ID]; ok {
						out.Agents[i].SandboxModes = modes
					}
				}
				return &out, nil
			}
		})
		models, err := h.g.catalog.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range models {
			if want, ok := c.want[m.Runtime]; ok && m.Sandboxable != want {
				t.Errorf("%s: %s has Sandboxable %v, want %v", name, m.ID, m.Sandboxable, want)
			}
		}
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

func toolEnv(v string) func(string) string {
	return func(k string) string {
		if k == "MONOAGENT_API_TOOL_RUNTIMES" {
			return v
		}
		return ""
	}
}

// none is the off switch: a list with nothing in it, which is not the default one,
// however it is written, and it is not a runtime to list with others. It is the same
// switch MONOAGENT_API_IMAGE_RUNTIMES has.
func TestParseToolRuntimesNoneSwitchesToolCallingOff(t *testing.T) {
	for _, in := range []string{"none", " None ", "NONE"} {
		got, err := ParseToolRuntimes(in)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("ParseToolRuntimes(%q) = %#v, %v, want a list that is empty and not nil", in, got, err)
		}
	}
	for _, in := range []string{"none,claude", "claude, none"} {
		if _, err := ParseToolRuntimes(in); err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_TOOL_RUNTIMES") {
			t.Errorf("ParseToolRuntimes(%q) = %v, want an error that names the variable", in, err)
		}
	}
	off, err := ConfigFromEnv(toolEnv("none"))
	if err != nil || off.ToolRuntimes == nil || !off.ToolsOff() || len(off.ToolRuntimeList()) != 0 {
		t.Fatalf("ConfigFromEnv(none) = %#v, %v: tool calling is off, and the list is not the default", off.ToolRuntimes, err)
	}
	claude := ModelInfo{ID: "claude/default", Runtime: "claude", Class: ChatOnly}
	if off.ServesTools(claude) {
		t.Error("a model serves tools with tool calling switched off")
	}
	if got := off.Capabilities(claude); !slices.Equal(got, []string{"text"}) {
		t.Errorf("capabilities with tool calling off: %v", got)
	}
	e := off.toolsRefusal(claude)
	if e == nil || e.Status != 400 || e.Code != "unsupported_parameter" || e.Param != "tools" ||
		!strings.Contains(e.Message, "switched off") || !strings.Contains(e.Message, "MONOAGENT_API_TOOL_RUNTIMES") {
		t.Errorf("the refusal must say that tool calling is switched off, and by what: %+v", e)
	}
	if (Config{}).ToolsOff() || (Config{ToolRuntimes: []string{"codex"}}).ToolsOff() {
		t.Error("tool calling is on unless the list was set to nothing")
	}
	// Unset leaves the default to the methods, like every other setting.
	if got, err := ConfigFromEnv(toolEnv("")); err != nil || got.ToolRuntimes != nil {
		t.Errorf("an unset variable must leave the list nil: %v, %v", got.ToolRuntimes, err)
	}
}

// The tool list of a report comes from the same variable the gateway reads: unset is
// the default list, "none" a list with nothing in it (not nil, which would be the
// default), and a bad value an error that names the variable.
func TestEffectiveToolRuntimes(t *testing.T) {
	for _, c := range []struct {
		value string
		want  []string
	}{
		{"", []string{"claude", "codex"}},
		{"agy, codex", []string{"antigravity", "codex"}},
		{"none", []string{}},
	} {
		got, err := EffectiveToolRuntimes(envOf(map[string]string{"MONOAGENT_API_TOOL_RUNTIMES": c.value}))
		if err != nil || got == nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("EffectiveToolRuntimes(%q) = %#v, %v; want %#v", c.value, got, err, c.want)
		}
	}
	got, err := EffectiveToolRuntimes(envOf(nil))
	if err != nil || !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Errorf("EffectiveToolRuntimes with the variable unset = %#v, %v; want the default list", got, err)
	}
	if _, err := EffectiveToolRuntimes(envOf(map[string]string{"MONOAGENT_API_TOOL_RUNTIMES": "co dex"})); err == nil || !strings.Contains(err.Error(), "MONOAGENT_API_TOOL_RUNTIMES") {
		t.Errorf("a bad value must be an error that names the variable, got %v", err)
	}
}

// A report says which models call tools, as GET /v1/models does and by the same
// predicate: the model's runtime is in the list, and its own tools are gated by
// monomind (chat-only) or monomind can run it read-only.
func TestModelsReportSaysWhichModelsCallTools(t *testing.T) {
	models := func(codexReadOnly, sandbox bool) []ModelInfo {
		return []ModelInfo{
			{ID: "claude/default", Runtime: "claude", Model: "default", Class: ChatOnly, Sandboxable: sandbox},
			{ID: "codex/default", Runtime: "codex", Model: "default", Class: Sandboxed, ReadAccess: codexReadOnly, Sandboxable: sandbox},
			{ID: "hermes/default", Runtime: "hermes", Model: "default", Class: Unconfined, ReadAccess: true, Sandboxable: sandbox},
			{ID: "antigravity/default", Runtime: "antigravity", Model: "default", Class: Unconfined, Sandboxable: sandbox},
		}
	}
	for _, c := range []struct {
		name     string
		list     []string
		readOnly bool // monomind can run codex read-only
		sandbox  bool // monomind can apply the sandbox every leg requires
		want     string
	}{
		{"the default list", nil, true, true, "claude/default,codex/default"},
		{"monomind cannot run codex read-only", nil, false, true, "claude/default"},
		{"monomind cannot apply the sandbox", nil, true, false, ""},
		{"a list of one", []string{"hermes"}, true, true, "hermes/default"},
		{"a listed runtime that cannot run read-only", []string{"antigravity"}, true, true, ""},
		// A list with nothing in it is tool calling switched off, which is not the default list.
		{"switched off", []string{}, true, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := NewModelsReport(ModelsReportInput{For: "loopback", Policy: Policy{Max: Unconfined}, Source: ReportSourceShell, Models: models(c.readOnly, c.sandbox), ToolRuntimes: c.list})
			var calls []string
			for _, m := range r.Models {
				if slices.Contains(m.Capabilities, "tools") {
					calls = append(calls, m.ID)
				}
				if m.Capabilities[0] != "text" {
					t.Errorf("%s: capabilities %v: text comes first", m.ID, m.Capabilities)
				}
			}
			if got := strings.Join(calls, ","); got != c.want {
				t.Errorf("the models that call tools: %q, want %q", got, c.want)
			}
		})
	}
}

// Switched off, tool calling says so where it matters: once at start in the log, in the
// refusal of a request that declares tools, and in the capabilities of the list.
func TestToolCallingSwitchedOffIsLoggedAtStartRefusedAndNotListed(t *testing.T) {
	h := toolHarness(t, okTurn("x"), func(_ *Deps, c *Config) { c.ToolRuntimes = []string{} })
	logged := false
	for _, l := range h.logged() {
		if strings.Contains(l, "tool calling is switched off") && strings.Contains(l, "MONOAGENT_API_TOOL_RUNTIMES") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("the start must say that tool calling is switched off, and by what: %q", h.logged())
	}
	secret := h.key(t, "default", "app", false)
	for id, caps := range capabilitiesOf(t, h, anyPolicy, secret) {
		if slices.Contains(caps, "tools") {
			t.Errorf("%s says it calls tools with tool calling switched off: %v", id, caps)
		}
	}
	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	e := decodeErrorBody(t, rec)
	msg, _ := e["message"].(string)
	if rec.Code != 400 || e["code"] != "unsupported_parameter" || e["param"] != "tools" || !strings.Contains(msg, "switched off") || !strings.Contains(msg, "MONOAGENT_API_TOOL_RUNTIMES") {
		t.Errorf("%d %v: want the refusal that says tool calling is switched off", rec.Code, e)
	}
	if rec := post(h, anyPolicy, secret, toolChatBody("claude", "", weatherQuestion)); rec.Code != 200 {
		t.Errorf("plain chat with tool calling off: %d %s", rec.Code, rec.Body)
	}
}
