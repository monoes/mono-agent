//go:build social

package x

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// reservedPaths contains X (Twitter) URL path segments that do not represent
// user profiles and should be skipped when extracting a username.
var reservedPaths = map[string]bool{
	"home":          true,
	"explore":       true,
	"search":        true,
	"notifications": true,
	"messages":      true,
	"i":             true,
	"settings":      true,
	"compose":       true,
	"intent":        true,
	"tos":           true,
	"privacy":       true,
	"hashtag":       true,
}

// XBot implements botpkg.BotAdapter for X (formerly Twitter). Its methods
// run on any browser.PageInterface (in production the user's own browser via
// the extension). The embedded JevPicker lets the node layer enable Jev
// element picks for controls whose selectors stopped matching.
type XBot struct {
	botpkg.JevPicker
}

// New returns a fresh X bot.
func New() *XBot { return &XBot{} }

func init() {
	botpkg.PlatformRegistry["X"] = func() botpkg.BotAdapter {
		return New()
	}
}

// Platform returns the canonical platform name.
func (b *XBot) Platform() string {
	return "X"
}

// LoginURL returns the X login flow URL.
func (b *XBot) LoginURL() string {
	return "https://x.com/i/flow/login"
}

// IsLoggedIn checks whether the user is authenticated on X: no login form,
// not on the login flow, and a navigation control only a signed-in session
// renders.
func (b *XBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	for _, sel := range []string{
		"input[autocomplete='username']",
		"[data-testid='LoginForm_Login_Button']",
		"[data-testid='loginButton']",
	} {
		if has, err := p.Has(sel); err == nil && has {
			return false, nil
		}
	}
	cur, err := p.GetURL()
	if err != nil {
		return false, err
	}
	if isLoginURL(cur) {
		return false, nil
	}
	for _, sel := range []string{
		"[data-testid='SideNav_AccountSwitcher_Button']",
		"[data-testid='SideNav_NewTweet_Button']",
		"[data-testid='AppTabBar_Profile_Link']",
	} {
		if has, err := p.Has(sel); err == nil && has {
			return true, nil
		}
	}
	return false, nil
}

// ResolveURL converts a relative X URL to an absolute URL. If the URL is
// already absolute it is returned unchanged.
func (b *XBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://x.com" + rawURL
	}
	return rawURL
}

// ExtractUsername parses an X profile URL and returns the username, skipping
// reserved path segments that do not represent user profiles.
func (b *XBot) ExtractUsername(pageURL string) string {
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
		lower := strings.ToLower(seg)
		if reservedPaths[lower] {
			continue
		}
		return seg
	}

	return ""
}

// SearchURL returns the X post search URL ("Top" tab) for the keyword; it is
// what search_posts opens.
func (b *XBot) SearchURL(keyword string) string {
	return "https://x.com/search?q=" + url.QueryEscape(strings.TrimSpace(keyword)) + "&src=typed_query"
}

// SendMessage sends a direct message to username (a handle or profile URL)
// from its profile's Message button, and verifies the message was sent.
func (b *XBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	_, err := b.SendDM(ctx, p, username, message)
	return err
}

// GetProfileData scrapes the currently loaded X profile page.
func (b *XBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	cur, err := p.GetURL()
	if err != nil {
		return nil, fmt.Errorf("x: read page URL: %w", err)
	}
	return b.scrapeProfile(ctx, p, cur)
}

// botMethod is the call_bot_method signature: args[0] is the page.
type botMethod func(ctx context.Context, args ...interface{}) (interface{}, error)

// GetMethodByName returns the call_bot_method entry point for name. Every
// method takes the page first (prepended by the executor), then:
//
//	get_profile_data   (profile URL or handle)                     → map
//	search_posts       (query, max?)                               → []map
//	list_followers     (profile URL or handle, sourceType?, max?)  → []map
//	like_post          (post URL)                                  → map
//	reply_post         (post URL, text)                            → map
//	publish_post       (text, media paths?)                        → map
//	send_dm            (profile URL or handle, message)            → map
//	list_conversations (max?, unreadOnly?)                         → []map
//	reply_dm           (conversation URL, message)                 → map
func (b *XBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	var m botMethod
	switch name {
	case "get_profile_data":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "profile")
			if err != nil {
				return nil, err
			}
			return b.GetProfile(ctx, p, a[0])
		}
	case "search_posts":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "query", "max?")
			if err != nil {
				return nil, err
			}
			return listResult(b.SearchPosts(ctx, p, a[0], intArg(a[1], 20)))
		}
	case "list_followers":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "profile", "sourceType?", "max?")
			if err != nil {
				return nil, err
			}
			return listResult(b.ListFollowers(ctx, p, a[0], a[1], intArg(a[2], 100)))
		}
	case "like_post":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "postURL")
			if err != nil {
				return nil, err
			}
			return b.LikePost(ctx, p, a[0])
		}
	case "reply_post":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "postURL", "text")
			if err != nil {
				return nil, err
			}
			return b.ReplyPost(ctx, p, a[0], a[1])
		}
	case "publish_post":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "text")
			if err != nil {
				return nil, err
			}
			var media interface{}
			if len(args) > 2 {
				media = args[2]
			}
			return b.PublishPost(ctx, p, a[0], mediaPaths(media))
		}
	case "send_dm":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "recipient", "message")
			if err != nil {
				return nil, err
			}
			return b.SendDM(ctx, p, a[0], a[1])
		}
	case "list_conversations":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "max?", "unreadOnly?")
			if err != nil {
				return nil, err
			}
			return listResult(b.ListConversations(ctx, p, intArg(a[0], 20), boolArg(a[1], true)))
		}
	case "reply_dm":
		m = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "conversationURL", "message")
			if err != nil {
				return nil, err
			}
			return b.ReplyDM(ctx, p, a[0], a[1])
		}
	default:
		return nil, false
	}
	return m, true
}

// listResult hands a list to the executor as []interface{} of maps (each map
// becomes one extracted row; a loop can iterate the variable).
func listResult(rows []map[string]interface{}, err error) (interface{}, error) {
	if err != nil {
		return nil, err
	}
	out := make([]interface{}, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out, nil
}

// mediaPaths accepts media as a comma-separated string or a list.
func mediaPaths(v interface{}) []string {
	var raw []string
	switch m := v.(type) {
	case nil:
	case string:
		raw = strings.Split(m, ",")
	case []string:
		raw = m
	case []interface{}:
		for _, x := range m {
			if s, ok := x.(string); ok {
				raw = append(raw, s)
			} else if mm, ok := x.(map[string]interface{}); ok {
				if s, ok := mm["path"].(string); ok {
					raw = append(raw, s)
				}
			}
		}
	}
	var out []string
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
