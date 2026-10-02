package openaiapi

import (
	"net/http"
	"strings"
	"testing"
)

// What the operator has to raise is said in the refusal: --confinement, and
// --context-confinement for a key created with --context.
func TestImagePolicyRefusalsSayWhatToRaise(t *testing.T) {
	h := newHarness(t, imageTurn("x", onePNG("a.png")))
	plain, ctxKey := h.key(t, "default", "app", false), h.key(t, "default", "notes", true)
	message := func(p Policy, secret, body string) string {
		rec := postImages(h, p, secret, body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
		}
		msg, _ := decodeErrorBody(t, rec)["message"].(string)
		return msg
	}
	for name, c := range map[string]struct {
		msg      string
		mention  []string
		mentions bool // whether --context-confinement is mentioned
	}{
		"no model, a chat-only server": {message(Policy{Max: ChatOnly}, plain, `{"prompt":"x"}`), []string{"--confinement", "sandboxed"}, false},
		// The policy the message names is the key's: a context key is held to chat-only on a server that serves everything.
		"no model, a context key":                     {message(anyPolicy, ctxKey, `{"prompt":"x"}`), []string{"--context-confinement", "sandboxed", "policy (chat-only)"}, true},
		"no model, a context key, a chat-only server": {message(Policy{Max: ChatOnly}, ctxKey, `{"prompt":"x"}`), []string{"--confinement", "--context-confinement"}, true},
		"codex, a chat-only server":                   {message(Policy{Max: ChatOnly}, plain, `{"model":"codex","prompt":"x"}`), []string{"--confinement"}, false},
		"codex, a context key":                        {message(anyPolicy, ctxKey, `{"model":"codex","prompt":"x"}`), []string{"--context-confinement"}, true},
	} {
		for _, want := range c.mention {
			if !strings.Contains(c.msg, want) {
				t.Errorf("%s: %q must say %q", name, c.msg, want)
			}
		}
		if got := strings.Contains(c.msg, "--context-confinement"); got != c.mentions {
			t.Errorf("%s: mentions --context-confinement = %v, want %v: %q", name, got, c.mentions, c.msg)
		}
		if strings.Contains(c.msg, "excerpts of the profile's knowledge") && !strings.Contains(name, "context") {
			t.Errorf("%s: a key without --context has no excerpts: %q", name, c.msg)
		}
	}
}

