//go:build social

package linkedin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// reactionLabels maps a reaction name to the label of its button in
// LinkedIn's reactions menu.
var reactionLabels = map[string]string{
	"like":       "Like",
	"celebrate":  "Celebrate",
	"support":    "Support",
	"love":       "Love",
	"insightful": "Insightful",
	"funny":      "Funny",
}

// postIDFromURL returns the numeric post id in a post URL, or "".
func postIDFromURL(u string) string {
	if m := reLinkedInActivity.FindStringSubmatch(u); len(m) == 2 {
		return m[1]
	}
	return ""
}

// reactState is what the page scripts report about a reaction button.
type reactState struct {
	State   string `json:"state"` // not_found | ambiguous | ready | reacted | no_comment | no_button
	Count   int    `json:"count"`
	Label   string `json:"label"`
	Current string `json:"current"`
	Menu    bool   `json:"menu"`
	Author  string `json:"author"`
	Text    string `json:"text"`
}

// reactJSHelpers classifies reaction buttons in both LinkedIn markups: the
// classic one ("React Like" / aria-pressed, .reactions-react-button) and the
// server-driven one ("Reaction button state: no reaction" / "…: Like").
const reactJSHelpers = `
	const commentSel = 'article.comments-comment-entity, article.comments-comment-item, .comments-comments-list, .feed-shared-update-v2__comments-container, [data-testid*="commentList"]';
	const isMenuTrigger = (b) => b.classList.contains('reactions-menu__trigger') || /^Open reactions menu/i.test(b.getAttribute('aria-label') || '');
	const isReactBtn = (b) => {
		if (isMenuTrigger(b)) return false;
		const l = b.getAttribute('aria-label') || '';
		return /^(React|Unreact)\b/i.test(l) || /^Reaction button state/i.test(l) || b.classList.contains('react-button__trigger') || !!b.closest('.reactions-react-button');
	};
	const stateOf = (b) => {
		const l = (b.getAttribute('aria-label') || '').trim();
		const sd = l.match(/^Reaction button state:\s*(.+)$/i);
		if (sd) {
			const cur = sd[1].trim();
			return { reacted: !/^no reaction$/i.test(cur), current: /^no reaction$/i.test(cur) ? '' : cur, label: l };
		}
		const pressed = b.getAttribute('aria-pressed') === 'true' || /^(Unreact|Remove)\b/i.test(l) || /\bunlike\b/i.test(l);
		let cur = '';
		if (pressed) {
			const t = b.querySelector('.react-button__text, .social-action-button__text');
			cur = L.text(t || b) || (l.match(/^Unreact\s+(\w+)/i) || [])[1] || '';
		}
		return { reacted: pressed, current: cur, label: l };
	};
	const menuTriggerNear = (b) => {
		let c = b.parentElement;
		for (let i = 0; c && i < 3; i++, c = c.parentElement) {
			const t = Array.from(c.querySelectorAll('button')).find((x) => isMenuTrigger(x) && !x.closest(commentSel));
			if (t) return t;
		}
		return null;
	};
`

// findPostReactionJS locates the post's own reaction button (never a
// comment's), tags it tok and its reactions-menu trigger tok+"-menu".
const findPostReactionJS = `(id, tok) => {` + reactJSHelpers + `
	L.unmarkAll(tok); L.unmarkAll(tok + '-menu');
	const rootSel = 'div.feed-shared-update-v2, [data-urn*="urn:li:activity:"], [data-urn*="urn:li:ugcPost:"], [data-urn*="urn:li:share:"], [componentkey^="update-card"], [role="article"]';
	const outer = (list) => list.filter((r) => !r.closest(commentSel) && !list.some((o) => o !== r && o.contains(r)));
	let roots = [];
	if (id) roots = outer(Array.from(document.querySelectorAll('[data-urn*="' + id + '"], [data-id*="' + id + '"]')));
	if (!roots.length && id) {
		const viaLink = Array.from(document.querySelectorAll('a[href*="' + id + '"]')).filter((a) => !a.closest(commentSel))
			.map((a) => a.closest('[componentkey^="update-card"], [role="listitem"], [role="article"], [data-urn]')).filter(Boolean);
		roots = outer(viaLink.filter((r, i) => viaLink.indexOf(r) === i));
	}
	if (!roots.length) roots = outer(Array.from(document.querySelectorAll(rootSel)));
	if (!roots.length) return { state: 'not_found', count: 0 };
	if (roots.length > 1) return { state: 'ambiguous', count: roots.length };
	const btns = Array.from(roots[0].querySelectorAll('button')).filter((b) => !b.closest(commentSel) && isReactBtn(b));
	if (!btns.length) return { state: 'not_found', count: 0 };
	if (btns.length > 1) return { state: 'ambiguous', count: btns.length };
	const b = btns[0];
	const s = stateOf(b);
	L.mark(b, tok);
	const trig = menuTriggerNear(b);
	if (trig) L.mark(trig, tok + '-menu');
	return { state: s.reacted ? 'reacted' : 'ready', count: 1, label: s.label, current: s.current, menu: !!trig };
}`

