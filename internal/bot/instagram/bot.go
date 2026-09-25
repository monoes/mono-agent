//go:build !nosocial

package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// jsonUnmarshal wraps encoding/json.Unmarshal for use in helpers.
var jsonUnmarshal = json.Unmarshal

// reservedPaths contains Instagram URL path segments that do not represent
// user profiles and should be skipped when extracting a username from a URL.
var reservedPaths = map[string]bool{
	"home":     true,
	"explore":  true,
	"reels":    true,
	"reel":     true,
	"direct":   true,
	"accounts": true,
	"p":        true,
	"stories":  true,
	"tv":       true,
}

// InstagramBot implements botpkg.BotAdapter for Instagram. Every method runs
// on browser.PageInterface (in production the user's own browser through the
// extension). The embedded JevPicker — enabled by the node layer when the
// profile opted in — lets a method ask Jev for a control its own finder
// could not locate; every pick is validated before it is acted on.
type InstagramBot struct {
	botpkg.JevPicker
}

func init() {
	botpkg.PlatformRegistry["INSTAGRAM"] = func() botpkg.BotAdapter {
		return &InstagramBot{}
	}
}

// New returns an InstagramBot.
func New() *InstagramBot { return &InstagramBot{} }

// Platform returns the canonical platform name.
func (b *InstagramBot) Platform() string {
	return "INSTAGRAM"
}

// LoginURL returns the Instagram login page URL.
func (b *InstagramBot) LoginURL() string {
	return "https://www.instagram.com/accounts/login/"
}

// IsLoggedIn checks whether the user is authenticated on Instagram by looking
// for navigation elements that only appear when logged in.
func (b *InstagramBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	if p == nil {
		return false, errors.New("instagram: nil page")
	}
	for _, sel := range []string{
		"svg[aria-label='New post']",
		"a[href*='/direct/inbox/']",
		"svg[aria-label='Home']",
		"svg[aria-label='Direct']",
		"svg[aria-label='Messenger']",
	} {
		if has, err := p.Has(sel); err == nil && has {
			return true, nil
		}
	}
	// A login form, or no logged-in signal at all: not logged in.
	if _, err := p.Has("input[name='username']"); err != nil {
		return false, fmt.Errorf("instagram: failed to check login state: %w", err)
	}
	return false, nil
}

// ResolveURL converts a relative Instagram URL to an absolute URL. If the URL
// is already absolute it is returned unchanged.
func (b *InstagramBot) ResolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "/") {
		return "https://www.instagram.com" + rawURL
	}
	return rawURL
}

// ExtractUsername parses a profile URL and returns the Instagram username.
// It skips reserved path segments that do not represent user profiles, and
// returns "" for post URLs without an author segment (/p/<code>/).
func (b *InstagramBot) ExtractUsername(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	trimmed := strings.Trim(parsed.Path, "/")
	if trimmed == "" {
		return ""
	}
	segments := strings.Split(trimmed, "/")
	if reservedPaths[segments[0]] {
		// /p/<code>/, /reel/<code>/, /explore/..., /direct/... carry no
		// username; /stories/<user>/<id>/ does.
		if segments[0] == "stories" && len(segments) > 1 && segments[1] != "highlights" {
			return segments[1]
		}
		return ""
	}
	return strings.TrimSpace(segments[0])
}

// SearchURL returns the Instagram explore/tags search URL for the given keyword.
func (b *InstagramBot) SearchURL(keyword string) string {
	safeKeyword := url.PathEscape(strings.TrimPrefix(strings.TrimSpace(keyword), "#"))
	return fmt.Sprintf("https://www.instagram.com/explore/tags/%s/", safeKeyword)
}

// usernameFrom accepts a username, "@username", or a profile/post URL.
func (b *InstagramBot) usernameFrom(input string) string {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "instagram.com") || strings.HasPrefix(input, "/") || strings.HasPrefix(input, "http") {
		return b.ExtractUsername(b.ResolveURL(input))
	}
	input = strings.Trim(strings.TrimPrefix(input, "@"), "/")
	if strings.ContainsAny(input, "/ ?#") {
		return ""
	}
	return input
}

