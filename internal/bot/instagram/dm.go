//go:build !nosocial

package instagram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// jsDM holds the direct-message helpers (prepended to DM scripts).
const jsDM = `
const dmComposer = () => {
	const sels = ['div[role="textbox"][contenteditable="true"][aria-label^="Message"]', 'div[contenteditable="true"][aria-label^="Message"]',
		'textarea[placeholder^="Message"]', 'textarea[aria-label^="Message"]', 'div[role="textbox"][contenteditable="true"]'];
	for (const sel of sels) {
		const el = Array.from(document.querySelectorAll(sel)).find((e) => vis(e) && !e.closest('form textarea[aria-label^="Add a comment"]'));
		if (el) return el;
	}
	return null;
};
// dmBubbles returns the texts of the rendered messages (not the composer).
const dmBubbles = () => {
	const box = dmComposer();
	const root = document.querySelector('[aria-label^="Messages in conversation"], [role="grid"]') || document.querySelector('main') || document.body;
	const out = [];
	for (const el of root.querySelectorAll('div[dir="auto"], span[dir="auto"], [data-testid="conversation_message"]')) {
		if (box && (box.contains(el) || el.contains(box))) continue;
		if (el.querySelector('div[dir="auto"], span[dir="auto"]')) continue;
		const t = T(el);
		if (t) out.push(t);
	}
	return out;
};
`

func dmJS(body string) string { return jsDM + "\nreturn (" + body + ")(...args);" }

// evalDM runs a DM script with both jsLib and jsDM in scope.
func evalDM(p browser.PageInterface, body string, out interface{}, args ...interface{}) error {
	return evalJS(p, "(...args) => {"+dmJS(body)+"}", out, args...)
}

// waitComposer waits for the open conversation's message box and marks it,
// falling back to Jev.
func (b *InstagramBot) waitComposer(ctx context.Context, p browser.PageInterface, m string, timeout time.Duration) error {
	err := poll(ctx, timeout, func() (bool, error) {
		var found bool
		err := evalDM(p, `(m) => { const c = dmComposer(); if (c) mark(c, m); return !!c; }`, &found, m)
		return found, err
	})
	if err == nil {
		return nil
	}
	d, jerr := b.jevPick(ctx, p, "fill", "the message text box of the open direct message conversation", "", m)
	if jerr == nil && (d.Tag == "textarea" || d.Tag == "div" || d.Tag == "p") && !d.InList {
		return nil
	}
	return fmt.Errorf("message box not found: %v (jev: %v)", err, jerr)
}

// typeAndSend types message into the open conversation, sends it and
// verifies the send (composer cleared, or a new bubble with the message).
func (b *InstagramBot) typeAndSend(ctx context.Context, p browser.PageInterface, message string) error {
	c := newMark()
	if err := b.waitComposer(ctx, p, c, findTimeout); err != nil {
		return err
	}
	snippet := messageSnippet(message)
	countBubbles := func() (int, []string, error) {
		var texts []string
		if err := evalDM(p, `() => dmBubbles()`, &texts); err != nil {
			return 0, nil, err
		}
		n := 0
		for _, t := range texts {
			if strings.Contains(normSpace(t), snippet) {
				n++
			}
		}
		return n, texts, nil
	}
	baseline, _, _ := countBubbles()
	if err := typeAtEnd(ctx, p, c, message); err != nil {
		return fmt.Errorf("type message: %w", err)
	}
	if err := pause(ctx, uiPause/2); err != nil {
		return err
	}
	// Send exactly once: the Send button when the thread shows one, Enter
	// otherwise — never both (a slow send would go out twice).
	composer := func() string {
		var v string
		_ = evalDM(p, `(m) => { const el = document.querySelector('['+MARK+'="'+m+'"]') || dmComposer(); return el ? boxValue(el) : ''; }`, &v, c)
		return v
	}
	s := newMark()
	var found bool
	_ = poll(ctx, 1500*time.Millisecond, func() (bool, error) {
		err := evalDM(p, `(m) => {
			const box = dmComposer();
			let root = box;
			for (let i = 0; i < 8 && root; i++, root = root.parentElement) {
				const b = btns(root).find((x) => /^send$/i.test(T(x)) || lbl(x) === 'Send' || !!x.querySelector('svg[aria-label="Send"]'));
				if (b) { mark(b, m); return true; }
			}
			return false;
		}`, &found, s)
		return found, err
	})
	if found {
		if err := clickMarked(p, s); err != nil {
			return fmt.Errorf("click Send: %w", err)
		}
	} else if err := PressEnterOn(p, c); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	var last string
	err := poll(ctx, sendVerificationTimeout, func() (bool, error) {
		n, texts, err := countBubbles()
		if err != nil {
			return false, err
		}
		var fail string
		_ = evalJS(p, `() => errorToast()`, &fail)
		if fail != "" {
			return false, stopErr{fmt.Errorf("Instagram reported: %s", fail)}
		}
		var fresh []string
		if n > baseline {
			fresh = texts
		}
		last = composer()
		return sendVerified(last, fresh, message), nil
	})
	unmark(p, c)
	if err != nil {
		return fmt.Errorf("instagram: send could not be verified (composer not cleared and no new message bubble): %w", err)
	}
	return nil
}