// markedReactStateJS reports the state of the button tagged tok.
const markedReactStateJS = `(tok) => {` + reactJSHelpers + `
	const b = L.marked(tok);
	if (!b) return { state: 'not_found' };
	const s = stateOf(b);
	return { state: s.reacted ? 'reacted' : 'ready', label: s.label, current: s.current, count: 1 };
}`

// adoptJevJS re-tags a Jev-picked element with tok, after checking it is a
// usable target: inside the post (not a comment) for kind "post", inside
// comment id for kind "comment", a reactions-menu entry for kind "menu".
const adoptJevJS = `(marker, tok, kind, id) => {` + reactJSHelpers + `
	const el = document.querySelector('[data-monoagent-jev="' + marker + '"]');
	if (!el) return { state: 'not_found' };
	const b = el.closest('button, [role="button"]') || el;
	if (kind === 'post') {
		if (b.closest(commentSel)) return { state: 'wrong_target', label: L.label(b) };
		if (id) {
			const r = b.closest('[data-urn], [data-id]');
			if (r && !(r.getAttribute('data-urn') || r.getAttribute('data-id') || '').includes(id)) return { state: 'wrong_target', label: L.label(b) };
		}
	}
	if (kind === 'comment') {
		const art = b.closest('article.comments-comment-entity, article.comments-comment-item');
		if (!art || art.getAttribute('data-id') !== id) return { state: 'wrong_target', label: L.label(b) };
	}
	L.unmarkAll(tok);
	L.mark(b, tok);
	const s = stateOf(b);
	return { state: s.reacted ? 'reacted' : 'ready', label: s.label, current: s.current, count: 1 };
}`

// findReactionInMenuJS tags the visible reactions-menu button for label.
const findReactionInMenuJS = `(label, tok) => {
	L.unmarkAll(tok);
	const want = label.toLowerCase();
	const menus = 'div.reactions-menu, .reactions-menu, [role="toolbar"], [role="menu"], [role="dialog"], .artdeco-hoverable-content, #artdeco-hoverable-outlet';
	const all = L.deepAll('button, [role="button"]').filter((b) => L.vis(b));
	const named = all.filter((b) => {
		const l = (b.getAttribute('aria-label') || '').trim().toLowerCase();
		return l === want || l.startsWith(want + ' ') || l.startsWith('react ' + want);
	});
	const inMenu = named.filter((b) => b.closest(menus) || b.closest('[class*="reactions-menu"]'));
	const pick = inMenu[0] || null;
	if (!pick) return { state: 'not_found', count: named.length };
	L.mark(pick, tok);
	return { state: 'ready', count: inMenu.length };
}`

