//go:build !nosocial

package x

// Real-browser tests: bottest launches a headless Chromium (only when
// $BOTTEST_BROWSER / $JEV_E2E_BROWSER is set) and serves x.com URLs from the
// synthetic fixtures in testdata/, with an X-like CSP that blocks eval — so
// every page script the bot runs goes through the CDP path, as it does on
// the extension page in production.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/browser"
)

const xCSP = "script-src 'self' 'unsafe-inline'"

// xRoutes maps x.com URLs to fixtures. First match wins.
func xRoutes() []bottest.Route {
	return []bottest.Route{
		{Pattern: "https://x.com/i/flow/login*", File: "testdata/login.html"},
		{Pattern: "https://x.com/synth_loggedout*", File: "testdata/redirect_login.html"},
		{Pattern: "https://x.com/synth_missing", File: "testdata/profile_missing.html"},
		{Pattern: "https://x.com/synth_dave", File: "testdata/profile_xweb.html"},
		{Pattern: "https://x.com/search?q=zzzsynthnothing*", File: "testdata/search_empty.html"},
		{Pattern: "https://x.com/search?q=*", File: "testdata/search.html"},
		{Pattern: "https://x.com/*/status/*", File: "testdata/status.html"},
		{Pattern: "https://x.com/*/followers", File: "testdata/followers.html"},
		{Pattern: "https://x.com/*/verified_followers", File: "testdata/followers.html"},
		{Pattern: "https://x.com/*/following", File: "testdata/followers.html"},
		{Pattern: "https://x.com/compose/post", File: "testdata/compose.html"},
		{Pattern: "https://x.com/home", File: "testdata/compose.html"},
		{Pattern: "https://x.com/messages", File: "testdata/inbox.html"},
		{Pattern: "https://x.com/i/chat", File: "testdata/chat_inbox.html"},
		{Pattern: "https://x.com/i/chat/pin/*", File: "testdata/chat_pin.html"},
		{Pattern: "https://x.com/messages/*", File: "testdata/thread.html"},
		{Pattern: "https://x.com/i/chat/*", File: "testdata/thread.html"},
		{Pattern: "https://x.com/*", File: "testdata/profile.html"},
		{Pattern: "https://pbs.example.test/*", Body: "", ContentType: "image/jpeg"},
	}
}

// fastTimings shortens the bot's waits for fixtures (restored on cleanup).
func fastTimings(t *testing.T) {
	t.Helper()
	old := []time.Duration{loadTimeout, pollInterval, verifyTimeout, scrollSettle, actionPause}
	loadTimeout, pollInterval, verifyTimeout, scrollSettle, actionPause = 6*time.Second, 100*time.Millisecond, 3*time.Second, 400*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() {
		loadTimeout, pollInterval, verifyTimeout, scrollSettle, actionPause = old[0], old[1], old[2], old[3], old[4]
	})
}

// xPage opens a fresh page on b serving the fixtures (plus extra routes,
// which take precedence).
func xPage(t *testing.T, b *bottest.Browser, extra ...bottest.Route) *bottest.Page {
	t.Helper()
	p := b.NewPage(t)
	p.Serve(append(extra, xRoutes()...)...)
	p.SetCSP(xCSP)
	return p
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// js evaluates expr on the page and returns its JSON-decoded value.
func js(t *testing.T, p *bottest.Page, expr string) interface{} {
	t.Helper()
	v, err := p.EvalCDP(expr)
	if err != nil {
		t.Fatalf("EvalCDP(%s): %v", expr, err)
	}
	return v
}

func jsString(t *testing.T, p *bottest.Page, expr string) string {
	t.Helper()
	s, _ := js(t, p, expr).(string)
	return s
}

func urlOf(t *testing.T, p browser.PageInterface) string {
	t.Helper()
	u, err := p.GetURL()
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func wantErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one containing %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err, substr)
	}
}

