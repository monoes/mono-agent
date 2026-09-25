//go:build !nosocial

package tiktok

// Direct messages and publishing. Like the other write actions these verify
// the outcome: a DM counts as sent only when a bubble with its text renders
// or the composer is cleared by the send, and only in a chat whose header
// names the intended recipient.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

var (
	// messageButtonSelectors open a DM from a profile.
	messageButtonSelectors = []string{
		"[data-e2e='message-button']",
		"[data-e2e='message-icon']",
	}
	// dmInputSelectors are the chat composer (or its container).
	dmInputSelectors = []string{
		"[data-e2e='message-input-area']",
		"[data-e2e='message-input']",
	}
	// dmSendSelectors send the typed message.
	dmSendSelectors = []string{
		"[data-e2e='message-send']",
		"[data-e2e='send-message-button']",
		"[data-e2e='send-message-icon']",
	}
)

// jsChatState reports the open chat's recipient and how many message
// bubbles contain snip.
const jsChatState = `
const head = document.querySelector('[data-e2e="chat-uniqueid"]');
const nick = document.querySelector('[data-e2e="chat-nickname"]');
const bubbles = [...document.querySelectorAll('[data-e2e="chat-item"],[data-e2e="chat-message"],[data-e2e="message-item"]')];
const ed = edToken ? marked(edToken) : null;
return {
  uniqueId: norm(head && (head.innerText || head.textContent)).replace(/^@/, ''),
  nickname: norm(nick && (nick.innerText || nick.textContent)),
  n: bubbles.filter(b => norm(b.innerText || b.textContent).includes(snip)).length,
  composer: ed ? textOf(ed) : null,
};
`

type chatState struct {
	UniqueID string  `json:"uniqueId"`
	Nickname string  `json:"nickname"`
	N        int     `json:"n"`
	Composer *string `json:"composer"`
}

// sendVerified reports whether the observable page state confirms a send:
// a new bubble carrying the text rendered, or the composer was cleared.
func sendVerified(before, after chatState) bool {
	if after.N > before.N {
		return true
	}
	return after.Composer != nil && strings.TrimSpace(*after.Composer) == ""
}