var errNoMessageButton = errors.New("no Message button on the profile")

// openThreadViaProfile opens the DM thread from the profile's Message button.
func (b *InstagramBot) openThreadViaProfile(ctx context.Context, p browser.PageInterface, username string) error {
	if err := b.open(ctx, p, profileURL(username)); err != nil {
		return err
	}
	if _, err := waitProfileHeader(ctx, p); err != nil {
		return fmt.Errorf("profile did not render: %w", err)
	}
	m := newMark()
	var found bool
	_ = poll(ctx, 3*time.Second, func() (bool, error) {
		err := evalJS(p, `(m) => { const b = profileButton(/^message$/i); if (b) mark(b, m); return !!b; }`, &found, m)
		return found, err
	})
	if !found {
		return errNoMessageButton
	}
	if err := clickMarked(p, m); err != nil {
		return fmt.Errorf("click Message: %w", err)
	}
	c := newMark()
	return b.waitComposer(ctx, p, c, findTimeout)
}

// openThreadViaCompose starts (or reopens) the conversation from the "New
// message" dialog, selecting only the search result whose username matches
// exactly.
func (b *InstagramBot) openThreadViaCompose(ctx context.Context, p browser.PageInterface, username string) error {
	if err := b.open(ctx, p, "https://www.instagram.com/direct/new/"); err != nil {
		return err
	}
	q := newMark()
	findSearch := func() (bool, error) {
		var found bool
		err := evalJS(p, `(m) => {
			const sels = ['input[name="queryBox"]', '[role="dialog"] input[placeholder^="Search"]', 'input[placeholder^="Search"]', '[role="dialog"] input[type="text"]'];
			for (const s of sels) { const el = Array.from(document.querySelectorAll(s)).find(vis); if (el) { mark(el, m); return true; } }
			return false;
		}`, &found, q)
		return found, err
	}
	if err := poll(ctx, findTimeout/2, findSearch); err != nil {
		// The inbox's "New message" button opens the same dialog.
		n := newMark()
		var found bool
		_ = evalJS(p, `(m) => { const s = document.querySelector('svg[aria-label="New message"]'); const b = s ? btnOf(s) : btns().find((x) => /^(new message|send message)$/i.test(T(x))); if (b) mark(b, m); return !!b; }`, &found, n)
		if !found {
			return fmt.Errorf("new-message dialog not found: %w", err)
		}
		if err := clickMarked(p, n); err != nil {
			return err
		}
		if err := poll(ctx, findTimeout, findSearch); err != nil {
			return fmt.Errorf("recipient search box not found: %w", err)
		}
	}
	if err := typeAtEnd(ctx, p, q, username); err != nil {
		return fmt.Errorf("type recipient: %w", err)
	}
	r := newMark()
	err := poll(ctx, findTimeout, func() (bool, error) {
		var found bool
		err := evalJS(p, `(u, m) => {
			const want = u.toLowerCase();
			const root = dialogs()[0] || document.querySelector('main') || document.body;
			for (const s of root.querySelectorAll('span, div[dir="auto"]')) {
				if (s.children.length || T(s).toLowerCase() !== want || s.closest('input')) continue;
				const row = s.closest('[role="button"], label, [role="checkbox"], [role="listitem"], li');
				if (row && !row.querySelector('input[name="queryBox"]')) { mark(row, m); return true; }
			}
			return false;
		}`, &found, username, r)
		return found, err
	})
	if err != nil {
		return fmt.Errorf("no search result with the exact username %q", username)
	}
	if err := clickMarked(p, r); err != nil {
		return fmt.Errorf("select recipient: %w", err)
	}
	ch := newMark()
	err = poll(ctx, findTimeout, func() (bool, error) {
		var st struct {
			Found    bool `json:"found"`
			Disabled bool `json:"disabled"`
		}
		err := evalJS(p, `(m) => {
			const root = dialogs()[0] || document.body;
			const b = byText(root, /^(chat|next)$/i)[0];
			if (!b) return { found: false };
			mark(b, m);
			return { found: true, disabled: isDisabled(b) };
		}`, &st, ch)
		return st.Found && !st.Disabled, err
	})
	if err != nil {
		return fmt.Errorf("Chat button did not become available: %w", err)
	}
	if err := clickMarked(p, ch); err != nil {
		return fmt.Errorf("click Chat: %w", err)
	}
	c := newMark()
	return b.waitComposer(ctx, p, c, findTimeout)
}