func ids(rows []map[string]interface{}, key string) []string {
	var out []string
	for _, r := range rows {
		s, _ := r[key].(string)
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// READ: get_profile_data
// ---------------------------------------------------------------------------

func TestBrowserProfile(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("signed-in layout", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "get_profile_data", "https://twitter.com/synth_alice?ref=x")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]interface{})
		want := map[string]interface{}{
			"username": "synth_alice", "profile_url": "https://x.com/synth_alice", "full_name": "Synth Alice",
			"bio": "Building imaginary things. Tea & tests.", "location": "Nowhere, Testland",
			"website": "synth-alice.example", "website_href": "https://t.co/synthetic1", "join_date": "Joined March 2011",
			"is_verified": true, "is_protected": false, "can_dm": true,
			"followers_count": int64(12500), "following_count": int64(321),
			"followers_text": "12.5K", "following_text": "321",
			"profile_picture_url": "https://pbs.example.test/avatars/synthetic_400x400.jpg",
			"banner_url":          "https://pbs.example.test/banners/synthetic_1500x500.jpg",
		}
		for k, v := range want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s = %#v, want %#v", k, got[k], v)
			}
		}
		// GetProfileData (BotAdapter) scrapes the page already loaded.
		again, err := bot.GetProfileData(ctxT(t), p)
		if err != nil || again["full_name"] != "Synth Alice" {
			t.Errorf("GetProfileData = %v, %v", again, err)
		}
		if ok, err := bot.IsLoggedIn(p); err != nil || !ok {
			t.Errorf("IsLoggedIn on a signed-in page = %v, %v", ok, err)
		}
	})

	t.Run("profile without DM button", func(t *testing.T) {
		p := xPage(t, b)
		got, err := bot.GetProfile(ctxT(t), p, "@synth_bob")
		if err != nil {
			t.Fatal(err)
		}
		if got["username"] != "synth_bob" || got["full_name"] != "Synth Bob" || got["can_dm"] != false || got["is_verified"] != false || got["followers_count"] != int64(12500) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("newer layout without data-testids", func(t *testing.T) {
		p := xPage(t, b)
		got, err := bot.GetProfile(ctxT(t), p, "synth_dave")
		if err != nil {
			t.Fatal(err)
		}
		// The logged-out layout marks bio/location/website/join date only by
		// structure (dir=auto bio, svg[data-icon] meta rows); the timeline
		// below the header must not leak into them.
		want := map[string]interface{}{
			"username": "synth_dave", "profile_url": "https://x.com/synth_dave", "full_name": "Synth Dave",
			"bio": "just testing", "location": "Somewhere, Synthland",
			"website": "dave.example", "website_href": "https://t.co/synthdave", "join_date": "Joined February 2007",
			"is_verified": true, "is_protected": false, "can_dm": false,
			"followers_count": int64(60700000), "following_count": int64(7),
			"followers_text": "60.7m", "following_text": "7",
			"profile_picture_url": "https://pbs.example.test/avatars/dave_400x400.jpg",
			"banner_url":          "https://pbs.example.test/banners/dave_1500x500",
		}
		for k, v := range want {
			if !reflect.DeepEqual(got[k], v) {
				t.Errorf("%s = %#v, want %#v", k, got[k], v)
			}
		}
	})

	t.Run("missing account", func(t *testing.T) {
		_, err := bot.GetProfile(ctxT(t), xPage(t, b), "synth_missing")
		wantErr(t, err, "doesn’t exist")
	})

	t.Run("signed out", func(t *testing.T) {
		p := xPage(t, b)
		_, err := bot.GetProfile(ctxT(t), p, "synth_loggedout")
		if !errors.Is(err, errNotLoggedIn) {
			t.Fatalf("err = %v, want errNotLoggedIn", err)
		}
		if ok, _ := bot.IsLoggedIn(p); ok {
			t.Error("IsLoggedIn on the login flow = true")
		}
	})
}

// ---------------------------------------------------------------------------
// READ: search_posts
// ---------------------------------------------------------------------------

func TestBrowserSearchPosts(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("first results, ad skipped, Admin/Add kept", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "search_posts", "synthkeyword", float64(3))
		if err != nil {
			t.Fatal(err)
		}
		list := res.([]interface{})
		var rows []map[string]interface{}
		for _, r := range list {
			rows = append(rows, r.(map[string]interface{}))
		}
		if got := ids(rows, "id"); !reflect.DeepEqual(got, []string{"2001", "2002", "2003"}) {
			t.Fatalf("ids = %v", got)
		}
		r := rows[0]
		if r["url"] != "https://x.com/synth_erin/status/2001" || r["author_username"] != "synth_erin" || r["author_name"] != "Synth Erin" ||
			r["post_text"] != "Quiet morning with #synthkeyword" || r["like_count"] != int64(12) || r["reply_count"] != int64(5) ||
			r["repost_count"] != int64(2) || r["liked"] != false || r["created_at"] != "2026-09-20T10:00:00.000Z" || r["keyword"] != "synthkeyword" {
			t.Errorf("row 0 = %v", r)
		}
		if rows[1]["liked"] != true || rows[1]["like_count"] != int64(30) {
			t.Errorf("row 1 = %v", rows[1])
		}
		if rows[2]["author_name"] != "Admin Person" || rows[2]["like_count"] != int64(4321) || rows[2]["reply_count"] != int64(1200) {
			t.Errorf("row 2 = %v", rows[2])
		}
		// No src: typed_query would mark the search as typed into the box.
		if u := urlOf(t, p); u != "https://x.com/search?q=synthkeyword" {
			t.Errorf("searched at %s", u)
		}
	})

	t.Run("scrolls a virtualised timeline", func(t *testing.T) {
		rows, err := bot.SearchPosts(ctxT(t), xPage(t, b), "synthkeyword", 7)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"2001", "2002", "2003", "2004", "2005", "2006", "2007"}
		if got := ids(rows, "id"); !reflect.DeepEqual(got, want) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	})

	t.Run("stops at the end", func(t *testing.T) {
		rows, err := bot.SearchPosts(ctxT(t), xPage(t, b), "synthkeyword", 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 8 {
			t.Fatalf("got %d rows (%v), want all 8 non-ad posts", len(rows), ids(rows, "id"))
		}
	})

	t.Run("no results", func(t *testing.T) {
		rows, err := bot.SearchPosts(ctxT(t), xPage(t, b), "zzzsynthnothing", 10)
		if err != nil || len(rows) != 0 {
			t.Fatalf("rows = %v, err = %v", rows, err)
		}
	})
}

