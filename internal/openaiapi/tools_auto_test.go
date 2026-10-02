package openaiapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// noTools is a capability list without tools: what the tests of the image
// capability are about.
func noTools(caps []string) []string {
	return slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return c == capTools })
}

func capabilitiesOf(t *testing.T, h *harness, p Policy, secret string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, m := range decodeModelList(t, h.serve(p, http.MethodGet, "/v1/models", secret, "")).Data {
		out[m.ID] = m.Monoagent.Capabilities
	}
	return out
}

// GET /v1/models says which models call tools: the ones the requests for tools
// are served by, decided by the very predicate that refuses the others.
func TestModelsListSaysWhichModelsCallTools(t *testing.T) {
	cases := []struct {
		name   string
		h      *harness
		policy Policy
		want   map[string][]string
	}{
		{"monomind that runs runtimes read-only", toolHarness(t, okTurn("x")), anyPolicy, map[string][]string{
			"claude/default":    {"text", "tools"},
			"codex/gpt-6-astra": {"text", "image", "tools"},
		}},
		{"monomind that cannot", newHarness(t, okTurn("x")), anyPolicy, map[string][]string{
			"claude/default":    {"text", "tools"}, // monomind gates claude's own tools
			"codex/gpt-6-astra": {"text", "image"},
		}},
		{"the operator's list", toolHarness(t, okTurn("x"), func(_ *Deps, c *Config) { c.ToolRuntimes = []string{"codex"} }), anyPolicy, map[string][]string{
			"claude/default":    {"text"},
			"codex/gpt-6-astra": {"text", "image", "tools"},
		}},
	}
	for _, c := range cases {
		secret := c.h.key(t, "default", "app", false)
		got := capabilitiesOf(t, c.h, c.policy, secret)
		for id, want := range c.want {
			if !slices.Equal(got[id], want) {
				t.Errorf("%s: %s has capabilities %v, want %v", c.name, id, got[id], want)
			}
		}
		if got["antigravity/default"] == nil || slices.Contains(got["antigravity/default"], "tools") {
			t.Errorf("%s: antigravity does not call tools: %v", c.name, got["antigravity/default"])
		}
	}

	// The retrieval of one model says the same.
	h := toolHarness(t, okTurn("x"))
	var one modelObject
	decodeInto(t, h.serve(anyPolicy, http.MethodGet, "/v1/models/codex/gpt-6-astra", h.key(t, "default", "app", false), ""), &one)
	if !slices.Equal(one.Monoagent.Capabilities, []string{"text", "image", "tools"}) {
		t.Errorf("GET /v1/models/codex/gpt-6-astra: %v", one.Monoagent.Capabilities)
	}
}

func TestAutoSaysItCallsToolsWhenAModelItMayPickDoes(t *testing.T) {
	f := &fakeAuto{id: "claude/default", p: 1}
	h := autoGateway(t, f, withReadAccess)
	secret := h.key(t, "default", "app", false)
	auto := func(p Policy) []string {
		list := decodeModelList(t, h.serve(p, http.MethodGet, "/v1/models", secret, ""))
		return list.Data[len(list.Data)-1].Monoagent.Capabilities
	}
	// Auto picks among chat-only models until the operator grants it more: claude.
	if got := auto(anyPolicy); !slices.Equal(got, []string{"text", "tools"}) {
		t.Errorf("auto among chat-only models: %v", got)
	}
	if got := auto(autoAnyPolicy); !slices.Equal(got, []string{"text", "image", "tools"}) {
		t.Errorf("auto among every model: %v", got)
	}
	// With no model it may pick that calls tools, it does not say so.
	h2 := autoGateway(t, f, withReadAccess, func(_ *Deps, c *Config) { c.ToolRuntimes = []string{"hermes"} })
	list := decodeModelList(t, h2.serve(autoAnyPolicy, http.MethodGet, "/v1/models", h2.key(t, "default", "app", false), ""))
	if got := list.Data[len(list.Data)-1].Monoagent.Capabilities; slices.Contains(got, "tools") {
		t.Errorf("auto with no model that calls tools: %v", got)
	}
}

