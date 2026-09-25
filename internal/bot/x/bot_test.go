//go:build !nosocial

package x

import (
	"context"
	"strings"
	"testing"
)

func TestXResolveURL(t *testing.T) {
	b := New()
	if got := b.ResolveURL("/username"); got != "https://x.com/username" {
		t.Errorf("ResolveURL relative = %q", got)
	}
	if got := b.ResolveURL("https://x.com/username"); got != "https://x.com/username" {
		t.Errorf("ResolveURL absolute changed: %q", got)
	}
}

func TestXExtractUsername(t *testing.T) {
	b := New()
	cases := map[string]string{
		"https://x.com/jack":            "jack",
		"https://x.com/jack/status/123": "jack",
		"https://x.com/home":            "", // reserved
		"https://x.com/notifications":   "", // reserved
		"https://x.com/messages":        "", // reserved
		"https://x.com/":                "",
		"":                              "",
	}
	for in, want := range cases {
		if got := b.ExtractUsername(in); got != want {
			t.Errorf("ExtractUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXPlatform(t *testing.T) {
	if (&XBot{}).Platform() != "X" {
		t.Error("Platform() should be X")
	}
}

func TestTruncateForError(t *testing.T) {
	if got := truncateForError("short", 80); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := strings.Repeat("x", 100)
	got := truncateForError(long, 80)
	if len([]rune(got)) != 81 || !strings.HasSuffix(got, "…") {
		t.Errorf("long string not truncated to 80 runes + ellipsis: %q (len %d)", got, len(got))
	}
}

func TestMessageSnippet(t *testing.T) {
	if got := messageSnippet("hello"); got != "hello" {
		t.Errorf("short message changed: %q", got)
	}
	long := strings.Repeat("y", 120)
	if got := messageSnippet(long); got != strings.Repeat("y", 80) {
		t.Errorf("long message not truncated to 80 runes: len %d", len(got))
	}
}

// TestSendVerified covers the honest post-send gate: a new bubble with the
// message confirms the send, a composer that held the typed text and is now
// empty (or gone) confirms it, and anything else is a verification failure.
func TestSendVerified(t *testing.T) {
	cases := []struct {
		name        string
		present     bool
		composer    string
		before, now int
		want        bool
		wantHow     sendOutcome
	}{
		{"new bubble", true, "hello", 0, 1, true, sentBubble},
		{"composer cleared", true, "", 0, 0, true, sentComposer},
		{"composer whitespace only", true, "  \n", 1, 1, true, sentComposer},
		{"composer removed", false, "", 0, 0, true, sentComposer},
		{"text still there, no new bubble", true, "hello", 1, 1, false, ""},
		{"text still there, old bubble only", true, "hello", 2, 2, false, ""},
	}
	for _, c := range cases {
		how, ok := sendVerified(c.present, c.composer, c.before, c.now)
		if ok != c.want || how != c.wantHow {
			t.Errorf("%s: sendVerified = %q, %v; want %q, %v", c.name, how, ok, c.wantHow, c.want)
		}
	}
}

func TestParseCount(t *testing.T) {
	cases := map[string]int64{
		"1,234":    1234,
		"12.5K":    12500,
		"60.7m":    60700000,
		"1.2M":     1200000,
		"3B":       3000000000,
		"987":      987,
		"12,5K":    12500,
		"0":        0,
		"1.234":    1234,
		"42 Likes": 42,
	}
	for in, want := range cases {
		if got, ok := parseCount(in); !ok || got != want {
			t.Errorf("parseCount(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Followers", "K"} {
		if _, ok := parseCount(in); ok {
			t.Errorf("parseCount(%q) ok, want no count", in)
		}
	}
}

func TestProfileURL(t *testing.T) {
	b := New()
	ok := map[string]string{
		"jack":                                "jack",
		"@jack":                               "jack",
		"/jack":                               "jack",
		"https://x.com/jack":                  "jack",
		"https://twitter.com/jack/":           "jack",
		"https://mobile.twitter.com/jack?s=1": "jack",
		"https://x.com/jack/status/1":         "jack",
	}
	for in, handle := range ok {
		u, h, err := b.profileURL(in)
		if err != nil || h != handle || u != "https://x.com/"+handle {
			t.Errorf("profileURL(%q) = %q, %q, %v", in, u, h, err)
		}
	}
	for _, in := range []string{"", "https://example.com/jack", "https://x.com/home", "not a handle!"} {
		if _, _, err := b.profileURL(in); err == nil {
			t.Errorf("profileURL(%q) accepted", in)
		}
	}
}

func TestPostURL(t *testing.T) {
	b := New()
	u, id, err := b.postURL("https://twitter.com/someone/status/1234567890?s=20")
	if err != nil || id != "1234567890" || u != "https://x.com/someone/status/1234567890" {
		t.Errorf("postURL = %q, %q, %v", u, id, err)
	}
	for _, in := range []string{"", "https://x.com/someone", "https://example.com/a/status/1"} {
		if _, _, err := b.postURL(in); err == nil {
			t.Errorf("postURL(%q) accepted", in)
		}
	}
}

func TestConversationURL(t *testing.T) {
	ok := map[string]string{
		"https://x.com/messages/111-222":   "https://x.com/messages/111-222",
		"/messages/111-222/":               "https://x.com/messages/111-222",
		"https://twitter.com/messages/333": "https://x.com/messages/333",
		"https://x.com/i/chat/abc-123":     "https://x.com/i/chat/abc-123",
	}
	for in, want := range ok {
		if got, err := conversationURL(in); err != nil || got != want {
			t.Errorf("conversationURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "https://x.com/messages", "https://x.com/messages/compose", "https://x.com/jack", "https://evil.test/messages/1-2"} {
		if _, err := conversationURL(in); err == nil {
			t.Errorf("conversationURL(%q) accepted", in)
		}
	}
}

func TestFollowListPath(t *testing.T) {
	cases := map[string]string{"": "followers", "FOLLOWERS_FETCH": "followers", "following_fetch": "following", "VERIFIED_FOLLOWERS_FETCH": "verified_followers"}
	for in, want := range cases {
		if got, err := followListPath(in); err != nil || got != want {
			t.Errorf("followListPath(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := followListPath("LIKERS"); err == nil {
		t.Error("unknown source type accepted")
	}
}

func TestIsLoginURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://x.com/i/flow/login":                   true,
		"https://x.com/login":                          true,
		"https://x.com/i/jf/onboarding/web?mode=login": true,
		"https://x.com/home":                           false,
		"https://x.com/loginhelper_fan":                false, // a profile, not the login page
		"https://x.com/someone/status/1":               false,
	} {
		if got := isLoginURL(in); got != want {
			t.Errorf("isLoginURL(%q) = %v", in, got)
		}
	}
}

func TestAlreadyLiked(t *testing.T) {
	for _, c := range []struct {
		info pickInfo
		want bool
	}{
		{pickInfo{TestID: "unlike"}, true},
		{pickInfo{Pressed: "true"}, true},
		{pickInfo{Label: "12 Likes. Liked"}, true},
		{pickInfo{Label: "Unlike"}, true},
		{pickInfo{TestID: "like", Label: "12 Likes. Like"}, false},
		{pickInfo{Label: "Likes"}, false},
	} {
		if got := alreadyLiked(c.info); got != c.want {
			t.Errorf("alreadyLiked(%+v) = %v", c.info, got)
		}
	}
}

func TestMediaPaths(t *testing.T) {
	if got := mediaPaths(" a.png , ,b.jpg"); len(got) != 2 || got[0] != "a.png" || got[1] != "b.jpg" {
		t.Errorf("string = %q", got)
	}
	if got := mediaPaths([]interface{}{"a.png", map[string]interface{}{"path": "b.jpg"}, 3}); len(got) != 2 {
		t.Errorf("list = %q", got)
	}
	if got := mediaPaths(nil); got != nil {
		t.Errorf("nil = %q", got)
	}
}

func TestGetMethodByName(t *testing.T) {
	b := New()
	for _, name := range []string{"get_profile_data", "search_posts", "list_followers", "like_post", "reply_post", "publish_post", "send_dm", "list_conversations", "reply_dm"} {
		fn, ok := b.GetMethodByName(name)
		if !ok || fn == nil {
			t.Errorf("%s missing", name)
			continue
		}
		if _, err := fn(context.Background()); err == nil {
			t.Errorf("%s without a page did not fail", name)
		}
	}
	if _, ok := b.GetMethodByName("follow_everyone"); ok {
		t.Error("unknown method resolved")
	}
}
