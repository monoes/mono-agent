//go:build !nosocial

package hackernews

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// HackerNewsBot implements botpkg.BotAdapter for Hacker News. The embedded
// JevPicker (disabled unless the node layer calls SetJevPicker) lets the
// write actions ask Jev for a form control their selectors miss.
type HackerNewsBot struct {
	botpkg.JevPicker
}

func init() {
	botpkg.PlatformRegistry["HACKERNEWS"] = func() botpkg.BotAdapter {
		return &HackerNewsBot{}
	}
}

func (b *HackerNewsBot) Platform() string { return "HACKERNEWS" }

// LoginURL points at the front page rather than /login: when already
// authenticated, Hacker News's /login page renders a bare "you're logged in
// as X" message with no site nav (so #me never appears and IsLoggedIn can
// never detect an existing session there). The front page always renders
// the nav bar, showing #me when logged in and a "login" link otherwise.
func (b *HackerNewsBot) LoginURL() string { return "https://news.ycombinator.com/" }

// IsLoggedIn checks for the logged-in username link in the top nav bar
// (id="me"), which Hacker News only renders for authenticated sessions.
func (b *HackerNewsBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	has, err := p.Has("#me")
	if err != nil {
		return false, nil
	}
	return has, nil
}

// ResolveURL converts a relative Hacker News URL to an absolute URL.
func (b *HackerNewsBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://news.ycombinator.com" + rawURL
	}
	if !strings.HasPrefix(rawURL, "http") {
		return "https://news.ycombinator.com/" + rawURL
	}
	return rawURL
}

// ExtractUsername is not meaningful for Hacker News item URLs (Hacker News
// profile URLs are "user?id=<username>", not what this bot navigates to for
// its actions) — returns "" always, matching the BotAdapter contract for
// platforms where this concept doesn't apply to the automated flows.
func (b *HackerNewsBot) ExtractUsername(pageURL string) string {
	return ""
}

// SearchURL is not supported by this bot (no keyword-search action is in
// scope) — returns the front page.
func (b *HackerNewsBot) SearchURL(keyword string) string {
	return "https://news.ycombinator.com/"
}

// SendMessage is not supported — Hacker News has no direct-messaging feature.
func (b *HackerNewsBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	return fmt.Errorf("hackernews: direct messaging is not supported by this platform")
}

// GetProfileData is not in scope for this bot (no profile-scraping action).
func (b *HackerNewsBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	return nil, fmt.Errorf("hackernews: profile scraping is not implemented")
}

// extractItemID parses the "id" query parameter from a Hacker News item URL.
func extractItemID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !strings.Contains(u.Path, "item") {
		return ""
	}
	return u.Query().Get("id")
}

// GetMethodByName returns a dispatchable wrapper for the named Hacker News
// action method, satisfying action.BotAdapter for call_bot_method steps.
// Arguments go through botpkg.Args, so numeric item ids (JSON numbers)
// arrive intact.
func (b *HackerNewsBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	switch name {
	case "submit_post":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 3, "title", "url?", "text?")
			if err != nil {
				return nil, fmt.Errorf("submit_post: %w", err)
			}
			return b.SubmitPost(ctx, page, a[0], a[1], a[2])
		}, true

	case "reply_to_comment":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "itemID", "text")
			if err != nil {
				return nil, fmt.Errorf("reply_to_comment: %w", err)
			}
			return b.ReplyToComment(ctx, page, a[0], a[1])
		}, true

	case "list_comments":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "itemID", "topLevelOnly?")
			if err != nil {
				return nil, fmt.Errorf("list_comments: %w", err)
			}
			top, _ := strconv.ParseBool(strings.TrimSpace(a[1]))
			return b.ListComments(ctx, page, a[0], top)
		}, true

	case "get_post_metrics":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "itemID")
			if err != nil {
				return nil, fmt.Errorf("get_post_metrics: %w", err)
			}
			return b.GetPostMetrics(ctx, page, a[0])
		}, true
	}
	return nil, false
}
