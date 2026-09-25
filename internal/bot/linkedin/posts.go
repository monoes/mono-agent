//go:build social

package linkedin

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/monoes/mono-agent/internal/browser"
)

// reLinkedInActivity matches the numeric id of a post in LinkedIn URLs and
// URNs: "activity-7123…", "activity:7123…", "ugcPost:7123…", "share:7123…".
var reLinkedInActivity = regexp.MustCompile(`(?:activity|ugcPost|share)[-:](\d{6,})`)

// postURL is the canonical permalink of a post URN.
func postURL(urn string) string {
	return "https://www.linkedin.com/feed/update/" + urn + "/"
}

// collectPostsJS returns every post rendered on the page, in document order,
// as {urn, id, author, authorUrl, text, timestamp, likes, comments, reposts}.
// A post is found by its URN: the classic markup puts it in data-urn
// (div.feed-shared-update-v2), the server-driven markup only in permalinks
// (a[href*="/feed/update/urn:li:activity:"]). Comments and reshared inner
// posts are not separate results.
const collectPostsJS = `() => {
	const re = /urn:li:(activity|ugcPost|share):(\d{6,})/;
	const out = [];
	const seen = new Set();
	const commentSel = 'article.comments-comment-entity, article.comments-comment-item, .comments-comments-list, [data-testid*="commentList"]';
	const rootSel = '[data-urn], [role="listitem"], [data-testid="carousel-child-container"], li, article';
	const add = (urn, id, root) => {
		if (seen.has(id)) return;
		seen.add(id);
		const q = (sel) => root ? root.querySelector(sel) : null;
		let author = '', authorUrl = '';
		const nameEl = q('.update-components-actor__title span[aria-hidden="true"], .update-components-actor__title, .update-components-actor__name');
		if (nameEl) author = L.lines(nameEl)[0] || '';
		const authorLink = q('a.update-components-actor__meta-link, a.update-components-actor__image, a[href*="/in/"], a[href*="/company/"]');
		if (authorLink) authorUrl = authorLink.href.split('?')[0];
		if (!author && authorLink) {
			const lbl = (authorLink.getAttribute('aria-label') || '').replace(/^View:?\s*/, '').replace(/[’']s\s+profile.*$/, '').replace(/,\s*graphic\.?$/, '');
			author = lbl || L.lines(authorLink)[0] || '';
		}
		const textEl = q('.update-components-text, .feed-shared-update-v2__description, [data-testid="expandable-text-box"], span.break-words');
		const timeEl = q('time, .update-components-actor__sub-description');
		let timestamp = '';
		if (timeEl) timestamp = timeEl.getAttribute('datetime') || (L.text(timeEl).split('•')[0] || '').trim();
		const likesEl = q('.social-details-social-counts__reactions-count, button[aria-label$=" reactions"], button[aria-label$=" reaction"]');
		const commentsEl = q('button[aria-label*=" comment"], .social-details-social-counts__comments');
		const repostsEl = q('button[aria-label*=" repost"]');
		out.push({
			urn, id, author, authorUrl,
			text: textEl ? L.text(textEl).slice(0, 500) : '',
			timestamp,
			likes: likesEl ? L.num(likesEl.getAttribute('aria-label') || L.text(likesEl)) : 0,
			comments: commentsEl ? L.num(commentsEl.getAttribute('aria-label') || L.text(commentsEl)) : 0,
			reposts: repostsEl ? L.num(repostsEl.getAttribute('aria-label') || L.text(repostsEl)) : 0,
		});
	};
	const outermost = (el) => {
		let cur = el, up;
		while ((up = cur.parentElement && cur.parentElement.closest('[data-urn*="urn:li:activity:"], [data-urn*="urn:li:ugcPost:"], [data-urn*="urn:li:share:"]'))) cur = up;
		return cur;
	};
	const candidates = [];
	document.querySelectorAll('[data-urn], [data-chameleon-result-urn], a[href*="/feed/update/urn:li:"], a[href*="-activity-"]').forEach((el) => {
		if (el.closest(commentSel)) return;
		const raw = el.getAttribute('data-urn') || el.getAttribute('data-chameleon-result-urn') || el.getAttribute('href') || '';
		const m = raw.match(re) || raw.match(/-(activity)-(\d{6,})/);
		if (!m) return;
		const urn = 'urn:li:' + m[1] + ':' + m[2];
		let root;
		if (el.tagName === 'A') {
			root = el.closest('[data-urn]') || el.closest(rootSel);
		} else {
			root = outermost(el);
		}
		candidates.push({ el: root || el, urn, id: m[2] });
	});
	candidates.sort((a, b) => (a.el === b.el ? 0 : (a.el.compareDocumentPosition(b.el) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1)));
	for (const c of candidates) {
		const urn = (c.el.getAttribute && c.el.getAttribute('data-urn')) || c.urn;
		const m = urn.match(re);
		add(m ? m[0] : c.urn, m ? m[2] : c.id, c.el);
	}
	return out;
}`