// What the operator is told to raise is the class that would help: with antigravity
// alone in the image list, which runs unconfined, `sandboxed` would not.
func TestImagePolicyRefusalNamesTheClassThatWouldHelp(t *testing.T) {
	h := newHarness(t, imageTurn("x", onePNG("a.png")), func(_ *Deps, c *Config) { c.ImageRuntimes = []string{"antigravity"} })
	plain, ctxKey := h.key(t, "default", "app", false), h.key(t, "default", "notes", true)
	message := func(p Policy, secret string) string {
		rec := postImages(h, p, secret, `{"prompt":"x"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
		}
		msg, _ := decodeErrorBody(t, rec)["message"].(string)
		return msg
	}
	for name, c := range map[string]struct {
		msg  string
		want string
		not  []string
	}{
		"a sandboxed server":                  {message(Policy{Max: Sandboxed}, plain), "--confinement any", []string{"sandboxed (or any)", "--context-confinement"}},
		"a chat-only server":                  {message(Policy{Max: ChatOnly}, plain), "--confinement any", []string{"sandboxed (or any)"}},
		"a context key raised to sandboxed":   {message(Policy{Max: Unconfined, ContextMax: Sandboxed}, ctxKey), "--context-confinement any", []string{"sandboxed (or any)", "--confinement any"}},
		"a context key on a sandboxed server": {message(Policy{Max: Sandboxed, ContextMax: Sandboxed}, ctxKey), "--confinement any", []string{"sandboxed (or any)"}},
		// The cap on a context key is raised already: only the listener's limit is in the way.
		"a context key whose cap is raised, on a chat-only server": {message(Policy{Max: ChatOnly, ContextMax: Unconfined}, ctxKey), "--confinement any", []string{"--context-confinement"}},
	} {
		if !strings.Contains(c.msg, c.want) {
			t.Errorf("%s: %q must say %q", name, c.msg, c.want)
		}
		for _, not := range c.not {
			if strings.Contains(c.msg, not) {
				t.Errorf("%s: %q must not say %q", name, c.msg, not)
			}
		}
		if strings.HasSuffix(strings.TrimSpace(c.msg), "with") || strings.Contains(c.msg, "with ,") {
			t.Errorf("%s: %q does not say what to raise", name, c.msg)
		}
	}
	// With codex in the list sandboxed is enough, whatever the order: it is the weakest of the installed ones.
	for _, list := range [][]string{{"codex", "antigravity"}, {"antigravity", "codex"}} {
		h2 := newHarness(t, imageTurn("x", onePNG("a.png")), func(_ *Deps, c *Config) { c.ImageRuntimes = list })
		rec := postImages(h2, Policy{Max: ChatOnly}, h2.key(t, "default", "app", false), `{"prompt":"x"}`)
		if msg, _ := decodeErrorBody(t, rec)["message"].(string); !strings.Contains(msg, "--confinement sandboxed (or any)") {
			t.Errorf("%v: the weakest installed image runtime is sandboxed: %q", list, msg)
		}
	}
}

// A key created with --context is held to --context-confinement on this route too;
// raised, it may make images with what that allows and no more.
func TestImagesForAContextKeyFollowTheContextCap(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("x", onePNG("a.png")))
	ctxKey := h.key(t, "default", "notes", true)
	raised := Policy{Max: Unconfined, ContextMax: Sandboxed}

	for _, c := range []struct {
		body   string
		status int
		model  string
	}{
		{`{"prompt":"x"}`, 200, "codex/default"},
		{`{"model":"codex","prompt":"x"}`, 200, "codex/default"},
		{`{"model":"antigravity","prompt":"x"}`, 403, ""},
	} {
		rec := postImages(h, raised, ctxKey, c.body)
		if rec.Code != c.status || rec.Header().Get("X-Monoagent-Model") != c.model {
			t.Errorf("%s: status %d model %q, want %d %q: %s", c.body, rec.Code, rec.Header().Get("X-Monoagent-Model"), c.status, c.model, rec.Body)
		}
	}
	// The excerpts of the knowledge are for chat: an image prompt is the client's own.
	for _, o := range log.opts {
		if strings.Contains(o.SystemPrompt, "<knowledge>") {
			t.Errorf("an image turn is not given knowledge: %q", o.SystemPrompt)
		}
		wantImageSystemPrompt(t, o)
	}
	if rec := postImages(h, raised, ctxKey, `{"model":"codex","prompt":"x"}`); rec.Header().Get("X-Monoagent-Context") != "" {
		t.Errorf("an image request adds no knowledge, so it reports none: %q", rec.Header().Get("X-Monoagent-Context"))
	}
}

// A model that exists but cannot make images is not a 404 (it exists) and not a
// 403 (no policy would make it work): it is a bad value for model.
func TestImagesAChatModelIsABadValueAndNamesTheImageRuntimes(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("x", onePNG("a.png")), func(_ *Deps, c *Config) { c.ImageRuntimes = []string{"claude", "codex"} })
	secret := h.key(t, "default", "app", false)

	for name, c := range map[string]struct{ model, id, why string }{
		"a chat-only runtime the operator listed": {"claude", "claude/default", "chat-only"},
		"a runtime that is not listed":            {"antigravity", "antigravity/default", "not one of the image runtimes"},
	} {
		rec := postImages(h, anyPolicy, secret, `{"model":"`+c.model+`","prompt":"x"}`)
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusBadRequest || e["code"] != "invalid_value" || e["param"] != "model" {
			t.Fatalf("%s: %d %v", name, rec.Code, e)
		}
		// It names the model, says why, and names the runtimes that can: the listed chat-only one is not among them.
		if !strings.Contains(msg, c.id) || !strings.Contains(msg, c.why) || !strings.Contains(msg, "Models of codex can") {
			t.Errorf("%s: %q must name the model (%s), say why (%s) and name the runtimes that make images", name, msg, c.id, c.why)
		}
	}
	if log.count() != 0 {
		t.Errorf("%d turns ran for a model that cannot make images", log.count())
	}
}

// Without a model it is the first installed image runtime the policy allows, in the
// order of the image list.
func TestImagesWithoutAModelTakeTheFirstInstalledRuntimeThePolicyAllows(t *testing.T) {
	for _, c := range []struct {
		name   string
		list   []string
		policy Policy
		want   string
	}{
		{"the default list", nil, anyPolicy, "codex/default"},
		{"the default list, antigravity not allowed anyway", nil, Policy{Max: Sandboxed}, "codex/default"},
		{"antigravity first", []string{"antigravity", "codex"}, anyPolicy, "antigravity/default"},
		{"antigravity first but above the policy", []string{"antigravity", "codex"}, Policy{Max: Sandboxed}, "codex/default"},
		{"a runtime that is not installed is passed over", []string{"grok", "codex"}, anyPolicy, "codex/default"},
		{"a chat-only runtime in the list is passed over", []string{"claude", "codex"}, anyPolicy, "codex/default"},
		{"antigravity alone", []string{"antigravity"}, anyPolicy, "antigravity/default"},
	} {
		log := &execLog{}
		h := newHarness(t, log.imageExec("x", onePNG("a.png")), func(_ *Deps, cfg *Config) { cfg.ImageRuntimes = c.list })
		rec := postImages(h, c.policy, h.key(t, "default", "app", false), `{"prompt":"x"}`)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Monoagent-Model") != c.want {
			t.Errorf("%s: status %d model %q, want %q: %s", c.name, rec.Code, rec.Header().Get("X-Monoagent-Model"), c.want, rec.Body)
		}
	}

	// None installed: not a policy matter, so not a 403.
	h := newHarness(t, imageTurn("x", onePNG("a.png")), func(_ *Deps, cfg *Config) { cfg.ImageRuntimes = []string{"grok"} })
	rec := postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x"}`)
	e := decodeErrorBody(t, rec)
	if msg, _ := e["message"].(string); rec.Code != http.StatusNotFound || e["code"] != "model_not_found" || !strings.Contains(msg, "grok") {
		t.Errorf("no image runtime installed: %d %v", rec.Code, e)
	}
}
