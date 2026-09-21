package crawlsite

import (
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
	// Several user-agents in a row select the same rules.
	rules := parseRobots([]byte("User-agent: foo\nUser-agent: *\nDisallow: /x\n"), "monoagent-crawl")
	if rules.allowed("/x/y") {
		t.Error("a group naming several agents applies to all of them")
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
