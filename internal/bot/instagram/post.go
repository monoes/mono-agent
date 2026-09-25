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

// openPost opens a post URL (absolute or /p/... relative).
func (b *InstagramBot) openPost(ctx context.Context, p browser.PageInterface, postURL string) (string, error) {
	postURL = b.ResolveURL(strings.TrimSpace(postURL))
	if postURL == "" {
		return "", errors.New("instagram: post URL is required")
	}
	if !strings.Contains(postURL, "/p/") && !strings.Contains(postURL, "/reel/") && !strings.Contains(postURL, "/tv/") {
		return "", fmt.Errorf("instagram: %q is not a post URL", postURL)
	}
	if err := b.open(ctx, p, postURL); err != nil {
		return "", err
	}
	var gone bool
	if err := evalJS(p, `() => /sorry, this page isn.t available|post isn.t available/i.test(VT())`, &gone); err == nil && gone {
		return "", fmt.Errorf("instagram: post %s is not available", postURL)
	}
	return postURL, nil
}

// ---------------------------------------------------------------------------
// Like a post (WRITE)
// ---------------------------------------------------------------------------

type likeState struct {
	State string `json:"state"` // liked | marked | not_found
	Count int    `json:"count"` // action bars seen
}

// findPostLike locates the post's own Like button (its action bar sits with
// Comment and Share; comment hearts live in the comment list) and marks it.
func findPostLike(p browser.PageInterface, m string) (likeState, error) {
	var st likeState
	err := evalJS(p, `(m) => {
		const { svg, count } = postLike();
		if (!svg) return { state: 'not_found', count };
		if (lbl(svg) === 'Unlike') return { state: 'liked', count };
		const b = btnOf(svg) || svg.parentElement;
		mark(b, m);
		return { state: 'marked', count };
	}`, &st, m)
	return st, err
}

// LikePost likes the post at postURL. A post already liked is left alone
// ("Unlike" is never clicked); the click is verified by the post's heart
// turning into Unlike, and an unverified like is an error.
func (b *InstagramBot) LikePost(ctx context.Context, p browser.PageInterface, postURL string) (map[string]interface{}, error) {
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	m := newMark()
	var st likeState
	ferr := poll(ctx, findTimeout, func() (bool, error) {
		var err error
		st, err = findPostLike(p, m)
		return err == nil && st.State != "not_found", err
	})
	if st.State == "liked" {
		return ok("url", postURL, "status", "already_liked"), nil
	}
	viaJev := false
	if ferr != nil || st.Count > 1 {
		d, jerr := b.jevPick(ctx, p, "click",
			"the Like (heart) button of the post shown on this page — the post's own Like button in its action bar next to Comment and Share, not the small Like button of a comment",
			"an 'Unlike' label means the post is already liked", m)
		switch {
		case jerr == nil && d.SVGLabel == "Unlike":
			return ok("url", postURL, "status", "already_liked"), nil
		case jerr == nil && d.SVGLabel == "Like" && !d.InList:
			viaJev = true
		case ferr != nil:
			return nil, fmt.Errorf("instagram: could not find the post's Like button on %s: %v (jev: %v)", postURL, ferr, jerr)
		}
		// Several action bars and no usable pick: the first bar's heart, as
		// marked by findPostLike.
	}
	if err := clickMarked(p, m); err != nil {
		return nil, fmt.Errorf("instagram: click Like on %s: %w", postURL, err)
	}
	err = poll(ctx, verifyTimeout, func() (bool, error) {
		var liked bool
		err := evalJS(p, `(m, viaJev) => {
			const el = document.querySelector('['+MARK+'="'+m+'"]');
			if (el && el.querySelector('svg[aria-label="Unlike"]')) return true;
			if (viaJev) return false;
			const { svg } = postLike();
			return !!svg && lbl(svg) === 'Unlike';
		}`, &liked, m, viaJev)
		return liked, err
	})
	unmark(p, m)
	if err != nil {
		return nil, fmt.Errorf("instagram: like not confirmed on %s (the heart did not turn into Unlike)", postURL)
	}
	return ok("url", postURL, "status", "liked"), nil
}

