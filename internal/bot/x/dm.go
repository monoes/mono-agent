//go:build !nosocial

package x

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// DM composer and send-button selectors: the classic /messages UI first,
// then the /i/chat UI X is moving DMs to.
const (
	dmComposerSel = "[data-testid='dmComposerTextInput'], [data-testid='dm-composer-textarea'], textarea[data-testid*='composer' i]"
	dmSendSel     = "[data-testid='dmComposerSendButton'], [data-testid='dm-composer-send-button']"
)

// dmBubbleSelectors are rendered message bubbles in an open conversation.
var dmBubbleSelectors = []string{
	"[data-testid='messageEntry']",
	"[data-testid='DmActivityContainer'] [data-testid='tweetText']",
	"[data-testid^='message-text']",
	"[data-testid='dm-message']",
}

// bubbleCountJS counts message bubbles whose text contains snippet
// (whitespace-collapsed), ignoring the composer itself.
const bubbleCountJS = `(sels, snippet) => {
	const norm = (s) => (s || '').replace(/\s+/g, ' ').trim();
	let n = 0;
	for (const sel of sels) {
		for (const e of document.querySelectorAll(sel)) {
			if (e.closest("[contenteditable='true'], textarea")) continue;
			if (norm(e.innerText || e.textContent).includes(snippet)) n++;
		}
	}
	return n;
}`

// sendOutcome says how a sent message was confirmed.
type sendOutcome string

const (
	sentBubble   sendOutcome = "bubble"
	sentComposer sendOutcome = "composer_cleared"
)

// sendVerified decides whether the page confirms a send: a new bubble with
// the message rendered, or the composer (which held the typed text) cleared.
func sendVerified(composerPresent bool, composerText string, bubblesBefore, bubblesNow int) (sendOutcome, bool) {
	if bubblesNow > bubblesBefore {
		return sentBubble, true
	}
	if !composerPresent || strings.TrimSpace(composerText) == "" {
		return sentComposer, true
	}
	return "", false
}

// sendInConversation types message into the open conversation's composer,
// sends it, and verifies the send.
func (b *XBot) sendInConversation(ctx context.Context, p browser.PageInterface, message string) (sendOutcome, error) {
	box, boxSel, err := findMarked(ctx, p, "", dmComposerSel, loadTimeout)
	if err != nil {
		pick, _, jerr := b.jevPick(ctx, p, "fill", "the message text box of the open direct message conversation", "", "")
		if jerr != nil {
			return "", fmt.Errorf("no message composer in the conversation (jev fallback: %v)", jerr)
		}
		defer pick.Release()
		box, boxSel = pick.Element, pick.Selector
	}
	snippet := verifySnippet(message)
	var before int
	if err := botpkg.EvalJSON(p, bubbleCountJS, &before, dmBubbleSelectors, snippet); err != nil {
		return "", fmt.Errorf("read conversation: %w", err)
	}
	if err := sleep(ctx, actionPause); err != nil {
		return "", err
	}
	if err := typeAndCheck(ctx, p, box, boxSel, message); err != nil {
		return "", err
	}

	send, _, err := findMarked(ctx, p, "", dmSendSel, 3*time.Second)
	if err != nil {
		pick, _, jerr := b.jevPick(ctx, p, "click", "the Send button of the open direct message conversation", "", "")
		if jerr == nil {
			defer pick.Release()
			send = pick.Element
		}
	}
	if send != nil {
		if err := botpkg.ClickTrusted(p, send); err != nil {
			return "", fmt.Errorf("click Send: %w", err)
		}
	} else if err := botpkg.PressEnter(p); err != nil { // Enter sends in X's DM composer
		return "", fmt.Errorf("press Enter to send: %w", err)
	}

	var outcome sendOutcome
	err = poll(ctx, verifyTimeout, func() (bool, error) {
		var now int
		var ct composerText
		if botpkg.EvalJSON(p, bubbleCountJS, &now, dmBubbleSelectors, snippet) != nil ||
			botpkg.EvalJSON(p, composerTextJS, &ct, boxSel) != nil {
			return false, nil
		}
		o, ok := sendVerified(ct.Present, ct.Text, before, now)
		outcome = o
		return ok, nil
	})
	if err != nil {
		return "", fmt.Errorf("send could not be verified (composer not cleared and no message bubble rendered within %s)", verifyTimeout)
	}
	return outcome, nil
}

// dmButtonRejectRe matches profile controls that must never stand in for the
// Message button.
var dmButtonRejectRe = regexp.MustCompile(`(?i)follow|subscribe|useractions|notification|block|mute|more`)

