//go:build social

package tiktok

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// TikTokBot implements botpkg.BotAdapter for TikTok.
type TikTokBot struct{}

// sendVerificationTimeout bounds the post-send poll that confirms a DM was
// actually delivered (composer cleared or message bubble rendered).
const sendVerificationTimeout = 3 * time.Second

// unwrapRodPage extracts the underlying *rod.Page from a browser.PageInterface.
// Most of this bot's helper methods are written directly against the Rod API
// and cannot operate on other drivers (e.g. the Chrome extension bridge). This
// returns a descriptive error instead of panicking when the active page isn't
// backed by Rod.
func unwrapRodPage(p browser.PageInterface) (*rod.Page, error) {
	rp, ok := p.(*browser.RodPage)
	if !ok {
		return nil, fmt.Errorf("tiktok: this operation requires the Rod browser driver, got %T", p)
	}
	return rp.UnwrapRodPage(), nil
}

// dmBubbleSelectors are best-effort selectors for rendered message bubbles in
// the chat thread, used as the "OR" branch of send verification.
var dmBubbleSelectors = []string{
	"div[data-e2e='chat-message']",
	"div[data-e2e='message-item']",
}

// handleTextMatches reports whether the text of a search-result row contains
// the target username (case-insensitive, ignoring the "@" prefix). It mirrors
// Instagram's approach of embedding the username in the search-result
// selector so a wrong-recipient click can never happen.
func handleTextMatches(resultText, target string) bool {
	if target == "" {
		return false
	}
	normalize := func(s string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "@")
	}
	return strings.Contains(normalize(resultText), normalize(target))
}