// LikePost reacts to a post with reaction — "like" (default), "celebrate",
// "support", "love", "insightful" or "funny" — and verifies the post then
// shows the reaction. A post that already carries the viewer's reaction is
// left alone (never un-reacted) and reported as already_reacted. The
// requested reaction is never substituted: when its button cannot be found
// Jev picks it (if this bot has a picker) or the call fails.
func (b *LinkedInBot) LikePost(ctx context.Context, page browser.PageInterface, postURL string, reaction string) (map[string]interface{}, error) {
	postURL = strings.TrimSpace(postURL)
	if postURL == "" {
		return nil, fmt.Errorf("linkedin: postURL is required")
	}
	reaction = strings.ToLower(strings.TrimSpace(reaction))
	if reaction == "" {
		reaction = "like"
	}
	label, ok := reactionLabels[reaction]
	if !ok {
		return nil, fmt.Errorf("linkedin: unknown reaction %q (want like, celebrate, support, love, insightful or funny)", reaction)
	}
	postURL = b.ResolveURL(postURL)
	id := postIDFromURL(postURL)
	if err := navigate(ctx, page, postURL); err != nil {
		return nil, err
	}
	if err := waitForPost(ctx, page); err != nil {
		return nil, err
	}

	tok := newToken("react")
	defer unmark(page, tok)
	defer unmark(page, tok+"-menu")
	var st reactState
	// The action bar may render a moment after the post body.
	_ = poll(ctx, verifyTimeout/2, func() (bool, error) {
		if err := run(page, findPostReactionJS, &st, id, tok); err != nil {
			return false, nil
		}
		return st.State != "not_found", nil
	})

	if st.State != "ready" && st.State != "reacted" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Like (React) button of the LinkedIn post shown on this page (%s) — the post's own reaction button, not a Like button that belongs to a comment", postURL),
			Hint:   "LinkedIn labels it e.g. \"React Like\" or \"Reaction button state: no reaction\"; \"Unreact …\" or a state other than \"no reaction\" means the post was already reacted to.",
		})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: could not find the post's reaction button on %s (%s, %d candidates) (jev fallback: %v)", postURL, st.State, st.Count, jerr)
		}
		err := run(page, adoptJevJS, &st, pick.Marker, tok, "post", id)
		pick.Release()
		if err != nil {
			return nil, fmt.Errorf("linkedin: jev pick: %w", err)
		}
		if st.State == "wrong_target" || st.State == "not_found" {
			return nil, fmt.Errorf("linkedin: jev picked %q, which is not the post's reaction button; nothing clicked", st.Label)
		}
	}

	if st.State == "reacted" {
		return map[string]interface{}{"success": true, "postURL": postURL, "reaction": reaction,
			"already_reacted": true, "current_reaction": strings.ToLower(st.Current)}, nil
	}

	if reaction == "like" {
		if err := clickMarked(page, tok); err != nil {
			return nil, fmt.Errorf("linkedin: failed to click Like: %w", err)
		}
	} else if err := b.pickReaction(ctx, page, tok, label, postURL); err != nil {
		return nil, err
	}

	if err := verifyReacted(ctx, page, tok, label); err != nil {
		return nil, fmt.Errorf("linkedin: %s on %s not confirmed: %w", reaction, postURL, err)
	}
	return map[string]interface{}{"success": true, "postURL": postURL, "reaction": reaction, "already_reacted": false}, nil
}

// pickReaction opens the reactions menu of the tagged button (its menu
// trigger when there is one, else a real mouse hover) and clicks label.
func (b *LinkedInBot) pickReaction(ctx context.Context, page browser.PageInterface, tok, label, postURL string) error {
	menuTok := tok + "-reaction"
	defer unmark(page, menuTok)
	var hasTrigger bool
	_ = run(page, `(tok) => !!L.marked(tok)`, &hasTrigger, tok+"-menu")
	open := func() error {
		if hasTrigger {
			return clickMarked(page, tok+"-menu")
		}
		return hoverMarked(page, tok)
	}
	if err := open(); err != nil {
		return fmt.Errorf("linkedin: failed to open the reactions menu: %w", err)
	}
	var st reactState
	err := poll(ctx, 3*time.Second, func() (bool, error) {
		if err := run(page, findReactionInMenuJS, &st, label, menuTok); err != nil {
			return false, nil
		}
		return st.State == "ready", nil
	})
	if err != nil && hasTrigger {
		// Some layouts only open the menu on hover.
		_ = hoverMarked(page, tok)
		err = poll(ctx, 3*time.Second, func() (bool, error) {
			if err := run(page, findReactionInMenuJS, &st, label, menuTok); err != nil {
				return false, nil
			}
			return st.State == "ready", nil
		})
	}
	if err != nil {
		// Never substitute another reaction: Jev finds the requested one in
		// the open menu, or the call fails.
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the %q reaction button in the LinkedIn reactions menu opened over the post's Like button — exactly the %s reaction, not Like and not any other reaction", label, label),
		})
		if jerr != nil {
			return fmt.Errorf("linkedin: %s reaction not found on %s (jev fallback: %v)", strings.ToLower(label), postURL, jerr)
		}
		var got reactState
		aerr := run(page, `(marker, tok, label) => {
			const el = document.querySelector('[data-monoagent-jev="' + marker + '"]');
			if (!el) return { state: 'not_found' };
			const b = el.closest('button, [role="button"]') || el;
			const l = ((b.getAttribute('aria-label') || '') + ' ' + L.text(b)).toLowerCase();
			if (!l.includes(label.toLowerCase())) return { state: 'wrong_target', label: l.trim() };
			L.unmarkAll(tok); L.mark(b, tok);
			return { state: 'ready' };
		}`, &got, pick.Marker, menuTok, label)
		pick.Release()
		if aerr != nil || got.State != "ready" {
			return fmt.Errorf("linkedin: jev picked %q for the %s reaction; nothing clicked", got.Label, label)
		}
	}
	if err := clickMarked(page, menuTok); err != nil {
		return fmt.Errorf("linkedin: failed to click the %s reaction: %w", label, err)
	}
	return nil
}

