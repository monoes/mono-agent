//go:build social

package x

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

// XBot implements botpkg.BotAdapter for X (formerly Twitter).
type XBot struct{}

// sendVerificationTimeout bounds the post-send poll that confirms a DM was
// actually delivered (composer cleared or message bubble rendered).
const sendVerificationTimeout = 3 * time.Second

// dmBubbleSelectors are best-effort selectors for rendered DM bubbles in the
// conversation thread, used as the "OR" branch of send verification.
var dmBubbleSelectors = []string{
	"div[data-testid='messageEntry']",
	"div[data-testid='DmActivityContainer'] div[dir='ltr']",
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
	return fmt.Errorf("x: send could not be verified (composer not cleared and no message bubble rendered within %s)", sendVerificationTimeout)
}

func init() {
	botpkg.PlatformRegistry["X"] = func() botpkg.BotAdapter {
		return &XBot{}
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

// IsLoggedIn checks whether the user is authenticated on X by looking for
// home timeline elements that only appear when logged in.
func (b *XBot) IsLoggedIn(p browser.PageInterface) (bool, error) {
	page := p.(*browser.RodPage).UnwrapRodPage()
	// First, check for login form elements — if present, definitely NOT logged in.
	loginSelectors := []string{
		"input[autocomplete='username']",
		"input[name='text']",
		"div[data-testid='LoginForm_Login_Button']",
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

	// Must be on an x.com page (not the login flow) to consider logged in.
	info, err := page.Info()
	if err != nil {
		return false, err
	}
	pageURL := info.URL
	if strings.Contains(pageURL, "/i/flow/login") || strings.Contains(pageURL, "/login") {
		return false, nil
	}

	// Require the account switcher button — this is the most reliable indicator
	// that the user is authenticated, as it only appears when logged in.
	strongSelectors := []string{
		"div[data-testid='SideNav_AccountSwitcher_Button']",
		"div[data-testid='SideNav_NewTweet_Button']",
		"div[data-testid='tweetTextarea_0']",
	}
	for _, sel := range strongSelectors {
		has, _, err := page.Has(sel)
		if err != nil {
			continue
		}
		if has {
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

// SearchURL returns the X people search URL for the given keyword.
func (b *XBot) SearchURL(keyword string) string {
	encoded := url.QueryEscape(strings.TrimSpace(keyword))
	return fmt.Sprintf("https://x.com/search?q=%s&src=typed_query&f=user", encoded)
}

// SendMessage navigates to the X Direct Messages interface and sends a message
// to the specified user.
func (b *XBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	page := p.(*browser.RodPage).UnwrapRodPage()
	if username == "" {
		return fmt.Errorf("x: username is required")
	}
	if message == "" {
		return fmt.Errorf("x: message is required")
	}

	// Navigate to the messages compose page.
	msgURL := "https://x.com/messages/compose"
	err := page.Navigate(msgURL)
	if err != nil {
		return fmt.Errorf("x: failed to navigate to messages compose: %w", err)
	}
	err = page.WaitLoad()
	if err != nil {
		return fmt.Errorf("x: messages page did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Search for the recipient in the compose dialog.
	searchSelectors := []string{
		"input[data-testid='searchPeople']",
		"input[placeholder='Search people']",
		"input[aria-label='Search people']",
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
		return fmt.Errorf("x: could not find people search input in message compose")
	}

	err = searchInput.Input(username)
	if err != nil {
		return fmt.Errorf("x: failed to type username in search: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Select the first search result whose handle matches the target
	// username. Clicking a result without verifying its handle risks DM-ing
	// the wrong user, so mismatched results are skipped and a fully
	// mismatched result set is a hard error — the message is never typed.
	resultSelectors := []string{
		"div[data-testid='TypeaheadUser']",
		"div[role='option']",
		"li[role='listitem']",
	}

	clicked := false
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
				clicked = true
				break
			}
		}
		if clicked {
			break
		}
	}

	if !clicked {
		if firstResultText != "" {
			return fmt.Errorf("x: search result '%s' does not match target username '%s'", truncateForError(firstResultText, 80), username)
		}
		return fmt.Errorf("x: could not select user %q from search results", username)
	}
	time.Sleep(1 * time.Second)

	// Click the "Next" button to open the conversation.
	nextBtnCSS := []string{
		"div[data-testid='nextButton']",
		"button[data-testid='nextButton']",
	}
	nextBtnXPaths := []string{
		"//div[@role='button'][normalize-space(.)='Next']",
	}
	nextClicked := false
	for _, sel := range nextBtnCSS {
		nextBtn, nErr := page.Timeout(3 * time.Second).Element(sel)
		if nErr == nil && nextBtn != nil {
			if clickErr := nextBtn.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
				nextClicked = true
				break
			}
		}
	}
	if !nextClicked {
		for _, xp := range nextBtnXPaths {
			tryErr := rod.Try(func() {
				btn := page.Timeout(3 * time.Second).MustElementX(xp)
				if clickErr := btn.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
					nextClicked = true
				}
			})
			if tryErr == nil && nextClicked {
				break
			}
		}
	}
	time.Sleep(2 * time.Second)

	// Find the message input field.
	inputSelectors := []string{
		"div[data-testid='dmComposerTextInput']",
		"div[data-testid='messageEntry'] div[contenteditable='true']",
		"div[role='textbox'][data-testid='dmComposerTextInput']",
		"div[aria-label='Text message'][role='textbox']",
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
		return fmt.Errorf("x: could not find message input field")
	}

	// Focus and type the message.
	err = msgInput.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("x: failed to focus message input: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	err = msgInput.Input(message)
	if err != nil {
		return fmt.Errorf("x: failed to type message: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Send the message by clicking the send button.
	sendBtnSelectors := []string{
		"div[data-testid='dmComposerSendButton']",
		"button[data-testid='dmComposerSendButton']",
		"div[role='button'][aria-label='Send']",
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
			return fmt.Errorf("x: failed to send message: %w", err)
		}
	}

	return verifyMessageSent(page, msgInput, message)
}

// GetProfileData scrapes the currently loaded X profile page and returns
// structured profile information.
func (b *XBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	page := p.(*browser.RodPage).UnwrapRodPage()
	data := make(map[string]interface{})

	err := page.WaitLoad()
	if err != nil {
		return data, fmt.Errorf("x: page did not finish loading: %w", err)
	}
	time.Sleep(3 * time.Second)

	pageURL := page.MustInfo().URL
	data["username"] = b.ExtractUsername(pageURL)
	data["profile_url"] = pageURL

	// Display name.
	nameSelectors := []string{
		"div[data-testid='UserName'] span:first-child",
		"h2[role='heading'] span",
		"div[data-testid='UserName'] div[dir='ltr'] span",
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

	// Bio / description.
	bioSelectors := []string{
		"div[data-testid='UserDescription']",
		"div[data-testid='UserDescription'] span",
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

	// Location.
	locationSelectors := []string{
		"span[data-testid='UserLocation']",
		"div[data-testid='UserProfileHeader_Items'] span[data-testid='UserLocation']",
	}
	for _, sel := range locationSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["location"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Website / URL.
	urlSelectors := []string{
		"a[data-testid='UserUrl']",
		"div[data-testid='UserProfileHeader_Items'] a[href]",
	}
	for _, sel := range urlSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
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

	// Join date.
	joinDateSelectors := []string{
		"span[data-testid='UserJoinDate']",
		"div[data-testid='UserProfileHeader_Items'] span[data-testid='UserJoinDate']",
	}
	for _, sel := range joinDateSelectors {
		el, findErr := page.Timeout(3 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			text, tErr := el.Text()
			if tErr == nil && strings.TrimSpace(text) != "" {
				data["join_date"] = strings.TrimSpace(text)
				break
			}
		}
	}

	// Follower and following counts.
	// X renders these as links: /{username}/following and /{username}/followers
	followingSelectors := []string{
		"a[href$='/following'] span span",
		"a[href*='/following'] span",
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

	followerSelectors := []string{
		"a[href$='/verified_followers'] span span",
		"a[href$='/followers'] span span",
		"a[href*='/followers'] span",
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

	// Verified badge.
	data["is_verified"] = false
	verifiedSelectors := []string{
		"svg[data-testid='icon-verified']",
		"div[data-testid='UserName'] svg[aria-label='Verified account']",
	}
	for _, sel := range verifiedSelectors {
		has, _, vErr := page.Has(sel)
		if vErr == nil && has {
			data["is_verified"] = true
			break
		}
	}

	// Profile picture URL.
	imgSelectors := []string{
		"div[data-testid='UserAvatar'] img",
		"a[href$='/photo'] img",
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

	// Banner / header image URL.
	bannerSelectors := []string{
		"div[data-testid='UserProfileHeader_Items'] a[href$='/header_photo'] img",
		"a[href$='/header_photo'] img",
	}
	for _, sel := range bannerSelectors {
		el, findErr := page.Timeout(2 * time.Second).Element(sel)
		if findErr == nil && el != nil {
			src, aErr := el.Attribute("src")
			if aErr == nil && src != nil && *src != "" {
				data["banner_url"] = *src
				break
			}
		}
	}

	return data, nil
}
