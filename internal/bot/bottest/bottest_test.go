package bottest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/browser"
)

// Self-tests. The browser ones skip unless BOTTEST_BROWSER (or
// JEV_E2E_BROWSER) names a Chromium binary:
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 -v ./internal/bot/bottest/

func servedPage(t *testing.T) (*bottest.Page, *bottest.Recorder) {
	t.Helper()
	b := bottest.Launch(t)
	p := b.NewPage(t)
	rec := p.Serve(
		bottest.Route{Pattern: "https://example.test/x", File: "testdata/feed.html"},
		bottest.Route{Pattern: "https://example.test/y", Body: `<!doctype html><title>Y</title><h1 id="y">Page Y</h1>`},
		bottest.Route{Pattern: "https://example.test/submit*", Method: "POST", Body: `<!doctype html><p id="ok">thanks</p>`},
	)
	if err := p.Navigate("https://example.test/x"); err != nil {
		t.Fatal(err)
	}
	return p, rec
}

func evalCDP(t *testing.T, p *bottest.Page, js string) interface{} {
	t.Helper()
	v, err := p.EvalCDP(js)
	if err != nil {
		t.Fatalf("EvalCDP(%s): %v", js, err)
	}
	return v
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, url string
		want     bool
	}{
		{"https://www.instagram.com/*", "https://www.instagram.com/p/ABC/", true},
		{"https://www.instagram.com/p/*/", "https://www.instagram.com/p/ABC/", true},
		{"https://www.instagram.com/p/*/", "https://www.instagram.com/p/ABC", false},
		{"*/api/v1/*", "https://i.instagram.com/api/v1/likes/1/", true},
		{"https://example.test/x", "https://example.test/x?y=1", false},
		{"https://example.test/x?y=?", "https://example.test/x?y=1", true},
		{"https://example.test/a.b", "https://example.test/aXb", false},
	}
	for _, c := range cases {
		if got := bottest.MatchGlob(c.pat, c.url); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pat, c.url, got, c.want)
		}
	}
}

func TestLaunchSkipsWithoutBrowser(t *testing.T) {
	if os.Getenv("BOTTEST_BROWSER") != "" || os.Getenv("JEV_E2E_BROWSER") != "" {
		t.Skip("a browser is configured")
	}
	ran := false
	t.Run("inner", func(t *testing.T) {
		bottest.Launch(t)
		ran = true
	})
	if ran {
		t.Fatal("Launch must skip without a browser binary")
	}
}