type rawPost struct {
	URN       string `json:"urn"`
	ID        string `json:"id"`
	Author    string `json:"author"`
	AuthorURL string `json:"authorUrl"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	Likes     int    `json:"likes"`
	Comments  int    `json:"comments"`
	Reposts   int    `json:"reposts"`
}

func (r rawPost) item() map[string]interface{} {
	return map[string]interface{}{
		"url":            postURL(r.URN),
		"urn":            r.URN,
		"activity_id":    r.ID,
		"shortcode":      r.ID,
		"text_preview":   r.Text,
		"author":         r.Author,
		"author_url":     r.AuthorURL,
		"timestamp":      r.Timestamp,
		"likes_count":    r.Likes,
		"comments_count": r.Comments,
		"reposts_count":  r.Reposts,
	}
}

// scrollCollectPosts gathers up to max posts from the current page,
// scrolling for more until three scrolls in a row add nothing. It clicks a
// "show more results" button when the list ends in one.
func scrollCollectPosts(ctx context.Context, page browser.PageInterface, max int) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	seen := map[string]bool{}
	stale := 0
	var lastErr error
	for len(out) < max && stale < 3 {
		var raw []rawPost
		if err := run(page, collectPostsJS, &raw); err != nil {
			lastErr = err
			stale++
		} else {
			added := 0
			for _, r := range raw {
				if r.ID == "" || seen[r.ID] {
					continue
				}
				seen[r.ID] = true
				out = append(out, r.item())
				added++
				if len(out) >= max {
					break
				}
			}
			if added == 0 {
				stale++
			} else {
				stale = 0
			}
		}
		if len(out) >= max {
			break
		}
		clickShowMore(page)
		scrollPage(page)
		if err := sleepCtx(ctx, scrollSettle); err != nil {
			return out, err
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, fmt.Errorf("linkedin: reading posts: %w", lastErr)
	}
	return out, nil
}

// clickShowMore clicks a visible end-of-list "show more" button, if any.
func clickShowMore(page browser.PageInterface) {
	tok := newToken("more")
	var found bool
	if err := run(page, `(tok) => {
		const btn = Array.from(document.querySelectorAll('button.scaffold-finite-scroll__load-button, .scaffold-finite-scroll button.artdeco-button--full, main button.artdeco-button--secondary.artdeco-button--full'))
			.find((b) => L.vis(b) && !b.disabled);
		if (!btn) return false;
		L.mark(btn, tok);
		return true;
	}`, &found, tok); err != nil || !found {
		return
	}
	_ = clickMarked(page, tok)
	unmark(page, tok)
}

// ListUserPosts scrapes posts from a LinkedIn member's recent activity or a
// company's posts page.
// profileURL: https://www.linkedin.com/in/<slug>/ or /company/<slug>/
// maxCount: max number of posts (default 20)
// activityType: "shares" | "all" (members only)
func (b *LinkedInBot) ListUserPosts(ctx context.Context, page browser.PageInterface, profileURL string, maxCount int, activityType string) ([]map[string]interface{}, error) {
	if strings.TrimSpace(profileURL) == "" {
		return nil, fmt.Errorf("linkedin: profileURL is required")
	}
	if maxCount <= 0 {
		maxCount = 20
	}
	feedURL, err := activityFeedURL(b.ResolveURL(strings.TrimSpace(profileURL)), activityType)
	if err != nil {
		return nil, err
	}
	if err := navigate(ctx, page, feedURL); err != nil {
		return nil, err
	}
	// Wait for the first post (or give up and report an empty feed).
	_ = waitFor(ctx, page, findTimeout, `() => ({ ok: !!document.querySelector('[data-urn*="urn:li:activity:"], [data-urn*="urn:li:ugcPost:"], a[href*="/feed/update/urn:li:"]') })`, nil)
	return scrollCollectPosts(ctx, page, maxCount)
}

// activityFeedURL maps a profile or company URL to its posts feed.
func activityFeedURL(profileURL, activityType string) (string, error) {
	u, err := url.Parse(profileURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("linkedin: invalid profile URL %q", profileURL)
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 {
		return "", fmt.Errorf("linkedin: %q is not a profile (/in/<name>) or company (/company/<name>) URL", profileURL)
	}
	base := "https://www.linkedin.com/" + segs[0] + "/" + segs[1] + "/"
	switch segs[0] {
	case "company", "showcase", "school":
		return base + "posts/?feedView=all", nil
	case "in":
		if strings.EqualFold(strings.TrimSpace(activityType), "shares") {
			return base + "recent-activity/shares/", nil
		}
		return base + "recent-activity/all/", nil
	}
	return "", fmt.Errorf("linkedin: %q is not a profile (/in/<name>) or company (/company/<name>) URL", profileURL)
}

// SearchPosts runs a LinkedIn content search and returns up to max posts
// (url, urn, author, text_preview, …). A keyword that is already a
// linkedin.com/search/results/content URL is used as is.
func (b *LinkedInBot) SearchPosts(ctx context.Context, page browser.PageInterface, keyword string, max int) ([]map[string]interface{}, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("linkedin: keyword is required")
	}
	if max <= 0 {
		max = 10
	}
	target := keyword
	if !strings.Contains(keyword, "linkedin.com/search/results/content") {
		target = "https://www.linkedin.com/search/results/content/?keywords=" + url.QueryEscape(keyword) + "&origin=GLOBAL_SEARCH_HEADER"
	}
	if err := navigate(ctx, page, target); err != nil {
		return nil, err
	}
	_ = waitFor(ctx, page, findTimeout, `() => ({ ok: !!document.querySelector('[data-urn*="urn:li:activity:"], [data-urn*="urn:li:ugcPost:"], [data-chameleon-result-urn], a[href*="/feed/update/urn:li:"]') })`, nil)
	posts, err := scrollCollectPosts(ctx, page, max)
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, fmt.Errorf("linkedin: no posts found for %q (the page rendered no post permalinks)", keyword)
	}
	return posts, nil
}

// commentsJS reads every rendered comment of the post page as
// {id, author, authorUrl, headline, text, timestamp, likes, replies,
// parentId, liked}.
const commentsJS = `() => {
	const artSel = 'article.comments-comment-entity, article.comments-comment-item';
	const own = (el, sel) => Array.from(el.querySelectorAll(sel)).find((x) => x.closest(artSel) === el) || null;
	return Array.from(document.querySelectorAll(artSel)).map((el) => {
		const parent = el.parentElement ? el.parentElement.closest(artSel) : null;
		const nameEl = own(el, '.comments-comment-meta__description-title, .comments-post-meta__name-text, .comments-comment-item__post-meta a span[aria-hidden="true"]');
		const linkEl = own(el, 'a.comments-comment-meta__description-container, a.comments-comment-meta__image-link, a.comments-post-meta__actor-link, a[href*="/in/"], a[href*="/company/"]');
		const headEl = own(el, '.comments-comment-meta__description-subtitle, .comments-post-meta__headline');
		const textEl = own(el, '.comments-comment-item__main-content, .comments-comment-entity__content .update-components-text, .comments-comment-item-content-body');
		const timeEl = own(el, 'time');
		const likesEl = own(el, 'button.comments-comment-social-bar__reactions-count--cr, button.comments-comment-social-bar__reactions-count, button[aria-label*="Reactions on"]');
		const repliesEl = own(el, '.comments-comment-social-bar__replies-count--cr, .comments-comment-social-bar__replies-count');
		const likeBtn = own(el, '.reactions-react-button button:not(.reactions-menu__trigger), button.comments-comment-social-bar__reaction-action-button, button[aria-label^="React Like"]');
		return {
			id: el.getAttribute('data-id') || '',
			author: nameEl ? L.lines(nameEl)[0] || '' : '',
			authorUrl: linkEl ? linkEl.href.split('?')[0] : '',
			headline: headEl ? L.text(headEl) : '',
			text: textEl ? L.text(textEl) : '',
			timestamp: timeEl ? (timeEl.getAttribute('datetime') || L.text(timeEl)) : '',
			likes: likesEl ? L.num(likesEl.getAttribute('aria-label') || L.text(likesEl)) : 0,
			replies: repliesEl ? L.num(L.text(repliesEl)) : 0,
			parentId: parent ? (parent.getAttribute('data-id') || '') : '',
			liked: !!likeBtn && likeBtn.getAttribute('aria-pressed') === 'true',
		};
	}).filter((c) => c.id);
}`

type rawComment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	AuthorURL string `json:"authorUrl"`
	Headline  string `json:"headline"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	Likes     int    `json:"likes"`
	Replies   int    `json:"replies"`
	ParentID  string `json:"parentId"`
	Liked     bool   `json:"liked"`
}