// isMessageButton vets a Jev pick for the profile's Message button: a
// button (not a link, which would navigate elsewhere) that is not a
// follow/subscribe/notifications/more control.
func isMessageButton(info pickInfo) bool {
	if info.Tag != "BUTTON" && !strings.EqualFold(info.Role, "button") {
		return false
	}
	return !dmButtonRejectRe.MatchString(info.TestID) && !dmButtonRejectRe.MatchString(info.Label)
}

// SendDM opens target's profile, clicks its Message button, sends message
// in the conversation that opens, and verifies it was sent. A profile
// without a Message button (the user does not accept DMs from this account)
// is an error — no other route is tried.
func (b *XBot) SendDM(ctx context.Context, p browser.PageInterface, target, message string) (map[string]interface{}, error) {
	if strings.TrimSpace(message) == "" {
		return nil, errors.New("x: message is required")
	}
	u, handle, err := b.profileURL(target)
	if err != nil {
		return nil, err
	}
	if err := navigate(p, u); err != nil {
		return nil, err
	}
	defer clearMarks(p)
	var state string
	if err := poll(ctx, loadTimeout, func() (bool, error) {
		if err := botpkg.EvalJSON(p, profileReadyJS, &state); err != nil {
			return false, nil
		}
		return state != "", nil
	}); err != nil {
		if lerr := checkLoginRedirect(p); lerr != nil {
			return nil, lerr
		}
		return nil, fmt.Errorf("x: profile %s did not render: %w", u, err)
	}
	if state == "empty" {
		return nil, fmt.Errorf("x: profile %s is unavailable", u)
	}

	btn, _, err := findMarked(ctx, p, "", "[data-testid='sendDMFromProfile']", 5*time.Second)
	if err != nil {
		pick, info, jerr := b.jevPick(ctx, p, "click",
			"the Message (envelope) button in @"+handle+"'s profile header that opens a direct message conversation with them — not Follow, Subscribe, Notifications or More",
			"", "main")
		if jerr == nil && !isMessageButton(info) {
			pick.Release()
			jerr = fmt.Errorf("picked %q (%s) is not a Message button", truncateForError(info.Label, 40), info.TestID)
		}
		if jerr != nil {
			return nil, fmt.Errorf("x: @%s cannot be messaged: no Message button on the profile (they may not accept DMs from you) (jev fallback: %v)", handle, jerr)
		}
		defer pick.Release()
		btn = pick.Element
	}
	if err := botpkg.ClickTrusted(p, btn); err != nil {
		return nil, fmt.Errorf("x: click Message on @%s: %w", handle, err)
	}
	outcome, err := b.sendInConversation(ctx, p, message)
	if err != nil {
		return nil, fmt.Errorf("x: DM to @%s: %w", handle, err)
	}
	res := map[string]interface{}{
		"recipient":   handle,
		"profile_url": u,
		"message":     message,
		"sent":        true,
		"verified_by": string(outcome),
	}
	if cur, err := p.GetURL(); err == nil {
		res["conversation_url"] = cur
	}
	return res, nil
}

// conversationPathRe matches a DM conversation path in either UI.
var conversationPathRe = regexp.MustCompile(`^/(messages/\d+(-\d+)?|i/chat/[A-Za-z0-9_:-]+)/?$`)

// conversationURL validates and normalises a conversation URL.
func conversationURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && u.Host == "" && strings.HasPrefix(u.Path, "/") {
		u, err = url.Parse("https://x.com" + strings.TrimSpace(raw))
	}
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("x: %q is not a conversation URL", raw)
	}
	canonicalHost(u)
	if u.Host != "x.com" || !conversationPathRe.MatchString(u.Path) {
		return "", fmt.Errorf("x: %q is not an X conversation URL", raw)
	}
	return "https://x.com" + strings.TrimSuffix(u.Path, "/"), nil
}

// ReplyDM opens a conversation and sends message in it, verified. Opening a
// conversation marks it read on X.
func (b *XBot) ReplyDM(ctx context.Context, p browser.PageInterface, convURL, message string) (map[string]interface{}, error) {
	if strings.TrimSpace(message) == "" {
		return nil, errors.New("x: message is required")
	}
	u, err := conversationURL(convURL)
	if err != nil {
		return nil, err
	}
	if err := navigate(p, u); err != nil {
		return nil, err
	}
	defer clearMarks(p)
	outcome, err := b.sendInConversation(ctx, p, message)
	if err != nil {
		return nil, fmt.Errorf("x: reply in %s: %w", u, err)
	}
	return map[string]interface{}{
		"conversation_url": u,
		"message":          message,
		"sent":             true,
		"verified_by":      string(outcome),
	}, nil
}

