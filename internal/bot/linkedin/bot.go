//go:build !nosocial

package linkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// LinkedInBot implements botpkg.BotAdapter for LinkedIn. Every method works
// on any browser.PageInterface (in production the user's own browser through
// the extension). The embedded JevPicker (disabled unless the node layer
// calls SetJevPicker) lets write methods ask Jev for a control their
// selectors could not pin down; a Jev pick is always checked against the
// intended target before anything is clicked.
type LinkedInBot struct {
	botpkg.JevPicker
}

func init() {
	botpkg.PlatformRegistry["LINKEDIN"] = func() botpkg.BotAdapter {
		return &LinkedInBot{}
	}
}

// Platform returns the canonical platform name.
func (b *LinkedInBot) Platform() string {
	return "LINKEDIN"
}

// LoginURL returns the LinkedIn login page URL.
func (b *LinkedInBot) LoginURL() string {
	return "https://www.linkedin.com/login"
}

// IsLoggedIn checks whether the user is authenticated on LinkedIn by looking
// for elements that are only rendered for logged-in users.
func (b *LinkedInBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	for _, sel := range []string{"input#username", "form.login__form", "input[name='session_key']"} {
		if has, err := p.Has(sel); err == nil && has {
			return false, nil
		}
	}
	for _, sel := range []string{
		"#global-nav", "div.global-nav", "nav[aria-label='Primary']", "nav[aria-label='Primary Navigation']",
		"[data-testid='primary-nav']", "img.global-nav__me-photo", "a[href*='/messaging/']",
	} {
		if has, err := p.Has(sel); err == nil && has {
			return true, nil
		}
	}
	return false, nil
}

// ResolveURL converts a relative LinkedIn URL to an absolute URL. If the URL
// is already absolute it is returned unchanged.
func (b *LinkedInBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://www.linkedin.com" + rawURL
	}
	return rawURL
}

// ExtractUsername parses a LinkedIn profile URL and returns the username from
// the /in/{username} path segment.
func (b *LinkedInBot) ExtractUsername(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}

	trimmed := strings.Trim(parsed.Path, "/")
	if trimmed == "" {
		return ""
	}

	segments := strings.Split(trimmed, "/")

	// LinkedIn profile URLs follow the pattern /in/{username}/
	for i, seg := range segments {
		if seg == "in" && i+1 < len(segments) {
			return strings.TrimSpace(segments[i+1])
		}
	}

	return ""
}

// SearchURL returns the LinkedIn people search URL for the given keyword.
// searchOrigin is the origin parameter of the search URLs the bot opens.
// GLOBAL_SEARCH_HEADER marks a query typed into the search bar, which is
// what LinkedIn keeps as a recent search; SWITCH_SEARCH_VERTICAL is what
// LinkedIn itself sends when a results page is reached by switching tabs
// (People, Posts) and returns the same results. Being searched still counts
// toward the members' "search appearances" either way.
const searchOrigin = "SWITCH_SEARCH_VERTICAL"

func (b *LinkedInBot) SearchURL(keyword string) string {
	encoded := url.QueryEscape(strings.TrimSpace(keyword))
	return fmt.Sprintf("https://www.linkedin.com/search/results/people/?keywords=%s", encoded)
}

func jsonMarshal(v interface{}) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(s string, v interface{}) error { return json.Unmarshal([]byte(s), v) }

// intArg parses an optional numeric argument ("" ⇒ def).
func intArg(s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	return int(f), nil
}

// boolArg parses an optional boolean argument ("" ⇒ def). call_bot_method
// passes template values as strings, so "false" must mean false.
func boolArg(s string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def, nil
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %q", s)
}

type method func(ctx context.Context, args ...interface{}) (interface{}, error)

// GetMethodByName returns the call_bot_method entry point for name. The
// executor passes the page first, then the step's resolved args.
func (b *LinkedInBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	m, ok := b.methods()[name]
	return m, ok
}

