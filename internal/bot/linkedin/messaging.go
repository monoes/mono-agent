//go:build social

package linkedin

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// messageSnippet returns the searchable portion of a message used to match a
// rendered message bubble (long messages may be visually truncated by the UI).
func messageSnippet(message string) string {
	return snippet(message)
}

// sendVerified reports whether the observable page state confirms the message
// was sent: the composer is cleared, or a message bubble containing the
// message snippet has rendered in the thread.
func sendVerified(composerText string, bubbleTexts []string, message string) bool {
	if strings.TrimSpace(composerText) == "" {
		return true
	}
	snip := messageSnippet(message)
	for _, bubble := range bubbleTexts {
		if strings.Contains(normSpace(bubble), snip) {
			return true
		}
	}
	return false
}

// messageEntryJS finds how to start a conversation from a profile page:
// a Message link (state "link", href returned), a Message button tagged tok
// (state "button"), or the top card's More button tagged tok+"-more"
// (state "more"). It only looks inside the profile's top card and never
// returns a Connect / Follow / Invite control.
const messageEntryJS = `(tok) => {
	L.unmarkAll(tok); L.unmarkAll(tok + '-more');
	const main = document.querySelector('main') || document.body;
	const card = main.querySelector('[componentkey*="Topcard"], [componentkey*="topcard"], section.pv-top-card, .pv-top-card, .ph5, section.artdeco-card') || main;
	const nameEl = card.querySelector('h1, h2');
	const name = nameEl ? L.text(nameEl) : '';
	const bad = /connect|invite|follow|pending|withdraw/i;
	const isMsg = (el) => {
		const l = (el.getAttribute('aria-label') || '').trim();
		const t = L.text(el);
		if (bad.test(l) && !/^Message\b/i.test(l)) return false;
		return /^Message\b/i.test(l) || /^Message$/i.test(t);
	};
	const inCard = Array.from(card.querySelectorAll('a, button, [role="button"]')).filter((e) => L.vis(e) && !e.closest('aside, nav, header'));
	const link = inCard.find((a) => a.tagName === 'A' && /\/messaging\/(compose|thread)/.test(a.getAttribute('href') || '') && !bad.test(L.label(a)));
	if (link) return { state: 'link', href: link.href, name };
	const btn = inCard.find(isMsg);
	if (btn) { L.mark(btn, tok); return { state: 'button', name }; }
	const more = inCard.find((b) => /^More( actions)?$/i.test((b.getAttribute('aria-label') || '').trim()) || /^More$/i.test(L.text(b)));
	if (more) { L.mark(more, tok + '-more'); return { state: 'more', name }; }
	return { state: 'none', name };
}`

// messageMenuItemJS tags the visible "Message" entry of an open dropdown.
const messageMenuItemJS = `(tok) => {
	L.unmarkAll(tok);
	const menus = L.deepAll('[role="menu"], .artdeco-dropdown__content, [role="listbox"]').filter((m) => L.vis(m) && !m.closest('aside, nav, header'));
	const items = [];
	for (const m of menus) {
		m.querySelectorAll('[role="menuitem"], .artdeco-dropdown__item, [role="button"], a, button, div[aria-label]').forEach((e) => { if (L.vis(e) && !items.includes(e)) items.push(e); });
	}
	const it = items.find((e) => {
		const l = (e.getAttribute('aria-label') || '').trim();
		const t = L.text(e);
		if (/connect|invite|follow|pending|withdraw/i.test(l + ' ' + t) && !/^Message\b/i.test(l || t)) return false;
		return /^Message\b/i.test(l) || /^Message$/i.test(t);
	});
	if (!it) return { state: 'none' };
	const a = it.closest('a[href*="/messaging/"]') || (it.tagName === 'A' ? it : it.querySelector('a[href*="/messaging/"]'));
	if (a && /\/messaging\/(compose|thread)/.test(a.getAttribute('href') || '')) return { state: 'link', href: a.href };
	L.mark(it, tok);
	return { state: 'button' };
}`

