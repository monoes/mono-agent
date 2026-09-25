//go:build !nosocial

package tiktok

// Read-only TikTok actions: they navigate and scrape, never click anything
// that changes account state. A page script that yields nothing is an error
// (EvalJSON), never "zero items".

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// maxStaleRounds is how many scrolls in a row may add nothing before a list
// is considered complete.
const maxStaleRounds = 3

// collectScrolling runs extract (which returns the full list rendered so far)
// and scroll alternately until max items are present or the list stops
// growing. The first extract error is returned as is.
func collectScrolling(ctx context.Context, max int, extract func() ([]map[string]interface{}, error), scroll func() error) ([]map[string]interface{}, error) {
	var items []map[string]interface{}
	prev, stale := -1, 0
	for {
		got, err := extract()
		if err != nil {
			return nil, err
		}
		items = got
		if max > 0 && len(items) >= max {
			return items[:max], nil
		}
		if len(items) == prev {
			stale++
			if stale >= maxStaleRounds {
				return items, nil
			}
		} else {
			stale = 0
		}
		prev = len(items)
		if err := scroll(); err != nil {
			return items, err
		}
		if err := pause(ctx, 1500*time.Millisecond); err != nil {
			return items, err
		}
	}
}

// scrollWindow scrolls the document to its bottom.
func scrollWindow(page browser.PageInterface) error {
	var ok bool
	return evalJS(page, "", `window.scrollTo(0, document.documentElement.scrollHeight); window.dispatchEvent(new Event('scroll')); return true;`, &ok)
}

// ---------------------------------------------------------------------------
// Profile videos
// ---------------------------------------------------------------------------

const jsUserVideos = jsRealImageSrc + `
const grid = document.querySelector('[data-e2e="user-post-item-list"]');
const items = [...document.querySelectorAll('[data-e2e="user-post-item"]')].map(el => {
  const a = el.querySelector('a[href*="/video/"],a[href*="/photo/"]') || el.querySelector('a[href]');
  const img = el.querySelector('img');
  const views = el.querySelector('[data-e2e="video-views"]');
  const href = a ? a.href : '';
  const m = href.match(/\/(?:video|photo)\/(\d+)/);
  return {
    url: href,
    id: m ? m[1] : '',
    thumbnail: realImageSrc(img),
    description: img ? altCaption(img.getAttribute('alt')) : '',
    views: norm(views && (views.innerText || views.textContent)),
  };
}).filter(v => v.url !== '');
return { grid: !!grid, items };
`

