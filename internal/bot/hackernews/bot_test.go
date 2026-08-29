//go:build social

package hackernews

import (
	"strings"
	"testing"
)

func TestExtractItemID(t *testing.T) {
	cases := map[string]string{
		"https://news.ycombinator.com/item?id=12345":     "12345",
		"https://news.ycombinator.com/item?id=12345&p=2": "12345",
		"item?id=999":                         "999",
		"https://news.ycombinator.com/newest": "",
	}
	b := &HackerNewsBot{}
	for in, want := range cases {
		if got := b.ExtractUsername(in); got != "" {
			t.Errorf("ExtractUsername(%q) should be empty for non-profile URLs, got %q", in, got)
		}
		if got := extractItemID(in); got != want {
			t.Errorf("extractItemID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHackerNewsResolveURL(t *testing.T) {
	b := &HackerNewsBot{}
	if got := b.ResolveURL("/item?id=1"); got != "https://news.ycombinator.com/item?id=1" {
		t.Errorf("ResolveURL relative = %q", got)
	}
	if got := b.ResolveURL("https://news.ycombinator.com/item?id=1"); got != "https://news.ycombinator.com/item?id=1" {
		t.Errorf("ResolveURL absolute changed unexpectedly: %q", got)
	}
}

// TestParseSubmittedItem is an honesty regression test: SubmitPost previously
// returned a nil map with a nil error when the read-back script found no
// tr.athing row (HN rejected the submission — duplicate URL, rate limit),
// which callers interpreted as success with no item. A null read-back must
// now surface as an explicit "submission not confirmed" error.
func TestParseSubmittedItem(t *testing.T) {
	// Confirmed submission → parsed map.
	item, err := parseSubmittedItem(`{"id":"42","title":"Hello","url":"https://news.ycombinator.com/item?id=42"}`)
	if err != nil {
		t.Fatalf("valid item should parse, got: %v", err)
	}
	if item["id"] != "42" {
		t.Fatalf("expected id 42, got %v", item["id"])
	}

	// Null read-back (nothing submitted) → error, not silent success.
	_, err = parseSubmittedItem(`null`)
	if err == nil {
		t.Fatal("null read-back must be an error, not a nil result")
	}
	if !strings.Contains(err.Error(), "submission not confirmed") {
		t.Fatalf("expected 'submission not confirmed' error, got: %v", err)
	}

	// Malformed JSON → parse error.
	_, err = parseSubmittedItem(`{not json`)
	if err == nil || !strings.Contains(err.Error(), "parsing submitted item JSON") {
		t.Fatalf("expected JSON parse error, got: %v", err)
	}
}
