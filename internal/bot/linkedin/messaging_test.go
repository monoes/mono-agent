//go:build !nosocial

package linkedin

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

var (
	profileRoute   = bottest.Route{Pattern: "https://www.linkedin.com/in/*", File: "testdata/profile.html"}
	messagingRoute = bottest.Route{Pattern: "https://www.linkedin.com/messaging/*", File: "testdata/messaging.html"}
)

// onlySends fails unless the recorded writes are exactly the message sends
// in want (recipient names) — no connection request, follow, decoy click or
// anything else.
func onlySends(t *testing.T, rec *bottest.Recorder, text string, want ...string) {
	t.Helper()
	w := writes(rec)
	if len(w) != len(want) {
		t.Fatalf("writes = %v, want %d message send(s) to %v", w, len(want), want)
	}
	for i, name := range want {
		if !strings.HasPrefix(w[i], "messaging/send ") || !strings.Contains(w[i], `"to":"`+name+`"`) || !strings.Contains(w[i], text) {
			t.Fatalf("write %d = %q, want a send of %q to %s", i, w[i], text, name)
		}
	}
}

func TestSendMessage(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	const msg = "Hi there, great to connect — quick question about your talk."

	t.Run("1st degree: Message button, overlay composer", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		res, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/ada-first-test/", msg)
		if err != nil {
			t.Fatal(err)
		}
		if m := res.(map[string]interface{}); m["sent"] != true || m["name"] != "Ada First" {
			t.Fatalf("res = %v", m)
		}
		onlySends(t, rec, msg, "Ada First")
	})

	t.Run("picks the recipient's conversation when another one is open", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		if _, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/ada-first-test/?otherOpen=1", msg); err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, msg, "Ada First")
	})

	t.Run("bare slug", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		if _, err := call(t, &LinkedInBot{}, p, "send_message", "ada-first-test", msg); err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, msg, "Ada First")
	})

	t.Run("3rd degree: Message under More, composer in a shadow root, Connect untouched", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		if _, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/bo-third-test/", msg); err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, msg, "Bo Third")
	})

	t.Run("no Message option: fails and never connects", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		_, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/cy-closed-test/", msg)
		if err == nil || !strings.Contains(err.Error(), "no Message option") {
			t.Fatalf("err = %v", err)
		}
		onlySends(t, rec, msg)
		if got := evalString(t, p, `document.querySelector('[data-act="connect"] span').textContent`); got != "Connect" {
			t.Fatalf("Connect button now reads %q", got)
		}
	})

	t.Run("Message link opens the full-page composer", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute, messagingRoute)
		if _, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/dee-link-test/", msg); err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, msg, "Dee Link")
		if len(rec.Matching("GET", "https://www.linkedin.com/messaging/compose/?recipient=dee-link-test")) != 1 {
			t.Fatalf("requests = %v", rec.Requests())
		}
	})

	t.Run("unconfirmed send is an error", func(t *testing.T) {
		p, rec := newPage(t, b, profileRoute)
		_, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/in/ada-first-test/?broken=send", msg)
		if err == nil || !strings.Contains(err.Error(), "could not be verified") {
			t.Fatalf("err = %v", err)
		}
		onlySends(t, rec, msg)
	})

	t.Run("thread URL replies in that thread", func(t *testing.T) {
		p, rec := newPage(t, b, messagingRoute)
		if _, err := call(t, &LinkedInBot{}, p, "send_message", "https://www.linkedin.com/messaging/thread/2-AAAgia/", "Tuesday works"); err != nil {
			t.Fatal(err)
		}
		onlySends(t, rec, "Tuesday works", "Gia Unread")
	})

	t.Run("an unsent draft is not sent over", func(t *testing.T) {
		p, rec := newPage(t, b, messagingRoute)
		_, err := call(t, &LinkedInBot{}, p, "reply_to_conversation", "https://www.linkedin.com/messaging/thread/2-AAAgia/?draft=1", "Tuesday works")
		if err == nil || !strings.Contains(err.Error(), "draft") {
			t.Fatalf("err = %v", err)
		}
		onlySends(t, rec, "")
	})
}

func TestListConversations(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	names := func(res interface{}) string {
		var out []string
		for _, c := range res.([]map[string]interface{}) {
			out = append(out, c["name"].(string))
		}
		return strings.Join(out, ",")
	}

	t.Run("unread, awaiting a reply (default)", func(t *testing.T) {
		p, rec := newPage(t, b, messagingRoute)
		res, err := call(t, &LinkedInBot{}, p, "list_conversations", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := names(res); got != "Gia Unread,Jon Waiting" {
			t.Fatalf("names = %s", got)
		}
		c := res.([]map[string]interface{})[0]
		if c["url"] != "https://www.linkedin.com/messaging/thread/2-AAAgia/" || c["snippet"] != "Are you free Tuesday?" || c["unread"] != true {
			t.Fatalf("first = %v", c)
		}
		if len(rec.Matching("GET", "https://www.linkedin.com/messaging/thread/*")) != 0 {
			t.Fatal("listing opened a conversation")
		}
	})

	t.Run("all, max", func(t *testing.T) {
		p, _ := newPage(t, b, messagingRoute)
		res, err := call(t, &LinkedInBot{}, p, "list_conversations", 3, "false")
		if err != nil {
			t.Fatal(err)
		}
		if got := names(res); got != "Gia Unread,Hal Replied,Ivy Read" {
			t.Fatalf("names = %s", got)
		}
		if res.([]map[string]interface{})[1]["last_from_me"] != true {
			t.Fatalf("Hal = %v", res.([]map[string]interface{})[1])
		}
	})
}
