package crawlsite

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxParsedCrawlDelay bounds what a Crawl-delay can parse to. The value is
// a float from a file the site controls: "1e10" seconds multiplied into a
// Duration overflows int64 and comes out negative, which is silently no
// delay at all — the opposite of what the site asked for. What a crawl
// actually waits is capped again, and much lower, by Options.MaxCrawlDelay.
const maxParsedCrawlDelay = 24 * time.Hour

// robotsRules is one user-agent group from a robots.txt, reduced to what a
// crawler needs: the patterns, and the delay the site asked for.
//
// Honouring robots.txt is the default and there is a flag to turn it off,
// because the person running the crawl is fetching pages they could open in
// their own browser. The flag exists; the default does not need to.
type robotsRules struct {
	rules      []robotsRule
	crawlDelay time.Duration
	// missing marks a site that served no robots.txt at all, which means
	// everything is allowed.
	missing bool
}

type robotsRule struct {
	pattern string
	allow   bool
}

// allowAll is the ruleset used when robots.txt is absent, unreadable, or
// served as an error page. A site that cannot tell us its rules has not
// told us no.
func allowAll() robotsRules { return robotsRules{missing: true} }

// disallowAll is the ruleset for a robots.txt the site could not serve
// because it is broken. RFC 9309 draws the distinction this pair encodes:
// "unavailable" (4xx) means there are no rules, "unreachable" (5xx) means
// the rules exist and we could not read them, and a crawler that cannot
// read them stays out.
func disallowAll() robotsRules {
	return robotsRules{rules: []robotsRule{{pattern: "/", allow: false}}}
}

// productToken is the name a robots.txt group has to match: everything up
// to the first "/" or space of the User-Agent header, per RFC 9309.
func productToken(agent string) string {
	token := strings.ToLower(strings.TrimSpace(agent))
	if i := strings.IndexAny(token, "/ \t"); i >= 0 {
		token = token[:i]
	}
	return token
}

// robotsTarget is what a rule is matched against: the path and the query,
// which is what Google's parser documents and what every faceted-navigation
// rule ("Disallow: /search?q=") is written for. Matching the path alone
// makes those rules dead letters.
func robotsTarget(u *url.URL) string {
	if u == nil {
		return "/"
	}
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
}

// parseRobots reads the groups that apply to agent, falling back to the
// "*" group. A robots.txt may list several user-agents before one set of
// rules; every name in that run selects the same group.
func parseRobots(body []byte, agent string) robotsRules {
	token := productToken(agent)
	var (
		out         robotsRules
		specific    bool // a group naming this agent beat the "*" group
		inGroup     bool
		groupIsStar bool
		// namingAgents is true while reading a run of User-agent lines that
		// has not yet been followed by a rule.
		namingAgents bool
	)
	for _, raw := range strings.Split(string(body), "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field = strings.ToLower(strings.TrimSpace(field))
		value = strings.TrimSpace(value)

		if field == "user-agent" {
			if !namingAgents {
				// A new run of User-agent lines starts a new group.
				inGroup, groupIsStar = false, false
				namingAgents = true
			}
			name := strings.ToLower(value)
			switch {
			case name == "*":
				inGroup, groupIsStar = true, true
			// A prefix match on the product token, not a substring match
			// anywhere in the User-Agent header: "User-agent: 1" must not
			// claim "monoagent-crawl/1 (+https://…)".
			case name != "" && strings.HasPrefix(token, name):
				inGroup, groupIsStar = true, false
			}
			continue
		}
		namingAgents = false
		if !inGroup {
			continue
		}
		// The first specific group wins outright: anything collected from
		// the "*" group is dropped the moment this agent is named.
		if !groupIsStar && !specific {
			specific = true
			out = robotsRules{}
		}
		if specific && groupIsStar {
			continue
		}
		switch field {
		case "disallow":
			out.rules = append(out.rules, robotsRule{pattern: value, allow: false})
		case "allow":
			out.rules = append(out.rules, robotsRule{pattern: value, allow: true})
		case "crawl-delay":
			if d, ok := parseCrawlDelay(value); ok {
				out.crawlDelay = d
			}
		}
	}
	return out
}