func (b *LinkedInBot) methods() map[string]method {
	return map[string]method{
		"list_user_posts": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "profileURL", "maxCount?", "activityType?")
			if err != nil {
				return nil, fmt.Errorf("list_user_posts: %w", err)
			}
			n, err := intArg(a[1], 20)
			if err != nil {
				return nil, fmt.Errorf("list_user_posts: maxCount: %w", err)
			}
			return b.ListUserPosts(ctx, p, a[0], n, a[2])
		},
		"list_post_comments": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "postURL", "maxCount?", "includeReplies?")
			if err != nil {
				return nil, fmt.Errorf("list_post_comments: %w", err)
			}
			n, err := intArg(a[1], 50)
			if err != nil {
				return nil, fmt.Errorf("list_post_comments: maxCount: %w", err)
			}
			replies, err := boolArg(a[2], true)
			if err != nil {
				return nil, fmt.Errorf("list_post_comments: includeReplies: %w", err)
			}
			return b.ListPostComments(ctx, p, a[0], n, replies)
		},
		"like_post": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "postURL", "reaction?")
			if err != nil {
				return nil, fmt.Errorf("like_post: %w", err)
			}
			return b.LikePost(ctx, p, a[0], a[1])
		},
		"comment_on_post": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "postURL", "commentText", "parentCommentID?")
			if err != nil {
				return nil, fmt.Errorf("comment_on_post: %w", err)
			}
			return b.CommentOnPost(ctx, p, a[0], a[1], a[2])
		},
		"like_comment": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "postURL", "commentID")
			if err != nil {
				return nil, fmt.Errorf("like_comment: %w", err)
			}
			return b.LikeComment(ctx, p, a[0], a[1])
		},
		"send_message": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "recipient", "message")
			if err != nil {
				return nil, fmt.Errorf("send_message: %w", err)
			}
			return b.SendMessageTo(ctx, p, a[0], a[1])
		},
		"reply_to_conversation": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "threadURL", "message")
			if err != nil {
				return nil, fmt.Errorf("reply_to_conversation: %w", err)
			}
			if err := b.ReplyToConversation(ctx, p, a[0], a[1]); err != nil {
				return nil, err
			}
			return map[string]interface{}{"success": true, "thread_url": a[0], "sent": true}, nil
		},
		"list_conversations": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "max?", "onlyUnread?")
			if err != nil {
				return nil, fmt.Errorf("list_conversations: %w", err)
			}
			n, err := intArg(a[0], 20)
			if err != nil {
				return nil, fmt.Errorf("list_conversations: max: %w", err)
			}
			unread, err := boolArg(a[1], true)
			if err != nil {
				return nil, fmt.Errorf("list_conversations: onlyUnread: %w", err)
			}
			return b.ListConversations(ctx, p, n, unread)
		},
		"get_profile_data": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "profileURL")
			if err != nil {
				return nil, fmt.Errorf("get_profile_data: %w", err)
			}
			return b.GetProfile(ctx, p, a[0])
		},
		"search_people": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "keyword", "max?")
			if err != nil {
				return nil, fmt.Errorf("search_people: %w", err)
			}
			n, err := intArg(a[1], 10)
			if err != nil {
				return nil, fmt.Errorf("search_people: max: %w", err)
			}
			return b.SearchPeople(ctx, p, a[0], n)
		},
		"search_posts": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "keyword", "max?")
			if err != nil {
				return nil, fmt.Errorf("search_posts: %w", err)
			}
			n, err := intArg(a[1], 10)
			if err != nil {
				return nil, fmt.Errorf("search_posts: max: %w", err)
			}
			return b.SearchPosts(ctx, p, a[0], n)
		},
		"list_followers": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "sourceType?", "max?")
			if err != nil {
				return nil, fmt.Errorf("list_followers: %w", err)
			}
			n, err := intArg(a[1], 50)
			if err != nil {
				return nil, fmt.Errorf("list_followers: max: %w", err)
			}
			return b.ListFollowers(ctx, p, a[0], n)
		},
		"publish_post": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "text", "media?")
			if err != nil {
				return nil, fmt.Errorf("publish_post: %w", err)
			}
			media := a[1]
			if len(args) > 2 {
				// A media list (schema array) arrives as a slice.
				if list, ok := args[2].([]interface{}); ok {
					parts := make([]string, 0, len(list))
					for _, v := range list {
						if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
							parts = append(parts, s)
						}
					}
					media = strings.Join(parts, ",")
				}
			}
			return b.PublishPost(ctx, p, a[0], media)
		},
	}
}
