package automation

import (
	"net/url"
	"strings"
)

// UnknownUsername is what a session stores when its account name couldn't be
// read at login (no usernameFrom probe, or the probe found nothing). It is a
// storage key, not a name: output meant for people shows it as no name at all.
const UnknownUsername = "unknown"

// DisplayUsername is a stored session username as it should be shown: ""
// for the UnknownUsername placeholder, the name otherwise.
func DisplayUsername(stored string) string {
	if strings.EqualFold(strings.TrimSpace(stored), UnknownUsername) {
		return ""
	}
	return stored
}

// NormalizeLoginUsername turns what a usernameFrom probe read into an account
// name. A profile link (an href such as "/jack", "/@jack" or
// "https://x.com/jack") gives its last path segment; a leading "@" is
// dropped. Anything else is returned trimmed.
func NormalizeLoginUsername(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		if u, err := url.Parse(v); err == nil {
			segs := strings.Split(strings.Trim(u.Path, "/"), "/")
			v = segs[len(segs)-1]
		}
	}
	return strings.TrimSpace(strings.TrimPrefix(v, "@"))
}
