package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A key created with --context puts excerpts of the profile's knowledge, captured web
// pages nobody vetted among them, into the system prompt. With tools declared, an
// instruction in one of them could steer which calls the model proposes, and the client
// runs them with its own authority. So such a key is refused tools unless the operator
// raised --context-confinement above chat-only, and the refusal comes before anything
// starts.

// markerFn is a function whose name is nothing a refusal may repeat.
const (
	markerName  = "MarkerFnName"
	markerTools = `"tools":[{"type":"function","function":{"name":"` + markerName + `","description":"d","parameters":{"type":"object","properties":{}}}}]`
)

var raisedContext = Policy{Max: Unconfined, ContextMax: Sandboxed}

// contextCounts is a gateway that counts what a refused request must not cost: a turn,
// a knowledge search and a Jev question.
type contextCounts struct {
	turns, searches atomic.Int32
	auto            *fakeAuto
}

func contextGateway(t *testing.T, exec execFunc, mutate ...func(*Deps, *Config)) (*harness, *contextCounts) {
	t.Helper()
	c := &contextCounts{auto: &fakeAuto{id: "claude/default", p: 1}}
	counted := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		c.turns.Add(1)
		return exec(ctx, o, onEvent)
	}
	h := toolHarness(t, counted, append([]func(*Deps, *Config){func(d *Deps, _ *Config) {
		d.Auto = c.auto.funcs()
		d.Knowledge = func(context.Context, string, string) ([]monomind.KnowledgeResult, error) {
			c.searches.Add(1)
			return []monomind.KnowledgeResult{{Path: "/a/b.md", Excerpt: "e", Score: 1}}, nil
		}
	}}, mutate...)...)
	return h, c
}

func TestToolsAreRefusedForAContextKeyUnlessTheOperatorRaisedItsConfinement(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy Policy
		model  string
		refuse bool
	}{
		{"the default policy", autoAnyPolicy, "claude", true},
		{"the default policy, on auto", autoAnyPolicy, "auto", true},
		{"the context maximum set to chat-only", Policy{Max: Unconfined, ContextMax: ChatOnly, AutoMax: Unconfined}, "claude", true},
		{"a listener that is chat-only, whatever the context maximum", Policy{Max: ChatOnly, ContextMax: Unconfined, AutoMax: Unconfined}, "claude", true},
		{"the context maximum raised to sandboxed", Policy{Max: Unconfined, ContextMax: Sandboxed, AutoMax: Unconfined}, "claude", false},
		{"the context maximum raised, on auto", Policy{Max: Unconfined, ContextMax: Sandboxed, AutoMax: Unconfined}, "auto", false},
		{"the context maximum raised to any", Policy{Max: Unconfined, ContextMax: Unconfined, AutoMax: Unconfined}, "claude", false},
	} {
		h, counts := contextGateway(t, callsWeather(true, "sess-1", ""))
		secret := h.key(t, "default", "ctx", true)
		body := toolChatBody(c.model, markerTools, weatherQuestion)
		if !c.refuse {
			body = toolChatBody(c.model, weatherTools, weatherQuestion)
		}

		rec := post(h, c.policy, secret, body)
		if !c.refuse {
			if rec.Code != http.StatusOK || len(decodeToolReply(t, rec).Choices[0].Message.ToolCalls) != 1 {
				t.Errorf("%s: the tools of a context key were refused: %d %s", c.name, rec.Code, rec.Body)
			}
			continue
		}
		e := decodeErrorBody(t, rec)
		if rec.Code != http.StatusForbidden || e["code"] != "policy_denied" || !strings.Contains(rec.Body.String(), "--context-confinement") {
			t.Errorf("%s: status %d, body %s, want 403 policy_denied that names --context-confinement", c.name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), markerName) {
			t.Errorf("%s: the refusal repeats the name of a function: %s", c.name, rec.Body)
		}
		if counts.turns.Load() != 0 || counts.searches.Load() != 0 || counts.auto.calls.Load() != 0 {
			t.Errorf("%s: a refused request started %d turns, %d knowledge searches and %d Jev questions", c.name, counts.turns.Load(), counts.searches.Load(), counts.auto.calls.Load())
		}
		if rec.Header().Get("X-Monoagent-Context") != "" {
			t.Errorf("%s: no knowledge was searched, and the response says %q", c.name, rec.Header().Get("X-Monoagent-Context"))
		}
	}
}

