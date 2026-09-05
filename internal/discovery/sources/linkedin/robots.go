package linkedin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fetchRobotsTxt fetches robotsURL directly via net/http — not via
// crawl.FetchPage, which parses/re-serializes as HTML and would corrupt
// robots.txt's line-based plain-text structure. A missing robots.txt
// (404) means nothing is disallowed.
func fetchRobotsTxt(ctx context.Context, robotsURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return "", fmt.Errorf("linkedin: robots.txt request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MonoAgent/1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("linkedin: robots.txt fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("linkedin: robots.txt returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("linkedin: reading robots.txt: %w", err)
	}
	return string(body), nil
}

// isDisallowedByRobots is a minimal, prefix-match check of a robots.txt
// body for whether path is disallowed under a "User-agent: *" block — not
// a full RFC9309 implementation, sufficient for a hard stop/no-bypass gate.
func isDisallowedByRobots(robotsTxt, path string) bool {
	lines := strings.Split(robotsTxt, "\n")
	inWildcardBlock := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			inWildcardBlock = val == "*"
		case "disallow":
			if inWildcardBlock && val != "" && strings.HasPrefix(path, val) {
				return true
			}
		}
	}
	return false
}