// ---------------------------------------------------------------------------
// Comment on a post (WRITE)
// ---------------------------------------------------------------------------

// commentBaseline counts rendered comments whose text contains text.
func commentBaseline(p browser.PageInterface, text string) (int, error) {
	var n int
	err := evalJS(p, `(t) => { const want = norm(t).toLowerCase(); return comments().filter((c) => norm(c.text).toLowerCase().includes(want)).length; }`, &n, text)
	return n, err
}

// findCommentBox marks the post's comment input, falling back to Jev.
func (b *InstagramBot) findCommentBox(ctx context.Context, p browser.PageInterface, m string) error {
	var state string
	err := poll(ctx, findTimeout, func() (bool, error) {
		err := evalJS(p, `(m) => {
			const box = commentBox();
			if (box) { mark(box, m); return 'marked'; }
			if (/comments on this post have been (limited|turned off)/i.test(VT())) return 'disabled';
			return 'not_found';
		}`, &state, m)
		if state == "disabled" {
			return false, stopErr{errors.New("instagram: comments are turned off on this post")}
		}
		return state == "marked", err
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, errTimeout) {
		d, jerr := b.jevPick(ctx, p, "fill", "the 'Add a comment…' text box under the post shown on this page", "", m)
		if jerr == nil && (d.Tag == "textarea" || d.Tag == "input" || d.Label != "") && !d.InDialog {
			return nil
		}
		return fmt.Errorf("instagram: comment box not found: %v (jev: %v)", err, jerr)
	}
	return err
}

// submitComment clicks the Post button next to box m (Enter when there is
// none) and waits until a comment containing text beyond the baseline
// appears, or the composer clears without an error message.
func (b *InstagramBot) submitComment(ctx context.Context, p browser.PageInterface, m, text string, baseline int) (string, error) {
	pb := newMark()
	var btn struct {
		Found    bool `json:"found"`
		Disabled bool `json:"disabled"`
	}
	_ = poll(ctx, 3*time.Second, func() (bool, error) {
		err := evalJS(p, `(m, pb) => {
			const box = document.querySelector('['+MARK+'="'+m+'"]');
			const b = box && postButtonFor(box);
			if (!b) return { found: false };
			mark(b, pb);
			return { found: true, disabled: isDisabled(b) };
		}`, &btn, m, pb)
		return btn.Found && !btn.Disabled, err
	})
	switch {
	case btn.Found && !btn.Disabled:
		if err := clickMarked(p, pb); err != nil {
			return "", fmt.Errorf("click Post: %w", err)
		}
	case btn.Found:
		return "", errors.New("the Post button stayed disabled after typing")
	default:
		d, jerr := b.jevPick(ctx, p, "click", "the Post button next to the comment box that publishes the typed comment", "", pb)
		if jerr == nil && strings.EqualFold(d.Text, "Post") {
			if err := clickMarked(p, pb); err != nil {
				return "", fmt.Errorf("click Post: %w", err)
			}
		} else if err := PressEnterOn(p, m); err != nil {
			return "", fmt.Errorf("no Post button and Enter failed: %w", err)
		}
	}
	var how string
	err := poll(ctx, verifyTimeout, func() (bool, error) {
		var st struct {
			Count int    `json:"count"`
			Box   string `json:"box"`
			Error string `json:"error"`
		}
		if err := evalJS(p, `(m, t) => {
			const want = norm(t).toLowerCase();
			const box = document.querySelector('['+MARK+'="'+m+'"]') || commentBox();
			return { count: comments().filter((c) => norm(c.text).toLowerCase().includes(want)).length,
				box: box ? boxValue(box) : '', error: errorToast() };
		}`, &st, m, text); err != nil {
			return false, err
		}
		if st.Error != "" {
			return false, stopErr{fmt.Errorf("Instagram reported: %s", st.Error)}
		}
		if st.Count > baseline {
			how = "comment_rendered"
			return true, nil
		}
		if strings.TrimSpace(st.Box) == "" {
			how = "composer_cleared"
		}
		return false, nil
	})
	if err != nil && how == "composer_cleared" && errors.Is(err, errTimeout) {
		return how, nil
	}
	if err != nil {
		return "", err
	}
	return how, nil
}

// PressEnterOn focuses the marked element and presses Enter.
func PressEnterOn(p browser.PageInterface, m string) error {
	var ok bool
	if err := evalJS(p, `(m) => { const el = document.querySelector('['+MARK+'="'+m+'"]'); if (el) el.focus(); return !!el; }`, &ok, m); err != nil || !ok {
		return errors.New("input vanished")
	}
	return pressEnter(p)
}

// CommentPost posts commentText under the post and verifies it appeared.
func (b *InstagramBot) CommentPost(ctx context.Context, p browser.PageInterface, postURL, commentText string) (map[string]interface{}, error) {
	if strings.TrimSpace(commentText) == "" {
		return nil, errors.New("instagram: comment text is required")
	}
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	m := newMark()
	if err := b.findCommentBox(ctx, p, m); err != nil {
		return nil, fmt.Errorf("instagram: %s: %w", postURL, err)
	}
	baseline, _ := commentBaseline(p, commentText)
	if err := typeAtEnd(ctx, p, m, commentText); err != nil {
		return nil, fmt.Errorf("instagram: type comment on %s: %w", postURL, err)
	}
	if err := pause(ctx, uiPause/2); err != nil {
		return nil, err
	}
	how, err := b.submitComment(ctx, p, m, commentText, baseline)
	unmark(p, m)
	if err != nil {
		return nil, fmt.Errorf("instagram: comment on %s not confirmed: %w", postURL, err)
	}
	return ok("url", postURL, "status", "commented", "verified_by", how), nil
}

// ---------------------------------------------------------------------------
// Comments: like, reply (WRITE), list (READ)
// ---------------------------------------------------------------------------

// resolveCommentTargetState interprets the result of the comment-target
// lookup scripts shared by LikeComments and ReplyToComment.
//
//   - "marked": a suitable target comment was found and marked.
//   - "not_found": the post shows no comments at all — an error: there is
//     nothing to act on, and reporting success would be a lie.
//   - "author_not_found": commentAuthor was specified but no comment by that
//     author exists. Acting on a different comment would target the wrong
//     user, so this is an error, never a silent fallback to another comment.
func resolveCommentTargetState(state, commentAuthor, postURL, action string) (bool, error) {
	switch state {
	case "marked":
		return true, nil
	case "not_found":
		return false, fmt.Errorf("instagram: no comments on %s to %s", postURL, action)
	case "author_not_found":
		return false, fmt.Errorf("instagram: no comment by author %q found on %s — refusing to %s a different comment", commentAuthor, postURL, action)
	default:
		return false, fmt.Errorf("instagram: unexpected comment lookup state %q on %s", state, postURL)
	}
}

// loadMoreComments clicks "Load more comments" up to rounds times.
func loadMoreComments(ctx context.Context, p browser.PageInterface, rounds int) {
	for i := 0; i < rounds; i++ {
		m := newMark()
		var found bool
		if err := evalJS(p, `(m) => {
			const s = document.querySelector('svg[aria-label="Load more comments"]');
			let b = s && btnOf(s);
			if (!b) b = btns().find((x) => /^(load more comments|view more comments|view all \d+ comments)$/i.test(T(x)));
			if (b) mark(b, m);
			return !!b;
		}`, &found, m); err != nil || !found {
			return
		}
		_ = clickMarked(p, m)
		_ = pause(ctx, scrollSettle)
	}
}

type commentRow struct {
	Author    string `json:"author"`
	Text      string `json:"text"`
	Liked     bool   `json:"liked"`
	Timestamp string `json:"timestamp"`
	HasLike   bool   `json:"hasLike"`
}

// readComments returns the rendered comments.
func readComments(p browser.PageInterface) ([]commentRow, error) {
	var rows []commentRow
	err := evalJS(p, `() => comments().map((c) => ({ author: c.author, text: c.text, liked: c.liked, timestamp: c.timestamp, hasLike: !!c.like }))`, &rows)
	return rows, err
}

// waitComments waits for the comment list (or a post without comments).
func waitComments(ctx context.Context, p browser.PageInterface) []commentRow {
	var rows []commentRow
	_ = poll(ctx, findTimeout, func() (bool, error) {
		var err error
		rows, err = readComments(p)
		if err != nil {
			return false, err
		}
		var bar bool
		_ = evalJS(p, `() => !!commentBox() || postBars().length > 0`, &bar)
		return len(rows) > 0 || bar, nil
	})
	return rows
}

// LikeComments likes comments on a post. With commentAuthor, it likes that
// author's first comment only — never anyone else's; without, it likes up to
// max comments (default 1) in page order. Comments already liked count as
// done and are never toggled. Each like is verified.
func (b *InstagramBot) LikeComments(ctx context.Context, p browser.PageInterface, postURL, commentAuthor string, max int) (map[string]interface{}, error) {
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	author := strings.TrimPrefix(strings.TrimSpace(commentAuthor), "@")
	if max <= 0 {
		max = 1
	}
	if author != "" {
		max = 1
	}
	rows := waitComments(ctx, p)
	if author != "" && !hasAuthor(rows, author) {
		loadMoreComments(ctx, p, 3)
		rows, _ = readComments(p)
	}
	state := "marked"
	switch {
	case len(rows) == 0:
		state = "not_found"
	case author != "" && !hasAuthor(rows, author):
		state = "author_not_found"
	}
	if _, err := resolveCommentTargetState(state, author, postURL, "like"); err != nil {
		return nil, err
	}
	liked, already := 0, 0
	var likedAuthors []string
	for i := 0; i < len(rows) && liked < max; i++ {
		r := rows[i]
		if author != "" && !strings.EqualFold(r.Author, author) {
			continue
		}
		if r.Liked {
			already++
			if author != "" {
				break
			}
			continue
		}
		if !r.HasLike {
			if author != "" {
				return nil, fmt.Errorf("instagram: %s's comment on %s has no Like button", author, postURL)
			}
			continue
		}
		if err := likeOneComment(ctx, p, r); err != nil {
			return nil, fmt.Errorf("instagram: like %s's comment on %s: %w", r.Author, postURL, err)
		}
		liked++
		likedAuthors = append(likedAuthors, r.Author)
		if err := pause(ctx, uiPause); err != nil {
			return nil, err
		}
	}
	if liked == 0 && already == 0 {
		return nil, fmt.Errorf("instagram: no likeable comment on %s", postURL)
	}
	status := "liked"
	if liked == 0 {
		status = "already_liked"
	}
	return ok("url", postURL, "status", status, "liked_count", liked, "already_liked_count", already,
		"liked_authors", strings.Join(likedAuthors, ",")), nil
}

func hasAuthor(rows []commentRow, author string) bool {
	for _, r := range rows {
		if strings.EqualFold(r.Author, author) {
			return true
		}
	}
	return false
}

// likeOneComment clicks the like heart of the comment matching r (author and
// text) and verifies it turned into Unlike.
func likeOneComment(ctx context.Context, p browser.PageInterface, r commentRow) error {
	m := newMark()
	var state string
	if err := evalJS(p, `(a, t, m) => {
		const c = comments().find((x) => x.author.toLowerCase() === a.toLowerCase() && x.text === t);
		if (!c) return 'gone';
		if (c.liked) return 'liked';
		if (!c.like) return 'no_button';
		mark(btnOf(c.like) || c.like.parentElement, m);
		return 'marked';
	}`, &state, r.Author, r.Text, m); err != nil {
		return err
	}
	switch state {
	case "liked":
		return nil
	case "marked":
	default:
		return fmt.Errorf("comment %s", state)
	}
	if err := clickMarked(p, m); err != nil {
		return err
	}
	return poll(ctx, verifyTimeout, func() (bool, error) {
		var liked bool
		err := evalJS(p, `(a, t) => { const c = comments().find((x) => x.author.toLowerCase() === a.toLowerCase() && x.text === t); return !!c && c.liked; }`, &liked, r.Author, r.Text)
		return liked, err
	})
}

// ReplyToComment replies to a comment: commentAuthor's first comment, or —
// when commentAuthor is empty — the first comment on the post. The reply is
// only posted once Instagram has put the "@author" reply prefix into the
// composer (so it is threaded under that comment, never posted as a
// top-level comment), and it is verified.
func (b *InstagramBot) ReplyToComment(ctx context.Context, p browser.PageInterface, postURL, commentAuthor, replyText string) (map[string]interface{}, error) {
	if strings.TrimSpace(replyText) == "" {
		return nil, errors.New("instagram: reply text is required")
	}
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	author := strings.TrimPrefix(strings.TrimSpace(commentAuthor), "@")
	rows := waitComments(ctx, p)
	if author != "" && !hasAuthor(rows, author) {
		loadMoreComments(ctx, p, 3)
		rows, _ = readComments(p)
	}
	rm := newMark()
	var target struct {
		State  string `json:"state"`
		Author string `json:"author"`
	}
	if err := evalJS(p, `(a, m) => {
		const cs = comments();
		if (!cs.length) return { state: 'not_found' };
		const c = a ? cs.find((x) => x.author.toLowerCase() === a.toLowerCase()) : cs[0];
		if (!c) return { state: 'author_not_found' };
		mark(c.reply, m);
		return { state: 'marked', author: c.author };
	}`, &target, author, rm); err != nil {
		return nil, err
	}
	if _, err := resolveCommentTargetState(target.State, author, postURL, "reply to"); err != nil {
		return nil, err
	}
	box := newMark()
	if err := b.findCommentBox(ctx, p, box); err != nil {
		return nil, fmt.Errorf("instagram: %s: %w", postURL, err)
	}
	baseline, _ := commentBaseline(p, replyText)
	if err := clickMarked(p, rm); err != nil {
		return nil, fmt.Errorf("instagram: click Reply: %w", err)
	}
	// Instagram answers the click by prefilling "@author " in the composer.
	prefix := "@" + target.Author
	err = poll(ctx, findTimeout/2, func() (bool, error) {
		var v string
		err := evalJS(p, `(m) => { const el = document.querySelector('['+MARK+'="'+m+'"]') || commentBox(); if (el && !el.hasAttribute(MARK)) mark(el, m); return boxValue(el); }`, &v, box)
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), strings.ToLower(prefix)), err
	})
	if err != nil {
		return nil, fmt.Errorf("instagram: Reply did not address %s in the composer on %s — not posting an unthreaded comment", prefix, postURL)
	}
	text := replyText
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), strings.ToLower(prefix)) {
		text = strings.TrimSpace(text[len(prefix):])
	}
	if err := typeAtEnd(ctx, p, box, text); err != nil {
		return nil, fmt.Errorf("instagram: type reply on %s: %w", postURL, err)
	}
	if err := pause(ctx, uiPause/2); err != nil {
		return nil, err
	}
	how, err := b.submitComment(ctx, p, box, text, baseline)
	unmark(p, box)
	if err != nil {
		return nil, fmt.Errorf("instagram: reply on %s not confirmed: %w", postURL, err)
	}
	return ok("url", postURL, "status", "replied", "replied_to", target.Author, "verified_by", how), nil
}