// What is not passed to the model is not refused: no tools, tool_choice none, a conversation
// with tool history and no tools, and a key without --context whatever the policy.
func TestAContextKeyMayStillChatAndAPlainKeyMayCallTools(t *testing.T) {
	h, _ := contextGateway(t, scriptedExec(evStart(false, "monomind"), evText("It is 21 C."), evUsage(5, 6), evResult("It is 21 C.", monomind.StopEndTurn), evDone(0)))
	ctxKey := h.key(t, "default", "ctx", true)
	history := strings.Replace(followUp("call_a", "21 C"), weatherTools+",", "", 1) // earlier rounds, and nothing to call
	for name, body := range map[string]string{
		"no tools":               toolChatBody("claude", "", weatherQuestion),
		"tool_choice none":       toolChatBody("claude", weatherTools+`,"tool_choice":"none"`, weatherQuestion),
		"tool history, no tools": history,
	} {
		if rec := post(h, autoAnyPolicy, ctxKey, body); rec.Code != http.StatusOK {
			t.Errorf("%s: a context key was refused: %d %s", name, rec.Code, rec.Body)
		}
	}

	for _, p := range []Policy{autoAnyPolicy, {Max: ChatOnly}} {
		h := toolHarness(t, callsWeather(true, "sess-1", ""))
		rec := post(h, p, h.key(t, "default", "plain", false), toolChatBody("claude", weatherTools, weatherQuestion))
		if rec.Code != http.StatusOK || len(decodeToolReply(t, rec).Choices[0].Message.ToolCalls) != 1 {
			t.Errorf("a key without --context on %v: %d %s", p, rec.Code, rec.Body)
		}
	}
}

// The refusal comes before the slot: a server that is busy answers the policy, not "busy".
func TestTheRefusalOfToolsForAContextKeyTakesNoSlot(t *testing.T) {
	hold, started := make(chan struct{}), make(chan struct{}, 1)
	h, counts := contextGateway(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		started <- struct{}{}
		<-hold
		return okTurn("late")(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	plain := h.key(t, "default", "app", false)
	ctxKey := h.key(t, "default", "ctx", true)

	done := make(chan *httpRecorder, 1)
	go func() { done <- post(h, autoAnyPolicy, plain, toolChatBody("claude", "", weatherQuestion)) }()
	<-started

	rec := post(h, autoAnyPolicy, ctxKey, toolChatBody("claude", weatherTools, weatherQuestion))
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Errorf("on a busy server the policy answers: status %d: %s", rec.Code, rec.Body)
	}
	close(hold)
	if first := <-done; first.Code != http.StatusOK {
		t.Errorf("the turn that held the slot: %d", first.Code)
	}
	if counts.turns.Load() != 1 || counts.searches.Load() != 0 {
		t.Errorf("%d turns and %d knowledge searches: only the one that held the slot may have run", counts.turns.Load(), counts.searches.Load())
	}
}

// The lists say what a key may do: a key that a request with tools would be refused for is
// not offered the capability, by a model or by auto, and one that would be served is.
func TestModelListsOfferToolsOnlyToAKeyThatMayUseThem(t *testing.T) {
	f := &fakeAuto{id: "claude/default", p: 1}
	h := autoGateway(t, f, withReadAccess)
	plain, ctxKey := h.key(t, "default", "plain", false), h.key(t, "default", "ctx", true)
	raised := Policy{Max: Unconfined, ContextMax: Sandboxed, AutoMax: Unconfined}

	for _, c := range []struct {
		name   string
		policy Policy
		secret string
		tools  bool
	}{
		{"a key without --context", autoAnyPolicy, plain, true},
		{"a context key under the default policy", autoAnyPolicy, ctxKey, false},
		{"a context key under a raised --context-confinement", raised, ctxKey, true},
	} {
		have := map[string]bool{}
		listed := decodeModelList(t, h.serve(c.policy, http.MethodGet, "/v1/models", c.secret, "")).Data
		for _, m := range listed {
			have[m.ID] = slices.Contains(m.Monoagent.Capabilities, "tools")
			if have[m.ID] && !c.tools {
				t.Errorf("%s: %s offers tools to a key that would be refused them", c.name, m.ID)
			}
		}
		if len(listed) < 2 || have["claude/default"] != c.tools {
			t.Errorf("%s: %d models listed, claude/default has tools = %v, want %v", c.name, len(listed), have["claude/default"], c.tools)
		}
		if _, hasAuto := have["auto"]; !hasAuto || have["auto"] != c.tools {
			t.Errorf("%s: auto has tools = %v (listed %v), want %v", c.name, have["auto"], hasAuto, c.tools)
		}
		for _, id := range []string{"claude/default", "auto"} {
			var one modelObject
			rec := h.serve(c.policy, http.MethodGet, "/v1/models/"+id, c.secret, "")
			if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("%s: GET /v1/models/%s: %d %s", c.name, id, rec.Code, rec.Body)
			}
			if slices.Contains(one.Monoagent.Capabilities, "tools") != c.tools || !slices.Contains(one.Monoagent.Capabilities, "text") {
				t.Errorf("%s: %s says %v, want tools = %v and text", c.name, id, one.Monoagent.Capabilities, c.tools)
			}
		}
	}
}
