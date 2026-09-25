//go:build social

package tiktok

// State-changing TikTok actions. Each one:
//   - reads the current state first and treats "already done" as success
//     without clicking (a like/follow button is a toggle: clicking it on a
//     liked video or a followed account would undo it);
//   - refuses to click when the state cannot be read;
//   - never falls back to "the first matching element" for a targeted control:
//     several matches go to Jev (when the bot has a picker) or fail;
//   - clicks with trusted input and reports success only once the outcome is
//     observable on the page.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// toggleFind is what the page-side finders report about a toggle control.
type toggleFind struct {
	Count    int    `json:"count"`
	Pressed  *bool  `json:"pressed"`
	State    string `json:"state"`
	Text     string `json:"text"`
	Rejected string `json:"rejected"`
}

// jevToggle asks Jev for a control, then vets and marks it with token using
// resolveJS (a body over params "sel, token" returning a toggleFind).
func (b *TikTokBot) jevToggle(ctx context.Context, page browser.PageInterface, t jevpick.Target, resolveJS, token string) (toggleFind, error) {
	var f toggleFind
	pick, err := b.JevElement(ctx, page, t)
	if err != nil {
		return f, err
	}
	defer pick.Release()
	if err := evalJS(page, "sel, token", resolveJS, &f, pick.Selector, token); err != nil {
		return f, err
	}
	if f.Rejected != "" {
		return f, fmt.Errorf("jev picked an unsuitable element (%s)", f.Rejected)
	}
	if f.Count != 1 {
		return f, errors.New("jev pick vanished")
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// Like a video
// ---------------------------------------------------------------------------

// videoLikeSelectors match the video's own like control (the icon or the
// button around it); comment likes use comment-like-* and never match.
var videoLikeSelectors = []string{
	"[data-e2e='browse-like-icon']",
	"[data-e2e='like-icon']",
	"[data-e2e='browse-like-button']",
	"[data-e2e='like-button']",
}

const jsFindVideoLike = `
let c = [];
for (const e of all(sels)) {
  if (e.closest('[data-e2e*="comment"]')) continue;
  const b = clickable(e);
  if (visible(b) && !c.includes(b)) c.push(b);
}
const inView = c.filter(b => { const r = b.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth; });
if (inView.length) c = inView;
if (c.length !== 1) return {count: c.length};
mark(c[0], token);
return {count: 1, pressed: pressedOf(c[0])};
`

const jsResolveVideoLike = `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const b = clickable(e);
if (b.closest('[data-e2e*="comment"]')) return {count: 1, rejected: 'belongs to a comment'};
mark(b, token);
return {count: 1, pressed: pressedOf(b)};
`

const jsReadMarkedPressed = `
const e = marked(token);
if (!e) return {count: 0};
return {count: 1, pressed: pressedOf(e)};
`

// LikeVideo likes the video at videoURL. It returns status "already_liked"
// without clicking when the video is liked already, "liked" once the like
// button reports pressed after the click, and an error otherwise.
func (b *TikTokBot) LikeVideo(ctx context.Context, page browser.PageInterface, videoURL string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	sels := videoLikeSelectors
	_, _, _ = page.Race(sels, findTimeout)

	tok := newToken()
	var f toggleFind
	if err := evalJS(page, "sels, token", jsFindVideoLike, &f, sels, tok); err != nil {
		return nil, fmt.Errorf("tiktok: find like button: %w", err)
	}
	if f.Count != 1 {
		if !b.JevAvailable(page) {
			if f.Count == 0 {
				return nil, fmt.Errorf("tiktok: like button not found on %s", u)
			}
			return nil, fmt.Errorf("tiktok: %d candidate like buttons on %s; refusing to guess", f.Count, u)
		}
		jf, jerr := b.jevToggle(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Like (heart) button of the TikTok video playing on this page (%s) — the video's own like heart in its action bar, not a comment's like button and not another video's", u),
			Hint:   "TikTok labels it e.g. \"Like video\"; aria-pressed=true means the video is already liked.",
		}, jsResolveVideoLike, tok)
		if jerr != nil {
			return nil, fmt.Errorf("tiktok: like button not found on %s (%d selector matches; jev fallback: %v)", u, f.Count, jerr)
		}
		f = jf
	}
	defer unmark(page, tok)
	if f.Pressed == nil {
		return nil, fmt.Errorf("tiktok: cannot tell whether %s is already liked (no aria-pressed); refusing to click a toggle", u)
	}
	if *f.Pressed {
		return map[string]interface{}{"success": true, "status": "already_liked", "videoURL": u}, nil
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("tiktok: click like on %s: %w", u, err)
	}
	ok, _ := waitFor(ctx, verifyTimeout, func() (bool, error) {
		var r toggleFind
		err := evalJS(page, "token", jsReadMarkedPressed, &r, tok)
		return r.Count == 1 && r.Pressed != nil && *r.Pressed, err
	})
	if !ok {
		return nil, fmt.Errorf("tiktok: like on %s not confirmed (button never reported pressed)", u)
	}
	return map[string]interface{}{"success": true, "status": "liked", "videoURL": u}, nil
}

// ---------------------------------------------------------------------------
// Like a comment
// ---------------------------------------------------------------------------

// maxCommentScrolls bounds how far LikeComment scrolls a long comment list.
const maxCommentScrolls = 40

// jsFindComment finds the one comment matching (id) or (username, text).
const jsFindComment = `
const want = norm(text).toLowerCase(), wantUser = (user || '').replace(/^@/, '').toLowerCase();
const matches = [];
for (const it of commentItems()) {
  const c = commentInfo(it);
  if (id && c.id && c.id === id) { matches.length = 0; matches.push(it); break; }
  if (!want) continue;
  if (!c.text.toLowerCase().includes(want)) continue;
  if (wantUser && c.username.toLowerCase() !== wantUser) continue;
  matches.push(it);
}
if (matches.length !== 1) return {count: matches.length};
const it = matches[0];
mark(it, itemToken);
const like = commentLike(it);
if (!like) return {count: 1, state: 'no_button'};
mark(like, token);
return {count: 1, pressed: pressedOf(like)};
`

const jsResolveCommentLike = `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const item = marked(itemToken);
const b = clickable(e);
const inside = item && item.contains(b) ? b : (item && item.contains(e) ? e : null);
if (!inside) return {count: 1, rejected: 'not inside the target comment'};
mark(inside, token);
return {count: 1, pressed: pressedOf(inside)};
`

// LikeComment likes one comment on the video at videoURL. The comment is
// identified by commentID when TikTok exposes one in the DOM, otherwise by
// its text (a unique substring) and, when given, its author's username. It
// returns status "already_liked" without clicking when it is liked already.
// Zero or several matching comments is an error — never another comment.
func (b *TikTokBot) LikeComment(ctx context.Context, page browser.PageInterface, videoURL, commentID, username, text string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	commentID, username, text = strings.TrimSpace(commentID), strings.TrimSpace(username), snippet(text)
	if commentID == "" && text == "" {
		return nil, fmt.Errorf("tiktok: like_comment needs the comment's text (and ideally its username) or an id")
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	if err := openComments(ctx, page); err != nil {
		return nil, fmt.Errorf("%w on %s", err, u)
	}
	desc := fmt.Sprintf("comment %q", text)
	if username != "" {
		desc = fmt.Sprintf("comment by @%s %q", strings.TrimPrefix(username, "@"), text)
	}
	if text == "" {
		desc = fmt.Sprintf("comment %s", commentID)
	}

	tok, itemTok := newToken(), newToken()
	find := func() (toggleFind, error) {
		var f toggleFind
		err := evalJS(page, "id, user, text, token, itemToken", jsFindComment, &f, commentID, username, text, tok, itemTok)
		return f, err
	}
	// Scroll the comment list until the comment shows up (or the list ends).
	var f toggleFind
	prev, stale := -1, 0
	for round := 0; ; round++ {
		f, err = find()
		if err != nil {
			return nil, fmt.Errorf("tiktok: find %s: %w", desc, err)
		}
		if f.Count > 0 || round >= maxCommentScrolls {
			break
		}
		var n int
		if err := evalJS(page, "", `return commentItems().length;`, &n); err != nil {
			return nil, fmt.Errorf("tiktok: count comments: %w", err)
		}
		if n == prev {
			if stale++; stale >= maxStaleRounds {
				break
			}
		} else {
			stale = 0
		}
		prev = n
		if err := scrollComments(page); err != nil {
			return nil, err
		}
		if err := pause(ctx, 1200*time.Millisecond); err != nil {
			return nil, err
		}
	}
	defer unmark(page, itemTok)
	defer unmark(page, tok)
	switch {
	case f.Count == 0:
		return nil, fmt.Errorf("tiktok: %s not found on %s", desc, u)
	case f.Count > 1:
		return nil, fmt.Errorf("tiktok: %d comments match %s on %s; refusing to guess (pass the username too)", f.Count, desc, u)
	}
	if f.State == "no_button" {
		if !b.JevAvailable(page) {
			return nil, fmt.Errorf("tiktok: like button of %s not found on %s", desc, u)
		}
		jf, jerr := b.jevToggle(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the like (heart) button of the %s under the TikTok video — that comment's own like button, not the video's like button and not another comment's", desc),
		}, jsResolveCommentLikeWithItem(itemTok), tok)
		if jerr != nil {
			return nil, fmt.Errorf("tiktok: like button of %s not found on %s (jev fallback: %v)", desc, u, jerr)
		}
		f = jf
	}
	if f.Pressed == nil {
		return nil, fmt.Errorf("tiktok: cannot tell whether %s is already liked; refusing to click a toggle", desc)
	}
	result := map[string]interface{}{"success": true, "videoURL": u, "commentID": commentID, "username": username, "text": text}
	if *f.Pressed {
		result["status"] = "already_liked"
		return result, nil
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("tiktok: click like of %s: %w", desc, err)
	}
	ok, _ := waitFor(ctx, verifyTimeout, func() (bool, error) {
		var r toggleFind
		err := evalJS(page, "token", jsReadMarkedPressed, &r, tok)
		return r.Count == 1 && r.Pressed != nil && *r.Pressed, err
	})
	if !ok {
		return nil, fmt.Errorf("tiktok: like of %s not confirmed", desc)
	}
	result["status"] = "liked"
	return result, nil
}

// jsResolveCommentLikeWithItem binds the comment's item token into the
// resolve script (jevToggle passes only sel and token).
func jsResolveCommentLikeWithItem(itemTok string) string {
	return "const itemToken = " + jsString(itemTok) + ";\n" + jsResolveCommentLike
}

// jsString renders s as a JavaScript string literal.
func jsString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, " ", ` `, " ", ` `, "<", `\x3c`)
	return "'" + r.Replace(s) + "'"
}