// verifyReacted waits until the tagged reaction button shows a reaction —
// label's, when the markup names the current reaction.
func verifyReacted(ctx context.Context, page browser.PageInterface, tok, label string) error {
	var st reactState
	err := poll(ctx, verifyTimeout, func() (bool, error) {
		if err := run(page, markedReactStateJS, &st, tok); err != nil {
			return false, nil
		}
		if st.State == "not_found" {
			return false, errors.New("the reaction button disappeared")
		}
		return st.State == "reacted", nil
	})
	if err != nil {
		return fmt.Errorf("button still reads %q: %w", st.Label, err)
	}
	if cur := strings.TrimSpace(st.Current); cur != "" && !strings.Contains(strings.ToLower(cur), strings.ToLower(label)) {
		return fmt.Errorf("the post now shows %q, not %s", cur, label)
	}
	return nil
}

// findCommentLikeJS tags the Like button that belongs to comment id itself
// (not to one of its replies).
const findCommentLikeJS = `(id, tok) => {` + reactJSHelpers + `
	L.unmarkAll(tok);
	const artSel = 'article.comments-comment-entity, article.comments-comment-item';
	const el = Array.from(document.querySelectorAll(artSel)).find((a) => a.getAttribute('data-id') === id);
	if (!el) return { state: 'no_comment' };
	const own = (sel) => Array.from(el.querySelectorAll(sel)).filter((x) => x.closest(artSel) === el);
	const nameEl = own('.comments-comment-meta__description-title, .comments-post-meta__name-text')[0];
	const textEl = own('.comments-comment-item__main-content, .update-components-text')[0];
	const author = nameEl ? L.lines(nameEl)[0] || '' : '';
	const text = textEl ? L.text(textEl).slice(0, 120) : '';
	const btns = own('button').filter(isReactBtn);
	if (btns.length !== 1) return { state: btns.length ? 'ambiguous' : 'no_button', count: btns.length, author, text };
	const s = stateOf(btns[0]);
	L.mark(btns[0], tok);
	return { state: s.reacted ? 'reacted' : 'ready', count: 1, label: s.label, current: s.current, author, text };
}`

// LikeComment likes comment commentID (a urn:li:comment:(…) id from
// list_post_comments) on postURL and verifies the Like registered. An
// already-liked comment is left alone.
func (b *LinkedInBot) LikeComment(ctx context.Context, page browser.PageInterface, postURL, commentID string) (map[string]interface{}, error) {
	postURL, commentID = strings.TrimSpace(postURL), strings.TrimSpace(commentID)
	if postURL == "" {
		return nil, fmt.Errorf("linkedin: postURL is required")
	}
	if commentID == "" {
		return nil, fmt.Errorf("linkedin: commentID is required")
	}
	postURL = b.ResolveURL(postURL)
	if err := navigate(ctx, page, postURL); err != nil {
		return nil, err
	}
	if err := waitForPost(ctx, page); err != nil {
		return nil, err
	}
	tok := newToken("clike")
	defer unmark(page, tok)
	var st reactState
	find := func(timeout time.Duration) {
		_ = poll(ctx, timeout, func() (bool, error) {
			if err := run(page, findCommentLikeJS, &st, commentID, tok); err != nil {
				return false, nil
			}
			return st.State != "no_comment", nil
		})
	}
	find(verifyTimeout / 2)
	if st.State == "no_comment" {
		expandComments(ctx, page, 0, true)
		find(time.Second)
	}
	if st.State == "no_comment" {
		return nil, fmt.Errorf("linkedin: comment %s not found on %s", commentID, postURL)
	}
	if st.State == "no_button" || st.State == "ambiguous" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Like button of the LinkedIn comment by %q that reads %q — that comment's own Like, not the post's and not a reply's", st.Author, st.Text),
		})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: could not find the Like button of comment %s (%s) (jev fallback: %v)", commentID, st.State, jerr)
		}
		err := run(page, adoptJevJS, &st, pick.Marker, tok, "comment", commentID)
		pick.Release()
		if err != nil || st.State == "wrong_target" || st.State == "not_found" {
			return nil, fmt.Errorf("linkedin: jev picked %q, which is not comment %s's Like button; nothing clicked", st.Label, commentID)
		}
	}
	if st.State == "reacted" {
		return map[string]interface{}{"success": true, "postURL": postURL, "commentID": commentID, "already_liked": true}, nil
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("linkedin: failed to click the comment's Like: %w", err)
	}
	if err := verifyReacted(ctx, page, tok, "Like"); err != nil {
		return nil, fmt.Errorf("linkedin: like on comment %s not confirmed: %w", commentID, err)
	}
	return map[string]interface{}{"success": true, "postURL": postURL, "commentID": commentID, "already_liked": false}, nil
}
