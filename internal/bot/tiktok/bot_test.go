//go:build social

package tiktok

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/browser"
)

// TestGetMethodByNameAcceptsPageInterface is a regression test: the executor
// (internal/action/steps.go) prepends ae.page — declared type
// browser.PageInterface, concretely *browser.RodPage or *browser.ExtensionPage
// — as args[0] to every call_bot_method dispatch. GetMethodByName's returned
// closures previously asserted args[0].(*rod.Page), a concrete type that is
// never the runtime value, so every call_bot_method step against this bot
// failed with "first arg must be *rod.Page" instead of reaching the actual
// action method.
func TestGetMethodByNameAcceptsPageInterface(t *testing.T) {
	b := &TikTokBot{}
	fn, ok := b.GetMethodByName("list_user_videos")
	if !ok {
		t.Fatal("expected list_user_videos method to be found")
	}

	// ListUserVideos validates profileURL before ever touching the page, so
	// an empty profileURL exercises the args[0] type assertion and returns
	// cleanly without needing a live browser page.
	var page browser.PageInterface = browser.NewRodPage(nil)
	_, err := fn(context.Background(), page, "", 0)
	if err == nil {
		t.Fatal("expected an error for empty profileURL")
	}
	if strings.Contains(err.Error(), "must be *rod.Page") {
		t.Fatalf("closure rejected a browser.PageInterface value: %v", err)
	}
	if !strings.Contains(err.Error(), "profileURL is required") {
		t.Fatalf("expected the profileURL validation error, got: %v", err)
	}
}

// TestHandleTextMatches is an outbound-safety regression test: the messages
// search fallback previously clicked the first user-search result
// unconditionally, so a stale or fuzzy result list would DM the wrong user.
// Only rows whose text contains the target username may be clicked.
func TestHandleTextMatches(t *testing.T) {
	cases := []struct {
		resultText string
		target     string
		want       bool
	}{
		{"Display Name\n@targetuser\n1.2K friends", "targetuser", true},
		{"Display Name\n@TargetUser", "targetuser", true}, // case-insensitive
		{"@targetuser", "@targetuser", true},              // @ on both sides
		{"Someone Else\n@otheruser", "targetuser", false}, // no occurrence at all
		{"Display Name\n@targetuser", "", false},          // empty target never matches
		{"", "targetuser", false},                         // empty result text
	}
	for _, c := range cases {
		if got := handleTextMatches(c.resultText, c.target); got != c.want {
			t.Errorf("handleTextMatches(%q, %q) = %v, want %v", c.resultText, c.target, got, c.want)
		}
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

// TestSendVerified covers the honest post-send gate: a cleared composer
// confirms the send, a rendered bubble confirms the send, and anything else
// is a verification failure that must surface as an error.
func TestSendVerified(t *testing.T) {
	cases := []struct {
		name     string
		composer string
		bubbles  []string
		message  string
		want     bool
	}{
		{"composer cleared", "", nil, "hello", true},
		{"composer whitespace only", "   \n", nil, "hello", true},
		{"bubble contains message", "hello", []string{"earlier msg", "hello"}, "hello", true},
		{"composer uncleared, no bubbles", "hello", nil, "hello", false},
		{"composer uncleared, bubble mismatch", "hello", []string{"different"}, "hello", false},
	}
	for _, c := range cases {
		if got := sendVerified(c.composer, c.bubbles, c.message); got != c.want {
			t.Errorf("%s: sendVerified(%q, %v, %q) = %v, want %v", c.name, c.composer, c.bubbles, c.message, got, c.want)
		}
	}
}

// TestStitchDuetDispatchReturnsHonestStatus asserts the call_bot_method
// wrappers exist for stitch/duet and that arg validation still works without
// a live page. The honest statuses themselves ("opened_stitch_editor" /
// "opened_duet_editor") are returned by StitchVideo/DuetVideo after the
// editor is opened — publishing is not claimed.
func TestStitchDuetDispatchReturnsHonestStatus(t *testing.T) {
	b := &TikTokBot{}
	for _, name := range []string{"stitch_video", "duet_video"} {
		fn, ok := b.GetMethodByName(name)
		if !ok {
			t.Fatalf("expected %s method to be found", name)
		}
		// Too few args → arg-count error (no page touched).
		if _, err := fn(nil); err == nil {
			t.Fatalf("%s: expected an error for missing args", name)
		}
		// Wrong page type → type error, mentions the arg position.
		if _, err := fn(nil, "not-a-page", "https://tiktok.com/x"); err == nil || !strings.Contains(err.Error(), "browser.PageInterface") {
			t.Fatalf("%s: expected PageInterface type error, got: %v", name, err)
		}
	}
}
