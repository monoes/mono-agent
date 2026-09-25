package action

// Domain allowlist enforcement (spec §6.2): navigate, http_fetch_in_page and
// any URL change observed after a step are checked against the package's
// site.domains. No package, or an empty list, means unrestricted.

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrOffDomain is the cause of a step error when a URL falls outside the
// package's domain allowlist (issue/step error code "off_domain").
var ErrOffDomain = errors.New("off_domain")

// HostAllowed reports whether host matches the allowlist. An entry is an
// exact host or "*.suffix"; "*.x.com" also matches the bare "x.com". Matching
// is case-insensitive and ignores any port. An empty list allows everything.
func HostAllowed(host string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if suffix, ok := strings.CutPrefix(d, "*."); ok {
			suffix = normalizeHost(suffix)
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == normalizeHost(d) {
			return true
		}
	}
	return false
}

// normalizeHost lower-cases host and strips a port and trailing dot.
func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return strings.TrimSuffix(host, ".")
}

// URLAllowed checks an absolute URL against domains. Non-network schemes
// (about:, data:, blob:, chrome:) carry no host and are allowed: they are
// what a tab shows between navigations, not a site the package reaches.
func URLAllowed(rawURL string, domains []string) error {
	if len(domains) == 0 {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("%w: unparsable URL %q", ErrOffDomain, rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss":
	case "":
		return fmt.Errorf("%w: URL %q has no scheme", ErrOffDomain, rawURL)
	default:
		return nil
	}
	if !HostAllowed(u.Host, domains) {
		return fmt.Errorf("%w: %s is not in the automation's domains %v", ErrOffDomain, u.Hostname(), domains)
	}
	return nil
}

// CheckURLAllowed checks rawURL against the attached package's domains. It
// returns nil when no package is attached or its domain list is empty.
// Step handlers that reach the network (navigate, http_fetch_in_page, new
// tabs) call it before acting.
func (ae *ActionExecutor) CheckURLAllowed(rawURL string) error {
	if ae.pkg == nil {
		return nil
	}
	return URLAllowed(rawURL, ae.pkg.Domains())
}

// checkPageDomain is the post-step check: the page must still be on an
// allowed host after the step ran (a click or a redirect may have left it).
func (ae *ActionExecutor) checkPageDomain() error {
	if ae.pkg == nil || ae.page == nil || len(ae.pkg.Domains()) == 0 {
		return nil
	}
	cur, err := ae.page.GetURL()
	if err != nil || cur == "" {
		return nil
	}
	return URLAllowed(cur, ae.pkg.Domains())
}
