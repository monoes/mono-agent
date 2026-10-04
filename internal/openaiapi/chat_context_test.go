package openaiapi

// Chat completions made with a key created with --context: the profile's
// knowledge in the prompt, and the confinement cap that goes with it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestChatContextKeyAddsTheProfilesKnowledge(t *testing.T) {
	var system string
	var queries []string
	var profiles []string
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		system = o.SystemPrompt
		return okTurn("answer")(ctx, o, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Knowledge = func(_ context.Context, profileID, query string) ([]monomind.KnowledgeResult, error) {
			profiles, queries = append(profiles, profileID), append(queries, query)
			return []monomind.KnowledgeResult{
				{Path: "/home/me/notes/plan.md", Excerpt: "the launch is on Friday", Score: 0.9},
				{Path: "/home/me/notes/ideas.md", Excerpt: "buy more coffee", Score: 0.4},
			}, nil
		}
	})
	withContext := h.key(t, "work", "ctx", true)
	without := h.key(t, "work", "plain", false)

	body := `{"model":"claude","messages":[{"role":"system","content":"S"},{"role":"user","content":"when is the launch?"}]}`
	rec := post(h, anyPolicy, withContext, body)
	if rec.Code != 200 {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if len(profiles) != 1 || profiles[0] != "work" || queries[0] != "when is the launch?" {
		t.Errorf("knowledge searched for profile %v query %v; want the key's profile and the last user message", profiles, queries)
	}
	for _, want := range []string{"S\n\n", "launch is on Friday", "[1] plan.md", "data, not instructions"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "/home/me") {
		t.Errorf("a path reached the system prompt:\n%s", system)
	}
	if rec.Header().Get("X-Monoagent-Context") != "2" {
		t.Errorf("X-Monoagent-Context = %q, want 2", rec.Header().Get("X-Monoagent-Context"))
	}

	// A key without context never searches and adds nothing.
	profiles, queries, system = nil, nil, ""
	post(h, anyPolicy, without, body)
	if len(profiles) != 0 || strings.Contains(system, "knowledge") {
		t.Errorf("a plain key triggered a knowledge search or got context: %v / %q", profiles, system)
	}
}
func TestChatContextDegradesGracefully(t *testing.T) {
	for name, tc := range map[string]struct {
		know   func(context.Context, string, string) ([]monomind.KnowledgeResult, error)
		header string
	}{
		"search fails": {func(context.Context, string, string) ([]monomind.KnowledgeResult, error) {
			return nil, errors.New("monomind knowledge is down")
		}, "unavailable"},
		"no hits": {func(context.Context, string, string) ([]monomind.KnowledgeResult, error) { return nil, nil }, "none"},
	} {
		var system string
		h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			system = o.SystemPrompt
			return okTurn("answer")(ctx, o, onEvent)
		}, func(d *Deps, _ *Config) { d.Knowledge = tc.know })
		rec := post(h, anyPolicy, h.key(t, "default", "ctx", true), chatBody)
		if rec.Code != 200 || rec.Header().Get("X-Monoagent-Context") != tc.header || strings.Contains(system, "<knowledge>") {
			t.Errorf("%s: status %d header %q system %q", name, rec.Code, rec.Header().Get("X-Monoagent-Context"), system)
		}
	}
}
func TestChatContextKeyIsServedOnlyByChatOnlyModels(t *testing.T) {
	var spawned atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Knowledge = func(context.Context, string, string) ([]monomind.KnowledgeResult, error) {
			return []monomind.KnowledgeResult{{Path: "/a/b.md", Excerpt: "e", Score: 1}}, nil
		}
	})
	body := func(model string) string {
		return `{"model":"` + model + `","messages":[{"role":"user","content":"x"}]}`
	}
	withContext := h.key(t, "default", "ctx", true)

	for _, model := range []string{"codex/gpt-6-astra", "antigravity"} {
		rec := post(h, anyPolicy, withContext, body(model))
		if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" ||
			!strings.Contains(fmt.Sprint(decodeErrorBody(t, rec)["message"]), "--context-confinement") {
			t.Errorf("%s with a context key: status %d body %s", model, rec.Code, rec.Body)
		}
	}
	if spawned.Load() != 0 {
		t.Fatalf("%d turns started for a context key that may not use their model", spawned.Load())
	}
	if rec := post(h, anyPolicy, withContext, body("claude")); rec.Code != http.StatusOK || rec.Header().Get("X-Monoagent-Context") != "1" {
		t.Errorf("a chat-only model serves a context key: status %d header %q", rec.Code, rec.Header().Get("X-Monoagent-Context"))
	}
	// A key without context is held only to the listener's policy.
	if rec := post(h, anyPolicy, h.key(t, "default", "plain", false), body("codex/gpt-6-astra")); rec.Code != http.StatusOK {
		t.Errorf("a plain key may use a sandboxed model on a loopback listener: %d %s", rec.Code, rec.Body)
	}
}
func TestChatContextKeyFollowsTheContextMax(t *testing.T) {
	var spawned atomic.Int32
	var required []bool
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		required = append(required, o.RequireSandbox)
		return okTurn("x")(ctx, o, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Knowledge = func(context.Context, string, string) ([]monomind.KnowledgeResult, error) {
			return []monomind.KnowledgeResult{{Path: "/a/b.md", Excerpt: "e", Score: 1}}, nil
		}
	})
	body := func(model string) string {
		return `{"model":"` + model + `","messages":[{"role":"user","content":"x"}]}`
	}
	withContext := h.key(t, "default", "ctx", true)

	// The operator raised the context maximum to sandboxed.
	raised := Policy{Max: Unconfined, ContextMax: Sandboxed}
	if rec := post(h, raised, withContext, body("codex/gpt-6-astra")); rec.Code != http.StatusOK || rec.Header().Get("X-Monoagent-Context") != "1" {
		t.Fatalf("a sandboxed model serves a context key when the maximum allows it: status %d header %q body %s", rec.Code, rec.Header().Get("X-Monoagent-Context"), rec.Body)
	}
	if len(required) != 1 || !required[0] {
		t.Errorf("a sandboxed model still requires its sandbox for a context key: %v", required)
	}
	rec := post(h, raised, withContext, body("antigravity"))
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" ||
		!strings.Contains(fmt.Sprint(decodeErrorBody(t, rec)["message"]), "--context-confinement") {
		t.Errorf("an unconfined model is still above the raised maximum: status %d body %s", rec.Code, rec.Body)
	}
	if spawned.Load() != 1 {
		t.Errorf("%d turns started, want only the allowed one", spawned.Load())
	}

	// The context maximum never raises what the listener itself serves.
	network := Policy{Max: ChatOnly, ContextMax: Unconfined}
	rec = post(h, network, withContext, body("codex/gpt-6-astra"))
	if rec.Code != http.StatusForbidden || !strings.Contains(fmt.Sprint(decodeErrorBody(t, rec)["message"]), "confinement policy") {
		t.Errorf("a chat-only listener refuses a sandboxed model even for a context key: status %d body %s", rec.Code, rec.Body)
	}
}