// truncateForError shortens a string for inclusion in error messages.
func truncateForError(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// messageSnippet returns the searchable portion of a message used to match a
// rendered message bubble (long messages may be visually truncated by the UI).
func messageSnippet(message string) string {
	const maxSnippet = 80
	r := []rune(message)
	if len(r) > maxSnippet {
		return string(r[:maxSnippet])
	}
	return message
}

// sendVerified reports whether the observable page state confirms the message
// was sent: the composer is cleared, or a message bubble containing the
// message snippet has rendered in the thread.
func sendVerified(composerText string, bubbleTexts []string, message string) bool {
	if strings.TrimSpace(composerText) == "" {
		return true
	}
	snippet := messageSnippet(message)
	for _, bubble := range bubbleTexts {
		if strings.Contains(bubble, snippet) {
			return true
		}
	}
	return false
}

// composerText reads the current text of a message composer element,
// handling both contenteditable divs (innerText) and textareas (value).
func composerText(el *rod.Element) string {
	if el == nil {
		return ""
	}
	res, err := el.Eval(`() => this.value !== undefined ? this.value : (this.innerText || '')`)
	if err != nil || res == nil {
		return ""
	}
	return res.Value.Str()
}

// verifyMessageSent polls the page for up to sendVerificationTimeout to
// confirm the message was actually sent. Verification failure is an error —
// a send we cannot observe is reported as "not sent", never as success.
func verifyMessageSent(page *rod.Page, msgInput *rod.Element, message string) error {
	deadline := time.Now().Add(sendVerificationTimeout)
	for time.Now().Before(deadline) {
		var bubbleTexts []string
		for _, sel := range dmBubbleSelectors {
			els, err := page.Elements(sel)
			if err != nil {
				continue
			}
			for _, el := range els {
				if text, tErr := el.Text(); tErr == nil {
					bubbleTexts = append(bubbleTexts, text)
				}
			}
		}
		if sendVerified(composerText(msgInput), bubbleTexts, message) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("tiktok: send could not be verified (composer not cleared and no message bubble rendered within %s)", sendVerificationTimeout)
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

// IsLoggedIn checks whether the user is authenticated on TikTok by looking
// for user-specific elements that only appear when logged in.
func (b *TikTokBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	page, err := unwrapRodPage(p)
	if err != nil {
		return false, err
	}
	selectors := []string{
		// User avatar/icon in the header when logged in.
		"div[data-e2e='profile-icon']",
		"span[data-e2e='profile-icon']",
		// Upload button only visible to logged-in users.
		"a[href='/upload']",
		"div[data-e2e='upload-icon']",
		// Inbox icon in the header.
		"div[data-e2e='inbox-icon']",
		// Profile link in sidebar navigation.
		"a[data-e2e='nav-profile']",
	}

	for _, sel := range selectors {
		has, _, err := page.Has(sel)
		if err != nil {
			continue
		}
		if has {
			return true, nil
		}
	}

	// Check for login-specific elements — if present, we are NOT logged in.
	loginSelectors := []string{
		"button[data-e2e='top-login-button']",
		"div[class*='LoginContainer']",
		"div[data-e2e='login-modal']",
	}
	for _, sel := range loginSelectors {
		has, _, err := page.Has(sel)
		if err != nil {
			continue
		}
		if has {
			return false, nil
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

// SendMessage navigates to the TikTok direct messaging interface and sends a
// message to the specified user.
func (b *TikTokBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	page, err := unwrapRodPage(p)
	if err != nil {
		return err
	}
	if username == "" {
		return fmt.Errorf("tiktok: username is required")
	}
	if message == "" {
		return fmt.Errorf("tiktok: message is required")
	}

	// Navigate to the user's profile first to initiate a message.
	profileURL := fmt.Sprintf("https://www.tiktok.com/@%s", url.PathEscape(username))
	err = page.Navigate(profileURL)
	if err != nil {
		return fmt.Errorf("tiktok: failed to navigate to profile: %w", err)
	}
	err = page.WaitLoad()
	if err != nil {
		return fmt.Errorf("tiktok: profile page did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Look for a "Message" button on the profile page.
	msgBtnSelectors := []string{
		"button[data-e2e='message-button']",
		"a[data-e2e='message-button']",
		"div[data-e2e='message-icon']",
	}

	clicked := false
	for _, sel := range msgBtnSelectors {
		btn, findErr := page.Timeout(5 * time.Second).Element(sel)
		if findErr == nil && btn != nil {
			if clickErr := btn.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
				clicked = true
				break
			}
		}
	}

	if !clicked {
		// Fallback: navigate to the messages page directly.
		msgURL := "https://www.tiktok.com/messages"
		err = page.Navigate(msgURL)
		if err != nil {
			return fmt.Errorf("tiktok: failed to navigate to messages: %w", err)
		}
		err = page.WaitLoad()
		if err != nil {
			return fmt.Errorf("tiktok: messages page did not load: %w", err)
		}
		time.Sleep(3 * time.Second)

		// Search for the user in the messaging interface.
		searchSelectors := []string{
			"input[data-e2e='search-user-input']",
			"input[placeholder*='Search']",
		}

		var searchInput *rod.Element
		for _, sel := range searchSelectors {
			el, findErr := page.Timeout(5 * time.Second).Element(sel)
			if findErr == nil && el != nil {
				searchInput = el
				break
			}
		}

		if searchInput == nil {
			return fmt.Errorf("tiktok: could not find user search input in messages")
		}

		err = searchInput.Input(username)
		if err != nil {
			return fmt.Errorf("tiktok: failed to type username in search: %w", err)
		}
		time.Sleep(2 * time.Second)

		// Click the first search result whose handle matches the target
		// username. Clicking a result without verifying its handle risks
		// DM-ing the wrong user, so mismatched results are skipped and a
		// fully mismatched result set is a hard error — the message is
		// never typed.
		resultSelectors := []string{
			"div[data-e2e='search-user-item']",
			"div[role='option']",
			"li[role='listitem']",
		}

		resultClicked := false
		var firstResultText string
		for _, sel := range resultSelectors {
			resultEls, rErr := page.Timeout(5 * time.Second).Elements(sel)
			if rErr != nil || len(resultEls) == 0 {
				continue
			}
			for _, resultEl := range resultEls {
				text, tErr := resultEl.Text()
				if tErr != nil {
					continue
				}
				trimmed := strings.TrimSpace(text)
				if firstResultText == "" {
					firstResultText = trimmed
				}
				if !handleTextMatches(trimmed, username) {
					continue
				}
				if clickErr := resultEl.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
					resultClicked = true
					break
				}
			}
			if resultClicked {
				break
			}
		}

		if !resultClicked {
			if firstResultText != "" {
				return fmt.Errorf("tiktok: search result '%s' does not match target username '%s'", truncateForError(firstResultText, 80), username)
			}
			return fmt.Errorf("tiktok: could not select user %q from search results", username)
		}
		time.Sleep(2 * time.Second)
	}

	// Wait for the chat window and find the message input.
	time.Sleep(2 * time.Second)

	inputSelectors := []string{
		"div[data-e2e='message-input'] div[contenteditable='true']",
		"div[contenteditable='true'][data-e2e='message-input']",
		"div[role='textbox'][contenteditable='true']",
		"div.public-DraftEditor-content[contenteditable='true']",
	}

	var msgInput *rod.Element
	for _, sel := range inputSelectors {
		el, findErr := page.Timeout(5 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			msgInput = el
			break
		}
	}

	if msgInput == nil {
		return fmt.Errorf("tiktok: could not find message input field")
	}

	// Focus and type the message.
	err = msgInput.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("tiktok: failed to focus message input: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	err = msgInput.Input(message)
	if err != nil {
		return fmt.Errorf("tiktok: failed to type message: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Send the message.
	sendBtnSelectors := []string{
		"button[data-e2e='send-message-button']",
		"div[data-e2e='send-message-icon']",
		"button[aria-label='Send']",
	}

	sent := false
	for _, sel := range sendBtnSelectors {
		sendBtn, sErr := page.Timeout(5 * time.Second).Element(sel)
		if sErr == nil && sendBtn != nil {
			if clickErr := sendBtn.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
				sent = true
				break
			}
		}
	}

	if !sent {
		// Fallback: press Enter.
		err = page.Keyboard.Press(input.Enter)
		if err != nil {
			return fmt.Errorf("tiktok: failed to send message: %w", err)
		}
	}

	return verifyMessageSent(page, msgInput, message)
}

// GetProfileData scrapes the currently loaded TikTok profile page and returns
// structured profile information.
func (b *TikTokBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	page, err := unwrapRodPage(p)
	if err != nil {
		return nil, err
	}
	data := make(map[string]interface{})

	err = page.WaitLoad()
	if err != nil {
		return data, fmt.Errorf("tiktok: page did not finish loading: %w", err)
	}
	time.Sleep(3 * time.Second)

	pageURL := page.MustInfo().URL
	data["username"] = b.ExtractUsername(pageURL)
	data["profile_url"] = pageURL

	// Display name / nickname.
	nameSelectors := []string{
		"h1[data-e2e='user-title']",
		"h2[data-e2e='user-subtitle']",
		"span[data-e2e='user-title']",
	}
	for _, sel := range nameSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["full_name"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Unique ID (@handle).
	handleSelectors := []string{
		"h2[data-e2e='user-subtitle']",
		"span[data-e2e='user-subtitle']",
	}
	for _, sel := range handleSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["handle"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Bio / signature.
	bioSelectors := []string{
		"h2[data-e2e='user-bio']",
		"span[data-e2e='user-bio']",
		"div[data-e2e='user-bio']",
	}
	for _, sel := range bioSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["bio"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Following count.
	followingSelectors := []string{
		"strong[data-e2e='following-count']",
		"span[data-e2e='following-count']",
	}
	for _, sel := range followingSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["following_count"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Follower count.
	followerSelectors := []string{
		"strong[data-e2e='followers-count']",
		"span[data-e2e='followers-count']",
	}
	for _, sel := range followerSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["follower_count"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Likes count.
	likesSelectors := []string{
		"strong[data-e2e='likes-count']",
		"span[data-e2e='likes-count']",
	}
	for _, sel := range likesSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["likes_count"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Profile picture URL.
	imgSelectors := []string{
		"img[data-e2e='user-avatar']",
		"span[data-e2e='user-avatar'] img",
		"div[data-e2e='user-avatar'] img",
	}
	for _, sel := range imgSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			src, aErr := el.Attribute("src")
			if aErr == nil && src != nil && *src != "" {
				data["profile_picture_url"] = *src
				break
			}
		}
	}

	// Website / link in bio.
	linkSelectors := []string{
		"a[data-e2e='user-link']",
		"div[data-e2e='user-link'] a",
	}
	for _, sel := range linkSelectors {
		el, findErr := page.Timeout(2 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			href, aErr := el.Attribute("href")
			if aErr == nil && href != nil && *href != "" {
				data["website"] = *href
				break
			}
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["website"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Verified badge.
	data["is_verified"] = false
	verifiedSelectors := []string{
		"svg[data-e2e='verify-badge']",
		"div[data-e2e='verify-badge']",
	}
	for _, sel := range verifiedSelectors {
		has, _, vErr := page.Has(sel)
		if vErr == nil && has {
			data["is_verified"] = true
			break
		}
	}

	return data, nil
}

// GetMethodByName returns a dispatchable wrapper for the named TikTok action method.
// This satisfies the action.BotAdapter interface so call_bot_method steps can resolve
// TikTok methods at runtime.
func (b *TikTokBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	switch name {
	case "list_user_videos":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("list_user_videos requires (page, profileURL, maxCount)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("list_user_videos: first arg must be browser.PageInterface")
			}
			profileURL, _ := args[1].(string)
			maxCount := 20
			if v, ok := args[2].(float64); ok {
				maxCount = int(v)
			}
			return b.ListUserVideos(ctx, page, profileURL, maxCount)
		}, true

	case "like_video":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("like_video requires (page, videoURL)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("like_video: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			if err := b.LikeVideo(ctx, page, videoURL); err != nil {
				return nil, err
			}
			return map[string]interface{}{"success": true, "videoURL": videoURL}, nil
		}, true

	case "comment_on_video":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("comment_on_video requires (page, videoURL, commentText)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("comment_on_video: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			commentText, _ := args[2].(string)
			if err := b.CommentOnVideo(ctx, page, videoURL, commentText); err != nil {
				return nil, err
			}
			return map[string]interface{}{"success": true, "videoURL": videoURL}, nil
		}, true

	case "list_video_comments":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("list_video_comments requires (page, videoURL, maxCount)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("list_video_comments: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			maxCount := 50
			if v, ok := args[2].(float64); ok {
				maxCount = int(v)
			}
			return b.ListVideoComments(ctx, page, videoURL, maxCount)
		}, true

	case "like_comment":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("like_comment requires (page, videoURL, commentID)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("like_comment: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			commentID, _ := args[2].(string)
			if err := b.LikeComment(ctx, page, videoURL, commentID); err != nil {
				return nil, err
			}
			return map[string]interface{}{"success": true, "videoURL": videoURL, "commentID": commentID}, nil
		}, true

	case "follow_user":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("follow_user requires (page, profileURL)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("follow_user: first arg must be browser.PageInterface")
			}
			profileURL, _ := args[1].(string)
			if err := b.FollowUser(ctx, page, profileURL); err != nil {
				return nil, err
			}
			return map[string]interface{}{"success": true, "profileURL": profileURL}, nil
		}, true

	case "stitch_video":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("stitch_video requires (page, videoURL)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("stitch_video: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			// Returns status "opened_stitch_editor" — the stitch is not
			// published automatically.
			return b.StitchVideo(ctx, page, videoURL)
		}, true

	case "duet_video":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("duet_video requires (page, videoURL)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("duet_video: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			// Returns status "opened_duet_editor" — the duet is not
			// published automatically.
			return b.DuetVideo(ctx, page, videoURL)
		}, true

	case "share_video":
		return func(ctx context.Context, args ...interface{}) (interface{}, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("share_video requires (page, videoURL)")
			}
			page, ok := args[0].(browser.PageInterface)
			if !ok {
				return nil, fmt.Errorf("share_video: first arg must be browser.PageInterface")
			}
			videoURL, _ := args[1].(string)
			return b.ShareVideo(ctx, page, videoURL)
		}, true
	}
	return nil, false
}