// sendInOpenChat types message into the open chat and sends it. recipient
// check: wantUser (handle) must match the chat header's unique id when given,
// wantNick the header nickname when given.
func (b *TikTokBot) sendInOpenChat(ctx context.Context, page browser.PageInterface, wantUser, wantNick, message string) (map[string]interface{}, error) {
	edTok, sendTok := newToken(), newToken()
	defer unmark(page, edTok)
	defer unmark(page, sendTok)

	var probe toggleFind
	ok, _ := waitFor(ctx, findTimeout, func() (bool, error) {
		err := evalJS(page, "sels, token", jsFindEditor, &probe, dmInputSelectors, edTok)
		return probe.Count > 0, err
	})
	if !ok && !b.JevAvailable(page) {
		return nil, errors.New("tiktok: message box not found (the chat did not open)")
	}
	if _, err := b.findEditor(ctx, page, dmInputSelectors, edTok,
		"the message text box of the open TikTok direct-message chat — not the search box and not a comment box"); err != nil {
		return nil, fmt.Errorf("tiktok: message box not found: %w", err)
	}

	snip := snippet(message)
	var before chatState
	// The chat header can render after the composer.
	_, err := waitFor(ctx, findTimeout, func() (bool, error) {
		err := evalJS(page, "snip, edToken", jsChatState, &before, snip, edTok)
		return before.UniqueID != "" || before.Nickname != "", err
	})
	if err != nil {
		return nil, fmt.Errorf("tiktok: read chat: %w", err)
	}
	// Recipient check: never type into somebody else's chat.
	if wantUser != "" && before.UniqueID != "" && normUser(before.UniqueID) != normUser(wantUser) {
		return nil, fmt.Errorf("tiktok: open chat is with @%s, not @%s; not sending", before.UniqueID, strings.TrimPrefix(wantUser, "@"))
	}
	if wantNick != "" && before.Nickname != "" && !strings.EqualFold(before.Nickname, strings.TrimSpace(wantNick)) {
		return nil, fmt.Errorf("tiktok: open chat is with %q, not %q; not sending", before.Nickname, wantNick)
	}
	if wantUser == "" && wantNick == "" {
		return nil, errors.New("tiktok: no recipient to check the chat against")
	}
	if before.UniqueID == "" && before.Nickname == "" {
		return nil, errors.New("tiktok: cannot confirm who the open chat is with (no chat header); not sending")
	}

	if err := typeInto(ctx, page, edTok, message); err != nil {
		return nil, fmt.Errorf("tiktok: type message: %w", err)
	}
	var sendF toggleFind
	if err := evalJS(page, "sels, edToken, token", jsFindSubmit, &sendF, dmSendSelectors, edTok, sendTok); err != nil {
		return nil, fmt.Errorf("tiktok: find send button: %w", err)
	}
	switch {
	case sendF.Count == 1 && sendF.State != "disabled":
		if err := clickMarked(page, sendTok); err != nil {
			return nil, fmt.Errorf("tiktok: click send: %w", err)
		}
	default:
		// No (unique, enabled) send button: Enter in the focused composer
		// sends on TikTok.
		el, err := markedElement(page, edTok)
		if err != nil {
			return nil, fmt.Errorf("tiktok: message box vanished: %w", err)
		}
		if err := el.Focus(); err != nil {
			return nil, fmt.Errorf("tiktok: focus message box: %w", err)
		}
		if err := botpkg.PressEnter(page); err != nil {
			return nil, fmt.Errorf("tiktok: send message: %w", err)
		}
	}

	var after chatState
	var how string
	ok, _ = waitFor(ctx, verifyTimeout, func() (bool, error) {
		if err := evalJS(page, "snip, edToken", jsChatState, &after, snip, edTok); err != nil {
			return false, err
		}
		if !sendVerified(before, after) {
			return false, nil
		}
		how = "composer_cleared"
		if after.N > before.N {
			how = "message_rendered"
		}
		return true, nil
	})
	if !ok {
		return nil, errors.New("tiktok: send could not be verified (no new message bubble and the composer still holds the text)")
	}
	return map[string]interface{}{
		"success": true, "status": "sent", "verified": how,
		"recipient": firstNonEmpty(after.UniqueID, before.UniqueID, wantUser), "nickname": firstNonEmpty(after.Nickname, before.Nickname),
	}, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// SendDM opens a chat with the account at target (profile URL or handle)
// through its profile's Message button and sends message, verified. An
// account without a Message button (DMs closed, not mutual) is an error — no
// other button is ever clicked instead.
func (b *TikTokBot) SendDM(ctx context.Context, page browser.PageInterface, target, message string) (map[string]interface{}, error) {
	u, handle, err := profileTarget(target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("tiktok: message is required")
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	_, _, _ = page.Race(messageButtonSelectors, findTimeout)
	tok := newToken()
	defer unmark(page, tok)
	var f toggleFind
	if err := evalJS(page, "sels, token", jsFindOption, &f, messageButtonSelectors, tok); err != nil {
		return nil, fmt.Errorf("tiktok: find Message button: %w", err)
	}
	if f.Count != 1 {
		if !b.JevAvailable(page) {
			return nil, fmt.Errorf("tiktok: Message button not found on %s (%d matches; the account may not accept messages)", u, f.Count)
		}
		jf, jerr := b.jevToggle(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Message button in the header of the TikTok profile @%s that opens a direct-message chat with them — not Follow, not Share", handle),
		}, `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const b = e.closest('button,[role="button"],a') || e;
if (b.matches('[data-e2e*="follow"]') || b.querySelector('[data-e2e*="follow"]')) return {count: 1, rejected: 'a follow control'};
mark(b, token);
return {count: 1};`, tok)
		if jerr != nil {
			return nil, fmt.Errorf("tiktok: Message button not found on %s (jev fallback: %v)", u, jerr)
		}
		f = jf
	}
	if f.State == "disabled" {
		return nil, fmt.Errorf("tiktok: messaging is disabled for %s", u)
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("tiktok: click Message on %s: %w", u, err)
	}
	res, err := b.sendInOpenChat(ctx, page, handle, "", message)
	if err != nil {
		return nil, fmt.Errorf("%w (to @%s)", err, handle)
	}
	res["profileURL"] = u
	res["username"] = handle
	return res, nil
}

// SendMessage implements botpkg.BotAdapter through SendDM.
func (b *TikTokBot) SendMessage(ctx context.Context, page browser.PageInterface, username, message string) error {
	_, err := b.SendDM(ctx, page, username, message)
	return err
}

// ---------------------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------------------

const messagesURL = "https://www.tiktok.com/messages"

const jsConversations = `
return [...document.querySelectorAll('[data-e2e="chat-list-item"]')].map(it => {
  const nick = it.querySelector('[data-e2e="chat-list-item-nickname"]');
  const last = it.querySelector('[data-e2e="chat-list-item-message"]');
  const time = it.querySelector('[data-e2e="chat-list-item-time"]');
  const lines = (it.innerText || '').split('\n').map(norm).filter(Boolean);
  return {
    name: norm(nick && (nick.innerText || nick.textContent)) || lines[0] || '',
    lastMessage: norm(last && (last.innerText || last.textContent)) || lines[1] || '',
    time: norm(time && (time.innerText || time.textContent)),
    unread: !!it.querySelector('[data-e2e="chat-list-item-unread"],[data-e2e*="unread"]'),
  };
}).filter(c => c.name !== '');
`

// ListConversations opens the inbox and returns up to maxCount conversations
// ({name, lastMessage, time, unread}); unreadOnly keeps the unread ones.
func (b *TikTokBot) ListConversations(ctx context.Context, page browser.PageInterface, maxCount int, unreadOnly bool) ([]map[string]interface{}, error) {
	if maxCount <= 0 {
		maxCount = 20
	}
	if err := open(ctx, page, messagesURL); err != nil {
		return nil, err
	}
	if _, _, err := page.Race([]string{"[data-e2e='chat-list-item']"}, findTimeout); err != nil {
		return nil, fmt.Errorf("tiktok: conversation list not found on %s (not logged in, or no conversations)", messagesURL)
	}
	var all []map[string]interface{}
	if err := evalJS(page, "", jsConversations, &all); err != nil {
		return nil, fmt.Errorf("tiktok: read conversations: %w", err)
	}
	out := []map[string]interface{}{}
	for _, c := range all {
		if unreadOnly {
			if u, _ := c["unread"].(bool); !u {
				continue
			}
		}
		out = append(out, c)
		if len(out) >= maxCount {
			break
		}
	}
	return out, nil
}

// ReplyToConversation opens the inbox conversation whose name is exactly
// name and sends message there, verified. Several conversations with that
// name is an error.
func (b *TikTokBot) ReplyToConversation(ctx context.Context, page browser.PageInterface, name, message string) (map[string]interface{}, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tiktok: conversation name is required")
	}
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("tiktok: message is required")
	}
	if err := open(ctx, page, messagesURL); err != nil {
		return nil, err
	}
	if _, _, err := page.Race([]string{"[data-e2e='chat-list-item']"}, findTimeout); err != nil {
		return nil, fmt.Errorf("tiktok: conversation list not found on %s", messagesURL)
	}
	tok := newToken()
	defer unmark(page, tok)
	var n int
	if err := evalJS(page, "name, token", `
const hits = [...document.querySelectorAll('[data-e2e="chat-list-item"]')].filter(it => {
  const nick = it.querySelector('[data-e2e="chat-list-item-nickname"]');
  const t = norm(nick && (nick.innerText || nick.textContent)) || ((it.innerText || '').split('\n').map(norm).filter(Boolean)[0] || '');
  return t === norm(name);
});
if (hits.length === 1) mark(hits[0], token);
return hits.length;`, &n, name, tok); err != nil {
		return nil, fmt.Errorf("tiktok: find conversation: %w", err)
	}
	switch {
	case n == 0:
		return nil, fmt.Errorf("tiktok: no conversation named %q", name)
	case n > 1:
		return nil, fmt.Errorf("tiktok: %d conversations named %q; refusing to guess", n, name)
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("tiktok: open conversation %q: %w", name, err)
	}
	if err := pause(ctx, time.Second); err != nil {
		return nil, err
	}
	res, err := b.sendInOpenChat(ctx, page, "", name, message)
	if err != nil {
		return nil, fmt.Errorf("%w (conversation %q)", err, name)
	}
	res["conversation"] = name
	return res, nil
}

