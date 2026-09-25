//go:build social

package tiktok

import (
	"fmt"
	"net/url"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// TikTokBot implements botpkg.BotAdapter for TikTok. JevPicker (disabled
// unless the node layer calls SetJevPicker) lets the write actions ask Jev
// for a control when their selectors find none or several.
type TikTokBot struct {
	botpkg.JevPicker
}

func init() {
	botpkg.PlatformRegistry["TIKTOK"] = func() botpkg.BotAdapter {
		return &TikTokBot{}
	}
}

// Platform returns the canonical platform name.
func (b *TikTokBot) Platform() string {
	return "TIKTOK"
}

// LoginURL returns the TikTok login page URL.
func (b *TikTokBot) LoginURL() string {
	return "https://www.tiktok.com/login"
}

// IsLoggedIn reports whether the current page shows a logged-in TikTok
// session. The header's Log in button means no (it is checked first: a
// logged-out page still links to the upload page and to "/@"); a profile or
// inbox control, or a profile link naming a handle, means yes.
func (b *TikTokBot) IsLoggedIn(page browser.PageInterface) (bool, error) {
	for _, sel := range []string{
		"[data-e2e='top-login-button']",
		"[data-e2e='login-modal']",
	} {
		has, err := page.Has(sel)
		if err != nil {
			return false, fmt.Errorf("tiktok: check login state: %w", err)
		}
		if has {
			return false, nil
		}
	}
	for _, sel := range []string{
		"[data-e2e='profile-icon']",
		"[data-e2e='inbox-icon']",
		"a[data-e2e='nav-profile'][href*='/@']:not([href='/@'])",
	} {
		if has, err := page.Has(sel); err == nil && has {
			return true, nil
		}
	}
	return false, nil
}

// ResolveURL converts a relative TikTok URL to an absolute URL. If the URL
// is already absolute it is returned unchanged.
func (b *TikTokBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://www.tiktok.com" + rawURL
	}
	return rawURL
}

// ExtractUsername parses a TikTok profile URL and returns the username.
// TikTok profile URLs follow the pattern /@{username}.
func (b *TikTokBot) ExtractUsername(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}

	trimmed := strings.Trim(parsed.Path, "/")
	if trimmed == "" {
		return ""
	}

	segments := strings.Split(trimmed, "/")
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// TikTok usernames are prefixed with @.
		if strings.HasPrefix(seg, "@") {
			// Return the username without the @ prefix.
			username := strings.TrimPrefix(seg, "@")
			if username != "" {
				return username
			}
		}
	}

	return ""
}

// SearchURL returns the TikTok user search URL for the given keyword.
func (b *TikTokBot) SearchURL(keyword string) string {
	encoded := url.QueryEscape(strings.TrimSpace(keyword))
	return fmt.Sprintf("https://www.tiktok.com/search/user?q=%s", encoded)
}
