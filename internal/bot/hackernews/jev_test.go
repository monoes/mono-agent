//go:build social

package hackernews

import (
	"net/url"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

// jevChoose answers every pick with the first offered element whose label
// and role satisfy match, or NONE.
func jevChoose(match func(label, role string) bool) jevtest.Handler {
	return func(req jev.Request) map[string]string {
		st, _ := req.State.(map[string]interface{})
		els, _ := st["elements"].([]interface{})
		for _, e := range els {
			m, _ := e.(map[string]interface{})
			label, _ := m["label"].(string)
			role, _ := m["role"].(string)
			if match(label, role) {
				idx, _ := m["index"].(string)
				return map[string]string{"target": idx}
			}
		}
		return map[string]string{"target": "NONE"}
	}
}

func jevBot(t *testing.T, h jevtest.Handler) (*HackerNewsBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, h)
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &HackerNewsBot{}
	b.SetJevPicker(c, 0)
	return b, srv
}

// The reply form's markup changed (no name="text", a <button>, a new form
// action): the selectors miss and Jev finds the reply box and its button —
// not the thread's "reply" link.
func TestBrowserReplyJevFallback(t *testing.T) {
	b, srv := jevBot(t, jevChoose(func(label, role string) bool {
		return label == "Your reply" || (label == "reply" && role == "button")
	}))
	p, rec := hnPage(t,
		file("https://news.ycombinator.com/reply?*", "reply_jev.html"),
		redirect("https://news.ycombinator.com/comment2*", "POST", "https://news.ycombinator.com/item?id=70000010"),
		file("https://news.ycombinator.com/item?id=70000010", "item_after_reply.html"),
	)
	res, err := bottest.CallMethod(t, b, p, "reply_to_comment", "70000010", replyText)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]interface{})["commentID"] != "70000099" {
		t.Fatalf("result = %v", res)
	}
	if srv.Calls() != 2 {
		t.Fatalf("jev calls = %d, want 2 (box, button)", srv.Calls())
	}
	posts := rec.Matching("POST", "*/comment2*")
	if len(posts) != 1 {
		t.Fatalf("POSTs = %d", len(posts))
	}
	form, _ := url.ParseQuery(posts[0].PostData)
	if !strings.Contains(form.Get("body"), "synthetic") {
		t.Fatalf("posted form = %v", form)
	}
}

// Jev finding nothing leaves the selector error: nothing is typed or sent.
func TestBrowserReplyJevNoneFails(t *testing.T) {
	b, _ := jevBot(t, jevChoose(func(string, string) bool { return false }))
	p, rec := hnPage(t, file("https://news.ycombinator.com/reply?*", "reply_jev.html"))
	_, err := bottest.CallMethod(t, b, p, "reply_to_comment", "70000010", replyText)
	if err == nil || !strings.Contains(err.Error(), "reply box not found") || !strings.Contains(err.Error(), "jev fallback") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("POSTs = %d", n)
	}
}