// ListPostComments returns up to maxCount comments of a post: author, text,
// timestamp, is_liked, likes_count and reply_count.
func (b *InstagramBot) ListPostComments(ctx context.Context, p browser.PageInterface, postURL string, maxCount int) ([]map[string]interface{}, error) {
	if maxCount <= 0 {
		maxCount = 50
	}
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	waitComments(ctx, p)
	return readCommentList(ctx, p, postURL, maxCount)
}

func readCommentList(ctx context.Context, p browser.PageInterface, postURL string, maxCount int) ([]map[string]interface{}, error) {
	type full struct {
		Author    string `json:"author"`
		Text      string `json:"text"`
		Timestamp string `json:"timestamp"`
		Liked     bool   `json:"is_liked"`
		Likes     int    `json:"likes_count"`
		Replies   int    `json:"reply_count"`
		IsReply   bool   `json:"is_reply"`
	}
	var rows []full
	for round := 0; round < 10; round++ {
		if err := evalJS(p, `() => {
			const cs = comments();
			const depth = (el) => { let n = 0; for (let u = el.closest('ul'); u; u = u.parentElement && u.parentElement.closest('ul')) n++; return n; };
			const min = Math.min(...cs.map((c) => depth(c.el)));
			return cs.map((c) => {
				const likes = (T(c.el).match(/(\d[\d,]*) likes?/i) || [])[1] || '0';
				const li = c.el.closest('li');
				const vr = li ? btns(li).map(T).find((t) => /view (all )?(\d+ )?repl|view replies/i.test(t)) : '';
				const n = vr ? ((vr.match(/(\d+)/) || [])[1] || '0') : '0';
				return { author: c.author, text: c.text, timestamp: c.timestamp, is_liked: c.liked,
					likes_count: parseInt(likes.replace(/,/g, ''), 10) || 0, reply_count: parseInt(n, 10) || 0,
					is_reply: depth(c.el) > min };
			});
		}`, &rows); err != nil {
			return nil, fmt.Errorf("instagram: read comments on %s: %w", postURL, err)
		}
		if len(rows) >= maxCount {
			break
		}
		before := len(rows)
		loadMoreComments(ctx, p, 1)
		var after []commentRow
		after, _ = readComments(p)
		if len(after) <= before {
			break
		}
	}
	out := make([]map[string]interface{}, 0, len(rows))
	for i, r := range rows {
		if i >= maxCount {
			break
		}
		out = append(out, map[string]interface{}{
			"platform": "INSTAGRAM", "post_url": postURL, "author": r.Author, "author_url": profileURL(r.Author),
			"text": r.Text, "timestamp": r.Timestamp, "is_liked": r.Liked, "likes_count": r.Likes,
			"reply_count": r.Replies, "is_reply": r.IsReply,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Post data (READ)
// ---------------------------------------------------------------------------

// ScrapePostData reads a post: author, caption, likes, comment count, date,
// media URLs and type (image, video, reel, carousel). maxComments > 0 also
// returns up to that many comments.
func (b *InstagramBot) ScrapePostData(ctx context.Context, p browser.PageInterface, postURL string, maxComments int) (map[string]interface{}, error) {
	postURL, err := b.openPost(ctx, p, postURL)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	err = poll(ctx, findTimeout, func() (bool, error) {
		data = nil
		if err := evalJS(p, `() => {
			const d = {};
			const meta = (p) => { const m = document.querySelector('meta[property="' + p + '"]') || document.querySelector('meta[name="' + p + '"]'); return m ? m.getAttribute('content') || '' : ''; };
			const ogRef = postRef(meta('og:url'));
			const ref = postRef(location.href) || ogRef;
			d.shortcode = ref ? ref.shortcode : '';
			d.post_url = ref ? 'https://www.instagram.com/' + (ref.kind === 'reel' ? 'reel' : 'p') + '/' + ref.shortcode + '/' : location.href;
			const sc = d.shortcode;
			// The post as Instagram embeds it in the page: the most reliable
			// source for author, caption, type and media, logged in or out.
			const em = embeddedMedia(sc);
			const cs = new Set(comments().map((c) => c.body));
			const inComment = (el) => Array.from(cs).some((b) => b.contains(el));
			// otherPost: el belongs to a link to a different post (the
			// "More posts from …" grid under the post).
			const otherPost = (el) => { const a = el.closest('a[href]'); const r = a && postRef(a.getAttribute('href')); return !!(r && r.shortcode !== sc); };
			// Author: the URL (/<user>/p/<code>/) or og:url, the embedded post,
			// the post header link, else "<user> on <date>:" in og:description.
			// Never an @mention of the caption.
			let author = (ref && ref.author) || (ogRef && ogRef.author) ||
				(em && ((em.user && em.user.username) || (em.owner && em.owner.username))) || '';
			let header = null;
			for (const h of document.querySelectorAll('main header, article header, header')) {
				const a = Array.from(h.querySelectorAll('a[href]')).find((x) => userOf(x.getAttribute('href')));
				if (a) { header = h; if (!author) author = userOf(a.getAttribute('href')); break; }
			}
			if (!author) { const m = meta('og:description').match(/\s-\s([A-Za-z0-9._]{1,30}) on [^:"]*:/); if (m) author = m[1]; }
			if (!author) { const m = meta('twitter:title').match(/\(@([A-Za-z0-9._]{1,30})\)/); if (m) author = m[1]; }
			d.author_username = author || '';
			const bars = postBars();
			d.rendered = !!(bars.length || header || em);
			// Caption: the embedded post's, else the rendered caption (an <h1>,
			// or the author's row — author · time, then the text — that is not
			// a comment), else og:description's / og:title's quoted text.
			// Caption text keeps its line breaks (<br>): innerText, not T.
			const ctext = (el) => (el.innerText || '').replace(/[ \t\u00a0]+/g, ' ').replace(/ *\n */g, '\n').replace(/\n{3,}/g, '\n\n').trim() || T(el);
			let caption = '';
			if (em) {
				const e2 = em.edge_media_to_caption && em.edge_media_to_caption.edges && em.edge_media_to_caption.edges[0];
				caption = (em.caption && em.caption.text) || (e2 && e2.node && e2.node.text) || '';
			}
			const h1 = Array.from(document.querySelectorAll('main h1, article h1')).find((h) => T(h) && !inComment(h) && !h.closest('header'));
			if (!caption && h1) caption = ctext(h1);
			if (!caption) {
				for (const li of document.querySelectorAll('ul li, ul > div')) {
					if (Array.from(cs).some((b) => li.contains(b) || b.contains(li))) continue;
					const a = Array.from(li.querySelectorAll('a[href]')).find((x) => userOf(x.getAttribute('href')));
					if (!a || userOf(a.getAttribute('href')).toLowerCase() !== (author || '').toLowerCase()) continue;
					const spans = Array.from(li.querySelectorAll('span, h1')).filter((s) => !a.contains(s) && !s.closest('time, button, [role="button"]'));
					caption = spans.map(T).sort((x, y) => y.length - x.length)[0] || '';
					if (caption) break;
				}
			}
			if (!caption && author) {
				const mine = Array.from(document.querySelectorAll('main a[href], article a[href]')).filter((a) =>
					userOf(a.getAttribute('href')).toLowerCase() === author.toLowerCase() && !inComment(a) && !otherPost(a));
				for (const a of mine) {
					let blk = a.parentElement;
					for (let i = 0; i < 8 && blk && !blk.querySelector('time'); i++) blk = blk.parentElement;
					if (!blk || inComment(blk) || Array.from(cs).some((b) => blk.contains(b))) continue;
					const texts = Array.from(blk.querySelectorAll('span, h1, div[dir="auto"]')).filter((s) =>
						!s.closest('a[href], time, button, [role="button"]') && !s.querySelector('time') && !s.contains(a))
						.map(ctext).filter((t) => t && t.toLowerCase() !== author.toLowerCase() && !REL_TIME.test(t) && !/^(edited|verified|follow|•|·)$/i.test(t));
					const best = texts.sort((x, y) => y.length - x.length)[0];
					if (best) { caption = best; break; }
				}
			}
			if (!caption) {
				const m = meta('og:description').match(/:\s*"([\s\S]*)"\s*\.?\s*$/) || meta('og:title').match(/:\s*"([\s\S]*)"\s*\.?\s*$/);
				if (m) caption = m[1];
			}
			d.caption = caption;
			d.hashtags = Array.from(new Set((caption.match(/#[\p{L}\p{N}_]+/gu) || [])));
			d.mentions = Array.from(new Set((caption.match(/@[A-Za-z0-9._]+/g) || []).map((x) => x.slice(1).replace(/\.+$/, '')).filter(Boolean)));
			// Likes: "1,234 likes" / "Liked by x and 1,233 others" in the post's sections.
			let likes = '';
			for (const s of document.querySelectorAll('section')) {
				if (s.closest('li')) continue;
				const t = T(s);
				let m = t.match(/([\d,.]+[kKmM]?)\s+likes?\b/);
				if (m) { likes = m[1]; break; }
				m = t.match(/liked by \S+ and ([\d,]+) others?/i);
				if (m) { likes = String(parseInt(m[1].replace(/,/g, ''), 10) + 1); break; }
			}
			if (em && typeof em.like_count === 'number' && em.like_count > 0) likes = String(em.like_count);
			if (!likes) { const m = meta('og:description').match(/^([\d,.]+[KM]?) likes?/i); if (m) likes = m[1]; }
			d.likes_count = likes;
			let cc = '';
			const vm = VT().match(/view all ([\d,]+) comments/i);
			if (vm) cc = vm[1];
			if (em && typeof em.comment_count === 'number' && em.comment_count > 0) cc = String(em.comment_count);
			if (!cc) { const m = meta('og:description').match(/([\d,.]+[KM]?) comments?/i); if (m) cc = m[1]; }
			d.comments_count = cc;
			d.comments_loaded = comments().length;
			// Date: the post's own time (a link to the post), else the first
			// time outside the comment list.
			let time = null;
			for (const t of document.querySelectorAll('time[datetime]')) {
				const a = t.closest('a[href]');
				let path = '';
				try { path = a ? new URL(a.getAttribute('href'), location.href).pathname : ''; } catch (e) {}
				if (/^\/(?:[^/]+\/)?(p|reel|tv)\/[^/]+\/?$/.test(path) && !inComment(t)) { time = t; break; }
			}
			if (!time) time = Array.from(document.querySelectorAll('time[datetime]')).find((t) => !inComment(t)) || null;
			d.post_date = time ? time.getAttribute('datetime') : '';
			if (!d.post_date && em && em.taken_at) d.post_date = new Date(em.taken_at * 1000).toISOString();
			// Media: the embedded post's own items; else the images/videos of the
			// post body — not avatars, comment images, the "more posts" grid, or
			// blob: stream URLs of the video player.
			const main = document.querySelector('main') || document.body;
			const own = (el) => !el.closest('header') && !inComment(el) && !otherPost(el) &&
				!(el.closest('a[href]') && userOf(el.closest('a[href]').getAttribute('href')));
			const dom = [];
			for (const img of main.querySelectorAll('img[src]')) {
				if (!own(img)) continue;
				const alt = img.getAttribute('alt') || '';
				if (/profile picture/i.test(alt)) continue;
				const src = img.getAttribute('src') || '';
				if (/^(blob|data):/.test(src)) continue;
				const w = img.naturalWidth || +img.getAttribute('width') || img.getBoundingClientRect().width;
				if (w && w < 150) continue;
				dom.push(src);
			}
			const vids = Array.from(main.querySelectorAll('video')).filter(own);
			for (const v of vids) {
				const s = v.getAttribute('src') || (v.querySelector('source') || { getAttribute: () => '' }).getAttribute('src') || '';
				const u = s && !/^blob:/.test(s) ? s : (v.getAttribute('poster') || '');
				if (u) dom.push(u);
			}
			const emMedia = em ? mediaURLs(em) : [];
			d.media_urls = Array.from(new Set(emMedia.length ? emMedia : dom));
			// Type: the embedded post's, else the page — a carousel has a Next
			// control in the media (not the story-highlight tray) or several
			// media items; /reel/ is a reel.
			const next = Array.from(main.querySelectorAll('button[aria-label="Next"], [role="button"][aria-label="Next"]')).some((b) => own(b) && !b.closest('[role="menu"]'));
			const domKind = (next || dom.length > 1) ? 'carousel' : ((ref && ref.kind === 'reel') ? 'reel' : (vids.length ? 'video' : 'image'));
			d.post_type = (em && mediaKind(em)) || domKind;
			d.is_liked = !!(bars.length && bars[0].likes.some((s) => lbl(s) === 'Unlike'));
			return d;
		}`, &data); err != nil {
			return false, err
		}
		rendered, _ := data["rendered"].(bool)
		return rendered, nil
	})
	if err != nil || data == nil {
		return nil, fmt.Errorf("instagram: post %s did not render: %v", postURL, err)
	}
	delete(data, "rendered")
	data["platform"] = "INSTAGRAM"
	if a := getString(data, "author_username"); a != "" {
		data["author_url"] = profileURL(a)
	}
	for _, k := range []string{"likes_count", "comments_count"} {
		if s := getString(data, k); s != "" {
			data[k] = parseCount(s)
		}
	}
	if maxComments > 0 {
		cs, err := readCommentList(ctx, p, postURL, maxComments)
		if err == nil {
			data["comments"] = listResult(cs)
		}
	}
	return data, nil
}