// conversationsJS lists the inbox's conversations: every link to a
// conversation path, with the row around it. unread comes from an explicit
// unread marker or a bold preview line; last_from_me from a "You: …"
// preview (English UI).
const conversationsJS = `() => {
	const txt = (el) => el ? (el.innerText || el.textContent || '').trim() : '';
	const re = /^\/(messages\/\d+(-\d+)?|i\/chat\/[A-Za-z0-9_:-]+)\/?$/;
	const rows = [], seen = new Set();
	for (const a of document.querySelectorAll('a[href]')) {
		let path;
		try { path = new URL(a.getAttribute('href'), location.origin).pathname; } catch (e) { continue; }
		if (!re.test(path)) continue;
		path = path.replace(/\/$/, '');
		if (seen.has(path)) continue;
		seen.add(path);
		const row = a.closest("[data-testid='conversation']") || a.closest("[data-testid^='dm-conversation-item']") || a;
		const lines = txt(row).split('\n').map((s) => s.trim()).filter(Boolean);
		const preview = lines.length > 1 ? lines[lines.length - 1] : '';
		let bold = false;
		for (const el of row.querySelectorAll('span, div')) {
			if (el.children.length || (el.textContent || '').trim() !== preview || !preview) continue;
			const w = parseInt(getComputedStyle(el).fontWeight, 10);
			if (w >= 700) bold = true;
			break;
		}
		const marker = !!row.querySelector("[aria-label*='unread' i], [data-testid*='unread' i]") || /unread/i.test(row.getAttribute('aria-label') || '');
		rows.push({
			url: location.origin + path,
			name: (lines[0] || '').split(/\s+@[A-Za-z0-9_]{1,15}\b/)[0].trim(),
			preview,
			unread: marker || bold,
			last_from_me: /^(you|you sent|you reacted)\b/i.test(preview),
		});
	}
	const cells = document.querySelectorAll("[data-testid='conversation']").length;
	const empty = !!document.querySelector("[data-testid='emptyState']");
	return { rows, cells, empty };
}`

type rawConversation struct {
	URL        string `json:"url"`
	Name       string `json:"name"`
	Preview    string `json:"preview"`
	Unread     bool   `json:"unread"`
	LastFromMe bool   `json:"last_from_me"`
}

type conversationsSnapshot struct {
	Rows  []rawConversation `json:"rows"`
	Cells int               `json:"cells"`
	Empty bool              `json:"empty"`
}

// ListConversations lists up to max inbox conversations that await a reply:
// conversations whose last message is the viewer's own are skipped, and with
// unreadOnly only unread ones are returned.
func (b *XBot) ListConversations(ctx context.Context, p browser.PageInterface, max int, unreadOnly bool) ([]map[string]interface{}, error) {
	if max <= 0 {
		max = 20
	}
	if err := navigate(p, "https://x.com/messages"); err != nil {
		return nil, err
	}
	var snap conversationsSnapshot
	if err := poll(ctx, loadTimeout, func() (bool, error) {
		if err := botpkg.EvalJSON(p, conversationsJS, &snap); err != nil {
			return false, nil
		}
		return len(snap.Rows) > 0 || snap.Empty, nil
	}); err != nil {
		if lerr := checkLoginRedirect(p); lerr != nil {
			return nil, lerr
		}
		if snap.Cells > 0 {
			return nil, fmt.Errorf("x: inbox shows %d conversations but no conversation links", snap.Cells)
		}
		return nil, fmt.Errorf("x: inbox did not render: %w", err)
	}
	// Rows render progressively; read once more after a beat.
	if err := sleep(ctx, actionPause); err == nil {
		var again conversationsSnapshot
		if botpkg.EvalJSON(p, conversationsJS, &again) == nil && len(again.Rows) >= len(snap.Rows) {
			snap = again
		}
	}
	var out []map[string]interface{}
	for _, c := range snap.Rows {
		if c.LastFromMe || (unreadOnly && !c.Unread) {
			continue
		}
		out = append(out, map[string]interface{}{
			"url":     c.URL,
			"name":    c.Name,
			"preview": c.Preview,
			"unread":  c.Unread,
		})
		if len(out) >= max {
			break
		}
	}
	return out, nil
}
