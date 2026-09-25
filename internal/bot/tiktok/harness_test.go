//go:build !nosocial

package tiktok

// Shared test plumbing: a real headless Chromium (bottest; skipped unless
// $BOTTEST_BROWSER is set) serving the synthetic fixtures in testdata/ at
// TikTok's real URLs, with a CSP that blocks eval (TikTok's does too) so any
// page.Eval use in the bot would fail loudly.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

const (
	vidPlain      = "https://www.tiktok.com/@fake_creator/video/7100000000000000001"
	vidLiked      = "https://www.tiktok.com/@fake_creator/video/7100000000000000002"
	vidLikeBroken = "https://www.tiktok.com/@fake_creator/video/7100000000000000003"
	vidCommentBad = "https://www.tiktok.com/@fake_creator/video/7100000000000000004"
	vidNoComments = "https://www.tiktok.com/@fake_creator/video/7100000000000000005"
	vidRemixOff   = "https://www.tiktok.com/@fake_creator/video/7100000000000000006"
	vidNoPressed  = "https://www.tiktok.com/@fake_creator/video/7100000000000000007"
	vidLikeless   = "https://www.tiktok.com/@fake_creator/video/7100000000000000008"
	vidAmbiguous  = "https://www.tiktok.com/@fake_creator/video/7100000000000000009"
	vidModern     = "https://www.tiktok.com/@fake_creator/video/7100000000000000010"
)

func profileURL(h string) string { return "https://www.tiktok.com/@" + h }

// fast shrinks the bot's pauses and timeouts for the test.
func fast(t *testing.T) {
	t.Helper()
	s, v, f, p, pt := settleScale, verifyTimeout, findTimeout, pollInterval, publishTimeout
	settleScale, verifyTimeout, findTimeout, pollInterval, publishTimeout = 0.05, 3*time.Second, 2*time.Second, 50*time.Millisecond, 3*time.Second
	t.Cleanup(func() {
		settleScale, verifyTimeout, findTimeout, pollInterval, publishTimeout = s, v, f, p, pt
	})
}

// fixtureRoutes maps TikTok URLs to the synthetic fixtures.
func fixtureRoutes() []bottest.Route {
	return []bottest.Route{
		{Pattern: "https://www.tiktok.com/messages*", File: "testdata/messages.html"},
		{Pattern: "https://www.tiktok.com/search/video?q=*", File: "testdata/search.html"},
		{Pattern: "https://www.tiktok.com/tiktokstudio/upload*", File: "testdata/upload.html"},
		{Pattern: "https://www.tiktok.com/tiktokstudio/content*", File: "testdata/content.html"},
		{Pattern: vidAmbiguous, File: "testdata/ambiguity.html"},
		{Pattern: "https://www.tiktok.com/@*/video/*", File: "testdata/video.html"},
		{Pattern: "https://www.tiktok.com/@*", File: "testdata/profile.html"},
		{Pattern: "https://www.tiktok.com/foryou*", File: "testdata/home.html"},
	}
}

// newPage launches a browser and returns a page serving the fixtures.
func newPage(t *testing.T) *bottest.Page {
	t.Helper()
	fast(t)
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(fixtureRoutes()...)
	p.SetCSP("script-src 'self' 'unsafe-inline'")
	return p
}

// pageEvents returns the fixture's window.__events log.
func pageEvents(t *testing.T, p *bottest.Page) []string {
	t.Helper()
	v, err := p.EvalCDP(`JSON.stringify(window.__events || [])`)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var out []string
	if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
		t.Fatalf("decode events %v: %v", v, err)
	}
	return out
}

// wantEvents asserts the fixture saw exactly these events (in order).
func wantEvents(t *testing.T, p *bottest.Page, want ...string) {
	t.Helper()
	got := pageEvents(t, p)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("page events = %q, want %q", got, want)
	}
}

// evalString evaluates js on the page and returns its string value.
func evalString(t *testing.T, p *bottest.Page, js string) string {
	t.Helper()
	v, err := p.EvalCDP(js)
	if err != nil {
		t.Fatalf("EvalCDP(%s): %v", js, err)
	}
	s, _ := v.(string)
	return s
}

// call invokes a bot method the way call_bot_method does.
func call(t *testing.T, b *TikTokBot, p *bottest.Page, method string, args ...interface{}) (interface{}, error) {
	t.Helper()
	return bottest.CallMethod(t, b, p, method, args...)
}

// resultMap asserts res is a map and returns it.
func resultMap(t *testing.T, res interface{}) map[string]interface{} {
	t.Helper()
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("result %T %v, want a map", res, res)
	}
	return m
}

// resultList asserts res is a list of maps and returns it.
func resultList(t *testing.T, res interface{}) []map[string]interface{} {
	t.Helper()
	m, ok := res.([]map[string]interface{})
	if !ok {
		t.Fatalf("result %T %v, want a list", res, res)
	}
	return m
}

// pickLabel is a jevtest handler choosing the option whose element label
// contains want (NONE when none does).
func pickLabel(want string) jevtest.Handler {
	return func(req jev.Request) map[string]string {
		crit, _ := req.Questions["target"].Criteria.(map[string]any)
		// Exact label first, then the lowest-numbered label containing want.
		best, bestN := "", 1<<30
		for id, v := range crit {
			m, ok := v.(map[string]any)
			if !ok || want == "" {
				continue
			}
			el, _ := m["element"].(string)
			label := el
			if i := strings.Index(el, "] "); i >= 0 {
				label = el[i+2:]
			}
			n, _ := strconv.Atoi(id)
			switch {
			case label == want:
				return map[string]string{"target": id}
			case strings.Contains(label, want) && n < bestN:
				best, bestN = id, n
			}
		}
		if best == "" {
			best = "NONE"
		}
		return map[string]string{"target": best}
	}
}

// jevBot returns a bot whose Jev picker talks to a jevtest server answering
// with the option labelled like want.
func jevBot(t *testing.T, want string) (*TikTokBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, pickLabel(want))
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &TikTokBot{}
	b.SetJevPicker(c, 0)
	return b, srv
}

// jevIntents lists the intents sent to the jevtest server.
func jevIntents(srv *jevtest.Server) []string {
	var out []string
	for _, r := range srv.Requests() {
		raw, _ := json.Marshal(r.Questions["target"].Instructions)
		var ins struct {
			Intent string `json:"intent"`
		}
		_ = json.Unmarshal(raw, &ins)
		out = append(out, ins.Intent)
	}
	return out
}

var bg = context.Background()

type jevReq = jev.Request

// intentOf returns the intent of a Jev request's target question.
func intentOf(req jev.Request) string {
	raw, _ := json.Marshal(req.Questions["target"].Instructions)
	var ins struct {
		Intent string `json:"intent"`
	}
	_ = json.Unmarshal(raw, &ins)
	return ins.Intent
}