func TestQueriesAndElements(t *testing.T) {
	p, _ := servedPage(t)

	if u, err := p.GetURL(); err != nil || u != "https://example.test/x" {
		t.Fatalf("GetURL = %q, %v", u, err)
	}
	el, err := p.Element(".post .caption", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := el.Text(); txt != "First synthetic post" {
		t.Errorf("Text = %q (want trimmed)", txt)
	}
	els, err := p.Elements(".post")
	if err != nil || len(els) != 2 {
		t.Fatalf("Elements = %d, %v", len(els), err)
	}
	if id, _ := els[1].Attribute("data-id"); id == nil || *id != "102" {
		t.Errorf("Attribute(data-id) = %v", id)
	}
	if a, err := els[1].Attribute("nope"); err != nil || a != nil {
		t.Errorf("missing attribute = %v, %v (want nil, nil)", a, err)
	}
	if x, err := p.ElementX("//button[@aria-label='Like']", time.Second); err != nil {
		t.Fatal(err)
	} else if h, _ := x.HTML(); h != "Like" {
		t.Errorf("HTML (inner) = %q", h)
	}
	if v, err := els[0].Property("className"); err != nil || v != "post" {
		t.Errorf("Property(className) = %v, %v", v, err)
	}
	if ok, _ := p.Has("#like"); !ok {
		t.Error("Has(#like) = false")
	}
	if ok, _ := p.Has("#absent"); ok {
		t.Error("Has(#absent) = true")
	}

	// Element polls: #late appears after ~600ms.
	start := time.Now()
	if _, err := p.Element("#late", 3*time.Second); err != nil {
		t.Fatalf("polling Element: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Element took %v", time.Since(start))
	}
	if _, err := p.Element("#never", 300*time.Millisecond); err == nil {
		t.Error("Element(#never) succeeded")
	}

	// Race: earliest present selector wins.
	idx, rel, err := p.Race([]string{"#absent", "#name", "#like"}, time.Second)
	if err != nil || idx != 1 {
		t.Fatalf("Race = %d, %v", idx, err)
	}
	if v, _ := rel.Property("id"); v != "name" {
		t.Errorf("Race element id = %v", v)
	}
	if _, _, err := p.Race([]string{"#absent"}, 200*time.Millisecond); err == nil {
		t.Error("Race timeout must be an error")
	}

	// Click is synthetic (untrusted), like the extension's.
	like, _ := p.Element("#like", time.Second)
	if err := like.Click(); err != nil {
		t.Fatal(err)
	}
	if got := evalCDP(t, p, `window.__log.join(',')`); got != "click:false" {
		t.Errorf("after Click log = %v", got)
	}
	if pressed, _ := like.Attribute("aria-pressed"); pressed == nil || *pressed != "true" {
		t.Error("click handler did not run")
	}

	// Input clears first.
	name, _ := p.Element("#name", time.Second)
	if err := name.Input("new value"); err != nil {
		t.Fatal(err)
	}
	if v, _ := name.Property("value"); v != "new value" {
		t.Errorf("value after Input = %v", v)
	}
	comp, _ := p.Element("#composer", time.Second)
	if err := comp.Input("hello there"); err != nil {
		t.Fatal(err)
	}
	if txt, _ := comp.Text(); txt != "hello there" {
		t.Errorf("contenteditable after Input = %q", txt)
	}

	// SetFiles.
	f := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(f, []byte("\x89PNG synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ := p.Element("#file", time.Second)
	if err := fi.SetFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
	if got := evalCDP(t, p, `window.__log[window.__log.length-1]`); got != "files:1:pic.png" {
		t.Errorf("after SetFiles log = %v", got)
	}
	if err := like.WaitStable(100 * time.Millisecond); err != nil {
		t.Errorf("WaitStable: %v", err)
	}
}

func TestEvalAndEvalCDP(t *testing.T) {
	p, _ := servedPage(t)
	r, err := p.Eval(`(a, b) => a + b.n`, 2, map[string]int{"n": 3})
	if err != nil || r.Int() != 5 {
		t.Fatalf("Eval = %v, %v", r.Raw(), err)
	}
	if r, _ := p.Eval(`document.title`); r.Str() != "Synthetic Feed" {
		t.Errorf("Eval expression = %v", r.Raw())
	}
	if r, err := p.Eval(`() => { throw new Error('boom') }`); err != nil || !r.Nil() {
		t.Errorf("non-strict Eval must swallow: %v, %v", r.Raw(), err)
	}
	if _, err := p.Strict().Eval(`() => { throw new Error('boom') }`); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("strict Eval err = %v", err)
	}
	if v := evalCDP(t, p, `Promise.resolve(document.querySelectorAll('.post').length)`); v != float64(2) {
		t.Errorf("EvalCDP = %v", v)
	}
	if _, err := p.EvalCDP(`nope.nope`); err == nil {
		t.Error("EvalCDP must report exceptions")
	}
}

func TestCSPBlocksEvalButNotEvalCDP(t *testing.T) {
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(bottest.Route{Pattern: "https://example.test/*", File: "testdata/feed.html"})
	p.SetCSP("script-src 'self' 'unsafe-inline'")
	if err := p.Navigate("https://example.test/x"); err != nil {
		t.Fatal(err)
	}
	// The page's own inline script still ran...
	if v := evalCDP(t, p, `Array.isArray(window.__log)`); v != true {
		t.Fatal("inline script blocked; CSP too strict for the test")
	}
	// ...but new Function is blocked, so Eval silently yields nil like the extension.
	r, err := p.Eval(`() => 42`)
	if err != nil || !r.Nil() {
		t.Fatalf("Eval under CSP = %v, %v; want nil result", r.Raw(), err)
	}
	if _, err := p.Strict().Eval(`() => 42`); err == nil {
		t.Fatal("strict Eval under CSP must fail")
	}
	if v := evalCDP(t, p, `(() => 42)()`); v != float64(42) {
		t.Fatalf("EvalCDP under CSP = %v", v)
	}
	// bot.EvalJSON takes the EvalCDP path and works.
	var n int
	if err := bot.EvalJSON(p, `(sel) => document.querySelectorAll(sel).length`, &n, ".post"); err != nil || n != 2 {
		t.Fatalf("EvalJSON under CSP = %d, %v", n, err)
	}
}

// evalOnly hides the page's CDP extras, like a driver without them.
type evalOnly struct{ browser.PageInterface }

func TestEvalJSONWithoutCDPReportsSwallowedCSPError(t *testing.T) {
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(bottest.Route{Pattern: "https://example.test/*", File: "testdata/feed.html"})
	p.SetCSP("script-src 'self' 'unsafe-inline'")
	if err := p.Navigate("https://example.test/x"); err != nil {
		t.Fatal(err)
	}
	err := bot.EvalJSON(evalOnly{p}, `() => 1`, nil)
	if !errors.Is(err, bot.ErrEmptyEval) {
		t.Fatalf("EvalJSON over a CSP-blocked Eval = %v, want ErrEmptyEval", err)
	}
}

func TestRecorderSeesFormPOSTAndBlockedRequests(t *testing.T) {
	p, rec := servedPage(t)
	note, _ := p.Element("#note", time.Second)
	if err := note.Input("synthetic note"); err != nil {
		t.Fatal(err)
	}
	send, _ := p.Element("#send", time.Second)
	if err := send.Click(); err != nil {
		t.Fatal(err)
	}
	req, ok := rec.Wait("POST", "https://example.test/submit*", 5*time.Second)
	if !ok {
		t.Fatalf("no POST recorded; saw %+v", rec.Requests())
	}
	if !strings.Contains(req.PostData, "note=synthetic+note") || !strings.Contains(req.PostData, "name=old") {
		t.Errorf("PostData = %q", req.PostData)
	}
	if _, err := p.Element("#ok", 5*time.Second); err != nil {
		t.Fatalf("POST response not rendered: %v", err)
	}
	// Unserved URLs are blocked, never fetched.
	if err := p.Navigate("https://unserved.test/"); err == nil || !strings.Contains(err.Error(), "BLOCKED_BY_CLIENT") {
		t.Errorf("navigate to unserved URL: %v", err)
	}
	if got := rec.Matching("GET", "https://unserved.test/*"); len(got) != 1 || !got[0].Blocked {
		t.Errorf("blocked request not recorded: %+v", got)
	}
}

func TestRouteHandlerAndOrder(t *testing.T) {
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(
		bottest.Route{Pattern: "https://example.test/api/*", Handler: func(r bottest.Request) bottest.Response {
			return bottest.Response{Body: `{"path":"` + strings.TrimPrefix(r.URL, "https://example.test") + `"}`, ContentType: "application/json"}
		}},
		bottest.Route{Pattern: "https://example.test/*", Body: `<!doctype html><p id="first">first</p>`},
		bottest.Route{Pattern: "https://example.test/*", Body: `<!doctype html><p id="second">second</p>`},
	)
	if err := p.Navigate("https://example.test/page"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := p.Has("#first"); !ok {
		t.Error("first matching route must win")
	}
	v := evalCDP(t, p, `fetch('/api/items?id=7').then(r => r.json()).then(j => j.path)`)
	if v != "/api/items?id=7" {
		t.Errorf("handler route = %v", v)
	}
}

func TestPageHelpersOnRealBrowser(t *testing.T) {
	p, _ := servedPage(t)
	ctx := context.Background()

	like, _ := p.Element("#like", time.Second)
	if err := bot.ClickTrusted(p, like); err != nil {
		t.Fatal(err)
	}
	if got := evalCDP(t, p, `window.__log.join(',')`); got != "click:true" {
		t.Errorf("ClickTrusted log = %v (want a trusted click)", got)
	}
	if n := evalCDP(t, p, `document.querySelectorAll('[data-monoagent-mark]').length`); n != float64(0) {
		t.Errorf("marker left behind: %v", n)
	}

	comp, _ := p.Element("#composer", time.Second)
	if err := bot.TypeInto(ctx, p, comp, "héllo 👋"); err != nil {
		t.Fatal(err)
	}
	if txt, _ := comp.Text(); txt != "héllo 👋" {
		t.Errorf("TypeInto text = %q", txt)
	}
	if err := bot.PressEnter(p); err != nil {
		t.Fatal(err)
	}
	if got := evalCDP(t, p, `window.__log[window.__log.length-1]`); got != "enter:true" {
		t.Errorf("PressEnter log = %v", got)
	}

	out, _, err := bot.WaitForOutcomes(p, []bot.Outcome{{Label: "none", Selector: "#absent"}, {Label: "form", Selector: "#f"}, {Label: "like", Selector: "#like"}}, 2*time.Second)
	if err != nil || out.Label != "form" {
		t.Errorf("WaitForOutcomes = %+v, %v", out, err)
	}
	el, idx, err := bot.FindFirst(p, "#absent", []string{"#next"}, time.Second)
	if err != nil || idx != 1 {
		t.Fatalf("FindFirst = %d, %v", idx, err)
	}
	if err := bot.PageClickAndWaitNavigation(ctx, p, el, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if u, _ := p.GetURL(); u != "https://example.test/y" {
		t.Errorf("after navigation URL = %q", u)
	}
	// Handles from the previous document are stale, as in the extension.
	if _, err := like.Text(); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("stale handle: %v", err)
	}
}

func TestTypeCDPFocusesFirstContentEditable(t *testing.T) {
	p, _ := servedPage(t)
	if err := p.TypeCDP("typed via cdp"); err != nil {
		t.Fatal(err)
	}
	if v := evalCDP(t, p, `document.getElementById('composer').textContent`); v != "typed via cdp" {
		t.Errorf("TypeCDP → %v", v)
	}
}

type fakeAdapter struct{ got []interface{} }

func (f *fakeAdapter) GetMethodByName(name string) (func(context.Context, ...interface{}) (interface{}, error), bool) {
	if name != "count_posts" {
		return nil, false
	}
	return func(ctx context.Context, args ...interface{}) (interface{}, error) {
		f.got = args
		page, rest, err := bot.Args(args, 1, "selector")
		if err != nil {
			return nil, err
		}
		els, err := page.Elements(rest[0])
		return map[string]interface{}{"count": len(els)}, err
	}, true
}

func TestCallMethodPrependsPage(t *testing.T) {
	p, _ := servedPage(t)
	a := &fakeAdapter{}
	res, err := bottest.CallMethod(t, a, p, "count_posts", ".post")
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]interface{})["count"] != 2 || a.got[0] != browser.PageInterface(p) {
		t.Errorf("res = %v, args = %v", res, a.got)
	}
	if _, err := bottest.CallMethod(t, a, p, "count_posts"); err == nil {
		t.Error("missing required arg must fail")
	}
}
