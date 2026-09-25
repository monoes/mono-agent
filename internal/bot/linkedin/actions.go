//go:build social

package linkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// reLinkedInActivity matches the numeric activity ID in LinkedIn post URLs.
// e.g. "activity-7123456789" or "activity:7123456789"
var reLinkedInActivity = regexp.MustCompile(`activity[-:](\d+)`)

// reactionButtonSelectors maps a reaction name to its static CSS selector for the popup button.
var reactionButtonSelectors = map[string]string{
	"celebrate":  "button[aria-label='Celebrate']",
	"support":    "button[aria-label='Support']",
	"love":       "button[aria-label='Love']",
	"insightful": "button[aria-label='Insightful']",
	"funny":      "button[aria-label='Funny']",
}

// jsonUnmarshal is a package-local helper to decode JSON strings from page.Eval results.
func jsonUnmarshal(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// ListUserPosts scrapes posts from a LinkedIn personal profile or company page.
// profileURL: full URL (e.g. https://www.linkedin.com/in/username/ or /company/slug/)
// maxCount: max number of posts to collect
// activityType: "shares" | "all"
func (b *LinkedInBot) ListUserPosts(ctx context.Context, page browser.PageInterface, profileURL string, maxCount int, activityType string) ([]map[string]interface{}, error) {
	if profileURL == "" {
		return nil, fmt.Errorf("linkedin: profileURL is required")
	}
	if maxCount <= 0 {
		maxCount = 20
	}
	if activityType == "" {
		activityType = "all"
	}

	base := strings.TrimRight(profileURL, "/") + "/"
	var feedURL string
	if strings.Contains(base, "/company/") {
		feedURL = base + "posts/"
	} else {
		if activityType == "shares" {
			feedURL = base + "recent-activity/shares/"
		} else {
			feedURL = base + "recent-activity/all/"
		}
	}

	if err := page.Navigate(feedURL); err != nil {
		return nil, fmt.Errorf("linkedin: failed to navigate to activity feed: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("linkedin: activity feed did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	var allPosts []map[string]interface{}
	seen := map[string]bool{}
	noNewCount := 0

	for len(allPosts) < maxCount && noNewCount < 3 {
		res, err := page.Eval(`() => {
			const posts = [];
			const links = Array.from(document.querySelectorAll('a[href*="/posts/"], a[href*="/feed/update/"]'));
			for (const a of links) {
				const href = a.href || '';
				if (!href) continue;
				const article = a.closest('div[data-id], article, li');
				let author = '', authorUrl = '', textPreview = '', timestamp = '', likesCount = 0, commentsCount = 0, repostsCount = 0;
				if (article) {
					const nameEl = article.querySelector('span.feed-shared-actor__name, span.update-components-actor__name, a.update-components-actor__meta-link span[aria-hidden="true"]');
					if (nameEl) author = nameEl.innerText.trim();
					const authorLink = article.querySelector('a[href*="/in/"], a[href*="/company/"]');
					if (authorLink) authorUrl = authorLink.href;
					const textEl = article.querySelector('span.break-words, div.feed-shared-update-v2__description, div.update-components-text');
					if (textEl) textPreview = textEl.innerText.trim().slice(0, 200);
					const timeEl = article.querySelector('time, span[class*="time"]');
					if (timeEl) timestamp = timeEl.getAttribute('datetime') || timeEl.innerText.trim();
					const socialEl = article.querySelector('span.social-details-social-counts__reactions-count, button[aria-label*="reaction"]');
					if (socialEl) likesCount = parseInt(socialEl.innerText.replace(/[^0-9]/g,'')) || 0;
				}
				posts.push({ url: href, author, authorUrl, textPreview, timestamp, likesCount, commentsCount, repostsCount });
			}
			return JSON.stringify(posts);
		}`)
		if err != nil {
			return nil, fmt.Errorf("linkedin: failed to evaluate posts script: %w", err)
		}

		var rawPosts []map[string]interface{}
		if jsonErr := jsonUnmarshal(res.Str(), &rawPosts); jsonErr != nil {
			return nil, fmt.Errorf("linkedin: failed to parse posts JSON: %w", jsonErr)
		}

		newThisScroll := 0
		for _, p := range rawPosts {
			rawURL, _ := p["url"].(string)
			if rawURL == "" {
				continue
			}
			m := reLinkedInActivity.FindStringSubmatch(rawURL)
			if len(m) < 2 {
				// URL doesn't contain activity ID pattern — skip silently (LinkedIn has varied URL formats)
				continue
			}
			activityID := m[1]
			if seen[activityID] {
				continue
			}
			seen[activityID] = true
			newThisScroll++
			post := map[string]interface{}{
				"url":            rawURL,
				"activity_id":    activityID,
				"shortcode":      activityID,
				"text_preview":   p["textPreview"],
				"author":         p["author"],
				"author_url":     p["authorUrl"],
				"timestamp":      p["timestamp"],
				"likes_count":    p["likesCount"],
				"comments_count": p["commentsCount"],
				"reposts_count":  p["repostsCount"],
			}
			allPosts = append(allPosts, post)
			if len(allPosts) >= maxCount {
				break
			}
		}

		if newThisScroll == 0 {
			noNewCount++
		} else {
			noNewCount = 0
		}

		if len(allPosts) < maxCount {
			_, _ = page.Eval(`() => window.scrollBy(0, window.innerHeight * 2)`)
			time.Sleep(2 * time.Second)
		}
	}

	return allPosts, nil
}

// ListPostComments scrapes comments (and optionally replies) from a LinkedIn post.
func (b *LinkedInBot) ListPostComments(ctx context.Context, page browser.PageInterface, postURL string, maxCount int, includeReplies bool) ([]map[string]interface{}, error) {
	if postURL == "" {
		return nil, fmt.Errorf("linkedin: postURL is required")
	}
	if maxCount <= 0 {
		maxCount = 50
	}

	if err := page.Navigate(postURL); err != nil {
		return nil, fmt.Errorf("linkedin: failed to navigate to post: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("linkedin: post page did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	// Click "Load more comments" up to 10 times.
	for i := 0; i < 10; i++ {
		loadMoreRes, loadMoreErr := page.Eval(`() => {
			const btns = Array.from(document.querySelectorAll('button'));
			const loadMore = btns.find(b => b.innerText.match(/load more comments/i) || b.innerText.match(/show more/i));
			if (loadMore) { loadMore.click(); return true; }
			return false;
		}`)
		if loadMoreErr != nil || loadMoreRes == nil || !loadMoreRes.Bool() {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// If includeReplies, click "Load X replies" for each comment.
	if includeReplies {
		for i := 0; i < 20; i++ {
			repliesRes, repliesErr := page.Eval(`() => {
				const btns = Array.from(document.querySelectorAll('button'));
				const loadReplies = btns.find(b => b.innerText.match(/load \d+ repl/i) || b.innerText.match(/view repl/i));
				if (loadReplies) { loadReplies.click(); return true; }
				return false;
			}`)
			if repliesErr != nil || repliesRes == nil || !repliesRes.Bool() {
				break
			}
			time.Sleep(1500 * time.Millisecond)
		}
	}

	res, err := page.Eval(`() => {
		const comments = [];
		const commentEls = document.querySelectorAll('article.comments-comment-entity, article.comments-comment-item');
		for (const el of commentEls) {
			const id = el.getAttribute('data-id') || el.getAttribute('id') || '';
			const authorEl = el.querySelector('span.comments-comment-meta__description-title, span.comments-post-meta__name, a.comments-post-meta__name span');
			const author = authorEl ? authorEl.innerText.trim() : '';
			const authorLinkEl = el.querySelector('a[href*="/in/"]');
			const authorUrl = authorLinkEl ? authorLinkEl.href : '';
			const textEl = el.querySelector('span.comments-comment-item__main-content, div.update-components-text');
			const text = textEl ? textEl.innerText.trim() : '';
			const timeEl = el.querySelector('time');
			const timestamp = timeEl ? (timeEl.getAttribute('datetime') || timeEl.innerText.trim()) : '';
			const likeEl = el.querySelector('button[aria-label*="Like"][aria-label*="comment"], span.social-details-social-counts__reactions-count');
			const likesCount = likeEl ? (parseInt((likeEl.innerText || '').replace(/[^0-9]/g, '')) || 0) : 0;
			const isReply = !!el.closest('div.comments-comment-item__nested-items, div[class*="nested"]');
			const parentEl = isReply ? el.parentElement.closest('article.comments-comment-entity, article.comments-comment-item') : null;
			const parentId = parentEl ? (parentEl.getAttribute('data-id') || '') : '';
			comments.push({ id, author, authorUrl, text, timestamp, likesCount, replyCount: 0, parentId: parentId || null });
		}
		return JSON.stringify(comments);
	}`)
	if err != nil {
		return nil, fmt.Errorf("linkedin: failed to extract comments: %w", err)
	}

	var rawComments []map[string]interface{}
	if err := jsonUnmarshal(res.Str(), &rawComments); err != nil {
		return nil, fmt.Errorf("linkedin: failed to parse comments JSON: %w", err)
	}

	var result []map[string]interface{}
	for i, c := range rawComments {
		if i >= maxCount {
			break
		}
		result = append(result, map[string]interface{}{
			"id":          c["id"],
			"author":      c["author"],
			"author_url":  c["authorUrl"],
			"text":        c["text"],
			"timestamp":   c["timestamp"],
			"likes_count": c["likesCount"],
			"reply_count": c["replyCount"],
			"parent_id":   c["parentId"],
		})
	}

	return result, nil
}

// likePostSleep is time.Sleep; tests replace it.
var likePostSleep = time.Sleep

// findReactionButtonJS finds the post's own Like/React button (not a
// comment's), marks the first candidate with data-monoagent-reaction-btn and
// reports how many candidates it saw, as JSON {state, count}. state is
// "not_found", "already_reacted" or "marked".
const findReactionButtonJS = `() => {
		const prev = document.querySelector('[data-monoagent-reaction-btn]');
		if (prev) prev.removeAttribute('data-monoagent-reaction-btn');

		const candidates = [];
		const actionBars = document.querySelectorAll('div.feed-shared-social-action-bar, div.social-actions-button, div[class*="social-actions"]');
		for (const bar of actionBars) {
			const btn = bar.querySelector('button[aria-label*="Like"], button[aria-label*="React"]');
			if (btn && !candidates.includes(btn)) candidates.push(btn);
		}
		if (candidates.length === 0) {
			const allBtns = document.querySelectorAll('button[aria-label*="React Like"], button[aria-label*="Like"]');
			for (const btn of allBtns) {
				if (!btn.closest('article.comments-comment-item') && !btn.closest('div[class*="comment-item"]')) {
					candidates.push(btn);
				}
			}
		}
		if (candidates.length === 0) return JSON.stringify({state: 'not_found', count: 0});
		const reactionBtn = candidates[0];

		const label = (reactionBtn.getAttribute('aria-label') || '').toLowerCase();
		if (label.includes('remove') || label.includes('unlike')) return JSON.stringify({state: 'already_reacted', count: candidates.length});

		reactionBtn.setAttribute('data-monoagent-reaction-btn', 'true');
		return JSON.stringify({state: 'marked', count: candidates.length});
	}`

// reactionLabel is the reaction's name as LinkedIn labels its button.
func reactionLabel(reaction string) string {
	if reaction == "" {
		return ""
	}
	return strings.ToUpper(reaction[:1]) + reaction[1:]
}

// alreadyReacted reports whether a reaction button's label says the post
// already carries the viewer's reaction.
func alreadyReacted(label string) bool {
	label = strings.ToLower(label)
	return strings.Contains(label, "remove") || strings.Contains(label, "unlike")
}

// LikePost reacts to a LinkedIn post with the specified reaction.
// reaction: "like"|"celebrate"|"support"|"love"|"insightful"|"funny" (default:
// "like"; any other value is an error). The requested reaction is never
// substituted: when its button is not in the reactions menu, Jev picks it
// (if this bot has a picker) or the call fails.
//
// The post's own reaction button is found by selectors; when they match no
// button or several, and a Jev picker is set, Jev picks the post's reaction
// button. A Jev failure keeps today's result (the first match, or the
// not-found error).
func (b *LinkedInBot) LikePost(ctx context.Context, page browser.PageInterface, postURL string, reaction string) error {
	if postURL == "" {
		return fmt.Errorf("linkedin: postURL is required")
	}
	reaction = strings.ToLower(strings.TrimSpace(reaction))
	if reaction == "" {
		reaction = "like"
	}
	if _, ok := reactionButtonSelectors[reaction]; !ok && reaction != "like" {
		return fmt.Errorf("linkedin: unknown reaction %q (want like, celebrate, support, love, insightful or funny)", reaction)
	}

	if err := page.Navigate(postURL); err != nil {
		return fmt.Errorf("linkedin: failed to navigate to post: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("linkedin: post page did not load: %w", err)
	}
	likePostSleep(3 * time.Second)

	res, err := page.Eval(findReactionButtonJS)
	if err != nil {
		return fmt.Errorf("linkedin: failed to evaluate reaction script: %w", err)
	}
	var found struct {
		State string `json:"state"`
		Count int    `json:"count"`
	}
	if err := jsonUnmarshal(res.Str(), &found); err != nil {
		return fmt.Errorf("linkedin: failed to parse reaction script result: %w", err)
	}

	btnSel := "[data-monoagent-reaction-btn='true']"
	var reactionBtn browser.ElementHandle
	if found.Count != 1 && b.JevAvailable(page) {
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Like (React) button of the LinkedIn post shown on this page (%s) — the post's own reaction button, not a Like button that belongs to a comment", postURL),
			Hint:   "LinkedIn labels it e.g. \"React Like\"; a label with \"Remove\" or \"Unlike\" means the post was already reacted to.",
		})
		switch {
		case jerr == nil:
			defer pick.Release()
			label := pick.Label
			if a, aerr := pick.Element.Attribute("aria-label"); aerr == nil && a != nil && *a != "" {
				label = *a
			}
			if alreadyReacted(label) {
				return nil
			}
			btnSel, reactionBtn, found.State = pick.Selector, pick.Element, "marked"
		case found.Count == 0:
			return fmt.Errorf("linkedin: could not find reaction button on %s (%s) (jev fallback: %v)", postURL, found.State, jerr)
		}
		// Several matches and no usable pick: the first match, as before.
	}

	if found.State == "already_reacted" {
		return nil
	}
	if found.State != "marked" {
		return fmt.Errorf("linkedin: could not find reaction button on %s (%s)", postURL, found.State)
	}

	if reactionBtn == nil {
		reactionBtn, err = page.Element(btnSel, 5*time.Second)
		if err != nil {
			return fmt.Errorf("linkedin: marked reaction button not found: %w", err)
		}
	}
	if err := reactionBtn.ScrollIntoView(); err != nil {
		return fmt.Errorf("linkedin: failed to scroll element into view: %w", err)
	}
	likePostSleep(300 * time.Millisecond)

	if reaction == "like" {
		if err := reactionBtn.Click(); err != nil {
			return fmt.Errorf("linkedin: failed to click Like: %w", err)
		}
	} else {
		if err := reactionBtn.ScrollIntoView(); err != nil {
			return fmt.Errorf("linkedin: failed to scroll reaction button into view: %w", err)
		}
		// Simulate hover by dispatching mouseover event via JS.
		_, _ = page.Eval(fmt.Sprintf(`() => {
			const el = document.querySelector(%q);
			if (el) el.dispatchEvent(new MouseEvent('mouseover', {bubbles: true}));
		}`, btnSel))
		likePostSleep(1 * time.Second)

		popupBtn, popupErr := page.Element(reactionButtonSelectors[reaction], 5*time.Second)
		if popupErr != nil {
			// Never substitute another reaction: Jev finds the requested
			// one in the open menu, or the call fails.
			label := reactionLabel(reaction)
			pick, jerr := b.JevElement(ctx, page, jevpick.Target{
				Kind:   "click",
				Intent: fmt.Sprintf("the %q reaction button in the LinkedIn reactions menu opened over the post's Like button — exactly the %s reaction, not Like and not any other reaction", label, label),
			})
			if jerr != nil {
				return fmt.Errorf("linkedin: %s reaction not found on %s: %w (jev fallback: %v)", reaction, postURL, popupErr, jerr)
			}
			defer pick.Release()
			popupBtn = pick.Element
		}
		if err := popupBtn.Click(); err != nil {
			return fmt.Errorf("linkedin: failed to click %s reaction: %w", reaction, err)
		}
	}

	likePostSleep(2 * time.Second)
	page.Eval(`() => {
		const el = document.querySelector('[data-monoagent-reaction-btn]');
		if (el) el.removeAttribute('data-monoagent-reaction-btn');
	}`)

	return nil
}

// CommentOnPost posts a comment on a LinkedIn post, or replies to a specific comment.
// parentCommentID: empty for top-level comment; URN string to reply to that comment.
func (b *LinkedInBot) CommentOnPost(ctx context.Context, page browser.PageInterface, postURL, commentText, parentCommentID string) error {
	if postURL == "" {
		return fmt.Errorf("linkedin: postURL is required")
	}
	if commentText == "" {
		return fmt.Errorf("linkedin: commentText is required")
	}

	if err := page.Navigate(postURL); err != nil {
		return fmt.Errorf("linkedin: failed to navigate to post: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("linkedin: post page did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	if parentCommentID != "" {
		replyRes, err := page.Eval(`(id) => {
			const prev = document.querySelector('[data-monoagent-reply-btn]');
			if (prev) prev.removeAttribute('data-monoagent-reply-btn');
			const commentEl = document.querySelector('[data-id="' + CSS.escape(id) + '"]');
			if (!commentEl) return 'not_found';
			const replyBtn = Array.from(commentEl.querySelectorAll('button')).find(b => b.innerText.trim() === 'Reply');
			if (!replyBtn) return 'no_reply_btn';
			replyBtn.setAttribute('data-monoagent-reply-btn', 'true');
			return 'marked';
		}`, parentCommentID)
		if err != nil || replyRes.Str() != "marked" {
			return fmt.Errorf("linkedin: could not find Reply button for comment %s", parentCommentID)
		}
		replyBtn, err := page.Element("[data-monoagent-reply-btn='true']", 5*time.Second)
		if err != nil {
			return fmt.Errorf("linkedin: marked Reply button not found: %w", err)
		}
		if err := replyBtn.ScrollIntoView(); err != nil {
			return fmt.Errorf("linkedin: failed to scroll element into view: %w", err)
		}
		time.Sleep(300 * time.Millisecond)
		if err := replyBtn.Click(); err != nil {
			return fmt.Errorf("linkedin: failed to click Reply: %w", err)
		}
		time.Sleep(1 * time.Second)
		page.Eval(`() => {
			const el = document.querySelector('[data-monoagent-reply-btn]');
			if (el) el.removeAttribute('data-monoagent-reply-btn');
		}`)
	}

	inputRes, err := page.Eval(`() => {
		const prev = document.querySelector('[data-monoagent-comment-input]');
		if (prev) prev.removeAttribute('data-monoagent-comment-input');

		const editors = document.querySelectorAll('div.ql-editor[contenteditable="true"], div[role="textbox"][contenteditable="true"]');
		if (editors.length > 0) {
			editors[editors.length - 1].setAttribute('data-monoagent-comment-input', 'true');
			return 'marked';
		}
		const textareas = document.querySelectorAll('textarea[aria-label*="comment" i]');
		if (textareas.length > 0) {
			textareas[textareas.length - 1].setAttribute('data-monoagent-comment-input', 'true');
			return 'marked';
		}
		return 'not_found';
	}`)
	if err != nil || inputRes.Str() != "marked" {
		return fmt.Errorf("linkedin: could not find comment input")
	}

	commentInput, err := page.Element("[data-monoagent-comment-input='true']", 5*time.Second)
	if err != nil {
		return fmt.Errorf("linkedin: marked comment input not found: %w", err)
	}
	if err := commentInput.ScrollIntoView(); err != nil {
		return fmt.Errorf("linkedin: failed to scroll element into view: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := commentInput.Click(); err != nil {
		return fmt.Errorf("linkedin: failed to click comment input: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	for _, ch := range commentText {
		if err := page.KeyboardType(ch); err != nil {
			return fmt.Errorf("linkedin: failed to type character: %w", err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)

	submitRes, err := page.Eval(`() => {
		const prev = document.querySelector('[data-monoagent-submit-btn]');
		if (prev) prev.removeAttribute('data-monoagent-submit-btn');
		// Try the known submit button class first (most reliable).
		let submitBtn = document.querySelector('button.comments-comment-box__submit-button--cr');
		if (!submitBtn) {
			// Search within the comment box container.
			const commentBox = document.querySelector('[data-monoagent-comment-input]')?.closest('.comments-comment-box--cr, .comments-comment-texteditor, .comments-reply-box');
			const searchIn = commentBox || document;
			const submitLabels = new Set(['Post', 'Done', 'Reply', 'Comment', 'Submit']);
			submitBtn = Array.from(searchIn.querySelectorAll('button')).find(b => submitLabels.has(b.innerText.trim()));
		}
		if (submitBtn) {
			submitBtn.setAttribute('data-monoagent-submit-btn', 'true');
			return 'marked';
		}
		return 'not_found';
	}`)
	if err != nil || submitRes.Str() != "marked" {
		if err := page.KeyboardPress('\n'); err != nil {
			return fmt.Errorf("keyboard enter press: %w", err)
		}
	} else {
		submitBtn, err := page.Element("[data-monoagent-submit-btn='true']", 5*time.Second)
		if err != nil {
			if err := page.KeyboardPress('\n'); err != nil {
				return fmt.Errorf("keyboard enter press: %w", err)
			}
		} else {
			if err := submitBtn.ScrollIntoView(); err != nil {
				return fmt.Errorf("linkedin: failed to scroll element into view: %w", err)
			}
			time.Sleep(200 * time.Millisecond)
			if err := submitBtn.Click(); err != nil {
				return fmt.Errorf("linkedin: failed to click submit: %w", err)
			}
		}
	}

	time.Sleep(3 * time.Second)
	page.Eval(`() => {
		['data-monoagent-comment-input', 'data-monoagent-submit-btn'].forEach(attr => {
			const el = document.querySelector('[' + attr + ']');
			if (el) el.removeAttribute(attr);
		});
	}`)

	return nil
}

// LikeComment likes a specific comment on a LinkedIn post.
// commentID: the URN string from list_post_comments output.
func (b *LinkedInBot) LikeComment(ctx context.Context, page browser.PageInterface, postURL, commentID string) error {
	if postURL == "" {
		return fmt.Errorf("linkedin: postURL is required")
	}
	if commentID == "" {
		return fmt.Errorf("linkedin: commentID is required")
	}

	if err := page.Navigate(postURL); err != nil {
		return fmt.Errorf("linkedin: failed to navigate to post: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("linkedin: post page did not load: %w", err)
	}
	time.Sleep(3 * time.Second)

	res, err := page.Eval(`(id) => {
		const prev = document.querySelector('[data-monoagent-comment-like]');
		if (prev) prev.removeAttribute('data-monoagent-comment-like');

		const commentEl = document.querySelector('[data-id="' + CSS.escape(id) + '"]');
		if (!commentEl) return 'not_found';
		commentEl.scrollIntoView({ behavior: 'smooth', block: 'center' });

		const likeBtn = commentEl.querySelector('button[aria-label*="Like"], button[aria-label*="React"]');
		if (!likeBtn) return 'no_like_btn';

		const label = (likeBtn.getAttribute('aria-label') || '').toLowerCase();
		if (label.includes('remove') || label.includes('unlike')) return 'already_liked';

		likeBtn.setAttribute('data-monoagent-comment-like', 'true');
		return 'marked';
	}`, commentID)
	if err != nil {
		return fmt.Errorf("linkedin: failed to evaluate like comment script: %w", err)
	}

	state := res.Str()
	if state == "already_liked" {
		return nil
	}
	if state != "marked" {
		return fmt.Errorf("linkedin: could not find Like button for comment %s (%s)", commentID, state)
	}

	likeBtn, err := page.Element("[data-monoagent-comment-like='true']", 5*time.Second)
	if err != nil {
		return fmt.Errorf("linkedin: marked comment like button not found: %w", err)
	}
	if err := likeBtn.ScrollIntoView(); err != nil {
		return fmt.Errorf("linkedin: failed to scroll element into view: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := likeBtn.Click(); err != nil {
		return fmt.Errorf("linkedin: failed to click comment Like: %w", err)
	}

	time.Sleep(1 * time.Second)
	page.Eval(`() => {
		const el = document.querySelector('[data-monoagent-comment-like]');
		if (el) el.removeAttribute('data-monoagent-comment-like');
	}`)

	return nil
}