// composerJS finds the message composer to type into and tags it tok.
// Several conversations may be open in the overlay, so with a recipient
// name it only takes the composer whose conversation names them, or the one
// LinkedIn focused on opening; without a name, a lone or focused composer.
// It never guesses between several.
const composerJS = `(tok, name) => {
	L.unmarkAll(tok);
	const sel = 'div.msg-form__contenteditable[contenteditable="true"], .msg-form [contenteditable="true"], form.msg-form textarea, div[role="textbox"][contenteditable="true"][aria-label*="message" i], textarea[aria-label*="message" i]';
	const eds = L.deepAll(sel).filter((e) => L.vis(e));
	const uniq = eds.filter((e, i) => eds.indexOf(e) === i && !eds.some((o) => o !== e && o.contains(e)));
	if (!uniq.length) return { ok: false, state: 'none' };
	const convo = (e) => L.convo(e);
	const a = L.activeDeep();
	const isFocused = (e) => !!a && (e === a || e.contains(a));
	let pick = null;
	if (name) {
		const full = name.toLowerCase(), first = full.split(/\s+/)[0];
		const named = uniq.filter((e) => { const c = convo(e); const t = c ? L.text(c).toLowerCase() : ''; return t.includes(full) || t.includes(first); });
		if (named.length === 1) pick = named[0];
		else if (named.length > 1) { const f = named.filter(isFocused); if (f.length === 1) pick = f[0]; }
	}
	if (!pick && !name) {
		const f = uniq.filter(isFocused);
		if (f.length === 1) pick = f[0];
		else if (uniq.length === 1) pick = uniq[0];
	}
	if (!pick) return { ok: false, state: 'ambiguous', count: uniq.length };
	L.mark(pick, tok);
	return { ok: true, state: 'ready', count: uniq.length };
}`

// sendButtonJS tags the Send button of the form holding the composer edTok.
const sendButtonJS = `(edTok, tok) => {
	L.unmarkAll(tok);
	const ed = L.marked(edTok);
	if (!ed) return { state: 'no_editor' };
	const form = ed.closest('form') || ed.closest('.msg-form, .msg-overlay-conversation-bubble, [role="dialog"], section');
	if (!form) return { state: 'no_form' };
	const btns = Array.from(form.querySelectorAll('button')).filter((b) => L.vis(b));
	const b = btns.find((x) => /msg-form__send-button/.test(x.className))
		|| btns.find((x) => x.type === 'submit')
		|| btns.find((x) => /^Send$/i.test(L.text(x)) || /^Send$/i.test((x.getAttribute('aria-label') || '').trim()));
	if (!b) return { state: 'no_button' };
	L.mark(b, tok);
	if (b.disabled || b.getAttribute('aria-disabled') === 'true') return { state: 'disabled' };
	return { state: 'ready' };
}`

// sentStateJS reports the composer's text and the texts of the message
// bubbles in its conversation.
const sentStateJS = `(edTok) => {
	const ed = L.marked(edTok);
	const scope = ed ? (ed.closest('.msg-overlay-conversation-bubble, .msg-convo-wrapper, .msg-thread, [role="dialog"]') || (ed.getRootNode() !== document ? ed.getRootNode() : document)) : document;
	const bubbles = Array.from(scope.querySelectorAll('.msg-s-event-listitem__body, p.msg-s-event-listitem__body, .msg-s-message-list__event, .msg-s-message-group__msg, [data-event-urn] p'))
		.map((b) => L.text(b));
	return { composer: ed ? L.editorText(ed) : '', bubbles, present: !!ed };
}`

type sentState struct {
	Composer string   `json:"composer"`
	Bubbles  []string `json:"bubbles"`
	Present  bool     `json:"present"`
}