// ---------------------------------------------------------------------------
// Publishing (after the action's upload step attached the file)
// ---------------------------------------------------------------------------

var (
	captionSelectors = []string{
		"[data-e2e='caption-editor']",
		"[data-e2e='caption_container']",
		".caption-editor",
	}
	postVideoSelectors = []string{
		"[data-e2e='post_video_button']",
		"[data-e2e='post-button']",
	}
	// publishOutcomes: success first, then the error states.
	publishOutcomes = []botpkg.Outcome{
		{Label: "success", Selector: "[data-e2e='post-success'],[data-e2e='upload-success']"},
		{Label: "error", Selector: "[data-e2e='post-error'],[data-e2e='upload-error'],[role='alert'][data-e2e*='error']"},
	}
	// publishTimeout bounds the wait for TikTok to accept the post.
	publishTimeout = 90 * time.Second
)

// PublishUploadedVideo finishes a TikTok Studio upload whose file the
// action's upload step already attached: it replaces the prefilled caption
// with caption, waits for the Post button to enable (upload processed),
// clicks it, and waits for TikTok to confirm (success banner, or the
// redirect to the content manager). Anything else is an error.
func (b *TikTokBot) PublishUploadedVideo(ctx context.Context, page browser.PageInterface, caption string) (map[string]interface{}, error) {
	edTok, postTok := newToken(), newToken()
	defer unmark(page, edTok)
	defer unmark(page, postTok)

	var probe toggleFind
	_, _ = waitFor(ctx, findTimeout, func() (bool, error) {
		err := evalJS(page, "sels, token", jsFindEditor, &probe, captionSelectors, edTok)
		return probe.Count > 0, err
	})
	if _, err := b.findEditor(ctx, page, captionSelectors, edTok,
		"the caption / description text box of the TikTok upload form"); err != nil {
		return nil, fmt.Errorf("tiktok: caption box not found (did the upload start?): %w", err)
	}
	if strings.TrimSpace(caption) != "" {
		// TikTok prefills the caption with the file name: select it so the
		// typed caption replaces it.
		var ok bool
		if err := evalJS(page, "token", `
const e = marked(token);
if (!e) return false;
e.focus();
if (/^(TEXTAREA|INPUT)$/.test(e.tagName)) { e.select(); return true; }
const r = document.createRange(); r.selectNodeContents(e);
const s = getSelection(); s.removeAllRanges(); s.addRange(r);
return true;`, &ok, edTok); err != nil || !ok {
			return nil, fmt.Errorf("tiktok: select caption: %v", err)
		}
		el, err := markedElement(page, edTok)
		if err != nil {
			return nil, err
		}
		if cc, isCDP := page.(interface {
			CDP(string, map[string]interface{}) (map[string]interface{}, error)
		}); isCDP {
			if _, err := cc.CDP("Input.insertText", map[string]interface{}{"text": caption}); err != nil {
				return nil, fmt.Errorf("tiktok: type caption: %w", err)
			}
		} else if err := el.Input(caption); err != nil {
			return nil, fmt.Errorf("tiktok: type caption: %w", err)
		}
		var got string
		if err := evalJS(page, "token", `const e = marked(token); return e ? textOf(e) : '';`, &got, edTok); err != nil {
			return nil, fmt.Errorf("tiktok: read caption: %w", err)
		}
		if !strings.Contains(strings.Join(strings.Fields(got), " "), snippet(caption)) {
			return nil, fmt.Errorf("tiktok: caption did not take (box holds %q)", got)
		}
	}

	// The Post button enables once the upload is processed.
	var f toggleFind
	ok, _ := waitFor(ctx, publishTimeout, func() (bool, error) {
		err := evalJS(page, "sels, edToken, token", jsFindSubmit, &f, postVideoSelectors, edTok, postTok)
		return f.Count == 1 && f.State == "enabled", err
	})
	if !ok {
		if f.Count == 1 {
			return nil, errors.New("tiktok: Post button never enabled (upload not processed or rejected)")
		}
		if !b.JevAvailable(page) {
			return nil, fmt.Errorf("tiktok: Post button not found (%d matches)", f.Count)
		}
		if err := b.findSubmit(ctx, page, postVideoSelectors, edTok, postTok, "the Post button that publishes the video in the TikTok upload form (not Discard, not Save draft)"); err != nil {
			return nil, fmt.Errorf("tiktok: Post button: %w", err)
		}
	}
	startURL, _ := page.GetURL()
	if err := clickMarked(page, postTok); err != nil {
		return nil, fmt.Errorf("tiktok: click Post: %w", err)
	}

	var outcome string
	ok, _ = waitFor(ctx, publishTimeout, func() (bool, error) {
		if cur, err := page.GetURL(); err == nil && cur != startURL && strings.Contains(cur, "/tiktokstudio/content") {
			outcome = "redirected_to_content"
			return true, nil
		}
		o, _, err := botpkg.WaitForOutcomes(page, publishOutcomes, 10*time.Millisecond)
		if err != nil {
			return false, nil
		}
		outcome = o.Label
		return true, nil
	})
	switch {
	case !ok:
		return nil, errors.New("tiktok: publish not confirmed (no success message or redirect)")
	case outcome == "error":
		return nil, errors.New("tiktok: TikTok reported an error publishing the video")
	}
	return map[string]interface{}{"success": true, "status": "published", "verified": outcome}, nil
}