// ---------------------------------------------------------------------------
// READ: list_followers
// ---------------------------------------------------------------------------

func TestBrowserListFollowers(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("followers, scrolled, sidebar excluded", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "list_followers", "https://x.com/synth_alice", "FOLLOWERS_FETCH", float64(100))
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]interface{}
		for _, r := range res.([]interface{}) {
			rows = append(rows, r.(map[string]interface{}))
		}
		want := []string{"synth_f1", "synth_f2", "synth_f3", "synth_f4", "synth_f5", "synth_f6", "synth_f7"}
		if got := ids(rows, "username"); !reflect.DeepEqual(got, want) {
			t.Fatalf("usernames = %v", got)
		}
		f1 := rows[0]
		if f1["full_name"] != "Follower One" || f1["bio"] != "likes tea" || f1["follows_you"] != true || f1["is_verified"] != true ||
			f1["is_following"] != false || f1["profile_url"] != "https://x.com/synth_f1" || f1["source"] != "followers" {
			t.Errorf("f1 = %v", f1)
		}
		if rows[1]["is_following"] != true || rows[1]["bio"] != "" {
			t.Errorf("f2 = %v", rows[1])
		}
		if rows[2]["bio"] != "line one\nline two" || rows[2]["follows_you"] != false {
			t.Errorf("f3 = %v", rows[2])
		}
		// The follow button's hidden aria description is never bio text.
		for _, r := range rows {
			if bio, _ := r["bio"].(string); strings.Contains(bio, "Click to") {
				t.Errorf("%s bio leaks the follow button description: %q", r["username"], bio)
			}
		}
		if rows[3]["bio"] != "four" || rows[4]["bio"] != "five" {
			t.Errorf("f4/f5 bios = %q / %q", rows[3]["bio"], rows[4]["bio"])
		}
		if u := urlOf(t, p); u != "https://x.com/synth_alice/followers" {
			t.Errorf("list opened at %s", u)
		}
	})

	for _, c := range []struct {
		source string
		want   []string
	}{
		{"FOLLOWING_FETCH", []string{"synth_g1", "synth_g2"}},
		{"VERIFIED_FOLLOWERS_FETCH", []string{"synth_v1"}},
	} {
		t.Run(c.source, func(t *testing.T) {
			rows, err := bot.ListFollowers(ctxT(t), xPage(t, b), "@synth_alice", c.source, 10)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(rows, "username"); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("usernames = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("max", func(t *testing.T) {
		rows, err := bot.ListFollowers(ctxT(t), xPage(t, b), "synth_alice", "", 2)
		if err != nil || len(rows) != 2 {
			t.Fatalf("rows = %v, err = %v", rows, err)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		rows, err := bot.ListFollowers(ctxT(t), xPage(t, b), "synth_nobody", "FOLLOWERS_FETCH", 10)
		if err != nil || len(rows) != 0 {
			t.Fatalf("rows = %v, err = %v", rows, err)
		}
	})
}

// ---------------------------------------------------------------------------
// WRITE: like_post / reply_post
// ---------------------------------------------------------------------------

func statusURL(id string) string { return "https://x.com/synth_alice/status/" + id }

// targetLike reads the target post's like control state.
func targetLike(t *testing.T, p *bottest.Page) string {
	return jsString(t, p, `(() => { const a = [...document.querySelectorAll('article')].find((x) => x.textContent.includes('the target post')); if (!a) return 'none';
		const b = a.querySelector('button.like'); return (b.getAttribute('data-testid') || '-') + '|' + (b.getAttribute('aria-pressed') || '-'); })()`)
}

func wrongLikes(t *testing.T, p *bottest.Page) float64 {
	n, _ := js(t, p, `window.__wrongLike || 0`).(float64)
	return n
}

func TestBrowserLikePost(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("likes the target post, not the parent above it", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "like_post", "https://twitter.com/synth_alice/status/1001?s=20")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]interface{})
		if got["liked"] != true || got["already_liked"] != false || got["url"] != statusURL("1001") {
			t.Errorf("result = %v", got)
		}
		if s := targetLike(t, p); s != "unlike|-" {
			t.Errorf("target like state = %s", s)
		}
		if n := wrongLikes(t, p); n != 0 {
			t.Errorf("%v clicks on other posts' Like buttons", n)
		}
	})

	t.Run("already liked is left alone", func(t *testing.T) {
		p := xPage(t, b)
		got, err := bot.LikePost(ctxT(t), p, statusURL("1003"))
		if err != nil {
			t.Fatal(err)
		}
		if got["already_liked"] != true {
			t.Errorf("result = %v", got)
		}
		if s := targetLike(t, p); s != "unlike|-" || wrongLikes(t, p) != 0 {
			t.Errorf("state = %s, wrong likes %v", s, wrongLikes(t, p))
		}
	})

	t.Run("like not confirmed", func(t *testing.T) {
		_, err := bot.LikePost(ctxT(t), xPage(t, b), statusURL("1005"))
		wantErr(t, err, "not confirmed")
	})

	t.Run("button without data-testid and no Jev: fails, clicks nothing", func(t *testing.T) {
		p := xPage(t, b)
		_, err := bot.LikePost(ctxT(t), p, statusURL("1004"))
		wantErr(t, err, "could not find the Like button")
		if s := targetLike(t, p); s != "-|-" || wrongLikes(t, p) != 0 {
			t.Errorf("state = %s, wrong likes %v", s, wrongLikes(t, p))
		}
	})

	t.Run("post gone: a reply quoting it is never used", func(t *testing.T) {
		p := xPage(t, b)
		_, err := bot.LikePost(ctxT(t), p, statusURL("1008"))
		wantErr(t, err, "not found")
		if wrongLikes(t, p) != 0 {
			t.Error("liked another post")
		}
	})

	t.Run("no action bar", func(t *testing.T) {
		_, err := bot.LikePost(ctxT(t), xPage(t, b), statusURL("1007"))
		wantErr(t, err, "could not find the Like button")
	})

	t.Run("signed out", func(t *testing.T) {
		_, err := bot.LikePost(ctxT(t), xPage(t, b), "https://x.com/synth_loggedout/status/1")
		if !errors.Is(err, errNotLoggedIn) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBrowserReplyPost(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("replies through the post's dialog", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "reply_post", statusURL("1001"), "nice synthetic post")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]interface{})
		if got["replied"] != true || got["reply_url"] != "https://x.com/synth_alice/status/1101" {
			t.Errorf("result = %v", got)
		}
		if r := js(t, p, `JSON.stringify(window.__replies)`); r != `["nice synthetic post"]` {
			t.Errorf("replies posted = %v", r)
		}
	})

	t.Run("X refuses the reply", func(t *testing.T) {
		_, err := bot.ReplyPost(ctxT(t), xPage(t, b), statusURL("1006"), "nice synthetic post")
		wantErr(t, err, "You already said that")
	})

	t.Run("no reply button", func(t *testing.T) {
		_, err := bot.ReplyPost(ctxT(t), xPage(t, b), statusURL("1007"), "hi")
		wantErr(t, err, "could not find the Reply button")
	})
}