// ---------------------------------------------------------------------------
// Follow
// ---------------------------------------------------------------------------

var followSelectors = []string{
	"[data-e2e='follow-button']",
	"[data-e2e='follow-btn']",
}

// jsFollowState classifies a follow button; shared by the finder and resolver.
const jsFollowState = `
const scoped = (e) => !e.closest('[data-e2e*="suggest"],[data-e2e*="recommend"],[data-e2e*="user-card"]');
const classify = (b) => {
  const p = b.getAttribute('aria-pressed');
  const t = norm(b.innerText || b.textContent || b.getAttribute('aria-label')).toLowerCase();
  if (p === 'true' || /^(following|friends|unfollow)$/.test(t)) return 'following';
  if (/^requested$/.test(t)) return 'requested';
  if (/^(follow|follow back)$/.test(t) || p === 'false') return 'not_following';
  return 'unknown';
};
`

const jsFindFollow = jsFollowState + `
const btns = all(sels).map(clickable).filter((b, i, a) => a.indexOf(b) === i && visible(b) && scoped(b));
if (btns.length === 1) { mark(btns[0], token); return {count: 1, state: classify(btns[0]), text: norm(btns[0].innerText)}; }
const unf = [...document.querySelectorAll('[data-e2e="unfollow-button"]')].filter(e => visible(e) && scoped(e));
if (btns.length === 0 && unf.length === 1) return {count: 1, state: 'following'};
return {count: btns.length};
`

