// Package crawlsite walks a site over plain HTTP and lands every HTML page
// it finds in the capture inbox as an envelope — the same shape the Chrome
// extension writes, through the same writer, so a crawled document and a
// clipped one are indistinguishable downstream. See
// docs/BROWSER_TRACK_PLAN.md ("Contract: the capture envelope") and GLU-06.
//
// The crawl is deliberately not a browser. Two hundred pages through CDP
// costs a tab and a render each, and what the document pipeline wants is
// the text. Pages that only exist after JavaScript runs are the
// extension's job, not this one's.
package crawlsite

import (
	"fmt"
	"net/url"
	"strings"
)

// exactTrackingParams are query parameters that identify the click rather
// than the document. Two URLs differing only by these are the same page,
// and keeping them apart means the same article lands once per newsletter
// that linked it. Anything with a "utm_" prefix is treated the same way.
var exactTrackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true,
	"yclid": true, "igshid": true, "mc_cid": true, "mc_eid": true,
	"ref_src": true, "ref_url": true, "_hsenc": true, "_hsmi": true,
	"vero_id": true, "vero_conv": true, "spm": true,
}

func isTrackingParam(key string) bool {
	k := strings.ToLower(key)
	return strings.HasPrefix(k, "utm_") || exactTrackingParams[k]
}

// normalizeURL returns the form of raw that is both fetched and used as the
// dedupe key: absolute, http(s) only, host lowercased, default port and
// fragment dropped, tracking parameters removed.
//
// The remaining query is re-encoded, which sorts it. That is what makes
// ?a=1&b=2 and ?b=2&a=1 one page instead of two; the cost is that a server
// which cares about parameter order sees the sorted form. No such server
// has been worth the duplicate captures.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", raw, err)
	}
	return normalizeParsed(u)
}

func normalizeParsed(u *url.URL) (string, error) {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q — only http and https can be crawled", u.Scheme)
	}
	out := *u
	out.Scheme = strings.ToLower(out.Scheme)
	host := strings.ToLower(out.Hostname())
	if host == "" {
		return "", fmt.Errorf("url %q has no host", u.Redacted())
	}
	port := out.Port()
	if (out.Scheme == "http" && port == "80") || (out.Scheme == "https" && port == "443") {
		port = ""
	}
	out.Host = host
	if port != "" {
		out.Host = host + ":" + port
	}
	out.Fragment, out.RawFragment = "", ""
	out.User = nil
	if out.RawQuery != "" {
		q := out.Query()
		for k := range q {
			if isTrackingParam(k) {
				q.Del(k)
			}
		}
		out.RawQuery = q.Encode()
	}
	if out.Path == "" {
		out.Path = "/"
	}
	return out.String(), nil
}

// scope decides which hosts the crawl is allowed to walk into. A link
// outside it is not an error — it is simply not followed.
type scope struct {
	hosts             map[string]bool
	includeSubdomains bool
}

func newScope(includeSubdomains bool) *scope {
	return &scope{hosts: map[string]bool{}, includeSubdomains: includeSubdomains}
}

func (s *scope) add(host string) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return
	}
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i] // an allow-list entry may carry a port; match on the host
	}
	s.hosts[strings.TrimPrefix(h, "*.")] = true
}

// allows reports whether host is in scope. With includeSubdomains, an
// allowed host also admits anything ending in "."+host — a suffix match,
// not a public-suffix-aware one, so "--allow-host com" would be a mistake
// the caller has to not make.
func (s *scope) allows(host string) bool {
	h := strings.ToLower(host)
	if s.hosts[h] {
		return true
	}
	if !s.includeSubdomains {
		return false
	}
	for allowed := range s.hosts {
		if strings.HasSuffix(h, "."+allowed) {
			return true
		}
	}
	return false
}

// hostOf returns the lowercased hostname of an already-normalized URL.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