// sendMessage opens the conversation with username (profile Message
// button, else the New message dialog) and sends message once. A failure
// after typing is never retried through the other route (no double send).
func (b *InstagramBot) sendMessage(ctx context.Context, p browser.PageInterface, username, message string) (map[string]interface{}, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return nil, errors.New("instagram: username is required")
	}
	if strings.TrimSpace(message) == "" {
		return nil, errors.New("instagram: message is required")
	}
	method := "profile"
	err := b.openThreadViaProfile(ctx, p, username)
	if errors.Is(err, ErrNotLoggedIn) {
		return nil, err
	}
	if err != nil {
		method = "compose"
		if err2 := b.openThreadViaCompose(ctx, p, username); err2 != nil {
			return nil, fmt.Errorf("instagram: could not open a conversation with %s: profile: %v; new message: %v", username, err, err2)
		}
	}
	if err := b.typeAndSend(ctx, p, message); err != nil {
		return nil, fmt.Errorf("instagram: message to %s: %w", username, err)
	}
	return ok("username", username, "url", profileURL(username), "status", "sent", "method", method), nil
}

// ReplyToConversation sends replyText into a conversation: a /direct/t/<id>/
// URL is opened directly; otherwise conversation is a username (or profile
// URL) whose inbox row — matched on the exact username — is opened, or, when
// the inbox does not list it, the conversation is started as a new message
// to that same user.
func (b *InstagramBot) ReplyToConversation(ctx context.Context, p browser.PageInterface, conversation, replyText string) (map[string]interface{}, error) {
	if strings.TrimSpace(replyText) == "" {
		return nil, errors.New("instagram: reply text is required")
	}
	conversation = strings.TrimSpace(conversation)
	if strings.Contains(conversation, "/direct/t/") {
		u := b.ResolveURL(conversation)
		if err := b.open(ctx, p, u); err != nil {
			return nil, err
		}
		if err := b.typeAndSend(ctx, p, replyText); err != nil {
			return nil, fmt.Errorf("instagram: reply in %s: %w", u, err)
		}
		return ok("url", u, "status", "sent"), nil
	}
	username := b.usernameFrom(conversation)
	if username == "" {
		return nil, fmt.Errorf("instagram: %q is neither a conversation URL nor a username", conversation)
	}
	if err := b.open(ctx, p, "https://www.instagram.com/direct/inbox/"); err != nil {
		return nil, err
	}
	m := newMark()
	err := poll(ctx, findTimeout, func() (bool, error) {
		var found bool
		err := evalJS(p, `(u, m) => {
			const want = u.toLowerCase();
			const rows = Array.from(document.querySelectorAll('a[href*="/direct/t/"], [role="listitem"], [role="button"]'));
			for (const r of rows) {
				if (r.closest('[role="dialog"]')) continue;
				const hit = Array.from(r.querySelectorAll('span, div[dir="auto"]')).some((s) => !s.children.length && T(s).toLowerCase() === want);
				if (!hit) continue;
				// Prefer the innermost clickable row holding the username.
				const inner = Array.from(r.querySelectorAll('a[href*="/direct/t/"], [role="button"]')).find((x) =>
					Array.from(x.querySelectorAll('span, div[dir="auto"]')).some((s) => !s.children.length && T(s).toLowerCase() === want));
				mark(inner || r, m);
				return true;
			}
			return false;
		}`, &found, username, m)
		return found, err
	})
	if err != nil {
		res, serr := b.sendMessage(ctx, p, username, replyText)
		if serr != nil {
			return nil, fmt.Errorf("instagram: no inbox conversation with %s, and starting one failed: %w", username, serr)
		}
		res["method"] = "new_message"
		return res, nil
	}
	if err := clickMarked(p, m); err != nil {
		return nil, fmt.Errorf("instagram: open conversation with %s: %w", username, err)
	}
	if err := b.typeAndSend(ctx, p, replyText); err != nil {
		return nil, fmt.Errorf("instagram: reply to %s: %w", username, err)
	}
	cur, _ := p.GetURL()
	return ok("url", cur, "username", username, "status", "sent", "method", "inbox"), nil
}