// parseCrawlDelay turns a Crawl-delay value into a Duration that cannot be
// negative, infinite or NaN however hostile the file is.
func parseCrawlDelay(value string) (time.Duration, bool) {
	secs, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 {
		return 0, false
	}
	if secs >= maxParsedCrawlDelay.Seconds() {
		return maxParsedCrawlDelay, true
	}
	return time.Duration(secs * float64(time.Second)), true
}

// allowed applies the longest-match rule, with Allow winning a tie — the
// behaviour Google's parser documents and every other crawler copied. An
// empty Disallow value means "nothing is disallowed" and is skipped.
func (r robotsRules) allowed(path string) bool {
	if path == "" {
		path = "/"
	}
	best, bestAllow := -1, true
	for _, rule := range r.rules {
		if rule.pattern == "" {
			continue
		}
		if !robotsMatch(rule.pattern, path) {
			continue
		}
		n := len(rule.pattern)
		if n > best || (n == best && rule.allow) {
			best, bestAllow = n, rule.allow
		}
	}
	if best < 0 {
		return true
	}
	return bestAllow
}

// robotsMatch implements the two wildcards robots.txt has: "*" for any run
// of characters and a trailing "$" anchoring the end of the path.
//
// The literal between two wildcards is matched at its leftmost occurrence,
// which leaves the longest possible tail for whatever follows and so never
// costs a match that some other alignment would have found. An anchored
// pattern's final literal is then matched from the right — the end of the
// path is where "$" says it is, not wherever the left-to-right scan
// happened to stop. Scanning left-greedily to the end instead is what made
// "Disallow: /*.pdf$" miss "/docs/report.pdf.pdf".
func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	rest := path[len(parts[0]):]
	parts = parts[1:]
	if len(parts) == 0 {
		// No wildcard: an anchored pattern must have consumed the path.
		return !anchored || rest == ""
	}
	tail := ""
	if anchored {
		tail, parts = parts[len(parts)-1], parts[:len(parts)-1]
	}
	for _, part := range parts {
		idx := strings.Index(rest, part)
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(part):]
	}
	if !anchored {
		return true
	}
	return len(rest) >= len(tail) && strings.HasSuffix(rest, tail)
}

// robotsCache fetches and remembers one robots.txt per host for the life of
// a crawl. A host is asked once; a request that never got an answer at all
// is remembered as allow-all, so a flaky network cannot turn into a fetch
// of robots.txt per page. A server that answered with an error is a
// different thing, and load() says what each answer means.
type robotsCache struct {
	client    *http.Client
	userAgent string
	byHost    map[string]robotsRules
}

func newRobotsCache(client *http.Client, userAgent string) *robotsCache {
	return &robotsCache{client: client, userAgent: userAgent, byHost: map[string]robotsRules{}}
}

// rulesFor returns the rules covering rawURL's host.
func (rc *robotsCache) rulesFor(ctx context.Context, rawURL string) robotsRules {
	u, err := url.Parse(rawURL)
	if err != nil {
		return allowAll()
	}
	key := u.Scheme + "://" + u.Host
	if rules, ok := rc.byHost[key]; ok {
		return rules
	}
	rules := rc.load(ctx, key+"/robots.txt")
	rc.byHost[key] = rules
	return rules
}

func (rc *robotsCache) load(ctx context.Context, robotsURL string) robotsRules {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return allowAll()
	}
	req.Header.Set("User-Agent", rc.userAgent)
	resp, err := rc.client.Do(req)
	if err != nil {
		return allowAll()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return disallowAll()
	}
	if resp.StatusCode != http.StatusOK {
		return allowAll()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return allowAll()
	}
	return parseRobots(body, rc.userAgent)
}