// ---------------------------------------------------------------------------
// WRITE: publish_post
// ---------------------------------------------------------------------------

func TestBrowserPublishPost(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("compose dialog", func(t *testing.T) {
		p := xPage(t, b)
		res, err := bottest.CallMethod(t, bot, p, "publish_post", "hello synthetic world")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]interface{})
		if got["posted"] != true || got["composer"] != "dialog" || got["post_url"] != "https://x.com/synth_me/status/5001" {
			t.Errorf("result = %v", got)
		}
		if r := js(t, p, `JSON.stringify(window.__posts)`); r != `[{"text":"hello synthetic world","files":0}]` {
			t.Errorf("posts = %v", r)
		}
	})

	t.Run("falls back to the inline composer on /home", func(t *testing.T) {
		p := xPage(t, b, bottest.Route{Pattern: "https://x.com/compose/post", Body: "<!doctype html><title>X</title><p>Something went wrong.</p>"})
		got, err := bot.PublishPost(ctxT(t), p, "inline synthetic post", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got["composer"] != "inline" || got["post_url"] != "https://x.com/synth_me/status/5001" {
			t.Errorf("result = %v", got)
		}
		if r := js(t, p, `JSON.stringify(window.__posts)`); r != `[{"text":"inline synthetic post","files":0}]` {
			t.Errorf("posts = %v", r)
		}
	})

	t.Run("with media", func(t *testing.T) {
		img := filepath.Join(t.TempDir(), "synthetic.png")
		if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nsynthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := xPage(t, b)
		fn, _ := bot.GetMethodByName("publish_post")
		if _, err := fn(ctxT(t), p, "with a picture", []interface{}{img}); err != nil {
			t.Fatal(err)
		}
		if r := js(t, p, `JSON.stringify(window.__posts)`); r != `[{"text":"with a picture","files":1}]` {
			t.Errorf("posts = %v", r)
		}
	})

	t.Run("X refuses the post", func(t *testing.T) {
		_, err := bot.PublishPost(ctxT(t), xPage(t, b), "a dup post", nil)
		wantErr(t, err, "You already said that")
	})

	t.Run("empty text", func(t *testing.T) {
		_, err := bot.PublishPost(ctxT(t), xPage(t, b), "  ", nil)
		wantErr(t, err, "text is required")
	})
}

