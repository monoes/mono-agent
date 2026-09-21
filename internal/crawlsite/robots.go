package crawlsite

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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

// parseRobots reads the groups that apply to agent, falling back to the
// "*" group. A robots.txt may list several user-agents before one set of
// rules; every name in that run selects the same group.
func parseRobots(body []byte, agent string) robotsRules {
	agent = strings.ToLower(agent)
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
			case name != "" && strings.Contains(agent, name):
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
			if secs, err := strconv.ParseFloat(value, 64); err == nil && secs > 0 {
				out.crawlDelay = time.Duration(secs * float64(time.Second))
			}
		}
	}
	return out
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
func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			if !strings.HasPrefix(path[pos:], part) {
				return false
			}
			pos += len(part)
			continue
		}
		idx := strings.Index(path[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	if anchored {
		last := parts[len(parts)-1]
		return strings.HasSuffix(path, last) && pos == len(path)
	}
	return true
}

// robotsCache fetches and remembers one robots.txt per host for the life of
// a crawl. A host is asked once; a failure is remembered as allow-all so a
// flaky robots.txt cannot turn into a fetch per page.
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
	if resp.StatusCode != http.StatusOK {
		return allowAll()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return allowAll()
	}
	return parseRobots(body, rc.userAgent)
}
