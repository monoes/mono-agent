//go:build social

package linkedin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// commentBoxJS finds the comment editor to type into and tags it tok.
// parentID "" is the post's own comment box (outside every comment); else
// the reply box inside comment parentID. When the box is not open yet it
// tags the control that opens it with tok+"-open" (the post's Comment
// button, or the comment's Reply button) and reports state "closed".
const commentBoxJS = `(parentID, tok) => {
	L.unmarkAll(tok); L.unmarkAll(tok + '-open');
	const artSel = 'article.comments-comment-entity, article.comments-comment-item';
	const edSel = 'div.ql-editor[contenteditable="true"], div[role="textbox"][contenteditable="true"], [contenteditable="true"][aria-label*="comment" i], textarea[aria-label*="comment" i]';
	const editors = Array.from(document.querySelectorAll(edSel)).filter((e) => !e.closest('.ql-clipboard'));
	if (parentID) {
		const art = Array.from(document.querySelectorAll(artSel)).find((a) => a.getAttribute('data-id') === parentID);
		if (!art) return { state: 'no_comment' };
		const eds = editors.filter((e) => art.contains(e) && L.vis(e));
		if (eds.length) { L.mark(eds[eds.length - 1], tok); return { state: 'open' }; }
		const own = Array.from(art.querySelectorAll('button')).filter((b) => b.closest(artSel) === art);
		const reply = own.find((b) => b.classList.contains('comments-comment-social-bar__reply-action-button--cr') || b.classList.contains('comments-comment-social-bar__reply-action-button'))
			|| own.find((b) => /^Reply\b/i.test(b.getAttribute('aria-label') || ''))
			|| own.find((b) => L.text(b) === 'Reply');
		const nameEl = Array.from(art.querySelectorAll('.comments-comment-meta__description-title')).find((x) => x.closest(artSel) === art);
		const author = nameEl ? L.text(nameEl) : '';
		if (!reply) return { state: 'no_reply_button', author };
		L.mark(reply, tok + '-open');
		return { state: 'closed', author };
	}
	const eds = editors.filter((e) => !e.closest(artSel) && !e.closest('[role="dialog"]') && L.vis(e));
	if (eds.length === 1) { L.mark(eds[0], tok); return { state: 'open' }; }
	if (eds.length > 1) return { state: 'ambiguous', count: eds.length };
	const btn = Array.from(document.querySelectorAll('button')).find((b) => !b.closest(artSel) && !b.closest('.comments-comments-list') &&
		(/^Comment$/i.test((b.getAttribute('aria-label') || '').trim()) || (b.id || '').startsWith('feed-shared-social-action-bar-comment')));
	if (!btn) return { state: 'no_comment_button' };
	L.mark(btn, tok + '-open');
	return { state: 'closed' };
}`

// submitButtonJS tags the submit button of the comment box holding the
// editor tagged edTok. It never looks outside that box.
const submitButtonJS = `(edTok, tok) => {
	L.unmarkAll(tok);
	const ed = L.marked(edTok);
	if (!ed) return { state: 'no_editor' };
	const box = ed.closest('form') || ed.closest('.comments-comment-box, .comments-comment-box--cr, .comments-comment-texteditor, .comments-reply-box');
	if (!box) return { state: 'no_box' };
	const btns = Array.from(box.querySelectorAll('button')).filter((b) => L.vis(b));
	const b = btns.find((x) => /comments-comment-box__submit-button/.test(x.className))
		|| btns.find((x) => x.type === 'submit')
		|| btns.find((x) => /^(Comment|Post|Reply|Submit)$/i.test(L.text(x)));
	if (!b) return { state: 'no_button' };
	if (b.disabled || b.getAttribute('aria-disabled') === 'true') { L.mark(b, tok); return { state: 'disabled' }; }
	L.mark(b, tok);
	return { state: 'ready' };
}`

// commentPostedJS reports whether a comment whose text contains snip now
// exists (inside comment parentID when set) that was not there before
// (known ids).
const commentPostedJS = `(snip, parentID, known) => {
	const artSel = 'article.comments-comment-entity, article.comments-comment-item';
	const norm = (s) => s.replace(/\s+/g, ' ').trim();
	const arts = Array.from(document.querySelectorAll(artSel)).filter((a) => {
		if (parentID) {
			const p = a.parentElement ? a.parentElement.closest(artSel) : null;
			if (!p || p.getAttribute('data-id') !== parentID) return false;
		}
		const own = Array.from(a.querySelectorAll('.comments-comment-item__main-content, .update-components-text, .comments-comment-entity__content')).find((x) => x.closest(artSel) === a);
		return own && norm(L.text(own)).includes(snip);
	});
	const fresh = arts.find((a) => !known.includes(a.getAttribute('data-id') || ('#' + Array.from(document.querySelectorAll(artSel)).indexOf(a))));
	return { ok: !!fresh, id: fresh ? (fresh.getAttribute('data-id') || '') : '' };
}`

// commentIDsJS lists the ids of the comments currently rendered, for
// commentPostedJS's "known" set (id-less comments by position).
const commentIDsJS = `() => Array.from(document.querySelectorAll('article.comments-comment-entity, article.comments-comment-item')).map((a, i) => a.getAttribute('data-id') || ('#' + i))`

