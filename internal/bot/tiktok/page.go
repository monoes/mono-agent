//go:build !nosocial

package tiktok

// Page plumbing shared by the TikTok bot methods. Everything here runs on
// browser.PageInterface (in production *extension.ExtensionPage): page-side
// JavaScript goes through botpkg.EvalJSON (EvalCDP, so TikTok's CSP cannot
// block it and an empty result is an error), clicks through
// botpkg.ClickTrusted (real CDP mouse input, which TikTok's React handlers
// require) and typing through botpkg.TypeInto (CDP Input.insertText, which
// DraftJS/Lexical composers accept).
//
// Elements the page-side finders choose are tagged with
// data-monoagent-tt="<token>" and then resolved through page.Element, so the
// Go side only ever clicks the element the finder vetted.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// Timing knobs; tests shrink them.
var (
	// settleScale multiplies every fixed pause (page settle, lazy loading).
	settleScale = 1.0
	// verifyTimeout bounds how long a write action waits for its outcome to
	// become observable before reporting failure.
	verifyTimeout = 10 * time.Second
	// findTimeout bounds how long finders wait for a control to render.
	findTimeout = 10 * time.Second
	// pollInterval is the polling period of the wait loops.
	pollInterval = 250 * time.Millisecond
)

// markAttr is the attribute the page-side finders tag chosen elements with.
const markAttr = "data-monoagent-tt"

func markSelector(token string) string {
	return fmt.Sprintf("[%s='%s']", markAttr, token)
}

func newToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// pause sleeps d (scaled by settleScale) or until ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	d = time.Duration(float64(d) * settleScale)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// open navigates to u and waits for the document to load.
func open(ctx context.Context, page browser.PageInterface, u string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := page.Navigate(u); err != nil {
		return fmt.Errorf("tiktok: navigate to %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("tiktok: %s did not load: %w", u, err)
	}
	return pause(ctx, 2*time.Second)
}

// openQuiet is open for the read actions: right after the document loads
// it pauses and mutes every <video> and keeps them paused (holdVideos), so
// reading a page does not keep playing, and counting, the videos on it. The
// page load itself may still register a view, and a video can start before
// the hold is in place. Call release when done with the page.
func openQuiet(ctx context.Context, page browser.PageInterface, u string) (release func(), err error) {
	release = func() {}
	if err := ctx.Err(); err != nil {
		return release, err
	}
	if err := page.Navigate(u); err != nil {
		return release, fmt.Errorf("tiktok: navigate to %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return release, fmt.Errorf("tiktok: %s did not load: %w", u, err)
	}
	release = holdVideos(page)
	if err := pause(ctx, 2*time.Second); err != nil {
		release()
		return func() {}, err
	}
	return release, nil
}

// holdVideosJS pauses and mutes every video now, and every video added or
// started later (a MutationObserver plus a capturing play listener), until
// window.__monoagentHold.stop() removes both.
const holdVideosJS = `() => {
	if (window.__monoagentHold) { window.__monoagentHold.apply(); return true; }
	const quiet = (v) => { try { v.muted = true; v.autoplay = false; v.removeAttribute('autoplay'); v.pause(); } catch (e) {} };
	const apply = () => document.querySelectorAll('video').forEach(quiet);
	const onPlay = (e) => { if (e.target && e.target.tagName === 'VIDEO') quiet(e.target); };
	document.addEventListener('play', onPlay, true);
	document.addEventListener('playing', onPlay, true);
	const mo = new MutationObserver(apply);
	mo.observe(document.documentElement, { childList: true, subtree: true });
	apply();
	window.__monoagentHold = { apply, stop: () => {
		mo.disconnect();
		document.removeEventListener('play', onPlay, true);
		document.removeEventListener('playing', onPlay, true);
		delete window.__monoagentHold;
	} };
	return true;
}`

// holdVideos starts the video hold on the current page and returns the
// function that stops it. Failures are ignored: a page the hold cannot run
// on is still read.
func holdVideos(page browser.PageInterface) (release func()) {
	var ok bool
	_ = botpkg.EvalJSON(page, holdVideosJS, &ok)
	return func() {
		var done bool
		_ = botpkg.EvalJSON(page, `() => { if (window.__monoagentHold) window.__monoagentHold.stop(); return true; }`, &done)
	}
}

// evalJS runs fnSrc with the shared helper prelude in scope. fnSrc is the
// BODY of a function whose parameters are named by params, e.g.
// evalJS(p, "sel", "return document.querySelectorAll(sel).length", &n, sel).
func evalJS(page browser.PageInterface, params, body string, out interface{}, args ...interface{}) error {
	src := "(" + params + ") => {\n" + jsPrelude + "\n" + body + "\n}"
	return botpkg.EvalJSON(page, src, out, args...)
}

// jsPrelude defines helpers available to every evalJS body.
const jsPrelude = `
const MARK = 'data-monoagent-tt';
const norm = (s) => (s || '').replace(/\s+/g, ' ').trim();
const visible = (e) => {
  if (!e || !e.isConnected) return false;
  const st = getComputedStyle(e);
  if (st.display === 'none' || st.visibility === 'hidden') return false;
  const r = e.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
};
const all = (sels, root) => {
  const out = [];
  for (const s of sels) for (const e of (root || document).querySelectorAll(s)) if (!out.includes(e)) out.push(e);
  return out;
};
const clickable = (e) => (e && e.closest('button,[role="button"]')) || e;
const mark = (e, token) => {
  document.querySelectorAll('[' + MARK + '="' + token + '"]').forEach(x => x.removeAttribute(MARK));
  e.setAttribute(MARK, token);
  return true;
};
const marked = (token) => document.querySelector('[' + MARK + '="' + token + '"]');
const pressedOf = (e) => {
  if (!e) return null;
  const btn = clickable(e);
  for (const x of [btn, e, ...e.querySelectorAll('[aria-pressed]')]) {
    const v = x && x.getAttribute && x.getAttribute('aria-pressed');
    if (v === 'true') return true;
    if (v === 'false') return false;
  }
  return null;
};
const disabledOf = (e) => !!e && (e.disabled === true || e.getAttribute('aria-disabled') === 'true' || e.hasAttribute('disabled'));
const textOf = (e) => !e ? '' : norm(/^(INPUT|TEXTAREA)$/.test(e.tagName) ? e.value : (e.innerText || e.textContent));
// altCaption turns a video thumbnail's alt text into its caption: TikTok
// writes the alt as "<caption> created by <name> with <sound>", so the part
// from the last " created by " (followed by a " with ") on is dropped. An alt
// without that suffix is returned as is. Only the English wording is known.
const altCaption = (alt) => {
  const s = norm(alt), i = s.toLowerCase().lastIndexOf(' created by ');
  if (i < 0 && /^created by .+ with /i.test(s)) return '';
  if (i < 0 || s.toLowerCase().indexOf(' with ', i + 12) < 0) return s;
  return s.slice(0, i).trim();
};
const userFromHref = (h) => { const m = (h || '').match(/\/@([^/?#]+)/); return m ? decodeURIComponent(m[1]) : ''; };
// Comments: TikTok renders either explicit comment-item containers or
// (current layout) a comment-level-1 text node whose nearest ancestor holding
// the author link is the comment; an ancestor holding a second comment text
// belongs to a thread, not to one comment.
const C_USER = '[data-e2e="comment-username"],[data-e2e="comment-username-1"]';
// data-key-interaction="comment_like" is the live like control (2026-09).
const C_LIKE = '[data-e2e="comment-like-icon"],[data-e2e="comment-like-btn"],[data-e2e="comment-like"],[data-key-interaction="comment_like"]';
const commentItems = () => {
  const explicit = [...document.querySelectorAll('[data-e2e="comment-item"]')];
  if (explicit.length) return explicit;
  const out = [];
  for (const t of document.querySelectorAll('[data-e2e="comment-level-1"]')) {
    let item = null;
    for (let a = t.parentElement; a && a !== document.body; a = a.parentElement) {
      if (a.querySelectorAll('[data-e2e^="comment-level-"]').length > 1) break;
      if (a.querySelector(C_USER) || a.querySelector('a[href*="/@"]')) {
        item = a;
        if (a.querySelector(C_LIKE) || a.querySelector(C_LIKE_LOOSE)) break;
      }
    }
    if (item && !out.includes(item)) out.push(item);
  }
  return out;
};
// The like control: a known data-e2e first, then (current layout, no
// data-e2e) a button labelled "like" or a *Like* wrapper. Nothing inside the
// comment's text, author or time counts.
const C_LIKE_LOOSE = '[role="button"][aria-label*="like" i],button[aria-label*="like" i],[aria-label*="like" i],[class*="LikeWrapper"],[class*="LikeContainer"],[class*="LikeIcon"]';
const C_TEXT = '[data-e2e="comment-content"],[data-e2e^="comment-level-"],[data-e2e^="comment-username"],[data-e2e^="comment-time"],[data-e2e^="comment-reply"]';
const inReplies = (item, e) => { const lv = e.closest('[data-e2e^="comment-level-"]'); return !!lv && item.contains(lv) && lv !== item.querySelector('[data-e2e^="comment-level-"]'); };
const ownOf = (item, e) => item.contains(e) && !e.closest(C_TEXT) && !inReplies(item, e);
const commentLike = (item) => {
  let icon = [...item.querySelectorAll(C_LIKE)].find(e => ownOf(item, e));
  if (!icon) icon = [...item.querySelectorAll(C_LIKE_LOOSE)].find(e => ownOf(item, e) && !/reply|dislike/i.test(e.getAttribute('aria-label') || ''));
  if (!icon) return null;
  const b = clickable(icon);
  return item.contains(b) ? b : icon;
};
// A count as TikTok prints it: 0, 12, 1,204, 3.4K, 1.2M.
const COUNT = /^\d[\d.,]*\s*[KMB]?$/i;
const countIn = (root, item) => {
  if (!root) return '';
  for (const e of [root, ...root.querySelectorAll('*')]) {
    if (e.children.length || !ownOf(item, e)) continue;
    const t = norm(e.innerText || e.textContent);
    if (COUNT.test(t)) return t;
  }
  return '';
};
// commentLikes reads a comment's like count: data-e2e="comment-like-count",
// else the number beside the like control (its button or the *Like*
// container around it). TikTok prints no number for a comment nobody liked,
// so a like control without one is "0".
const commentLikes = (item, like) => {
  const cnt = item.querySelector('[data-e2e="comment-like-count"]');
  if (cnt && ownOf(item, cnt)) return norm(cnt.innerText || cnt.textContent);
  if (!like) return '';
  for (let a = like; a && a !== item; a = a.parentElement) {
    const n = countIn(a, item);
    if (n) return n;
    if (a !== like && a.parentElement !== item && a.parentElement && a.parentElement.querySelector(C_TEXT)) break;
  }
  return '0';
};
// hash53 is cyrb53: a stable 53-bit hash rendered as hex.
const hash53 = (str) => {
  let h1 = 0xdeadbeef, h2 = 0x41c6ce57;
  for (let i = 0; i < str.length; i++) { const ch = str.charCodeAt(i); h1 = Math.imul(h1 ^ ch, 2654435761); h2 = Math.imul(h2 ^ ch, 1597334677); }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(16).padStart(14, '0');
};
// commentDomID is the id TikTok puts in the DOM, if any: a data-comment-id /
// data-cid, or a numeric (comment-id shaped) id attribute on the item or its
// wrapper.
const commentDomID = (item) => {
  for (const e of [item, item.parentElement, ...item.querySelectorAll('[data-comment-id],[data-cid],[id]')]) {
    if (!e || !e.getAttribute) continue;
    const v = e.getAttribute('data-comment-id') || e.getAttribute('data-cid') || '';
    if (v) return v;
    const id = e.getAttribute('id') || '';
    if (/^\d{12,}$/.test(id) && (e === item || e === item.parentElement || !inReplies(item, e))) return id;
  }
  return '';
};
// commentInfo describes a comment. id is TikTok's own when the DOM carries
// one; otherwise it is derived: "tth-" + a hash of the author's username and
// the comment text (the time is left out on purpose: TikTok prints it
// relative, "2d ago", so it changes from day to day). A derived id is stable
// across runs and the same for one author's identical comments.
const commentInfo = (item) => {
  const t = item.querySelector('[data-e2e="comment-content"]') || item.querySelector('[data-e2e="comment-level-1"]') || item.querySelector('[data-e2e^="comment-level-"]');
  const u = item.querySelector(C_USER);
  const link = (u && u.closest('a[href*="/@"]')) || item.querySelector('a[href*="/@"]');
  const like = commentLike(item);
  const username = userFromHref(link && link.getAttribute('href'));
  const text = norm(t && (t.innerText || t.textContent));
  return {
    id: commentDomID(item) || (text ? 'tth-' + hash53(username.toLowerCase() + '\n' + text) : ''),
    username,
    displayName: norm(u && (u.innerText || u.textContent)),
    text,
    likes: commentLikes(item, like),
    liked: like ? pressedOf(like) : null,
  };
};
`

// markedElement resolves the element tagged with token.
func markedElement(page browser.PageInterface, token string) (browser.ElementHandle, error) {
	el, err := page.Element(markSelector(token), 5*time.Second)
	if err == nil && el == nil {
		err = fmt.Errorf("marked element %s not found", token)
	}
	return el, err
}

// clickMarked clicks the element tagged with token using trusted input.
func clickMarked(page browser.PageInterface, token string) error {
	el, err := markedElement(page, token)
	if err != nil {
		return err
	}
	return botpkg.ClickTrusted(page, el)
}

// unmark removes a token's tag (best effort).
func unmark(page browser.PageInterface, token string) {
	var ok bool
	_ = evalJS(page, "token", `document.querySelectorAll('['+MARK+'="'+token+'"]').forEach(e => e.removeAttribute(MARK)); return true;`, &ok, token)
}

// waitFor polls cond until it reports true, returns an error, or timeout
// elapses (then it returns ok=false with the last error, if any).
func waitFor(ctx context.Context, timeout time.Duration, cond func() (bool, error)) (bool, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		ok, err := cond()
		if err == nil && ok {
			return true, nil
		}
		if err != nil {
			last = err
		}
		if time.Now().After(deadline) {
			return false, last
		}
		t := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return false, ctx.Err()
		case <-t.C:
		}
	}
}

// normUser strips "@", surrounding space and case from a TikTok handle.
func normUser(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

// profileTarget turns a profile URL or a bare handle into (url, handle).
func profileTarget(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("tiktok: profile URL or username is required")
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "/") {
		u := (&TikTokBot{}).ResolveURL(target)
		return u, (&TikTokBot{}).ExtractUsername(u), nil
	}
	h := strings.TrimPrefix(target, "@")
	if h == "" || strings.ContainsAny(h, "/?# ") {
		return "", "", fmt.Errorf("tiktok: %q is not a profile URL or username", target)
	}
	return "https://www.tiktok.com/@" + url.PathEscape(h), h, nil
}

// requireVideoURL validates a video URL argument.
func requireVideoURL(videoURL string) (string, error) {
	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		return "", fmt.Errorf("tiktok: videoURL is required")
	}
	return (&TikTokBot{}).ResolveURL(videoURL), nil
}

// snippet is the searchable prefix of a text used to recognise it once
// rendered (long texts may be truncated by the UI).
func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 60
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