// A request for tools sent to auto is for the models that can serve them: Jev is
// only offered those, and what it picks runs as a leg.
func TestAutoWithToolsPicksAmongTheModelsThatCallTools(t *testing.T) {
	f := &fakeAuto{id: "codex/gpt-6-astra", p: 0.9}
	script := &execScript{turns: []execFunc{callsWeather(false, "thread-1", "")}}
	h := autoGateway(t, f, withReadAccess, func(d *Deps, _ *Config) { d.Exec = script.exec })
	shortGrace(t)
	secret := h.key(t, "default", "app", false)

	rec := post(h, autoAnyPolicy, secret, toolChatBody("auto", weatherTools, weatherQuestion))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := decodeToolReply(t, rec); got.Model != "codex/gpt-6-astra" || len(got.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("answer: %+v", got)
	}
	if rec.Header().Get("X-Monoagent-Model") != "codex/gpt-6-astra" || rec.Header().Get("X-Monoagent-Auto") != "jev" {
		t.Errorf("headers: model %q auto %q", rec.Header().Get("X-Monoagent-Model"), rec.Header().Get("X-Monoagent-Auto"))
	}
	offered := make([]string, 0, len(f.options))
	for id := range f.options {
		offered = append(offered, id)
	}
	slices.Sort(offered)
	if want := []string{"claude/default", "claude/opus[1m]", "codex/default", "codex/gpt-6-astra"}; !slices.Equal(offered, want) {
		t.Errorf("Jev was offered %v, want the models that call tools: %v", offered, want)
	}
	opts := script.calls()[0]
	if opts.Runtime != "codex" || opts.Access != monomind.AccessRead || len(opts.Tools) != 1 || !opts.RequireSandbox {
		t.Errorf("the turn: runtime %q access %q tools %d require sandbox %v", opts.Runtime, opts.Access, len(opts.Tools), opts.RequireSandbox)
	}
	if line := logLineOf(h, rec); !strings.Contains(line, "auto=jev") || !strings.Contains(line, "tools=1") || !strings.Contains(line, "leg=first") {
		t.Errorf("log line: %q", line)
	}
}

// A request without tools is not held to them: auto still picks among every model.
func TestAutoWithoutToolsIsNotHeldToTools(t *testing.T) {
	f := &fakeAuto{id: "antigravity/default", p: 0.9}
	h := autoGateway(t, f, withReadAccess)
	rec := post(h, autoAnyPolicy, h.key(t, "default", "app", false), toolChatBody("auto", "", weatherQuestion))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Monoagent-Model") != "antigravity/default" {
		t.Errorf("%d model %q: %s", rec.Code, rec.Header().Get("X-Monoagent-Model"), rec.Body)
	}
}

func TestAutoWithToolsHasNoCandidatesWhenNoModelCallsThem(t *testing.T) {
	var spawned int
	f := &fakeAuto{id: "claude/default", p: 1}
	h := autoGateway(t, f, withReadAccess, func(d *Deps, c *Config) {
		c.ToolRuntimes = []string{"codex"} // codex runs as sandboxed: auto may not pick it unless the operator says so
		d.Exec = func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			spawned++
			return okTurn("x")(ctx, o, onEvent)
		}
	})
	secret := h.key(t, "default", "app", false)

	// Auto picks among chat-only models, and none of them calls tools here.
	rec := post(h, anyPolicy, secret, toolChatBody("auto", weatherTools, weatherQuestion))
	e := decodeErrorBody(t, rec)
	msg, _ := e["message"].(string)
	if rec.Code != http.StatusNotFound || e["code"] != "model_not_found" || e["param"] != "model" ||
		!strings.Contains(msg, "tool") || !strings.Contains(msg, "--auto-confinement") {
		t.Fatalf("%d %v: want a 404 model_not_found that says tools and what to raise", rec.Code, e)
	}
	if spawned != 0 || f.asked != 0 {
		t.Errorf("%d turns ran and Jev was asked %d times for a request without candidates", spawned, f.asked)
	}

	// With nothing anywhere that calls tools the message says so, not what to raise.
	h2 := autoGateway(t, f, withReadAccess, func(_ *Deps, c *Config) { c.ToolRuntimes = []string{"hermes"} })
	rec = post(h2, autoAnyPolicy, h2.key(t, "default", "app", false), toolChatBody("auto", weatherTools, weatherQuestion))
	msg, _ = decodeErrorBody(t, rec)["message"].(string)
	if rec.Code != http.StatusNotFound || !strings.Contains(msg, "hermes") || strings.Contains(msg, "--auto-confinement") {
		t.Errorf("%d %s", rec.Code, msg)
	}
}

// With tool calling switched off there is no model auto could pick for tools, and the
// 404 says that and by what, not what to raise.
func TestAutoWithToolsSaysWhenToolCallingIsSwitchedOff(t *testing.T) {
	f := &fakeAuto{id: "claude/default", p: 1}
	h := autoGateway(t, f, withReadAccess, func(_ *Deps, c *Config) { c.ToolRuntimes = []string{} })
	rec := post(h, autoAnyPolicy, h.key(t, "default", "app", false), toolChatBody("auto", weatherTools, weatherQuestion))
	msg, _ := decodeErrorBody(t, rec)["message"].(string)
	if rec.Code != http.StatusNotFound || !strings.Contains(msg, "switched off") || !strings.Contains(msg, "MONOAGENT_API_TOOL_RUNTIMES") || strings.Contains(msg, "--auto-confinement") {
		t.Errorf("%d %s", rec.Code, msg)
	}
	if f.asked != 0 {
		t.Errorf("Jev was asked %d times for a request without candidates", f.asked)
	}
}