// sendInComposer types message into the conversation composer on the page,
// clicks its Send button and verifies the message went out (composer cleared
// or a bubble with the text rendered). name, when known, identifies the
// recipient among several open conversations.
func (b *LinkedInBot) sendInComposer(ctx context.Context, page browser.PageInterface, name, message string) error {
	tok := newToken("msg")
	defer unmark(page, tok)
	var cs struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
		Count int    `json:"count"`
	}
	if err := waitFor(ctx, page, findTimeout, composerJS, &cs, tok, name); err != nil {
		// One more read for the diagnosis (ambiguous vs none).
		_ = run(page, composerJS, &cs, tok, name)
		if cs.State == "ambiguous" {
			return fmt.Errorf("linkedin: %d conversations are open and none is clearly %q's; not sending", cs.Count, name)
		}
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{Kind: "fill",
			Intent: fmt.Sprintf("the message text box of the open LinkedIn conversation with %s", orUnknown(name))})
		if jerr != nil {
			return fmt.Errorf("linkedin: message composer not found: %w (jev fallback: %v)", err, jerr)
		}
		var ok bool
		aerr := run(page, `(marker, tok, name) => {
			const el = L.deepAll('[data-monoagent-jev="' + marker + '"]')[0];
			if (!el || !(el.isContentEditable || el.tagName === 'TEXTAREA')) return false;
			if (name) {
				const c = L.convo(el);
				const t = c ? L.text(c).toLowerCase() : '';
				if (!t.includes(name.toLowerCase().split(/\s+/)[0])) return false;
			}
			L.unmarkAll(tok); L.mark(el, tok); return true;
		}`, &ok, pick.Marker, tok, name)
		pick.Release()
		if aerr != nil || !ok {
			return fmt.Errorf("linkedin: jev did not pick an editable message box in the conversation with %s; not sending", orUnknown(name))
		}
	}
	var before string
	_ = run(page, `(tok) => { const el = L.marked(tok); return el ? L.editorText(el) : ''; }`, &before, tok)
	if strings.TrimSpace(before) != "" {
		return fmt.Errorf("linkedin: the message composer already holds a draft (%q); not sending on top of it", truncate(before, 60))
	}
	if err := typeMarked(ctx, page, tok, message); err != nil {
		return fmt.Errorf("linkedin: typing the message failed: %w", err)
	}

	sendTok := tok + "-send"
	defer unmark(page, sendTok)
	var st reactState
	_ = poll(ctx, 3*time.Second, func() (bool, error) {
		if err := run(page, sendButtonJS, &st, tok, sendTok); err != nil {
			return false, nil
		}
		return st.State == "ready", nil
	})
	if st.State != "ready" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{Kind: "click",
			Intent: fmt.Sprintf("the Send button of the LinkedIn message composer for the conversation with %s", orUnknown(name))})
		if jerr != nil {
			return fmt.Errorf("linkedin: Send button not usable (%s) (jev fallback: %v)", st.State, jerr)
		}
		var ok bool
		aerr := run(page, `(marker, edTok, tok) => {
			const el = L.deepAll('[data-monoagent-jev="' + marker + '"]')[0];
			const ed = L.marked(edTok);
			if (!el || !ed) return false;
			const b = el.closest('button') || el;
			const form = ed.closest('form') || ed.closest('.msg-form, .msg-overlay-conversation-bubble');
			if (form && !form.contains(b)) return false;
			L.unmarkAll(tok); L.mark(b, tok); return true;
		}`, &ok, pick.Marker, tok, sendTok)
		pick.Release()
		if aerr != nil || !ok {
			return fmt.Errorf("linkedin: jev picked a button outside the composer; not sending")
		}
	}
	if err := clickMarked(page, sendTok); err != nil {
		return fmt.Errorf("linkedin: failed to click Send: %w", err)
	}
	var last sentState
	err := poll(ctx, verifyTimeout, func() (bool, error) {
		if err := run(page, sentStateJS, &last, tok); err != nil {
			return false, nil
		}
		if !last.Present {
			// The composer went away with the conversation; only a bubble
			// counts then.
			return sendVerified("x", last.Bubbles, message), nil
		}
		return sendVerified(last.Composer, last.Bubbles, message), nil
	})
	if err != nil {
		return fmt.Errorf("linkedin: send could not be verified (composer still holds %q and no message bubble with the text rendered): %w", truncate(last.Composer, 60), err)
	}
	return nil
}

