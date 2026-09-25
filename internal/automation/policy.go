package automation

import (
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/bot"
)

// socialDomains maps the social site list (spec §6.4) to the platform whose
// bot gate decides whether it is allowed.
var socialDomains = map[string]string{
	"instagram.com": "instagram",
	"linkedin.com":  "linkedin",
	"x.com":         "x",
	"twitter.com":   "x",
	"tiktok.com":    "tiktok",
	"facebook.com":  "instagram",
	"threads.net":   "instagram",
}

// socialBuild reports whether social automation is compiled into this
// binary. The bot gate is per platform; any gated platform stands for the
// whole social build (they are switched together by the nosocial tag).
func socialBuild() bool { return bot.PlatformCompiledIn("instagram") }

// socialPlatform returns the platform a manifest is gated on ("" when the
// manifest is not social).
func socialPlatform(m Manifest) string {
	for _, d := range m.Site.Domains {
		host := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "*.")
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		for sd, platform := range socialDomains {
			if host == sd || strings.HasSuffix(host, "."+sd) {
				return platform
			}
		}
	}
	if m.Policy.Tier == "social" {
		if m.Requires.Native != "" {
			return m.Requires.Native
		}
		return m.ID
	}
	return ""
}

// PolicyAllows is the social gate (spec §6.4): a social-tier package, or one
// whose domains match the social list, is allowed only in a binary built
// with social support.
func PolicyAllows(m Manifest) (bool, string) {
	platform := socialPlatform(m)
	if platform == "" {
		return true, ""
	}
	if socialBuild() && bot.PlatformCompiledIn(platform) {
		return true, ""
	}
	return false, fmt.Sprintf("social automation %q is not available in this build: %v (see docs/USAGE_POLICY.md)",
		m.ID, bot.ErrNotCompiledIn(platform))
}

// availability decides whether an installed manifest can run in this
// binary: policy gate, native bot compiled in, engine range.
func availability(m Manifest) (bool, string) {
	if ok, reason := PolicyAllows(m); !ok {
		return false, reason
	}
	if n := m.Requires.Native; n != "" && !bot.PlatformCompiledIn(n) {
		return false, fmt.Sprintf("requires native bot %q: %v", n, bot.ErrNotCompiledIn(n))
	}
	if is := engineIssue(m); is != nil {
		return false, is.Message
	}
	return true, ""
}
