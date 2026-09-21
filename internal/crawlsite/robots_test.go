package crawlsite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRobotsStarGroup(t *testing.T) {
	rules := parseRobots([]byte(`
# comment
User-agent: *
Disallow: /private
Allow: /private/public
Crawl-delay: 2
`), "monoagent-crawl/1")

	if rules.allowed("/private/secret") {
		t.Error("/private/secret should be disallowed")
	}
	if !rules.allowed("/private/public/page") {
		t.Error("the longer Allow should win")
	}
	if !rules.allowed("/") {
		t.Error("/ should be allowed")
	}
	if rules.crawlDelay != 2*time.Second {
		t.Errorf("crawlDelay = %s, want 2s", rules.crawlDelay)
	}
}

func TestParseRobotsSpecificGroupBeatsStar(t *testing.T) {
	body := []byte(`
User-agent: *
Disallow: /

User-agent: monoagent-crawl
Disallow: /admin
`)
	rules := parseRobots(body, "monoagent-crawl/1 (+https://example)")
	if !rules.allowed("/articles/1") {
		t.Error("the named group should replace the * group entirely")
	}
	if rules.allowed("/admin/users") {
		t.Error("/admin should still be disallowed")
	}
}

func TestParseRobotsSharedGroup(t *testing.T) {
	// Several user-agents in a row select the same rules. There is no "*"
	// group here, so only the shared run can produce the rule — and the
	// name that matches has to work wherever it sits in the run, which is
	// what makes it a run and not just the last line winning.
	for _, body := range []string{
		"User-agent: foo\nUser-agent: monoagent-crawl\nDisallow: /x\n",
		"User-agent: monoagent-crawl\nUser-agent: foo\nDisallow: /x\n",
	} {
		rules := parseRobots([]byte(body), "monoagent-crawl/1 (+https://example)")
		if rules.allowed("/x/y") {
			t.Errorf("a group naming several agents applies to all of them:\n%s", body)
		}
	}
	// ...and a run that does not name this crawler still does not apply.
	other := parseRobots([]byte("User-agent: foo\nUser-agent: bar\nDisallow: /x\n"), "monoagent-crawl/1")
	if !other.allowed("/x/y") {
		t.Error("a group naming other agents must not apply to this one")
	}
}

func TestRobotsWildcards(t *testing.T) {
	rules := parseRobots([]byte("User-agent: *\nDisallow: /*.json$\nDisallow: /a/*/b\n"), "x")
	if rules.allowed("/data/file.json") {
		t.Error("wildcard + $ should match")
	}
	if !rules.allowed("/data/file.jsonl") {
		t.Error("$ anchors the end of the path")
	}
	if rules.allowed("/a/mid/b") {
		t.Error("mid-pattern wildcard should match")
	}
	if !rules.allowed("/a/mid/c") {
		t.Error("non-matching path should be allowed")
	}
}

func TestRobotsEmptyDisallowAllowsAll(t *testing.T) {
	rules := parseRobots([]byte("User-agent: *\nDisallow:\n"), "x")
	if !rules.allowed("/anything") {
		t.Error("an empty Disallow disallows nothing")
	}
}

// TestRobotsAnchoredWildcardMatchesTheRealTail is the single most common
// real rule. A left-greedy scan that insists on ending at len(path) lets
// every one of these through.
func TestRobotsAnchoredWildcardMatchesTheRealTail(t *testing.T) {
	rules := parseRobots([]byte("User-agent: *\nDisallow: /*.pdf$\n"), "x")
	for _, p := range []string{
		"/report.pdf", "/docs/report.pdf.pdf", "/a.pdf/b.pdf", "/x.pdfy.pdf",
	} {
		if rules.allowed(p) {
			t.Errorf("%s ends in .pdf and is disallowed", p)
		}
	}
	for _, p := range []string{"/report.pdfx", "/pdf/notes.txt", "/a.pdf/b.html"} {
		if !rules.allowed(p) {
			t.Errorf("%s does not end in .pdf and should be allowed", p)
		}
	}
}

func TestRobotsMatchesQueryStrings(t *testing.T) {
	rules := parseRobots([]byte("User-agent: *\nDisallow: /search?q=\nDisallow: /*?sort=\n"), "x")
	if rules.allowed("/search?q=shoes") {
		t.Error("a rule written against the query must match the query")
	}
	if rules.allowed("/catalog/x?sort=price") {
		t.Error("faceted-navigation rules must match")
	}
	if !rules.allowed("/search") {
		t.Error("the bare path is still allowed")
	}
}

// TestParseRobotsGroupNameIsNotASubstring: "1" and "gent" both occur inside
// the default user agent, and neither names this crawler.
func TestParseRobotsGroupNameIsNotASubstring(t *testing.T) {
	for _, name := range []string{"1", "gent", "crawl", "mono-agent"} {
		rules := parseRobots([]byte("User-agent: "+name+"\nDisallow: /\n"), DefaultUserAgent)
		if !rules.allowed("/anything") {
			t.Errorf("User-agent: %q must not claim %q", name, DefaultUserAgent)
		}
	}
	rules := parseRobots([]byte("User-agent: monoagent-crawl\nDisallow: /\n"), DefaultUserAgent)
	if rules.allowed("/anything") {
		t.Error("the product token itself must select the group")
	}
}

func TestParseRobotsCrawlDelayNeverOverflows(t *testing.T) {
	for _, value := range []string{"1e10", "1e18", "99999999999"} {
		rules := parseRobots([]byte("User-agent: *\nCrawl-delay: "+value+"\n"), "x")
		if rules.crawlDelay <= 0 {
			t.Errorf("Crawl-delay: %s parsed to %s — an absurd delay must not become no delay",
				value, rules.crawlDelay)
		}
	}
	for _, value := range []string{"NaN", "-5", "abc", "1e400"} {
		rules := parseRobots([]byte("User-agent: *\nCrawl-delay: "+value+"\n"), "x")
		if rules.crawlDelay != 0 {
			t.Errorf("Crawl-delay: %s should be ignored, got %s", value, rules.crawlDelay)
		}
	}
}

func TestRobotsServerErrorDisallowsEverything(t *testing.T) {
	codes := map[int]bool{500: false, 503: false, 404: true, 410: true}
	for code, wantAllowed := range codes {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		rc := newRobotsCache(srv.Client(), "x")
		rules := rc.rulesFor(context.Background(), srv.URL+"/page")
		if got := rules.allowed("/page"); got != wantAllowed {
			t.Errorf("robots.txt served %d: allowed = %v, want %v", code, got, wantAllowed)
		}
		srv.Close()
	}
}
