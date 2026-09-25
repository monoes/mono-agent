//go:build !nosocial

package x

// Jev fallback paths, end to end: a real headless page (bottest) and a fake
// TypeSafe server (jevtest) — no network. The fake answers with the option
// whose element label contains a given text, so each test decides which
// element "Jev" picks.

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

// pickLabel answers the element question with the first option whose
// label contains sub ("NONE" when there is none).
func pickLabel(sub string) jevtest.Handler {
	return func(req jev.Request) map[string]string {
		q := req.Questions["target"]
		crit, _ := q.Criteria.(map[string]interface{})
		keys := make([]string, 0, len(crit))
		for id := range crit {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			m, ok := crit[id].(map[string]interface{})
			if !ok {
				continue
			}
			if el, _ := m["element"].(string); strings.Contains(el, sub) {
				return map[string]string{"target": id}
			}
		}
		return map[string]string{"target": "NONE"}
	}
}

// jevBot returns a bot with a Jev picker wired to a fake server.
func jevBot(t *testing.T, h jevtest.Handler) (*XBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, h)
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.SetJevPicker(c, 0)
	return b, srv
}

func parentPressed(t *testing.T, p *bottest.Page) string {
	return jsString(t, p, `(() => { const a = [...document.querySelectorAll('article')].find((x) => x.textContent.includes('the parent post'));
		return a.querySelector('button.like').getAttribute('aria-pressed') || '-'; })()`)
}

func TestJevLikePost(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)

	t.Run("not consulted when the selector matches", func(t *testing.T) {
		bot, srv := jevBot(t, pickLabel("7 Likes"))
		if _, err := bot.LikePost(ctxT(t), xPage(t, b), statusURL("1001")); err != nil {
			t.Fatal(err)
		}
		if srv.Calls() != 0 {
			t.Errorf("Jev called %d times", srv.Calls())
		}
	})

	t.Run("picks the post's Like button", func(t *testing.T) {
		bot, srv := jevBot(t, pickLabel("7 Likes"))
		p := xPage(t, b)
		got, err := bot.LikePost(ctxT(t), p, statusURL("1004"))
		if err != nil {
			t.Fatal(err)
		}
		if got["liked"] != true || got["already_liked"] != false || srv.Calls() != 1 {
			t.Errorf("result = %v, calls = %d", got, srv.Calls())
		}
		if s := targetLike(t, p); s != "-|true" || wrongLikes(t, p) != 0 {
			t.Errorf("state = %s, wrong likes %v", s, wrongLikes(t, p))
		}
	})

	t.Run("a pick on another post is rejected, never clicked", func(t *testing.T) {
		bot, _ := jevBot(t, pickLabel("3 Likes"))
		p := xPage(t, b)
		_, err := bot.LikePost(ctxT(t), p, statusURL("1004"))
		wantErr(t, err, "outside the target")
		if wrongLikes(t, p) != 0 || parentPressed(t, p) != "-" || targetLike(t, p) != "-|-" {
			t.Errorf("something was clicked: wrong=%v parent=%s target=%s", wrongLikes(t, p), parentPressed(t, p), targetLike(t, p))
		}
	})

	t.Run("NONE fails", func(t *testing.T) {
		bot, _ := jevBot(t, pickLabel("no such element"))
		p := xPage(t, b)
		_, err := bot.LikePost(ctxT(t), p, statusURL("1004"))
		wantErr(t, err, "could not find the Like button")
		if targetLike(t, p) != "-|-" {
			t.Error("target changed")
		}
	})
}

func TestJevSendDM(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)

	for _, c := range []struct{ name, label string }{
		{"the Following button", "Following @"},
		{"the following-count link", "321"},
		{"the More menu", "More"},
	} {
		t.Run(c.name+" never stands in for Message", func(t *testing.T) {
			bot, srv := jevBot(t, pickLabel(c.label))
			p := xPage(t, b)
			_, err := bot.SendDM(ctxT(t), p, "synth_bob", "hello")
			wantErr(t, err, "is not a Message button")
			if srv.Calls() != 1 {
				t.Errorf("Jev calls = %d", srv.Calls())
			}
			if u := urlOf(t, p); u != "https://x.com/synth_bob" {
				t.Errorf("navigated to %s", u)
			}
		})
	}

	t.Run("a Message button Jev finds is used", func(t *testing.T) {
		// synth_bob's page with the Message button renamed away from its data-testid.
		bot, srv := jevBot(t, pickLabel("Message"))
		p := xPage(t, b, bottest.Route{Pattern: "https://x.com/synth_renamed", Handler: func(bottest.Request) bottest.Response {
			raw, err := os.ReadFile("testdata/profile.html")
			if err != nil {
				return bottest.Response{Status: 500, Body: err.Error()}
			}
			html := strings.Replace(string(raw), `if (who !== 'synth_alice') {`, `if (who === 'synth_renamed') { document.getElementById('dm').setAttribute('data-testid', 'profileMessageButton'); } else if (who !== 'synth_alice') {`, 1)
			return bottest.Response{Body: html, ContentType: "text/html"}
		}})
		got, err := bot.SendDM(ctxT(t), p, "synth_renamed", "renamed button works")
		if err != nil {
			t.Fatal(err)
		}
		if got["sent"] != true || srv.Calls() != 1 {
			t.Errorf("result = %v, calls = %d", got, srv.Calls())
		}
		if s := sentDMs(t, p); s != `["renamed button works"]` {
			t.Errorf("sent = %s", s)
		}
	})
}