// CommentOnPost posts commentText on postURL — or, with parentCommentID
// (a urn:li:comment:(…) id), replies to that comment — and verifies the new
// comment appeared. It fails rather than report success when the comment
// cannot be seen on the page afterwards.
func (b *LinkedInBot) CommentOnPost(ctx context.Context, page browser.PageInterface, postURL, commentText, parentCommentID string) (map[string]interface{}, error) {
	postURL, parentCommentID = strings.TrimSpace(postURL), strings.TrimSpace(parentCommentID)
	if postURL == "" {
		return nil, fmt.Errorf("linkedin: postURL is required")
	}
	if strings.TrimSpace(commentText) == "" {
		return nil, fmt.Errorf("linkedin: commentText is required")
	}
	postURL = b.ResolveURL(postURL)
	if err := navigate(ctx, page, postURL); err != nil {
		return nil, err
	}
	if err := waitForPost(ctx, page); err != nil {
		return nil, err
	}

	tok := newToken("cbox")
	defer unmark(page, tok)
	defer unmark(page, tok+"-open")
	var st reactState
	find := func() error {
		return run(page, commentBoxJS, &st, parentCommentID, tok)
	}
	_ = poll(ctx, verifyTimeout/2, func() (bool, error) {
		if err := find(); err != nil {
			return false, nil
		}
		return st.State == "open" || st.State == "closed", nil
	})
	if st.State == "no_comment" && parentCommentID != "" {
		expandComments(ctx, page, 0, true)
		_ = find()
	}
	if st.State == "no_comment" {
		return nil, fmt.Errorf("linkedin: comment %s not found on %s", parentCommentID, postURL)
	}
	if st.State == "closed" {
		if err := clickMarked(page, tok+"-open"); err != nil {
			return nil, fmt.Errorf("linkedin: failed to open the comment box: %w", err)
		}
		_ = poll(ctx, verifyTimeout/2, func() (bool, error) {
			if err := find(); err != nil {
				return false, nil
			}
			return st.State == "open", nil
		})
	}
	if st.State != "open" {
		intent := "the text box for writing a new comment on the LinkedIn post (the post's own comment box, not a reply box)"
		if parentCommentID != "" {
			intent = fmt.Sprintf("the reply text box under the LinkedIn comment by %q", st.Author)
		}
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{Kind: "fill", Intent: intent})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: could not find the comment box on %s (%s) (jev fallback: %v)", postURL, st.State, jerr)
		}
		var ok bool
		err := run(page, `(marker, tok, parentID) => {
			const el = document.querySelector('[data-monoagent-jev="' + marker + '"]');
			if (!el) return false;
			const artSel = 'article.comments-comment-entity, article.comments-comment-item';
			if (parentID) {
				const art = el.closest(artSel);
				if (!art || (art.getAttribute('data-id') !== parentID && !(art.parentElement && art.parentElement.closest(artSel) && art.parentElement.closest(artSel).getAttribute('data-id') === parentID))) return false;
			} else if (el.closest(artSel)) return false;
			L.unmarkAll(tok); L.mark(el, tok);
			return true;
		}`, &ok, pick.Marker, tok, parentCommentID)
		pick.Release()
		if err != nil || !ok {
			return nil, fmt.Errorf("linkedin: jev picked a text box outside the target comment thread; nothing typed")
		}
	}

	var known []string
	if err := run(page, commentIDsJS, &known); err != nil {
		known = []string{}
	}
	if err := typeMarked(ctx, page, tok, commentText); err != nil {
		return nil, fmt.Errorf("linkedin: typing the comment failed: %w", err)
	}
	if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
		return nil, err
	}

	subTok := tok + "-submit"
	defer unmark(page, subTok)
	var sub reactState
	_ = poll(ctx, 3*time.Second, func() (bool, error) {
		if err := run(page, submitButtonJS, &sub, tok, subTok); err != nil {
			return false, nil
		}
		return sub.State == "ready", nil
	})
	if sub.State != "ready" {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: "the button that submits the comment just typed into the LinkedIn comment box (labelled Comment, Post or Reply)",
		})
		if jerr != nil {
			return nil, fmt.Errorf("linkedin: comment submit button not usable (%s) (jev fallback: %v)", sub.State, jerr)
		}
		var ok bool
		err := run(page, `(marker, edTok, tok) => {
			const el = document.querySelector('[data-monoagent-jev="' + marker + '"]');
			const ed = L.marked(edTok);
			if (!el || !ed) return false;
			const b = el.closest('button') || el;
			const box = ed.closest('form') || ed.closest('.comments-comment-box, .comments-comment-box--cr, .comments-comment-texteditor');
			if (box && !box.contains(b)) return false;
			L.unmarkAll(tok); L.mark(b, tok);
			return true;
		}`, &ok, pick.Marker, tok, subTok)
		pick.Release()
		if err != nil || !ok {
			return nil, fmt.Errorf("linkedin: jev picked a button outside the comment box; comment not submitted")
		}
	}
	if err := clickMarked(page, subTok); err != nil {
		return nil, fmt.Errorf("linkedin: failed to click the comment submit button: %w", err)
	}

	var posted struct {
		OK bool   `json:"ok"`
		ID string `json:"id"`
	}
	if err := waitFor(ctx, page, verifyTimeout, commentPostedJS, &posted, snippet(commentText), parentCommentID, known); err != nil {
		return nil, fmt.Errorf("linkedin: comment on %s not confirmed — it did not appear on the page: %w", postURL, err)
	}
	return map[string]interface{}{"success": true, "postURL": postURL, "comment_id": posted.ID, "parent_comment_id": parentCommentID}, nil
}