func orUnknown(name string) string {
	if strings.TrimSpace(name) == "" {
		return "the intended recipient"
	}
	return name
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// SendMessageTo sends message to the member at target — a profile URL
// (https://www.linkedin.com/in/<slug>/), a bare profile slug, or a
// messaging thread URL — and verifies it was sent. It opens the conversation
// through the profile's Message control (directly or under More); it never
// clicks Connect, Follow or anything else when there is no Message option,
// and fails instead.
func (b *LinkedInBot) SendMessageTo(ctx context.Context, page browser.PageInterface, target, message string) (map[string]interface{}, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("linkedin: recipient is required")
	}
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("linkedin: message is required")
	}
	if strings.Contains(target, "/messaging/thread/") {
		if err := b.ReplyToConversation(ctx, page, target, message); err != nil {
			return nil, err
		}
		return map[string]interface{}{"success": true, "recipient": target, "sent": true}, nil
	}
	profileURL := target
	if !strings.Contains(target, "linkedin.com/") && !strings.HasPrefix(target, "/") {
		profileURL = "https://www.linkedin.com/in/" + url.PathEscape(strings.Trim(target, "/")) + "/"
	}
	profileURL = b.ResolveURL(profileURL)
	if err := navigate(ctx, page, profileURL); err != nil {
		return nil, err
	}

	tok := newToken("entry")
	defer unmark(page, tok)
	defer unmark(page, tok+"-more")
	var entry struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
		Href  string `json:"href"`
		Name  string `json:"name"`
	}
	_ = poll(ctx, findTimeout, func() (bool, error) {
		if err := run(page, messageEntryJS, &entry, tok); err != nil {
			return false, nil
		}
		return entry.State == "link" || entry.State == "button" || (entry.State == "more" && entry.Name != ""), nil
	})
	name := entry.Name
	if entry.State == "more" {
		if err := clickMarked(page, tok+"-more"); err != nil {
			return nil, fmt.Errorf("linkedin: failed to open the profile's More menu: %w", err)
		}
		itemTok := tok + "-item"
		defer unmark(page, itemTok)
		var it struct {
			State string `json:"state"`
			Href  string `json:"href"`
		}
		_ = poll(ctx, 3*time.Second, func() (bool, error) {
			if err := run(page, messageMenuItemJS, &it, itemTok); err != nil {
				return false, nil
			}
			return it.State != "none", nil
		})
		switch it.State {
		case "link":
			entry.State, entry.Href = "link", it.Href
		case "button":
			if err := clickMarked(page, itemTok); err != nil {
				return nil, fmt.Errorf("linkedin: failed to click Message in the More menu: %w", err)
			}
			entry.State = "opened"
		default:
			entry.State = "none"
		}
	}
	switch entry.State {
	case "link":
		if err := navigate(ctx, page, b.ResolveURL(entry.Href)); err != nil {
			return nil, err
		}
	case "button":
		if err := clickMarked(page, tok); err != nil {
			return nil, fmt.Errorf("linkedin: failed to click Message: %w", err)
		}
	case "opened":
	default:
		return nil, fmt.Errorf("linkedin: %s has no Message option (not a connection, or messaging is restricted); not sending — no connection request was made", profileURL)
	}
	if err := sleepCtx(ctx, uiSettle); err != nil {
		return nil, err
	}
	if err := b.sendInComposer(ctx, page, name, message); err != nil {
		return nil, err
	}
	return map[string]interface{}{"success": true, "recipient": profileURL, "name": name, "sent": true}, nil
}

