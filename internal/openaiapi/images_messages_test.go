package openaiapi

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// MONOAGENT_API_IMAGE_RUNTIMES=none switches image generation off, and every way of
// asking says so, in words that are about the switch and not about runtimes that are
// not installed.
func TestImagesSwitchedOffSaysSo(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("a.png", onePNG("a.png")), func(_ *Deps, c *Config) { c.ImageRuntimes = []string{} })
	secret := h.key(t, "default", "app", false)

	for name, c := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"no model":              {`{"prompt":"x"}`, 404, "model_not_found"},
		"auto":                  {`{"model":"auto","prompt":"x"}`, 404, "model_not_found"},
		"a model of the list":   {`{"model":"codex","prompt":"x"}`, 400, "invalid_value"},
		"a model of a chat one": {`{"model":"claude","prompt":"x"}`, 400, "invalid_value"},
	} {
		rec := postImages(h, anyPolicy, secret, c.body)
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != c.status || e["code"] != c.code || e["param"] != "model" {
			t.Errorf("%s: %d %v, want %d %s about model", name, rec.Code, e, c.status, c.code)
		}
		if !strings.Contains(msg, "switched off") || !strings.Contains(msg, "MONOAGENT_API_IMAGE_RUNTIMES") {
			t.Errorf("%s: %q must say that image generation is switched off, and by what", name, msg)
		}
		for _, wrong := range []string{"is installed", "(none)", "Models of", "image runtimes ("} {
			if strings.Contains(msg, wrong) {
				t.Errorf("%s: %q must not say %q: there is no list to look in", name, msg, wrong)
			}
		}
	}
	if log.count() != 0 {
		t.Errorf("%d turns ran with image generation switched off", log.count())
	}
}

// A listed runtime that cannot make images is told apart from one that is not installed:
// the 404 says which is which, for each runtime of the list.
func TestImagesNoImageRuntimeSaysWhyEachOneIsOut(t *testing.T) {
	for name, c := range map[string]struct {
		list []string
		in   []string
		out  []string
	}{
		"not installed, and chat-only": {[]string{"grok", "claude"}, []string{"grok is not installed", "claude runs as chat-only"}, []string{"(grok, claude) is installed", "claude is not installed"}},
		"a typo":                       {[]string{"codx"}, []string{"codx is not installed"}, []string{"switched off"}},
		"chat-only alone":              {[]string{"claude"}, []string{"claude runs as chat-only"}, []string{"is not installed", "switched off"}},
	} {
		h := newHarness(t, okTurn("x"), func(_ *Deps, cfg *Config) { cfg.ImageRuntimes = c.list })
		rec := postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x"}`)
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusNotFound || e["code"] != "model_not_found" || e["param"] != "model" {
			t.Errorf("%s: %d %v", name, rec.Code, e)
		}
		for _, s := range c.in {
			if !strings.Contains(msg, s) {
				t.Errorf("%s: %q must say %q", name, msg, s)
			}
		}
		for _, s := range c.out {
			if strings.Contains(msg, s) {
				t.Errorf("%s: %q must not say %q", name, msg, s)
			}
		}
	}
}

// The hint of the 400 for a model that cannot make images points to what the key may use,
// the way GET /v1/models lists it: a model the key's policy keeps from it is not one to
// try, and when there is none the message says none can.
func TestImagesTheHintOfABadModelNamesWhatTheKeyMayUse(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	plain, ctxKey := h.key(t, "default", "app", false), h.key(t, "default", "notes", true)
	for name, c := range map[string]struct {
		policy Policy
		secret string
		in     []string
		out    []string
	}{
		"everything is served":    {anyPolicy, plain, []string{"Models of codex, antigravity can"}, []string{"None of"}},
		"a sandboxed listener":    {Policy{Max: Sandboxed}, plain, []string{"Models of codex can"}, []string{"antigravity", "None of"}},
		"a chat-only listener":    {Policy{Max: ChatOnly}, plain, []string{"None of the models this key may use can generate images"}, []string{"Models of", "codex", "antigravity"}},
		"a context key, raised":   {Policy{Max: Unconfined, ContextMax: Sandboxed}, ctxKey, []string{"Models of codex can"}, []string{"antigravity", "None of"}},
		"a context key, as it is": {anyPolicy, ctxKey, []string{"None of the models this key may use can generate images"}, []string{"Models of", "codex"}},
	} {
		rec := postImages(h, c.policy, c.secret, `{"model":"claude","prompt":"x"}`)
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusBadRequest || e["code"] != "invalid_value" || !strings.Contains(msg, "claude/default") {
			t.Fatalf("%s: %d %v", name, rec.Code, e)
		}
		for _, s := range c.in {
			if !strings.Contains(msg, s) {
				t.Errorf("%s: %q must say %q", name, msg, s)
			}
		}
		for _, s := range c.out {
			if strings.Contains(msg, s) {
				t.Errorf("%s: %q must not say %q", name, msg, s)
			}
		}
	}
}

// The operator hears at the start which runtimes of the image list cannot make images,
// once: not from a client's 404, and not again at every reload of the list.
func TestImageRuntimesThatCannotMakeImagesAreLoggedOnce(t *testing.T) {
	imageLines := func(h *harness) []string {
		var out []string
		for _, l := range h.logged() {
			if strings.Contains(l, "image runtimes") {
				out = append(out, l)
			}
		}
		return out
	}
	h := newHarness(t, okTurn("x"), func(_ *Deps, c *Config) {
		c.ImageRuntimes = []string{"grok", "claude", "codex"}
		c.CatalogTTL = time.Millisecond // the list is reloaded behind the next request
	})
	secret := h.key(t, "default", "app", false)
	if lines := imageLines(h); len(lines) != 0 {
		t.Fatalf("said before the list of models was loaded: %q", lines)
	}
	for range 3 {
		h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, "")
		time.Sleep(10 * time.Millisecond)
		waitForRefresh(t, h.g.catalog)
	}
	lines := imageLines(h)
	if len(lines) != 1 || !strings.Contains(lines[0], "grok is not installed") || !strings.Contains(lines[0], "claude runs as chat-only") || strings.Contains(lines[0], "codex") {
		t.Errorf("want one line that names grok (not installed) and claude (chat-only) and not codex, which is fine: %q", lines)
	}

	// Nothing is wrong with the default list: nothing is said.
	ok := newHarness(t, okTurn("x"))
	ok.serve(anyPolicy, http.MethodGet, "/v1/models", ok.key(t, "default", "app", false), "")
	if lines := imageLines(ok); len(lines) != 0 {
		t.Errorf("the default list is fine: %q", lines)
	}

	// Switched off is said when the gateway is made.
	off := newHarness(t, okTurn("x"), func(_ *Deps, c *Config) { c.ImageRuntimes = []string{} })
	var said []string
	for _, l := range off.logged() {
		if strings.Contains(l, "switched off") {
			said = append(said, l)
		}
	}
	if len(said) != 1 || !strings.Contains(said[0], "MONOAGENT_API_IMAGE_RUNTIMES") {
		t.Errorf("image generation was switched off: want one line that names the variable, got %q", off.logged())
	}
}
