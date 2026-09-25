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
	"sync"
)

// ErrOffDomain is the cause of a step error when a URL falls outside the
// package's domain allowlist (issue/step error code "off_domain").
var ErrOffDomain = errors.New("off_domain")

// HostAllowed reports whether host matches the allowlist. An entry is an
// exact host or "*.suffix"; "*.x.com" also matches the bare "x.com". Matching
// is case-insensitive. An entry with a port ("localhost:8080") matches only
// that port; an entry without one matches any port. An empty list allows
// everything.
func HostAllowed(host string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	name, port := splitHostPort(host)
	return hostMatches(name, port, domains)
}

func hostMatches(name, port string, domains []string) bool {
	if name == "" {
		return false
	}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		wild := false
		if rest, ok := strings.CutPrefix(d, "*."); ok {
			d, wild = rest, true
		}
		pname, pport := splitHostPort(d)
		if pport != "" && pport != port {
			continue
		}
		if name == pname || (wild && strings.HasSuffix(name, "."+pname)) {
			return true
		}
	}
	return false
}

// splitHostPort lower-cases host and splits off a port; it strips IPv6
// brackets and a trailing dot.
func splitHostPort(host string) (name, port string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, p, err := net.SplitHostPort(host); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")
	return strings.TrimSuffix(host, "."), port
}

var (
	hostDenyMu     sync.RWMutex
	globalHostDeny func(host string) (bool, string)
)

// SetGlobalHostDeny installs a deny rule applied in every URL check, with or
// without a package (the runtime denies social hosts when their platform is
// not compiled in). fn returns (true, reason) to deny host. nil removes it.
func SetGlobalHostDeny(fn func(host string) (bool, string)) {
	hostDenyMu.Lock()
	globalHostDeny = fn
	hostDenyMu.Unlock()
}

func hostDenied(host string) (bool, string) {
	hostDenyMu.RLock()
	fn := globalHostDeny
	hostDenyMu.RUnlock()
	if fn == nil {
		return false, ""
	}
	return fn(host)
}

var defaultPorts = map[string]string{"http": "80", "ws": "80", "https": "443", "wss": "443"}

// URLAllowed checks an absolute URL against the global deny rule and
// domains. Non-network schemes (about:, data:, blob:, chrome:) carry no host
// and are allowed: they are what a tab shows between navigations, not a
// site the package reaches. The URL's port (or its scheme's default) takes
// part in the match when an entry names one.
func URLAllowed(rawURL string, domains []string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		if len(domains) == 0 {
			return nil
		}
		return fmt.Errorf("%w: unparsable URL %q", ErrOffDomain, rawURL)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "ws", "wss":
	case "":
		if len(domains) == 0 {
			return nil
		}
		return fmt.Errorf("%w: URL %q has no scheme", ErrOffDomain, rawURL)
	default:
		return nil
	}
	name, _ := splitHostPort(u.Host)
	if deny, why := hostDenied(name); deny {
		return fmt.Errorf("%w: %s is blocked: %s", ErrOffDomain, name, why)
	}
	if len(domains) == 0 {
		return nil
	}
	port := u.Port()
	if port == "" {
		port = defaultPorts[scheme]
	}
	if !hostMatches(name, port, domains) {
		return fmt.Errorf("%w: %s is not in the automation's domains %v", ErrOffDomain, u.Host, domains)
	}
	return nil
}

// CheckURLAllowed checks rawURL against the global deny rule and the
// attached package's domains (none attached, or an empty list: domains are
// unrestricted). Step handlers that reach the network (navigate,
// http_fetch_in_page, new tabs) call it before acting.
func (ae *ActionExecutor) CheckURLAllowed(rawURL string) error {
	var domains []string
	if ae.pkg != nil {
		domains = ae.pkg.Domains()
	}
	return URLAllowed(rawURL, domains)
}

// checkPageDomain is the post-step check: the page must still be on an
// allowed host after the step ran (a click or a redirect may have left it).
// It fails closed: with a package attached, a page URL that cannot be read
// is an error.
func (ae *ActionExecutor) checkPageDomain() error {
	if ae.pkg == nil || ae.page == nil {
		return nil
	}
	hostDenyMu.RLock()
	hasDeny := globalHostDeny != nil
	hostDenyMu.RUnlock()
	if len(ae.pkg.Domains()) == 0 && !hasDeny {
		return nil
	}
	cur, err := ae.page.GetURL()
	if err != nil || strings.TrimSpace(cur) == "" {
		return fmt.Errorf("%w: cannot read the page URL to check it (%v)", ErrOffDomain, err)
	}
	return URLAllowed(cur, ae.pkg.Domains())
}