func profileURL(username string) string {
	return "https://www.instagram.com/" + url.PathEscape(username) + "/"
}

// SendMessage sends a direct message to the specified Instagram user and
// verifies it was delivered to the thread.
func (b *InstagramBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	_, err := b.sendMessage(ctx, p, username, message)
	return err
}

// GetProfileData parses the currently loaded Instagram profile page.
func (b *InstagramBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	if p == nil {
		return nil, errors.New("instagram: nil page")
	}
	cur, _ := p.GetURL()
	u := b.ExtractUsername(cur)
	res, err := b.profileFromDOM(p, u)
	if err != nil {
		return nil, err
	}
	res["profile_url"] = cur
	return res, nil
}

// getString safely extracts a string value from a map.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getBool safely extracts a boolean value from a map.
func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// atoiOr parses s as a (possibly float-formatted) integer, else def.
func atoiOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return def
}

// truthy reads a boolean-ish template value ("true", "1", "yes").
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// ok builds the result map of a write.
func ok(kv ...interface{}) map[string]interface{} {
	m := map[string]interface{}{"success": true}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, isStr := kv[i].(string); isStr {
			m[k] = kv[i+1]
		}
	}
	return m
}

func listResult(rows []map[string]interface{}) []interface{} {
	out := make([]interface{}, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

type botMethod = func(ctx context.Context, args ...interface{}) (interface{}, error)

// GetMethodByName implements action.BotAdapter — it exposes bot methods that
// can be called from JSON action definitions via the "call_bot_method" step
// type. args[0] is always the page (browser.PageInterface).
func (b *InstagramBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	m, found := map[string]botMethod{
		"get_user_info": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "usernameOrURL?")
			if err != nil {
				return nil, fmt.Errorf("get_user_info: %w", err)
			}
			username := b.usernameFrom(a[0])
			if username == "" {
				return nil, fmt.Errorf("get_user_info: could not determine username from %q", a[0])
			}
			return b.GetUserInfo(ctx, p, username)
		},
		"extract_username_from_metadata": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, _, err := botpkg.Args(args, 0)
			if err != nil {
				return nil, fmt.Errorf("extract_username_from_metadata: %w", err)
			}
			return b.ExtractUsernameFromMetadata(ctx, p)
		},
		"send_message": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "usernameOrURL", "message")
			if err != nil {
				return nil, fmt.Errorf("send_message: %w", err)
			}
			username := b.usernameFrom(a[0])
			if username == "" {
				return nil, fmt.Errorf("send_message: could not determine username from %q", a[0])
			}
			return b.sendMessage(ctx, p, username, a[1])
		},
		"reply_to_conversation": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "conversationURL", "replyText")
			if err != nil {
				return nil, fmt.Errorf("reply_to_conversation: %w", err)
			}
			return b.ReplyToConversation(ctx, p, a[0], a[1])
		},
		"like_post": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "postURL")
			if err != nil {
				return nil, fmt.Errorf("like_post: %w", err)
			}
			return b.LikePost(ctx, p, a[0])
		},
		"comment_post": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "postURL", "commentText")
			if err != nil {
				return nil, fmt.Errorf("comment_post: %w", err)
			}
			return b.CommentPost(ctx, p, a[0], a[1])
		},
		"like_comment": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "postURL", "commentAuthor?", "maxComments?")
			if err != nil {
				return nil, fmt.Errorf("like_comment: %w", err)
			}
			return b.LikeComments(ctx, p, a[0], a[1], atoiOr(a[2], 1))
		},
		"reply_comment": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "postURL", "commentAuthor?", "replyText")
			if err != nil {
				return nil, fmt.Errorf("reply_comment: %w", err)
			}
			return b.ReplyToComment(ctx, p, a[0], a[1], a[2])
		},
		"follow_user": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "profileURL")
			if err != nil {
				return nil, fmt.Errorf("follow_user: %w", err)
			}
			return b.FollowUser(ctx, p, a[0])
		},
		"unfollow_user": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 1, "profileURL")
			if err != nil {
				return nil, fmt.Errorf("unfollow_user: %w", err)
			}
			return b.UnfollowUser(ctx, p, a[0])
		},
		"view_stories": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "profileURL", "maxStories?")
			if err != nil {
				return nil, fmt.Errorf("view_stories: %w", err)
			}
			return b.ViewStories(ctx, p, a[0], atoiOr(a[1], 0))
		},
		"fetch_followers_list": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "profileURL?", "sourceType?", "maxCount?")
			if err != nil {
				return nil, fmt.Errorf("fetch_followers_list: %w", err)
			}
			users, err := b.FetchFollowersList(ctx, p, a[0], a[1], atoiOr(a[2], 100))
			if err != nil {
				return nil, err
			}
			return listResult(users), nil
		},
		"list_user_posts": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "username", "maxCount?")
			if err != nil {
				return nil, fmt.Errorf("list_user_posts: %w", err)
			}
			posts, err := b.ListUserPosts(ctx, p, a[0], atoiOr(a[1], 20))
			if err != nil {
				return nil, err
			}
			return listResult(posts), nil
		},
		"search_posts": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "keyword", "maxCount?")
			if err != nil {
				return nil, fmt.Errorf("search_posts: %w", err)
			}
			posts, err := b.SearchPosts(ctx, p, a[0], atoiOr(a[1], 20))
			if err != nil {
				return nil, err
			}
			return listResult(posts), nil
		},
		"find_post_authors": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "keyword", "maxCount?")
			if err != nil {
				return nil, fmt.Errorf("find_post_authors: %w", err)
			}
			profiles, err := b.FindPostAuthors(ctx, p, a[0], atoiOr(a[1], 20))
			if err != nil {
				return nil, err
			}
			return listResult(profiles), nil
		},
		"list_post_comments": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 2, "postURL", "maxCount?")
			if err != nil {
				return nil, fmt.Errorf("list_post_comments: %w", err)
			}
			comments, err := b.ListPostComments(ctx, p, a[0], atoiOr(a[1], 50))
			if err != nil {
				return nil, err
			}
			return listResult(comments), nil
		},
		"scrape_post_data": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "postURL", "includeComments?", "maxComments?")
			if err != nil {
				return nil, fmt.Errorf("scrape_post_data: %w", err)
			}
			maxComments := 0
			if truthy(a[1]) {
				maxComments = atoiOr(a[2], 0)
				if maxComments <= 0 {
					maxComments = 50
				}
			}
			return b.ScrapePostData(ctx, p, a[0], maxComments)
		},
		"publish_content": func(ctx context.Context, args ...interface{}) (interface{}, error) {
			p, a, err := botpkg.Args(args, 3, "mediaPath", "caption?", "locationTag?")
			if err != nil {
				return nil, fmt.Errorf("publish_content: %w", err)
			}
			if err := b.PublishContent(ctx, p, a[0], a[1], a[2]); err != nil {
				return nil, err
			}
			return ok(), nil
		},
	}[name]
	if !found {
		return nil, false
	}
	return m, true
}

// sendVerificationTimeout bounds the post-send poll that confirms a DM was
// actually delivered (composer cleared or message bubble rendered).
var sendVerificationTimeout = 8 * time.Second

// messageSnippet returns the searchable portion of a message used to match a
// rendered message bubble (long messages may be visually truncated by the UI).
func messageSnippet(message string) string {
	const maxSnippet = 80
	r := []rune(normSpace(message))
	if len(r) > maxSnippet {
		return string(r[:maxSnippet])
	}
	return string(r)
}

// sendVerified reports whether the observable page state confirms the message
// was sent: the composer is cleared, or a (new) message bubble containing the
// message snippet has rendered in the thread.
func sendVerified(composerText string, bubbleTexts []string, message string) bool {
	if strings.TrimSpace(composerText) == "" {
		return true
	}
	snippet := messageSnippet(message)
	for _, bubble := range bubbleTexts {
		if strings.Contains(normSpace(bubble), snippet) {
			return true
		}
	}
	return false
}