// ---------------------------------------------------------------------------
// WRITE: send_dm / reply_dm, READ: list_conversations
// ---------------------------------------------------------------------------

func sentDMs(t *testing.T, p *bottest.Page) string {
	return jsString(t, p, `JSON.stringify(window.__sent || null)`)
}

func TestBrowserDMs(t *testing.T) {
	b := bottest.Launch(t)
	fastTimings(t)
	bot := New()

	t.Run("send_dm from the profile's Message button", func(t *testing.T) {
		p := xPage(t, b)
		// The thread already holds this exact text: only a new bubble counts.
		res, err := bottest.CallMethod(t, bot, p, "send_dm", "https://x.com/synth_alice", "Thanks for the message!")
		if err != nil {
			t.Fatal(err)
		}
		got := res.(map[string]interface{})
		if got["sent"] != true || got["recipient"] != "synth_alice" || got["verified_by"] != "bubble" ||
			got["conversation_url"] != "https://x.com/messages/100-200" {
			t.Errorf("result = %v", got)
		}
		if s := sentDMs(t, p); s != `["Thanks for the message!"]` {
			t.Errorf("sent = %s", s)
		}
	})

	t.Run("no Message button: fails without messaging anyone", func(t *testing.T) {
		p := xPage(t, b)
		_, err := bot.SendDM(ctxT(t), p, "synth_bob", "hello")
		wantErr(t, err, "cannot be messaged")
		if u := urlOf(t, p); u != "https://x.com/synth_bob" {
			t.Errorf("navigated to %s", u)
		}
	})

	t.Run("send not confirmed", func(t *testing.T) {
		p := xPage(t, b)
		_, err := bot.ReplyDM(ctxT(t), p, "https://x.com/messages/100-400", "Thanks for the message!")
		wantErr(t, err, "could not be verified")
	})

	t.Run("reply_dm in the chat UI", func(t *testing.T) {
		p := xPage(t, b)
		// The thread already holds this text: only a new message-text-<id>
		// bubble confirms the send.
		got, err := bot.ReplyDM(ctxT(t), p, "/i/chat/1111111111-2222222222", "Thanks for the message!")
		if err != nil {
			t.Fatal(err)
		}
		if got["sent"] != true || got["conversation_url"] != "https://x.com/i/chat/1111111111-2222222222" || got["verified_by"] != "bubble" {
			t.Errorf("result = %v", got)
		}
		if s := sentDMs(t, p); s != `["Thanks for the message!"]` {
			t.Errorf("sent = %s", s)
		}
	})

	t.Run("reply_dm: X Chat locked behind its passcode", func(t *testing.T) {
		p := xPage(t, b, bottest.Route{Pattern: "https://x.com/i/chat/1111111111-2222222222",
			Body: `<!doctype html><script>location.replace('/i/chat/pin/verify')</script>`})
		_, err := bot.ReplyDM(ctxT(t), p, "https://x.com/i/chat/1111111111-2222222222", "hello")
		wantErr(t, err, "passcode")
	})

	t.Run("list_conversations: unread only, never last-from-me", func(t *testing.T) {
		res, err := bottest.CallMethod(t, bot, xPage(t, b), "list_conversations")
		if err != nil {
			t.Fatal(err)
		}
		var urls []string
		for _, r := range res.([]interface{}) {
			urls = append(urls, r.(map[string]interface{})["url"].(string))
		}
		want := []string{"https://x.com/messages/100-200", "https://x.com/messages/100-500"}
		if !reflect.DeepEqual(urls, want) {
			t.Fatalf("urls = %v, want %v", urls, want)
		}
	})

	t.Run("list_conversations: all awaiting a reply", func(t *testing.T) {
		rows, err := bot.ListConversations(ctxT(t), xPage(t, b), 10, false)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"https://x.com/messages/100-200", "https://x.com/messages/100-400", "https://x.com/messages/100-500"}
		if got := ids(rows, "url"); !reflect.DeepEqual(got, want) {
			t.Fatalf("urls = %v, want %v", got, want)
		}
		if rows[0]["name"] != "Synth Alice" || rows[0]["preview"] != "hey, are you around?" {
			t.Errorf("row 0 = %v", rows[0])
		}
	})

	t.Run("list_conversations: chat UI", func(t *testing.T) {
		p := xPage(t, b, bottest.Route{Pattern: "https://x.com/messages", Body: `<!doctype html><script>location.replace('/i/chat')</script>`})
		rows, err := bot.ListConversations(ctxT(t), p, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		// Requests / Grok rows are not conversations; Finn's last message is
		// ours ("You: …").
		want := []string{"https://x.com/i/chat/1111111111-2222222222", "https://x.com/i/chat/1111111111-4444444444", "https://x.com/i/chat/g1555555555"}
		if got := ids(rows, "url"); !reflect.DeepEqual(got, want) {
			t.Fatalf("urls = %v, want %v", got, want)
		}
		if rows[0]["name"] != "Synth Eve" || rows[0]["preview"] != "ping from eve" || rows[0]["unread"] != true {
			t.Errorf("row 0 = %v", rows[0])
		}
		if rows[1]["unread"] != false || rows[2]["unread"] != true || rows[2]["preview"] != "Synth Hal: see you there" {
			t.Errorf("rows = %v", rows)
		}
	})

	t.Run("list_conversations: chat UI, unread only", func(t *testing.T) {
		rows, err := bot.ListConversations(ctxT(t), xPage(t, b, bottest.Route{Pattern: "https://x.com/messages", Body: `<!doctype html><script>location.replace('/i/chat')</script>`}), 10, true)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(rows, "url"); !reflect.DeepEqual(got, []string{"https://x.com/i/chat/1111111111-2222222222", "https://x.com/i/chat/g1555555555"}) {
			t.Fatalf("urls = %v", got)
		}
	})

	t.Run("list_conversations: X Chat passcode gate", func(t *testing.T) {
		p := xPage(t, b, bottest.Route{Pattern: "https://x.com/messages", Body: `<!doctype html><script>location.replace('/i/chat/pin/new?from=%2Fi%2Fchat')</script>`})
		_, err := bot.ListConversations(ctxT(t), p, 10, false)
		wantErr(t, err, "passcode")
	})
}
