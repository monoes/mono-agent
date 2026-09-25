//go:build social

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
const userFromHref = (h) => { const m = (h || '').match(/\/@([^/?#]+)/); return m ? decodeURIComponent(m[1]) : ''; };
// Comments: TikTok renders either explicit comment-item containers or
// (current layout) a comment-level-1 text node whose nearest ancestor holding
// the author link is the comment; an ancestor holding a second comment text
// belongs to a thread, not to one comment.
const C_USER = '[data-e2e="comment-username"],[data-e2e="comment-username-1"]';
const C_LIKE = '[data-e2e="comment-like-icon"],[data-e2e="comment-like-btn"],[data-e2e="comment-like"]';
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
        if (a.querySelector(C_LIKE)) break;
      }
    }
    if (item && !out.includes(item)) out.push(item);
  }
  return out;
};
const commentLike = (item) => {
  const icon = item.querySelector(C_LIKE);
  if (!icon) return null;
  const b = clickable(icon);
  return item.contains(b) ? b : icon;
};
const commentInfo = (item) => {
  const t = item.querySelector('[data-e2e="comment-content"]') || item.querySelector('[data-e2e="comment-level-1"]') || item.querySelector('[data-e2e^="comment-level-"]');
  const u = item.querySelector(C_USER);
  const link = (u && u.closest('a[href*="/@"]')) || item.querySelector('a[href*="/@"]');
  const like = commentLike(item);
  const cnt = item.querySelector('[data-e2e="comment-like-count"]');
  return {
    id: item.getAttribute('data-comment-id') || item.getAttribute('data-cid') || '',
    username: userFromHref(link && link.getAttribute('href')),
    displayName: norm(u && (u.innerText || u.textContent)),
    text: norm(t && (t.innerText || t.textContent)),
    likes: norm(cnt && (cnt.innerText || cnt.textContent)),
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