// clickLoadMoreJS marks the first visible "load more comments" (kind
// "comments") or "load previous replies" (kind "replies") button. Buttons
// are recognised by class, not by their (localised) text.
const clickLoadMoreJS = `(kind, tok) => {
	const sels = kind === 'replies'
		? ['.comments-replies-list button.show-prev-replies', '.comments-replies-list button[class*="load-previous"]', '.comments-replies-list button[class*="replies-list__load"]', 'button.comments-comment-item__replies-load-more', 'button[class*="show-previous-replies"]']
		: ['button.comments-comments-list__load-more-comments-button--cr', 'button.comments-comments-list__load-more-comments-button', '.comments-comment-list__load-more-container button', 'button.comments-comments-list__show-previous-button'];
	for (const sel of sels) {
		const b = Array.from(document.querySelectorAll(sel)).find((x) => L.vis(x) && !x.disabled);
		if (b) { L.mark(b, tok); return true; }
	}
	return false;
}`

// expandComments clicks "load more comments" (and, with replies, every
// "load previous replies") until nothing is left or the limits are hit.
func expandComments(ctx context.Context, page browser.PageInterface, want int, replies bool) {
	count := func() int {
		var n int
		_ = run(page, `() => document.querySelectorAll('article.comments-comment-entity, article.comments-comment-item').length`, &n)
		return n
	}
	kinds := []string{"comments"}
	if replies {
		kinds = append(kinds, "replies")
	}
	for _, kind := range kinds {
		for i := 0; i < 20; i++ {
			if kind == "comments" && want > 0 && count() >= want {
				break
			}
			tok := newToken("load")
			var found bool
			if err := run(page, clickLoadMoreJS, &found, kind, tok); err != nil || !found {
				break
			}
			before := count()
			_ = clickMarked(page, tok)
			unmark(page, tok)
			_ = poll(ctx, 4*scrollSettle, func() (bool, error) { return count() > before, nil })
		}
	}
}