// ListUserVideos navigates to a TikTok profile, scrolls its video grid and
// returns up to maxCount videos ({url, id, thumbnail, description, views}).
// A profile without a video grid (private account, missing profile, page not
// rendered) is an error; a rendered but empty grid is an empty list.
func (b *TikTokBot) ListUserVideos(ctx context.Context, page browser.PageInterface, profileURL string, maxCount int) ([]map[string]interface{}, error) {
	if strings.TrimSpace(profileURL) == "" {
		return nil, fmt.Errorf("tiktok: profileURL is required")
	}
	u, _, err := profileTarget(profileURL)
	if err != nil {
		return nil, err
	}
	if maxCount <= 0 {
		maxCount = 20
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	// Wait for the grid (or its absence) to settle.
	_, _, _ = page.Race([]string{"[data-e2e='user-post-item']", "[data-e2e='user-post-item-list']"}, findTimeout)

	// Make sure the Videos tab (not Reposts/Liked) is the active one.
	var tab struct {
		Found    bool `json:"found"`
		Selected bool `json:"selected"`
	}
	tok := newToken()
	if err := evalJS(page, "token", `
const t = document.querySelector('[data-e2e="videos-tab"]');
if (!t || !visible(t)) return {found: false, selected: false};
mark(t, token);
return {found: true, selected: t.getAttribute('aria-selected') === 'true'};`, &tab, tok); err != nil {
		return nil, fmt.Errorf("tiktok: read profile tabs: %w", err)
	}
	if tab.Found && !tab.Selected {
		if err := clickMarked(page, tok); err != nil {
			return nil, fmt.Errorf("tiktok: open Videos tab: %w", err)
		}
		if err := pause(ctx, 1500*time.Millisecond); err != nil {
			return nil, err
		}
	}

	var sawGrid bool
	videos, err := collectScrolling(ctx, maxCount, func() ([]map[string]interface{}, error) {
		var r struct {
			Grid  bool                     `json:"grid"`
			Items []map[string]interface{} `json:"items"`
		}
		if err := evalJS(page, "", jsUserVideos, &r); err != nil {
			return nil, fmt.Errorf("tiktok: read video grid: %w", err)
		}
		sawGrid = sawGrid || r.Grid || len(r.Items) > 0
		return r.Items, nil
	}, func() error { return scrollWindow(page) })
	if err != nil {
		return nil, err
	}
	if !sawGrid {
		return nil, fmt.Errorf("tiktok: no video grid on %s (private account, unavailable profile, or the page did not render)", u)
	}
	if videos == nil {
		videos = []map[string]interface{}{}
	}
	return videos, nil
}

// ---------------------------------------------------------------------------
// Video comments
// ---------------------------------------------------------------------------

// commentIconSelectors open a video's comment panel.
var commentIconSelectors = []string{
	"[data-e2e='comment-icon']",
	"[data-e2e='browse-comment-icon']",
	"[data-e2e='browse-comment-button']",
}

// openComments makes sure the comment panel of the current video is open:
// comments or the composer are rendered, or the comment button is clicked.
func openComments(ctx context.Context, page browser.PageInterface) error {
	var st struct {
		Open bool `json:"open"`
		Icon bool `json:"icon"`
	}
	tok := newToken()
	if err := evalJS(page, "sels, token", `
const list = document.querySelector('[data-e2e="comment-list"]');
const open = commentItems().length > 0 || (!!list && visible(list));
if (open) return {open: true, icon: false};
const icon = all(sels).map(clickable).find(visible);
if (!icon) return {open: false, icon: false};
mark(icon, token);
return {open: false, icon: true};`, &st, commentIconSelectors, tok); err != nil {
		return fmt.Errorf("tiktok: inspect comment panel: %w", err)
	}
	if st.Open {
		return nil
	}
	if !st.Icon {
		return fmt.Errorf("tiktok: comment button not found (comments may be turned off)")
	}
	if err := clickMarked(page, tok); err != nil {
		return fmt.Errorf("tiktok: open comments: %w", err)
	}
	ok, _ := waitFor(ctx, findTimeout, func() (bool, error) {
		var open bool
		err := evalJS(page, "", `const l = document.querySelector('[data-e2e="comment-list"]'); return commentItems().length > 0 || (!!l && visible(l));`, &open)
		return open, err
	})
	if !ok {
		return fmt.Errorf("tiktok: comment panel did not open")
	}
	return nil
}

// scrollComments scrolls the comment list (or the window) to its end.
func scrollComments(page browser.PageInterface) error {
	var ok bool
	return evalJS(page, "", `
const items = commentItems();
let box = document.querySelector('[data-e2e="comment-list"]');
const scrollable = (e) => e && e.scrollHeight > e.clientHeight + 4 && /(auto|scroll)/.test(getComputedStyle(e).overflowY);
if (!scrollable(box)) { box = null; for (let a = items.length ? items[items.length-1].parentElement : null; a; a = a.parentElement) if (scrollable(a)) { box = a; break; } }
if (box) { box.scrollTop = box.scrollHeight; box.dispatchEvent(new Event('scroll')); }
else { window.scrollTo(0, document.documentElement.scrollHeight); window.dispatchEvent(new Event('scroll')); }
return true;`, &ok)
}

// ListVideoComments opens a video's comments and returns up to maxCount
// top-level comments ({id, username, displayName, text, likes, liked,
// videoUrl}). username/text identify a comment for like_comment; id is only
// set when TikTok exposes one in the DOM.
func (b *TikTokBot) ListVideoComments(ctx context.Context, page browser.PageInterface, videoURL string, maxCount int) ([]map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if maxCount <= 0 {
		maxCount = 50
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	if err := openComments(ctx, page); err != nil {
		return nil, fmt.Errorf("%w on %s", err, u)
	}
	comments, err := collectScrolling(ctx, maxCount, func() ([]map[string]interface{}, error) {
		var items []map[string]interface{}
		if err := evalJS(page, "", `return commentItems().map(commentInfo).filter(c => c.text !== '');`, &items); err != nil {
			return nil, fmt.Errorf("tiktok: read comments: %w", err)
		}
		return items, nil
	}, func() error { return scrollComments(page) })
	if err != nil {
		return nil, err
	}
	for _, c := range comments {
		c["videoUrl"] = u
	}
	if comments == nil {
		comments = []map[string]interface{}{}
	}
	return comments, nil
}

// ---------------------------------------------------------------------------
// Profile data
// ---------------------------------------------------------------------------

const jsProfile = `
const txt = (sels) => { for (const s of sels) { const e = document.querySelector(s); if (e && norm(e.innerText || e.textContent)) return norm(e.innerText || e.textContent); } return ''; };
const titleEl = document.querySelector('[data-e2e="user-title"]');
const subEl = document.querySelector('[data-e2e="user-subtitle"]');
const title = txt(['[data-e2e="user-title"]']);
const subtitle = txt(['[data-e2e="user-subtitle"]']);
if (!title && !subtitle) return {found: false};
const img = document.querySelector('img[data-e2e="user-avatar"], [data-e2e="user-avatar"] img');
const link = document.querySelector('a[data-e2e="user-link"], [data-e2e="user-link"] a, [data-e2e="user-link"]');
// The verified badge carries no data-e2e or label on the current layout: it
// is an svg drawn as a #20D5EC circle next to the name/handle.
const idBox = (() => {
  const a = titleEl || subEl;
  for (let p = a && a.parentElement; p && p !== document.body; p = p.parentElement)
    if ((!titleEl || p.contains(titleEl)) && (!subEl || p.contains(subEl))) return p;
  return null;
})();
const badge = (root) => !!root && [...root.querySelectorAll('svg')].some(v =>
  [...v.querySelectorAll('circle,path,rect')].some(x => /^#?20d5ec$/i.test(x.getAttribute('fill') || '')) ||
  /verified/i.test((v.getAttribute('aria-label') || '') + ' ' + ((v.querySelector('title') || {}).textContent || '')));
return {
  found: true,
  title: title,
  subtitle: subtitle,
  bio: txt(['[data-e2e="user-bio"]']),
  following_count: txt(['[data-e2e="following-count"]']),
  follower_count: txt(['[data-e2e="followers-count"]']),
  likes_count: txt(['[data-e2e="likes-count"]']),
  profile_picture_url: img ? (img.currentSrc || img.src || '') : '',
  website: link ? (link.getAttribute('href') || norm(link.innerText)) : '',
  is_verified: !!document.querySelector('[data-e2e="verify-badge"], [data-e2e="user-verified"]') || badge(idBox),
};
`

// bioPlaceholder is what TikTok shows in user-bio when the account has no
// bio; it is not the account's bio.
var bioPlaceholder = regexp.MustCompile(`(?i)^no bio yet\.?$`)

// profileNames splits the profile header into (handle, display name). On
// tiktok.com user-title is the display name and user-subtitle the @handle.
// The handle in the URL is authoritative: whichever element spells it is the
// handle (the subtitle when both do, i.e. the display name equals the
// handle), the other one the display name.
func profileNames(urlHandle, title, subtitle string) (handle, fullName string) {
	same := func(a, b string) bool { return a != "" && normUser(a) == normUser(b) }
	switch {
	case urlHandle == "":
		return strings.TrimPrefix(subtitle, "@"), title
	case same(subtitle, urlHandle):
		return strings.TrimPrefix(subtitle, "@"), title
	case same(title, urlHandle):
		return strings.TrimPrefix(title, "@"), subtitle
	default:
		// Neither element spells the URL's handle (e.g. the account renamed
		// and TikTok redirected): the layout rule decides.
		return urlHandle, title
	}
}

// GetProfileData scrapes the currently loaded TikTok profile page.
func (b *TikTokBot) GetProfileData(ctx context.Context, page browser.PageInterface) (map[string]interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	_, _, _ = page.Race([]string{"[data-e2e='user-title']", "[data-e2e='user-subtitle']"}, findTimeout)
	var data map[string]interface{}
	if err := evalJS(page, "", jsProfile, &data); err != nil {
		return nil, fmt.Errorf("tiktok: read profile: %w", err)
	}
	if found, _ := data["found"].(bool); !found {
		cur, _ := page.GetURL()
		return nil, fmt.Errorf("tiktok: no profile header on %s", cur)
	}
	delete(data, "found")
	cur, _ := page.GetURL()
	data["profile_url"] = cur
	title, _ := data["title"].(string)
	subtitle, _ := data["subtitle"].(string)
	delete(data, "title")
	delete(data, "subtitle")
	username := b.ExtractUsername(cur)
	handle, fullName := profileNames(username, title, subtitle)
	if handle == "" {
		handle = username
	}
	if username == "" {
		username = handle
	}
	data["handle"], data["full_name"], data["username"] = handle, fullName, username
	if bio, _ := data["bio"].(string); bioPlaceholder.MatchString(strings.TrimSpace(bio)) {
		data["bio"] = ""
	}
	return data, nil
}

// getProfileData navigates to target (profile URL or handle) when given and
// scrapes it.
func (b *TikTokBot) getProfileData(ctx context.Context, page browser.PageInterface, target string) (map[string]interface{}, error) {
	if strings.TrimSpace(target) != "" {
		u, _, err := profileTarget(target)
		if err != nil {
			return nil, err
		}
		if err := open(ctx, page, u); err != nil {
			return nil, err
		}
	}
	return b.GetProfileData(ctx, page)
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

const jsSearchVideos = jsRealImageSrc + `
const cardSel = '[data-e2e="search_video-item"],[data-e2e="search_top-item"],[data-e2e="search-card-item"]';
const out = [], seen = new Set();
for (const a of document.querySelectorAll('a[href*="/video/"]')) {
  const card = a.closest(cardSel);
  if (!card) continue;
  const href = a.href.split('?')[0];
  if (seen.has(href)) continue;
  seen.add(href);
  const m = href.match(/\/video\/(\d+)/);
  // The caption and author sit next to the card, in the wrapper around it
  // (used only while that wrapper holds this one video).
  const wrap = card.parentElement && new Set([...card.parentElement.querySelectorAll('a[href*="/video/"]')].map(x => x.href.split('?')[0])).size === 1 ? card.parentElement : card;
  const pick = (sel) => card.querySelector(sel) || wrap.querySelector(sel);
  // search-card-desc also holds the author line; only its caption counts.
  let desc = pick('[data-e2e="search-card-video-caption"]');
  if (!desc && (desc = pick('[data-e2e="search-card-desc"]'))) {
    desc = desc.cloneNode(true);
    desc.querySelectorAll('[data-e2e="search-card-info-container"],[data-e2e*="user"],[data-e2e*="like"],a[href*="/@"]').forEach(e => e.remove());
  }
  const author = pick('[data-e2e="search-card-user-unique-id"]');
  const authorText = norm(author && (author.innerText || author.textContent));
  // The video URL (/@handle/video/id) is the authoritative handle; the card's
  // "unique id" element shows the display name on the current layout.
  const handle = userFromHref(href);
  const img = card.querySelector('img');
  out.push({
    url: href,
    id: m ? m[1] : '',
    author: handle || authorText,
    author_name: authorText && authorText !== handle ? authorText : '',
    description: norm(desc && (desc.innerText || desc.textContent)) || (img ? altCaption(img.getAttribute('alt')) : ''),
    thumbnail: realImageSrc(img),
  });
}
return out;
`

// SearchVideos runs a TikTok video search and returns up to maxCount results
// ({url, id, author, description, thumbnail}). No result cards is an error:
// a search page that never rendered must not look like "no matches".
func (b *TikTokBot) SearchVideos(ctx context.Context, page browser.PageInterface, keyword string, maxCount int) ([]map[string]interface{}, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("tiktok: keyword is required")
	}
	if maxCount <= 0 {
		maxCount = 20
	}
	u := "https://www.tiktok.com/search/video?q=" + url.QueryEscape(keyword)
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	_, _, _ = page.Race([]string{"[data-e2e='search_video-item']", "[data-e2e='search_top-item']", "[data-e2e='search-card-item']"}, findTimeout)
	res, err := collectScrolling(ctx, maxCount, func() ([]map[string]interface{}, error) {
		var items []map[string]interface{}
		if err := evalJS(page, "", jsSearchVideos, &items); err != nil {
			return nil, fmt.Errorf("tiktok: read search results: %w", err)
		}
		return items, nil
	}, func() error { return scrollWindow(page) })
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("tiktok: no video results for %q (or the search page did not render)", keyword)
	}
	for _, r := range res {
		r["keyword"] = keyword
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Followers / following
// ---------------------------------------------------------------------------

const jsFollowList = `
const pop = document.querySelector('[data-e2e="follow-info-popup"]') || [...document.querySelectorAll('[role="dialog"]')].find(visible);
if (!pop) return {open: false, items: []};
// rowOf is the widest ancestor of a (inside the popup) that still names only
// this one account: the avatar link, the texts and the follow button.
const usersIn = (e) => new Set([...e.querySelectorAll('a[href*="/@"]')].map(x => userFromHref(x.getAttribute('href'))).filter(Boolean));
const rowOf = (a, user) => {
  let row = a.closest('li,[data-e2e="follow-info-item"]');
  if (row && pop.contains(row) && usersIn(row).size === 1) return row;
  row = a;
  for (let p = a.parentElement; p && p !== pop && pop.contains(p); p = p.parentElement) {
    const us = usersIn(p);
    if (us.size !== 1 || !us.has(user)) break;
    row = p;
  }
  return row;
};
// A button's label or a relationship word is never a display name.
const NOT_NAME = /^(follow|following|follow back|friends|remove|message|unfollow|requested)$/i;
const nickOf = (row, user) => {
  const hint = row.querySelector('[data-e2e="follow-info-nickname"],[class*="PNickname"],[class*="Nickname"]');
  if (hint && norm(hint.innerText || hint.textContent)) return norm(hint.innerText || hint.textContent);
  // Otherwise the first text leaf that is neither the @handle nor a button.
  const handle = user.toLowerCase();
  for (const e of row.querySelectorAll('*')) {
    if (e.children.length || e.closest('button,[role="button"]')) continue;
    const t = norm(e.innerText || e.textContent);
    if (!t || NOT_NAME.test(t) || t.replace(/^@/, '').toLowerCase() === handle) continue;
    return t;
  }
  return '';
};
// stateOf is the row's follow-state button text as TikTok prints it
// ("Follow", "Follow back", "Following", "Friends", "Requested"; live rows
// label it "<state> <nickname>").
const stateOf = (row) => {
  const b = row.querySelector('[data-e2e="follow-button"]') || row.querySelector('button');
  return b ? norm(b.innerText || b.textContent) : '';
};
const out = [], seen = new Set();
for (const a of pop.querySelectorAll('a[href*="/@"]')) {
  const user = userFromHref(a.getAttribute('href'));
  if (!user || seen.has(user)) continue;
  seen.add(user);
  const row = rowOf(a, user);
  out.push({ username: user, url: 'https://www.tiktok.com/@' + encodeURIComponent(user), displayName: nickOf(row, user), followState: stateOf(row) });
}
return {open: true, items: out};
`

// ListFollowers opens a profile's followers (sourceType FOLLOWERS*) or
// following (FOLLOWING*) list and returns up to maxCount accounts
// ({username, url, displayName, followState}); followState is the viewer's
// relationship button as TikTok labels it ("Follow back", "Following",
// "Friends", …), "" when the row has none.
func (b *TikTokBot) ListFollowers(ctx context.Context, page browser.PageInterface, profile, sourceType string, maxCount int) ([]map[string]interface{}, error) {
	u, _, err := profileTarget(profile)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(sourceType)
	var countSel string
	switch {
	case strings.HasPrefix(kind, "following"):
		countSel, kind = "[data-e2e='following-count']", "following"
	case kind == "" || strings.HasPrefix(kind, "follower"):
		countSel, kind = "[data-e2e='followers-count']", "followers"
	default:
		return nil, fmt.Errorf("tiktok: unknown sourceType %q (want FOLLOWERS_FETCH or FOLLOWING_FETCH)", sourceType)
	}
	if maxCount <= 0 {
		maxCount = 50
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	el, err := page.Element(countSel, findTimeout)
	if err != nil || el == nil {
		return nil, fmt.Errorf("tiktok: %s count not found on %s", kind, u)
	}
	if err := botpkg.ClickTrusted(page, el); err != nil {
		return nil, fmt.Errorf("tiktok: open %s list: %w", kind, err)
	}
	var st struct {
		Open  bool                     `json:"open"`
		Items []map[string]interface{} `json:"items"`
	}
	ok, _ := waitFor(ctx, findTimeout, func() (bool, error) {
		err := evalJS(page, "", jsFollowList, &st)
		return st.Open && len(st.Items) > 0, err
	})
	if !ok {
		if st.Open {
			return nil, fmt.Errorf("tiktok: the %s list of %s is empty or private", kind, u)
		}
		return nil, fmt.Errorf("tiktok: the %s list of %s did not open", kind, u)
	}
	return collectScrolling(ctx, maxCount, func() ([]map[string]interface{}, error) {
		if err := evalJS(page, "", jsFollowList, &st); err != nil {
			return nil, fmt.Errorf("tiktok: read %s list: %w", kind, err)
		}
		for _, it := range st.Items {
			it["source"] = kind
			it["profileUrl"] = u
		}
		return st.Items, nil
	}, func() error {
		var ok bool
		return evalJS(page, "", `
const pop = document.querySelector('[data-e2e="follow-info-popup"]') || [...document.querySelectorAll('[role="dialog"]')].find(visible);
if (!pop) return false;
const box = [pop, ...pop.querySelectorAll('*')].find(e => e.scrollHeight > e.clientHeight + 4 && /(auto|scroll)/.test(getComputedStyle(e).overflowY));
if (box) { box.scrollTop = box.scrollHeight; box.dispatchEvent(new Event('scroll')); }
return true;`, &ok)
	})
}

// jsRealImageSrc defines realImageSrc(img): the image's real URL, skipping
// lazy-load placeholders (data: URIs such as the 1×1 transparent GIF TikTok
// renders before the image loads) in favour of data-src/srcset; "" when only
// a placeholder is available.
const jsRealImageSrc = `
function realImageSrc(img) {
  if (!img) return '';
  const cands = [img.currentSrc, img.src, img.getAttribute('data-src'),
    (img.getAttribute('srcset') || '').split(',')[0].trim().split(' ')[0]];
  for (const c of cands) if (c && !c.startsWith('data:') && !c.startsWith('blob:')) return c;
  return '';
}
`
