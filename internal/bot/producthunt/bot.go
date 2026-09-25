//go:build !nosocial

package producthunt

import (
	"context"
	"fmt"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// ProductHuntBot implements botpkg.BotAdapter for Product Hunt. The
// embedded JevPicker (disabled unless the node layer calls SetJevPicker)
// lets CommentOnLaunch ask Jev for a composer control its selectors miss.
type ProductHuntBot struct {
	botpkg.JevPicker
}

func init() {
	botpkg.PlatformRegistry["PRODUCTHUNT"] = func() botpkg.BotAdapter {
		return &ProductHuntBot{}
	}
}

func (b *ProductHuntBot) Platform() string { return "PRODUCTHUNT" }

func (b *ProductHuntBot) LoginURL() string { return "https://www.producthunt.com/login" }

// IsLoggedIn checks for the user avatar menu button, which Product Hunt
// only renders in the header nav for authenticated sessions.
func (b *ProductHuntBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	has, err := p.Has("[aria-label='User menu']")
	if err != nil {
		return false, nil
	}
	return has, nil
}

// ResolveURL converts a relative Product Hunt URL to an absolute URL.
func (b *ProductHuntBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://www.producthunt.com" + rawURL
	}
	if !strings.HasPrefix(rawURL, "http") {
		return "https://www.producthunt.com/" + rawURL
	}
	return rawURL
}

// ExtractUsername is not meaningful for the launch-page URLs this bot
// navigates to — returns "" always.
func (b *ProductHuntBot) ExtractUsername(pageURL string) string {
	return ""
}

// SearchURL is not supported by this bot (no keyword-search action is in scope).
func (b *ProductHuntBot) SearchURL(keyword string) string {
	return "https://www.producthunt.com/"
}

// SendMessage is not supported — this bot only comments on launches.
func (b *ProductHuntBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	return fmt.Errorf("producthunt: direct messaging is not supported by this bot")
}

// GetProfileData is not in scope for this bot (no profile-scraping action).
func (b *ProductHuntBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	return nil, fmt.Errorf("producthunt: profile scraping is not implemented")
}

// GetMethodByName returns a dispatchable wrapper for the named Product Hunt
// action method, satisfying action.BotAdapter for call_bot_method steps.
func (b *ProductHuntBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	switch name {
	case "comment_on_launch":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "launchURL", "text")
			if err != nil {
				return nil, fmt.Errorf("comment_on_launch: %w", err)
			}
			return b.CommentOnLaunch(ctx, page, a[0], a[1])
		}, true

	case "list_comments":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "launchURL")
			if err != nil {
				return nil, fmt.Errorf("list_comments: %w", err)
			}
			return b.ListComments(ctx, page, a[0])
		}, true

	case "get_launch_metrics":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "launchURL")
			if err != nil {
				return nil, fmt.Errorf("get_launch_metrics: %w", err)
			}
			return b.GetLaunchMetrics(ctx, page, a[0])
		}, true
	}
	return nil, false
}