// ListPostComments scrapes comments (and optionally replies) of a post.
func (b *LinkedInBot) ListPostComments(ctx context.Context, page browser.PageInterface, postURL string, maxCount int, includeReplies bool) ([]map[string]interface{}, error) {
	if strings.TrimSpace(postURL) == "" {
		return nil, fmt.Errorf("linkedin: postURL is required")
	}
	if maxCount <= 0 {
		maxCount = 50
	}
	if err := navigate(ctx, page, b.ResolveURL(strings.TrimSpace(postURL))); err != nil {
		return nil, err
	}
	if err := waitForPost(ctx, page); err != nil {
		return nil, err
	}
	// Comments render lazily after the post itself.
	_ = waitFor(ctx, page, verifyTimeout/2, `() => ({ ok: !!document.querySelector('article.comments-comment-entity, article.comments-comment-item') })`, nil)
	expandComments(ctx, page, maxCount, includeReplies)

	var raw []rawComment
	if err := run(page, commentsJS, &raw); err != nil {
		return nil, fmt.Errorf("linkedin: reading comments: %w", err)
	}
	result := []map[string]interface{}{}
	for _, c := range raw {
		if !includeReplies && c.ParentID != "" {
			continue
		}
		if len(result) >= maxCount {
			break
		}
		var parent interface{}
		if c.ParentID != "" {
			parent = c.ParentID
		}
		result = append(result, map[string]interface{}{
			"id":          c.ID,
			"post_url":    postURL,
			"author":      c.Author,
			"author_url":  c.AuthorURL,
			"headline":    c.Headline,
			"text":        c.Text,
			"timestamp":   c.Timestamp,
			"likes_count": c.Likes,
			"reply_count": c.Replies,
			"parent_id":   parent,
			"is_reply":    c.ParentID != "",
			"liked":       c.Liked,
		})
	}
	return result, nil
}

// waitForPost waits until a post has rendered on a single-post page.
func waitForPost(ctx context.Context, page browser.PageInterface) error {
	err := waitFor(ctx, page, findTimeout, `() => ({ ok: !!document.querySelector('[data-urn*="urn:li:activity:"], [data-urn*="urn:li:ugcPost:"], [data-urn*="urn:li:share:"], div.feed-shared-update-v2, [role="article"], [componentkey^="update-card"]') })`, nil)
	if err != nil {
		return fmt.Errorf("linkedin: no post rendered on the page: %w", err)
	}
	return nil
}
