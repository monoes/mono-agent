//go:build !nosocial

package linkedin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

// linkedinCSP mirrors LinkedIn's policy in the one way that matters here:
// no 'unsafe-eval', so ExtensionPage.Eval (new Function) is blocked and the
// bot must go through EvalCDP.
const linkedinCSP = "script-src 'self' 'unsafe-inline'"

// fastTimings shortens every wait for fixture pages.
func fastTimings(t *testing.T) {
	t.Helper()
	old := []time.Duration{pageSettle, uiSettle, pollEvery, findTimeout, verifyTimeout, scrollSettle}
	pageSettle, uiSettle, pollEvery = 30*time.Millisecond, 80*time.Millisecond, 40*time.Millisecond
	findTimeout, verifyTimeout, scrollSettle = 3*time.Second, 3*time.Second, 150*time.Millisecond
	t.Cleanup(func() {
		pageSettle, uiSettle, pollEvery, findTimeout, verifyTimeout, scrollSettle = old[0], old[1], old[2], old[3], old[4], old[5]
	})
}

// voyagerRoute answers the fixtures' write beacons, so the Recorder shows
// which writes a method caused.
var voyagerRoute = bottest.Route{Pattern: "https://www.linkedin.com/voyager/api/*", Method: "POST", Body: `{}`, ContentType: "application/json"}

// newPage opens a fixture tab with LinkedIn's CSP and the given routes.
func newPage(t *testing.T, b *bottest.Browser, routes ...bottest.Route) (*bottest.Page, *bottest.Recorder) {
	t.Helper()
	p := b.NewPage(t)
	p.SetCSP(linkedinCSP)
	rec := p.Serve(append(routes, voyagerRoute)...)
	return p, rec
}

// writes returns the fixture write beacons (voyager POSTs) recorded so far.
func writes(rec *bottest.Recorder) []string {
	var out []string
	for _, r := range rec.Matching("POST", "https://www.linkedin.com/voyager/api/*") {
		out = append(out, strings.TrimPrefix(r.URL, "https://www.linkedin.com/voyager/api/")+" "+r.PostData)
	}
	return out
}

// evalString evaluates js on p and returns its string value.
func evalString(t *testing.T, p *bottest.Page, js string) string {
	t.Helper()
	v, err := p.EvalCDP(js)
	if err != nil {
		t.Fatalf("EvalCDP(%s): %v", js, err)
	}
	s, _ := v.(string)
	return s
}

func call(t *testing.T, b *LinkedInBot, p *bottest.Page, method string, args ...interface{}) (interface{}, error) {
	t.Helper()
	return bottest.CallMethod(t, b, p, method, args...)
}

// jevBot returns a bot whose Jev picker is served by a fake that picks the
// candidate whose label contains want ("" ⇒ NONE).
func jevBot(t *testing.T, want string) (*LinkedInBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, func(req jev.Request) map[string]string {
		return map[string]string{"target": pickByLabel(req, want)}
	})
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &LinkedInBot{}
	b.SetJevPicker(c, 0)
	return b, srv
}

// pickByLabel returns the option id whose element label contains want.
func pickByLabel(req jev.Request, want string) string {
	if want == "" {
		return "NONE"
	}
	raw, _ := json.Marshal(req.Questions["target"].Criteria)
	var crit map[string]map[string]interface{}
	_ = json.Unmarshal(raw, &crit)
	for id, c := range crit {
		if el, _ := c["element"].(string); strings.Contains(el, want) {
			return id
		}
	}
	return "NONE"
}

// jevIntents lists the intents Jev was asked about.
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