const jsResolveFollow = jsFollowState + `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const b = clickable(e);
if (!scoped(b)) return {count: 1, rejected: 'belongs to a suggested account'};
mark(b, token);
return {count: 1, state: classify(b), text: norm(b.innerText)};
`

const jsReadFollow = jsFollowState + `
const e = marked(token);
if (e && visible(e)) return {count: 1, state: classify(e)};
const btns = all(sels).map(clickable).filter((b, i, a) => a.indexOf(b) === i && visible(b) && scoped(b));
if (btns.length === 1) return {count: 1, state: classify(btns[0])};
const unf = [...document.querySelectorAll('[data-e2e="unfollow-button"]')].filter(e => visible(e) && scoped(e));
if (unf.length === 1) return {count: 1, state: 'following'};
return {count: btns.length};
`

// FollowUser follows the account at profile (URL or handle). It returns
// status "already_following" (or "already_requested") without clicking when
// the account is followed already, "followed"/"requested" once the button
// reports the new state, and an error when the state cannot be read or the
// follow is not confirmed.
func (b *TikTokBot) FollowUser(ctx context.Context, page browser.PageInterface, profile string) (map[string]interface{}, error) {
	u, handle, err := profileTarget(profile)
	if err != nil {
		return nil, err
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	_, _, _ = page.Race(append(append([]string{}, followSelectors...), "[data-e2e='unfollow-button']"), findTimeout)

	tok := newToken()
	var f toggleFind
	if err := evalJS(page, "sels, token", jsFindFollow, &f, followSelectors, tok); err != nil {
		return nil, fmt.Errorf("tiktok: find follow button: %w", err)
	}
	if f.Count != 1 {
		if !b.JevAvailable(page) {
			if f.Count == 0 {
				return nil, fmt.Errorf("tiktok: follow button not found on %s", u)
			}
			return nil, fmt.Errorf("tiktok: %d candidate follow buttons on %s; refusing to guess", f.Count, u)
		}
		jf, jerr := b.jevToggle(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: fmt.Sprintf("the Follow button of the TikTok profile shown on this page (@%s) — the profile owner's own Follow button in the profile header, not a Follow button of a suggested account", handle),
			Hint:   "If the button reads Following or Friends the account is already followed.",
		}, jsResolveFollow, tok)
		if jerr != nil {
			return nil, fmt.Errorf("tiktok: follow button not found on %s (%d selector matches; jev fallback: %v)", u, f.Count, jerr)
		}
		f = jf
	}
	defer unmark(page, tok)
	res := map[string]interface{}{"success": true, "profileURL": u, "username": handle}
	switch f.State {
	case "following":
		res["status"] = "already_following"
		return res, nil
	case "requested":
		res["status"] = "already_requested"
		return res, nil
	case "not_following":
	default:
		return nil, fmt.Errorf("tiktok: cannot read the follow state of %s (button %q); refusing to click a toggle", u, f.Text)
	}
	if err := clickMarked(page, tok); err != nil {
		return nil, fmt.Errorf("tiktok: click follow on %s: %w", u, err)
	}
	var state string
	ok, _ := waitFor(ctx, verifyTimeout, func() (bool, error) {
		var r toggleFind
		err := evalJS(page, "sels, token", jsReadFollow, &r, followSelectors, tok)
		state = r.State
		return r.State == "following" || r.State == "requested", err
	})
	if !ok {
		return nil, fmt.Errorf("tiktok: follow of %s not confirmed (state %q)", u, state)
	}
	if state == "requested" {
		res["status"] = "requested"
	} else {
		res["status"] = "followed"
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Comment on a video
// ---------------------------------------------------------------------------

var commentInputSelectors = []string{
	"[data-e2e='comment-input']",
	"[data-e2e='comment-text']",
}

var commentPostSelectors = []string{
	"[data-e2e='comment-post']",
	"[data-e2e='comment-send-btn']",
	"[data-e2e='comment-post-btn']",
}

// jsFindEditor finds the single visible editor inside the containers sels.
const jsFindEditor = `
const eds = [];
for (const c of all(sels)) {
  const ed = (c.isContentEditable || /^(TEXTAREA|INPUT)$/.test(c.tagName)) ? c : c.querySelector('[contenteditable="true"],textarea,input[type="text"]');
  if (ed && visible(ed) && !eds.includes(ed)) eds.push(ed);
}
if (eds.length !== 1) return {count: eds.length};
mark(eds[0], token);
return {count: 1, text: textOf(eds[0])};
`

const jsResolveEditor = `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const ed = (e.isContentEditable || /^(TEXTAREA|INPUT)$/.test(e.tagName)) ? e : e.querySelector('[contenteditable="true"],textarea');
if (!ed) return {count: 1, rejected: 'not a text box'};
mark(ed, token);
return {count: 1, text: textOf(ed)};
`

// jsFindSubmit finds the submit control nearest to the marked editor.
const jsFindSubmit = `
const ed = marked(edToken);
let found = [];
for (let a = ed && ed.parentElement, i = 0; a && i < 8 && !found.length; a = a.parentElement, i++) found = all(sels, a).map(clickable).filter(visible);
if (!found.length) found = all(sels).map(clickable).filter(visible);
found = found.filter((b, i, a) => a.indexOf(b) === i);
if (found.length !== 1) return {count: found.length};
mark(found[0], token);
return {count: 1, state: disabledOf(found[0]) ? 'disabled' : 'enabled'};
`

const jsResolveSubmit = `
const e = document.querySelector(sel);
if (!e) return {count: 0};
const b = clickable(e);
mark(b, token);
return {count: 1, state: disabledOf(b) ? 'disabled' : 'enabled'};
`

// findEditor locates (and marks with token) a text box by container
// selectors, asking Jev when the selectors are inconclusive.
func (b *TikTokBot) findEditor(ctx context.Context, page browser.PageInterface, sels []string, token, intent string) (toggleFind, error) {
	var f toggleFind
	if err := evalJS(page, "sels, token", jsFindEditor, &f, sels, token); err != nil {
		return f, err
	}
	if f.Count == 1 {
		return f, nil
	}
	if !b.JevAvailable(page) {
		return f, fmt.Errorf("%d matching text boxes", f.Count)
	}
	jf, err := b.jevToggle(ctx, page, jevpick.Target{Kind: "fill", Intent: intent}, jsResolveEditor, token)
	if err != nil {
		return f, fmt.Errorf("%d matching text boxes (jev fallback: %v)", f.Count, err)
	}
	return jf, nil
}

// findSubmit locates (and marks with token) the submit control for the
// editor marked edToken, asking Jev when the selectors are inconclusive, and
// waits for it to become enabled.
func (b *TikTokBot) findSubmit(ctx context.Context, page browser.PageInterface, sels []string, edToken, token, intent string) error {
	var f toggleFind
	find := func() error {
		return evalJS(page, "sels, edToken, token", jsFindSubmit, &f, sels, edToken, token)
	}
	if err := find(); err != nil {
		return err
	}
	jev := false
	if f.Count != 1 {
		if !b.JevAvailable(page) {
			return fmt.Errorf("%d matching submit buttons", f.Count)
		}
		jf, err := b.jevToggle(ctx, page, jevpick.Target{Kind: "click", Intent: intent}, jsResolveSubmit, token)
		if err != nil {
			return fmt.Errorf("%d matching submit buttons (jev fallback: %v)", f.Count, err)
		}
		f, jev = jf, true
	}
	if f.State != "disabled" {
		return nil
	}
	ok, _ := waitFor(ctx, 5*time.Second, func() (bool, error) {
		var r toggleFind
		var err error
		if jev {
			err = evalJS(page, "token", `const e = marked(token); return e ? {count: 1, state: disabledOf(e) ? 'disabled' : 'enabled'} : {count: 0};`, &r, token)
		} else {
			err = evalJS(page, "sels, edToken, token", jsFindSubmit, &r, sels, edToken, token)
		}
		return r.Count == 1 && r.State == "enabled", err
	})
	if !ok {
		return errors.New("submit button stays disabled")
	}
	return nil
}

// typeInto types text into the editor marked token and checks it arrived.
func typeInto(ctx context.Context, page browser.PageInterface, token, text string) error {
	el, err := markedElement(page, token)
	if err != nil {
		return err
	}
	if err := botpkg.TypeInto(ctx, page, el, text); err != nil {
		return err
	}
	want := snippet(text)
	ok, _ := waitFor(ctx, 3*time.Second, func() (bool, error) {
		var got string
		err := evalJS(page, "token", `const e = marked(token); return e ? textOf(e) : '';`, &got, token)
		return strings.Contains(strings.Join(strings.Fields(got), " "), want), err
	})
	if !ok {
		return errors.New("typed text did not appear in the text box")
	}
	return nil
}

const jsCommentProgress = `
const n = commentItems().filter(it => commentInfo(it).text.includes(snip)).length;
const ed = marked(edToken);
return {n, composer: ed ? textOf(ed) : null};
`

type commentProgress struct {
	N        int     `json:"n"`
	Composer *string `json:"composer"`
}

// CommentOnVideo posts commentText on the video at videoURL and confirms it:
// a new comment carrying the text appears, or the composer is cleared by the
// submission. Anything else is an error.
func (b *TikTokBot) CommentOnVideo(ctx context.Context, page browser.PageInterface, videoURL, commentText string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(commentText) == "" {
		return nil, fmt.Errorf("tiktok: commentText is required")
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}

	edTok, postTok := newToken(), newToken()
	defer unmark(page, edTok)
	defer unmark(page, postTok)

	// The composer only renders once the comment panel is open.
	var probe toggleFind
	if err := evalJS(page, "sels, token", jsFindEditor, &probe, commentInputSelectors, edTok); err != nil {
		return nil, fmt.Errorf("tiktok: find comment box: %w", err)
	}
	if probe.Count == 0 {
		if err := openComments(ctx, page); err != nil {
			return nil, fmt.Errorf("%w on %s", err, u)
		}
		_, _ = waitFor(ctx, findTimeout, func() (bool, error) {
			err := evalJS(page, "sels, token", jsFindEditor, &probe, commentInputSelectors, edTok)
			return probe.Count > 0, err
		})
	}
	if _, err := b.findEditor(ctx, page, commentInputSelectors, edTok,
		fmt.Sprintf("the \"Add comment\" text box of the comment section of the TikTok video on this page (%s) — not a direct-message box and not the search box", u)); err != nil {
		return nil, fmt.Errorf("tiktok: comment box not found on %s: %w", u, err)
	}

	snip := snippet(commentText)
	var before commentProgress
	if err := evalJS(page, "snip, edToken", jsCommentProgress, &before, snip, edTok); err != nil {
		return nil, fmt.Errorf("tiktok: read comments: %w", err)
	}
	if err := typeInto(ctx, page, edTok, commentText); err != nil {
		return nil, fmt.Errorf("tiktok: type comment on %s: %w", u, err)
	}
	if err := b.findSubmit(ctx, page, commentPostSelectors, edTok, postTok,
		"the Post button that submits the comment typed into the video's comment box"); err != nil {
		return nil, fmt.Errorf("tiktok: comment Post button on %s: %w", u, err)
	}
	if err := clickMarked(page, postTok); err != nil {
		return nil, fmt.Errorf("tiktok: click Post on %s: %w", u, err)
	}

	var how string
	ok, _ := waitFor(ctx, verifyTimeout, func() (bool, error) {
		var p commentProgress
		if err := evalJS(page, "snip, edToken", jsCommentProgress, &p, snip, edTok); err != nil {
			return false, err
		}
		switch {
		case p.N > before.N:
			how = "comment_rendered"
		case p.Composer != nil && strings.TrimSpace(*p.Composer) == "":
			how = "composer_cleared"
		default:
			return false, nil
		}
		return true, nil
	})
	if !ok {
		return nil, fmt.Errorf("tiktok: comment on %s not confirmed (no new comment and the composer still holds the text)", u)
	}
	return map[string]interface{}{"success": true, "status": "commented", "verified": how, "videoURL": u, "text": commentText}, nil
}

// ---------------------------------------------------------------------------
// Share / Stitch / Duet — these only open TikTok's own UI (share sheet, the
// stitch/duet creator); nothing is published.
// ---------------------------------------------------------------------------

var shareSelectors = []string{
	"[data-e2e='share-icon']",
	"[data-e2e='browse-share-icon']",
	"[data-e2e='share-btn']",
}

const jsFindOption = `
const c = all(sels).map(e => e.closest('button,[role="button"],a,li') || e).filter((b, i, a) => a.indexOf(b) === i && visible(b));
if (c.length !== 1) return {count: c.length};
mark(c[0], token);
return {count: 1, state: disabledOf(c[0]) || c[0].querySelector('[aria-disabled="true"]') ? 'disabled' : 'enabled'};
`

// clickOption opens the share sheet if needed, finds the single option
// matching sels and clicks it.
func clickOption(ctx context.Context, page browser.PageInterface, u, what string, sels []string) error {
	tok := newToken()
	defer unmark(page, tok)
	var f toggleFind
	if err := evalJS(page, "sels, token", jsFindOption, &f, shareSelectors, tok); err != nil {
		return fmt.Errorf("tiktok: find share button: %w", err)
	}
	if f.Count != 1 {
		return fmt.Errorf("tiktok: share button not found on %s (%d matches)", u, f.Count)
	}
	if err := clickMarked(page, tok); err != nil {
		return fmt.Errorf("tiktok: open share menu on %s: %w", u, err)
	}
	ok, _ := waitFor(ctx, findTimeout, func() (bool, error) {
		err := evalJS(page, "sels, token", jsFindOption, &f, sels, tok)
		return f.Count > 0, err
	})
	if !ok {
		return fmt.Errorf("tiktok: %s option not found in the share menu of %s", what, u)
	}
	if f.Count != 1 {
		return fmt.Errorf("tiktok: %d %s options in the share menu of %s", f.Count, what, u)
	}
	if f.State == "disabled" {
		return fmt.Errorf("tiktok: %s is disabled for %s (the creator does not allow it)", what, u)
	}
	return clickMarked(page, tok)
}

// StitchVideo opens the Stitch creator for the video. Status
// "opened_stitch_editor": the stitch itself is not recorded or published.
func (b *TikTokBot) StitchVideo(ctx context.Context, page browser.PageInterface, videoURL string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	if err := clickOption(ctx, page, u, "Stitch", []string{"[data-e2e='share-stitch']"}); err != nil {
		return nil, err
	}
	return map[string]interface{}{"success": true, "status": "opened_stitch_editor", "published": false, "videoURL": u}, nil
}

// DuetVideo opens the Duet creator for the video. Status
// "opened_duet_editor": the duet itself is not recorded or published.
func (b *TikTokBot) DuetVideo(ctx context.Context, page browser.PageInterface, videoURL string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	if err := clickOption(ctx, page, u, "Duet", []string{"[data-e2e='share-duet']"}); err != nil {
		return nil, err
	}
	return map[string]interface{}{"success": true, "status": "opened_duet_editor", "published": false, "videoURL": u}, nil
}

// ShareVideo opens the share menu and clicks "Copy link". Nothing is posted;
// the result carries the video URL.
func (b *TikTokBot) ShareVideo(ctx context.Context, page browser.PageInterface, videoURL string) (map[string]interface{}, error) {
	u, err := requireVideoURL(videoURL)
	if err != nil {
		return nil, err
	}
	if err := open(ctx, page, u); err != nil {
		return nil, err
	}
	if err := clickOption(ctx, page, u, "Copy link", []string{"[data-e2e='copy-link-icon']", "[data-e2e='copy-link']"}); err != nil {
		return nil, err
	}
	return map[string]interface{}{"success": true, "status": "link_copied", "published": false, "url": u, "videoURL": u}, nil
}
