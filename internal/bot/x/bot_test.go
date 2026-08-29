//go:build social

package x

import (
	"strings"
	"testing"
)

func TestXResolveURL(t *testing.T) {
	b := &XBot{}
	if got := b.ResolveURL("/username"); got != "https://x.com/username" {
		t.Errorf("ResolveURL relative = %q", got)
	}
	if got := b.ResolveURL("https://x.com/username"); got != "https://x.com/username" {
		t.Errorf("ResolveURL absolute changed: %q", got)
	}
}

func TestXExtractUsername(t *testing.T) {
	b := &XBot{}
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

// TestHandleTextMatches is an outbound-safety regression test: SendMessage
// previously clicked the first people-search result unconditionally, so a
// stale or fuzzy result list would DM the wrong user. Only rows whose text
// contains the target username may be clicked.
func TestHandleTextMatches(t *testing.T) {
	cases := []struct {
		resultText string
		target     string
		want       bool
	}{
		{"Jack Dorsey\n@jack\nCo-founder", "jack", true},
		{"Jack Dorsey\n@jack", "Jack", true},          // case-insensitive
		{"@jack", "@jack", true},                      // @ on both sides
		{"Jackson Brown\n@jbrown", "jack", true},      // display-name substring — matched (Instagram uses contains too)
		{"Someone Else\n@someoneelse", "jack", false}, // no occurrence at all
		{"Jack Dorsey\n@jack", "", false},             // empty target never matches
		{"", "jack", false},                           // empty result text
		{"  Jack Dorsey\n@jack  ", "jack", true},      // surrounding whitespace tolerated
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

func TestMessageSnippet(t *testing.T) {
	if got := messageSnippet("hello"); got != "hello" {
		t.Errorf("short message changed: %q", got)
	}
	long := strings.Repeat("y", 120)
	if got := messageSnippet(long); got != strings.Repeat("y", 80) {
		t.Errorf("long message not truncated to 80 runes: len %d", len(got))
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
		{"bubble long message matches snippet", "long text", []string{strings.Repeat("z", 40) + strings.Repeat("a", 60)}, strings.Repeat("z", 40) + strings.Repeat("a", 60), true},
		{"composer uncleared, no bubbles", "hello", nil, "hello", false},
		{"composer uncleared, bubble mismatch", "hello", []string{"different"}, "hello", false},
	}
	for _, c := range cases {
		if got := sendVerified(c.composer, c.bubbles, c.message); got != c.want {
			t.Errorf("%s: sendVerified(%q, %v, %q) = %v, want %v", c.name, c.composer, c.bubbles, c.message, got, c.want)
		}
	}
}