// SendMessage sends a direct message to username (a profile slug or URL).
// It implements botpkg.BotAdapter.
func (b *LinkedInBot) SendMessage(ctx context.Context, p browser.PageInterface, username, message string) error {
	_, err := b.SendMessageTo(ctx, p, username, message)
	return err
}

// ReplyToConversation opens a messaging thread URL and sends message there.
func (b *LinkedInBot) ReplyToConversation(ctx context.Context, page browser.PageInterface, threadURL, message string) error {
	threadURL = strings.TrimSpace(threadURL)
	if !strings.Contains(threadURL, "/messaging/thread/") {
		return fmt.Errorf("linkedin: %q is not a messaging thread URL", threadURL)
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("linkedin: message is required")
	}
	if err := navigate(ctx, page, b.ResolveURL(threadURL)); err != nil {
		return err
	}
	var name string
	_ = run(page, `() => { const h = document.querySelector('.msg-entity-lockup__entity-title, .msg-thread__link-to-profile, h2.msg-overlay-bubble-header__title'); return h ? L.text(h) : ''; }`, &name)
	return b.sendInComposer(ctx, page, name, message)
}

// conversationsJS lists the inbox's conversations without opening any.
const conversationsJS = `() => {
	const items = Array.from(document.querySelectorAll('li.msg-conversation-listitem, li.msg-conversations-container__convo-item'));
	return items.map((li) => {
		const a = li.querySelector('a[href*="/messaging/thread/"]');
		const nameEl = li.querySelector('.msg-conversation-listitem__participant-names, .msg-conversation-card__participant-names');
		const snipEl = li.querySelector('.msg-conversation-card__message-snippet, .msg-conversation-card__message-snippet-body');
		const timeEl = li.querySelector('time, .msg-conversation-listitem__time-stamp, .msg-conversation-card__time-stamp');
		const snippet = snipEl ? L.text(snipEl) : '';
		const unread = !!li.querySelector('.msg-conversation-card__convo-item-container--unread, .msg-conversation-card__unread-count, .notification-badge--show') || /--unread/.test(li.innerHTML.slice(0, 2000));
		return {
			url: a ? a.href.split('?')[0] : '',
			name: nameEl ? L.text(nameEl) : '',
			snippet,
			timestamp: timeEl ? (timeEl.getAttribute('datetime') || L.text(timeEl)) : '',
			unread,
			lastFromMe: /^You:/i.test(snippet),
		};
	}).filter((c) => c.url);
}`

// ListConversations reads up to max conversations from the messaging inbox
// (url, name, snippet, timestamp, unread, last_from_me) without opening
// them. onlyUnread keeps unread conversations whose last message is not
// the viewer's own.
func (b *LinkedInBot) ListConversations(ctx context.Context, page browser.PageInterface, max int, onlyUnread bool) ([]map[string]interface{}, error) {
	if max <= 0 {
		max = 20
	}
	if err := navigate(ctx, page, "https://www.linkedin.com/messaging/"); err != nil {
		return nil, err
	}
	var raw []struct {
		URL        string `json:"url"`
		Name       string `json:"name"`
		Snippet    string `json:"snippet"`
		Timestamp  string `json:"timestamp"`
		Unread     bool   `json:"unread"`
		LastFromMe bool   `json:"lastFromMe"`
	}
	err := poll(ctx, findTimeout, func() (bool, error) {
		if err := run(page, conversationsJS, &raw); err != nil {
			return false, nil
		}
		return len(raw) > 0, nil
	})
	if err != nil {
		return nil, fmt.Errorf("linkedin: no conversations found in the inbox: %w", err)
	}
	out := []map[string]interface{}{}
	for _, c := range raw {
		if onlyUnread && (!c.Unread || c.LastFromMe) {
			continue
		}
		out = append(out, map[string]interface{}{
			"url": c.URL, "name": c.Name, "snippet": c.Snippet, "timestamp": c.Timestamp,
			"unread": c.Unread, "last_from_me": c.LastFromMe,
		})
		if len(out) >= max {
			break
		}
	}
	return out, nil
}
