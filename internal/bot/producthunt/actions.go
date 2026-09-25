//go:build social

package producthunt

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// Product Hunt's class names are hashed; the stable hooks are its data-test
// attributes: comment-form (the composer, whose submit button is
// form-submit-button), comments-feed, thread-<id> (a top-level comment and
// its replies), comment-<id> (one comment), vote-button (every upvote
// button on the page — the launch's own and those of other launches or
// forum threads) and action-bar-vote-button (a comment's upvote). Page reads
// go through botpkg.EvalJSON (CSP-proof, JSON arguments).

var (
	// controlWait is how long a control may take to appear before the Jev
	// fallback (or the error) takes over.
	controlWait = 10 * time.Second
	// confirmTimeout bounds how long CommentOnLaunch waits for the posted
	// comment to show.
	confirmTimeout = 15 * time.Second
	pollInterval   = 250 * time.Millisecond
)

// checkLaunchURL accepts only https Product Hunt pages (a bare path is
// resolved against www.producthunt.com), so the actions never navigate the
// user's logged-in browser anywhere else.
func checkLaunchURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("producthunt: launchURL is required")
	}
	if strings.HasPrefix(raw, "/") {
		raw = "https://www.producthunt.com" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Host != "www.producthunt.com" && u.Host != "producthunt.com") {
		return "", fmt.Errorf("producthunt: launchURL must be an https://www.producthunt.com/... URL, got %q", raw)
	}
	return u.String(), nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func open(page browser.PageInterface, u string) error {
	if err := page.Navigate(u); err != nil {
		return fmt.Errorf("producthunt: navigate to %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("producthunt: %s did not load: %w", u, err)
	}
	return nil
}

// normText collapses whitespace and case for comparing typed and rendered
// comment text.
func normText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func needle(s string) string {
	n := []rune(strings.Join(strings.Fields(normText(s)), ""))
	if len(n) > 80 {
		n = n[:80]
	}
	return string(n)
}

// commentSection brings the comment section into view (Product Hunt renders
// it lazily) and waits until the composer or the feed exists.
func commentSection(page browser.PageInterface) error {
	var ok bool
	_ = botpkg.EvalJSON(page, `() => {
		const c = document.querySelector('#comments, [data-test="comment-form"], [data-test="comments-feed"]');
		if (c) c.scrollIntoView({ block: 'start' });
		return true;
	}`, &ok)
	if _, _, err := botpkg.FindFirst(page, "[data-test='comments-feed']", []string{"[data-test='comment-form']", "#comments"}, controlWait); err != nil {
		return fmt.Errorf("producthunt: no comment section on this page: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// list_comments
// ---------------------------------------------------------------------------

// commentsJS reads every rendered comment: an element whose data-test is
// exactly comment-<digits> (not comment-form / comment-menu-button). Author,
// body and upvotes are taken only from the comment's own subtree, never
// from a reply nested inside it. depth/parentId come from an enclosing
// comment element or, in the current layout (each comment in its own
// thread-<id>, a reply's thread nested inside its parent's thread), from
// the nearest thread-<id> ancestor that is not the comment's own.
const commentsJS = `() => {
	const isC = el => /^comment-[0-9]+$/.test(el.getAttribute('data-test') || '');
	const idOf = el => el.getAttribute('data-test').slice(8);
	const owner = x => { for (let e = x; e; e = e.parentElement) if (e.nodeType === 1 && isC(e)) return e; return null; };
	const nodes = Array.from(document.querySelectorAll('[data-test^="comment-"]')).filter(isC);
	const num = s => {
		const m = (s || '').replace(/,/g, '').match(/([0-9]+(?:\.[0-9]+)?)\s*([KkMm])?/);
		if (!m) return 0;
		let n = parseFloat(m[1]);
		if (m[2]) n *= /k/i.test(m[2]) ? 1000 : 1000000;
		return Math.round(n);
	};
	const depthOf = new Map(); // id -> depth; document order visits parents first
	return nodes.map(el => {
		const own = sel => Array.from(el.querySelectorAll(sel)).filter(x => owner(x) === el);
		const author = own('a[href^="/@"]').find(a => a.textContent.trim());
		const body = own('.prose, [class*="richText"]')[0];
		const parent = owner(el.parentElement);
		const myId = idOf(el);
		// Every comment sits in its own thread-<id>; a reply's thread is
		// nested inside its parent's thread, so the parent is the nearest
		// thread ancestor that is not the comment's own.
		let threadId = '';
		for (let e = el.parentElement; e; e = e.parentElement) {
			const t = /^thread-([0-9]+)$/.exec(e.getAttribute('data-test') || '');
			if (t && t[1] !== myId) { threadId = t[1]; break; }
		}
		let depth = 0, parentId = '';
		if (parent) { depth = depthOf.get(idOf(parent)) + 1; parentId = idOf(parent); }
		else if (threadId) { parentId = threadId; depth = depthOf.has(threadId) ? depthOf.get(threadId) + 1 : 1; }
		depthOf.set(myId, depth);
		const vote = own('[data-test="action-bar-vote-button"]')[0];
		const href = author ? author.getAttribute('href') : '';
		return {
			id: idOf(el),
			author: author ? author.textContent.trim() : '',
			username: href ? href.slice(2) : '',
			text: body ? (body.innerText || body.textContent || '').trim() : '',
			depth, parentId,
			upvotes: vote ? num(vote.textContent.replace(/upvoted?/ig, '')) : 0,
		};
	});
}`

// Comment is one Product Hunt comment.
type Comment struct {
	ID       string `json:"id"`
	Author   string `json:"author"`
	Username string `json:"username"`
	Text     string `json:"text"`
	Depth    int    `json:"depth"`
	ParentID string `json:"parentId"`
	Upvotes  int    `json:"upvotes"`
}

func readComments(page browser.PageInterface) ([]Comment, error) {
	var cs []Comment
	if err := botpkg.EvalJSON(page, commentsJS, &cs); err != nil {
		return nil, fmt.Errorf("producthunt: reading comments: %w", err)
	}
	return cs, nil
}

// ListComments returns the comments rendered on a launch page, in page
// order: top-level comments (depth 0) and replies (depth 1+, parentId set).
// Comments Product Hunt has not loaded yet ("show more") are not included.
func (b *ProductHuntBot) ListComments(ctx context.Context, page browser.PageInterface, launchURL string) ([]map[string]interface{}, error) {
	u, err := checkLaunchURL(launchURL)
	if err != nil {
		return nil, err
	}
	if err := open(page, u); err != nil {
		return nil, err
	}
	if err := commentSection(page); err != nil {
		return nil, err
	}
	cs, err := readComments(page)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]interface{}{
			"id": c.ID, "author": c.Author, "username": c.Username, "text": c.Text,
			"depth": c.Depth, "parentId": c.ParentID, "upvotes": c.Upvotes,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// get_launch_metrics
// ---------------------------------------------------------------------------

// launchMetricsJS finds the launch's own upvote button among all the
// vote-button elements: the one labelled "Upvote" (other launches and forum
// threads show a bare count). When several carry the label, those inside a
// card that links to a different product/launch are dropped; anything but
// exactly one remaining is an error, never a guess. The count is the number
// on that button (null while Product Hunt hides it on launch day). The
// comment count is an explicit "N comments" label when the page has one,
// else the number of comments rendered.
const launchMetricsJS = `() => {
	const num = s => {
		const m = (s || '').replace(/,/g, '').match(/([0-9]+(?:\.[0-9]+)?)\s*([KkMm])?/);
		if (!m) return null;
		let n = parseFloat(m[1]);
		if (m[2]) n *= /k/i.test(m[2]) ? 1000 : 1000000;
		return Math.round(n);
	};
	const label = el => [el.textContent, el.getAttribute('aria-label'), el.getAttribute('area-label'), el.getAttribute('title')].join(' ');
	const slugOf = h => { const m = (h || '').match(/^(?:https:\/\/www\.producthunt\.com)?\/(?:posts|products)\/([^\/?#]+)(?:\/launches\/([^\/?#]+))?/); return m ? (m[2] || m[1]) : ''; };
	const pageSlugs = new Set([slugOf(location.pathname), (location.pathname.match(/^\/products\/([^\/?#]+)/) || [])[1]].filter(Boolean));
	const all = Array.from(document.querySelectorAll('[data-test="vote-button"]'));
	let cands = all.filter(v => /upvote/i.test(label(v)));
	if (cands.length > 1) {
		cands = cands.filter(v => {
			let card = v;
			while (card.parentElement && card.parentElement.querySelectorAll('[data-test="vote-button"]').length === 1) card = card.parentElement;
			return !Array.from(card.querySelectorAll('a[href]')).some(a => { const s = slugOf(a.getAttribute('href')); return s && !pageSlugs.has(s); });
		});
	}
	if (cands.length !== 1) return { error: cands.length ? 'ambiguous' : 'missing', voteButtons: all.length };
	const v = cands[0];
	const upvotes = num(v.textContent.replace(/upvoted?/ig, ''));
	const isC = el => /^comment-[0-9]+$/.test(el.getAttribute('data-test') || '');
	const loaded = Array.from(document.querySelectorAll('[data-test^="comment-"]')).filter(isC).length;
	let explicit = null;
	for (const el of document.querySelectorAll('h2, h3, h4, button, a, span, p, div')) {
		if (el.children.length > 2 || el.closest('[data-test^="thread-"]')) continue;
		const t = el.textContent.replace(/\s+/g, ' ').trim();
		if (t.length > 30) continue;
		const m = t.match(/^([0-9][0-9.,]*[KkM]?)\s+comments?$/i) || t.match(/^comments?\s*\(?\s*([0-9][0-9.,]*[KkM]?)\s*\)?$/i);
		if (m) { explicit = num(m[1]); break; }
	}
	const h1 = document.querySelector('h1');
	return {
		upvotes, upvotesHidden: upvotes === null,
		comments: explicit !== null ? explicit : loaded,
		commentsSource: explicit !== null ? 'label' : 'loaded',
		name: h1 ? h1.textContent.trim() : '',
	};
}`

type launchMetrics struct {
	Error          string `json:"error"`
	VoteButtons    int    `json:"voteButtons"`
	Upvotes        *int   `json:"upvotes"`
	UpvotesHidden  bool   `json:"upvotesHidden"`
	Comments       int    `json:"comments"`
	CommentsSource string `json:"commentsSource"`
	Name           string `json:"name"`
}

// GetLaunchMetrics reads a launch's upvotes (from its own upvote button,
// null while hidden) and comment count.
func (b *ProductHuntBot) GetLaunchMetrics(ctx context.Context, page browser.PageInterface, launchURL string) (map[string]interface{}, error) {
	u, err := checkLaunchURL(launchURL)
	if err != nil {
		return nil, err
	}
	if err := open(page, u); err != nil {
		return nil, err
	}
	if _, _, err := botpkg.FindFirst(page, "[data-test='vote-button']", nil, controlWait); err != nil {
		return nil, fmt.Errorf("producthunt: no upvote button on %s: %w", u, err)
	}
	// Comments render lazily; bring them in before counting.
	_ = commentSection(page)
	var m launchMetrics
	if err := botpkg.EvalJSON(page, launchMetricsJS, &m); err != nil {
		return nil, fmt.Errorf("producthunt: reading metrics: %w", err)
	}
	switch m.Error {
	case "":
	case "ambiguous":
		return nil, fmt.Errorf("producthunt: cannot tell the launch's own upvote button from the other %d vote buttons on %s", m.VoteButtons, u)
	default:
		return nil, fmt.Errorf("producthunt: the launch's upvote button was not found on %s (%d other vote buttons)", u, m.VoteButtons)
	}
	out := map[string]interface{}{
		"launchURL": u, "name": m.Name, "upvotes": nil, "upvotesHidden": m.UpvotesHidden,
		"comments": m.Comments, "commentsSource": m.CommentsSource,
	}
	if m.Upvotes != nil {
		out["upvotes"] = *m.Upvotes
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// comment_on_launch
// ---------------------------------------------------------------------------

const formMark = "data-monoagent-ph-form"

// markComposerJS tags the launch's top-level composer (a comment-form that
// is not a reply form inside a thread) and reports its state.
const markComposerJS = `(mark) => {
	document.querySelectorAll('[' + mark + ']').forEach(e => e.removeAttribute(mark));
	const form = Array.from(document.querySelectorAll('form[data-test="comment-form"]')).find(f => !f.closest('[data-test^="thread-"]'));
	const signIn = !!document.querySelector('[data-test="header-nav-link-sign-in"]');
	if (!form) return { found: false, signIn };
	form.setAttribute(mark, '1');
	const btn = form.querySelector('[data-test="form-submit-button"], button[type="submit"]');
	return { found: true, signIn, button: btn ? btn.textContent.replace(/\s+/g, ' ').trim() : '',
		editor: !!form.querySelector('[contenteditable="true"], textarea') };
}`

type composerState struct {
	Found  bool   `json:"found"`
	SignIn bool   `json:"signIn"`
	Button string `json:"button"`
	Editor bool   `json:"editor"`
}

// newCommentJS finds a comment that was not on the page before (prior ids)
// whose own body contains needle.
const newCommentJS = `(prior, needle) => {
	const isC = el => /^comment-[0-9]+$/.test(el.getAttribute('data-test') || '');
	const owner = x => { for (let e = x; e; e = e.parentElement) if (e.nodeType === 1 && isC(e)) return e; return null; };
	const seen = new Set(prior);
	for (const el of document.querySelectorAll('[data-test^="comment-"]')) {
		if (!isC(el)) continue;
		const id = el.getAttribute('data-test').slice(8);
		if (seen.has(id)) continue;
		const body = Array.from(el.querySelectorAll('.prose, [class*="richText"]')).find(x => owner(x) === el);
		const t = body ? (body.innerText || body.textContent || '') : '';
		if (t.replace(/\s+/g, '').toLowerCase().includes(needle)) return { id };
	}
	return { id: '' };
}`

func isLoginLabel(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "login") || strings.Contains(s, "log in") ||
		strings.Contains(s, "sign in") || strings.Contains(s, "sign up")
}

// elementText reads what an input control holds: value for textarea/input,
// textContent for a contenteditable editor.
func elementText(el browser.ElementHandle) string {
	if v, err := el.Property("value"); err == nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if v, err := el.Property("textContent"); err == nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// CommentOnLaunch posts a top-level comment on a launch and confirms it: a
// comment that was not on the page before, whose text is the typed text,
// must appear. The composer is the launch's own comment form (never a reply
// form), its submit button is looked up inside that form (never a
// "Comments" tab elsewhere), and a "Login to comment" button means the
// session is logged out — nothing is typed or clicked then.
func (b *ProductHuntBot) CommentOnLaunch(ctx context.Context, page browser.PageInterface, launchURL, text string) (map[string]interface{}, error) {
	u, err := checkLaunchURL(launchURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("producthunt: comment text is required")
	}
	if err := open(page, u); err != nil {
		return nil, err
	}
	_ = commentSection(page)

	var cs composerState
	if err := botpkg.EvalJSON(page, markComposerJS, &cs, formMark); err != nil {
		return nil, fmt.Errorf("producthunt: reading the comment form: %w", err)
	}
	if isLoginLabel(cs.Button) || (!cs.Found && cs.SignIn) {
		return nil, fmt.Errorf("producthunt: not logged in (the comment form asks to log in)")
	}
	form := "[" + formMark + "]"
	editorSel := form + " [contenteditable='true']"
	editorAlts := []string{form + " textarea"}

	var editor browser.ElementHandle
	var release = func() {}
	defer func() { release() }()
	if cs.Found && !cs.Editor {
		// The composer starts as a placeholder; clicking it opens the editor.
		if ph, _, err := botpkg.FindFirst(page, form+" p", []string{form + " > div"}, 2*time.Second); err == nil {
			_ = botpkg.ClickTrusted(page, ph)
		}
	}
	if cs.Found {
		editor, _, err = botpkg.FindFirst(page, editorSel, editorAlts, controlWait)
	}
	if editor == nil {
		if !b.JevAvailable(page) {
			return nil, fmt.Errorf("producthunt: comment box not found on %s: %v", u, err)
		}
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "fill",
			Intent: "the text box for writing a new top-level comment on this Product Hunt launch (placeholder like \"What do you think?\") — not a reply box under an existing comment, not the search box",
		})
		if jerr != nil {
			return nil, fmt.Errorf("producthunt: comment box not found on %s: %v (jev fallback: %v)", u, err, jerr)
		}
		editor, release = pick.Element, pick.Release
	}

	before, err := readComments(page)
	if err != nil {
		return nil, err
	}
	prior := make([]string, 0, len(before))
	for _, c := range before {
		prior = append(prior, c.ID)
	}

	if err := botpkg.TypeInto(ctx, page, editor, text); err != nil {
		return nil, fmt.Errorf("producthunt: typing the comment: %w", err)
	}
	if got := elementText(editor); !strings.Contains(normText(got), normText(text)) {
		return nil, fmt.Errorf("producthunt: the comment box holds %q after typing — not submitting", got)
	}

	var submit browser.ElementHandle
	if cs.Found {
		submit, _, err = botpkg.FindFirst(page, form+" [data-test='form-submit-button']", []string{form + " button[type='submit']"}, controlWait)
	}
	if submit == nil {
		if !b.JevAvailable(page) {
			return nil, fmt.Errorf("producthunt: comment submit button not found: %v", err)
		}
		pick, jerr := b.JevElement(ctx, page, jevpick.Target{
			Kind:   "click",
			Intent: "the button that posts the comment just typed into the launch's comment box (labelled e.g. \"Comment\") — not a \"Comments\" tab, not a Reply button, not an Upvote button",
		})
		if jerr != nil {
			return nil, fmt.Errorf("producthunt: comment submit button not found: %v (jev fallback: %v)", err, jerr)
		}
		defer pick.Release()
		submit = pick.Element
	}
	if lbl, err := submit.Text(); err == nil && isLoginLabel(lbl) {
		return nil, fmt.Errorf("producthunt: not logged in (the submit button reads %q)", lbl)
	}
	if err := botpkg.ClickTrusted(page, submit); err != nil {
		return nil, fmt.Errorf("producthunt: clicking the comment submit button: %w", err)
	}

	want := needle(text)
	deadline := time.Now().Add(confirmTimeout)
	for {
		var found struct {
			ID string `json:"id"`
		}
		if err := botpkg.EvalJSON(page, newCommentJS, &found, prior, want); err == nil && found.ID != "" {
			return map[string]interface{}{"success": true, "launchURL": u, "commentID": found.ID}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("producthunt: comment not confirmed — it did not appear on %s within %v", u, confirmTimeout)
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, err
		}
	}
}
