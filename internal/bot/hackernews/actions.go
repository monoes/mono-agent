//go:build !nosocial

package hackernews

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// Every page read here goes through botpkg.EvalJSON: Hacker News serves a
// CSP without 'unsafe-eval', which blocks the extension's plain Eval, while
// EvalJSON uses the DevTools Runtime.evaluate path and passes arguments as
// JSON, never spliced into the script source.

const hnOrigin = "https://news.ycombinator.com"

// maxCommentPages bounds how many "More" pages ListComments follows.
const maxCommentPages = 20

var (
	// pollInterval paces the post-write confirmation loops.
	pollInterval = 250 * time.Millisecond
	// controlWait is how long a form control may take to appear before
	// the Jev fallback (or the error) takes over.
	controlWait = 8 * time.Second
	// confirmTimeout bounds how long a write waits for its result to show.
	confirmTimeout = 5 * time.Second
	// readBackAttempts is how often SubmitPost reloads /submitted looking
	// for the new item before giving up (without resubmitting).
	readBackAttempts = 3
	readBackDelay    = 2 * time.Second
)

var itemIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// checkItemID accepts only numeric Hacker News item ids, so an id can never
// smuggle anything into a URL or a script.
func checkItemID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !itemIDRe.MatchString(id) {
		return "", fmt.Errorf("hackernews: item id must be numeric, got %q", id)
	}
	return id, nil
}

func itemURL(id string) string { return hnOrigin + "/item?id=" + url.QueryEscape(id) }

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
		return fmt.Errorf("hackernews: navigate to %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("hackernews: %s did not load: %w", u, err)
	}
	return nil
}

// pageState is what every flow checks on the current document: where it is,
// who is logged in (#me in the top bar), and the start of the visible text
// (Hacker News reports errors and rate limits as bare text pages).
type pageState struct {
	URL  string `json:"url"`
	User string `json:"user"`
	Text string `json:"text"`
	// Lines is the start of the visible text with line breaks kept (one
	// non-empty, whitespace-collapsed line each), so a refusal can be quoted
	// without the login form that follows it.
	Lines string `json:"lines"`
	// Main is true on regular HN pages (#hnmain); refusals such as the
	// rate-limit message are bare text pages without it.
	Main bool `json:"main"`
}

// refusal returns HN's refusal message when the page is a bare refusal
// page, or "". Regular pages are never scanned, so a comment that happens
// to quote a refusal phrase is not mistaken for one.
func (s pageState) refusal() string {
	if s.Main {
		return ""
	}
	if p := problem(s.Lines); p != "" {
		return p
	}
	return ""
}

const pageStateJS = `() => {
	const me = document.getElementById('me');
	const b = document.body;
	const text = b ? (b.innerText || b.textContent || '') : '';
	const lines = text.split(/\n/).map(l => l.replace(/\s+/g, ' ').trim()).filter(Boolean).join('\n').slice(0, 600);
	return { url: location.href, user: me ? me.textContent.trim() : '', text: text.replace(/\s+/g, ' ').trim().slice(0, 600), lines, main: !!document.getElementById('hnmain') };
}`

func readState(page browser.PageInterface) (pageState, error) {
	var st pageState
	if err := botpkg.EvalJSON(page, pageStateJS, &st); err != nil {
		return st, fmt.Errorf("hackernews: reading page state: %w", err)
	}
	return st, nil
}

// problemPhrases are Hacker News's refusal messages (lower-case).
var problemPhrases = []string{
	"you're posting too fast",
	"posting too fast",
	"not able to serve your requests this quickly",
	"you have to be logged in",
	"please log in",
	"please try again",
	"that comment is too long",
	"you can't post",
	"validation required",
	"we've limited requests",
}

// maxRefusal caps how much of a refusal is quoted in an error.
const maxRefusal = 160

// problem returns the refusal message on the page, or "". Only the sentence
// carrying the refusal is returned (capped at maxRefusal characters): a
// logged-out page continues with the login and create-account forms
// ("Login username: password: …"), which must not end up in the error.
func problem(text string) string {
	for _, line := range strings.Split(text, "\n") {
		low := strings.ToLower(line)
		for _, p := range problemPhrases {
			if i := strings.Index(low, p); i >= 0 {
				return clip(sentenceAt(line, i), maxRefusal)
			}
		}
	}
	return ""
}

// sentenceAt returns the sentence of line that contains byte offset i: from
// just after the previous ". ", "! " or "? " to the next sentence end
// (inclusive of its punctuation).
func sentenceAt(line string, i int) string {
	start := 0
	for j := 0; j+1 < i && j+1 < len(line); j++ {
		if strings.ContainsRune(".!?", rune(line[j])) && line[j+1] == ' ' {
			start = j + 2
		}
	}
	end := len(line)
	for j := i; j < len(line); j++ {
		if strings.ContainsRune(".!?", rune(line[j])) && (j+1 == len(line) || line[j+1] == ' ') {
			end = j + 1
			break
		}
	}
	return strings.TrimSpace(line[start:end])
}

// clip shortens s to at most n runes, marking a cut with "…".
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// normText is how posted text is compared with what Hacker News renders:
// HN turns *x* into italics and reflows whitespace, so asterisks go and runs
// of whitespace collapse; the comparison is case-insensitive.
func normText(s string) string {
	s = strings.ReplaceAll(s, "*", "")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// needle is what a confirmation looks for in a rendered comment: the start
// of the normalised text with all whitespace removed (HN turns blank lines
// into <p> elements, whose textContent has no separator at all).
func needle(s string) string {
	n := []rune(strings.Join(strings.Fields(normText(s)), ""))
	if len(n) > 80 {
		n = n[:80]
	}
	return string(n)
}

// findControl returns the first element matching primary/alts, or asks Jev
// (when the bot has a picker) for the element described by target. The
// returned release func must be called when the element is no longer used.
func (b *HackerNewsBot) findControl(ctx context.Context, page browser.PageInterface, primary string, alts []string, timeout time.Duration, target jevpick.Target) (browser.ElementHandle, func(), error) {
	el, _, err := botpkg.FindFirst(page, primary, alts, timeout)
	if err == nil {
		return el, func() {}, nil
	}
	if !b.JevAvailable(page) {
		return nil, nil, err
	}
	pick, jerr := b.JevElement(ctx, page, target)
	if jerr != nil {
		return nil, nil, fmt.Errorf("%w (jev fallback: %v)", err, jerr)
	}
	return pick.Element, pick.Release, nil
}

// typeAndCheck types text into el and verifies the field now holds it, so a
// half-typed or dropped value is never submitted.
func typeAndCheck(ctx context.Context, page browser.PageInterface, el browser.ElementHandle, text, what string) error {
	if err := botpkg.TypeInto(ctx, page, el, text); err != nil {
		return fmt.Errorf("hackernews: typing %s: %w", what, err)
	}
	v, err := el.Property("value")
	if err != nil {
		return fmt.Errorf("hackernews: reading back %s: %w", what, err)
	}
	got, _ := v.(string)
	if normText(got) != normText(text) {
		return fmt.Errorf("hackernews: %s field holds %q after typing, want %q — not submitting", what, got, text)
	}
	return nil
}

// ---------------------------------------------------------------------------
// submit_post
// ---------------------------------------------------------------------------

type submittedRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Link  string `json:"link"`
}

// submittedRowsJS lists the stories on a /submitted?id=<user> page.
const submittedRowsJS = `() => Array.from(document.querySelectorAll('tr.athing')).map(r => {
	const a = r.querySelector('.titleline > a');
	return { id: r.id, title: a ? a.textContent.trim() : '', link: a ? a.href : '' };
})`

func submittedURL(user string) string {
	return hnOrigin + "/submitted?id=" + url.QueryEscape(user)
}

func readSubmitted(page browser.PageInterface, user string) ([]submittedRow, error) {
	if err := open(page, submittedURL(user)); err != nil {
		return nil, err
	}
	var rows []submittedRow
	if err := botpkg.EvalJSON(page, submittedRowsJS, &rows); err != nil {
		return nil, fmt.Errorf("hackernews: reading /submitted: %w", err)
	}
	return rows, nil
}

// sameLink compares two URLs loosely (scheme, trailing slash and case of
// the host do not matter).
func sameLink(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(strings.ToLower(s))
		s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
		s = strings.TrimPrefix(s, "www.")
		return strings.TrimSuffix(s, "/")
	}
	return a != "" && b != "" && norm(a) == norm(b)
}

// SubmitPost submits a story through /submit and confirms it by finding the
// new item on the logged-in user's /submitted?id=<user> page: a story that
// was not listed before the submit and carries the submitted title (or
// link). HN answers a duplicate URL by redirecting to the existing item and
// a rate limit with a bare text page; both are errors. Once the form has
// been submitted, a failed read-back is reported as an error that says so —
// the action never resubmits.
func (b *HackerNewsBot) SubmitPost(ctx context.Context, page browser.PageInterface, title, linkURL, text string) (map[string]interface{}, error) {
	title = strings.TrimSpace(title)
	linkURL = strings.TrimSpace(linkURL)
	if title == "" {
		return nil, fmt.Errorf("hackernews: title is required")
	}
	if linkURL != "" {
		u, err := url.Parse(linkURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("hackernews: url must be an absolute http(s) URL, got %q", linkURL)
		}
	}

	if err := open(page, hnOrigin+"/submit"); err != nil {
		return nil, err
	}
	st, err := readState(page)
	if err != nil {
		return nil, err
	}
	if st.User == "" {
		if p := st.refusal(); p != "" {
			return nil, fmt.Errorf("hackernews: submit page refused: %s", p)
		}
		return nil, fmt.Errorf("hackernews: not logged in (no #me on the submit page)")
	}
	user := st.User

	before, err := readSubmitted(page, user)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range before {
		known[r.ID] = true
	}

	if err := open(page, hnOrigin+"/submit"); err != nil {
		return nil, err
	}
	titleEl, release, err := b.findControl(ctx, page, "form input[name='title']", nil, controlWait, jevpick.Target{
		Kind: "fill", Intent: "the title field of the Hacker News submit form",
	})
	if err != nil {
		return nil, fmt.Errorf("hackernews: title field not found: %w", err)
	}
	defer release()
	if err := typeAndCheck(ctx, page, titleEl, title, "title"); err != nil {
		return nil, err
	}
	if linkURL != "" {
		urlEl, rel, err := b.findControl(ctx, page, "form input[name='url']", nil, controlWait, jevpick.Target{
			Kind: "fill", Intent: "the url field of the Hacker News submit form",
		})
		if err != nil {
			return nil, fmt.Errorf("hackernews: url field not found: %w", err)
		}
		defer rel()
		if err := typeAndCheck(ctx, page, urlEl, linkURL, "url"); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(text) != "" {
		textEl, rel, err := b.findControl(ctx, page, "form textarea[name='text']", nil, controlWait, jevpick.Target{
			Kind: "fill", Intent: "the text box of the Hacker News submit form",
		})
		if err != nil {
			return nil, fmt.Errorf("hackernews: text field not found: %w", err)
		}
		defer rel()
		if err := typeAndCheck(ctx, page, textEl, text, "text"); err != nil {
			return nil, err
		}
	}

	submit, rel, err := b.findControl(ctx, page, "form:has(input[name='title']) input[type='submit']",
		[]string{"form:has(input[name='title']) button[type='submit']"}, controlWait, jevpick.Target{
			Kind: "click", Intent: "the submit button of the Hacker News submit form",
		})
	if err != nil {
		return nil, fmt.Errorf("hackernews: submit button not found: %w", err)
	}
	defer rel()

	// From here on the post may exist: every failure says so, and nothing
	// submits again.
	const noRetry = "the form was submitted; not resubmitting to avoid a double post — check " // + URL
	navErr := botpkg.PageClickAndWaitNavigation(ctx, page, submit, 30*time.Second)
	after, err := readState(page)
	if err != nil {
		return nil, fmt.Errorf("hackernews: submission not confirmed (%v); %s%s", err, noRetry, submittedURL(user))
	}
	if p := after.refusal(); p != "" {
		return nil, fmt.Errorf("hackernews: Hacker News refused the submission: %s", p)
	}
	if navErr != nil {
		return nil, fmt.Errorf("hackernews: submission not confirmed (%v); %s%s", navErr, noRetry, submittedURL(user))
	}
	if u, perr := url.Parse(after.URL); perr == nil {
		switch u.Path {
		case "/item":
			return nil, fmt.Errorf("hackernews: the URL was already submitted — Hacker News redirected to existing item %s", u.Query().Get("id"))
		case "/submit", "/r":
			if has, _ := page.Has("form input[name='title']"); has {
				return nil, fmt.Errorf("hackernews: Hacker News rejected the submission: %s", after.Text)
			}
		}
	}

	wantTitle := normText(title)
	for attempt := 0; attempt < readBackAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, readBackDelay); err != nil {
				return nil, err
			}
		}
		rows, err := readSubmitted(page, user)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if known[r.ID] || !itemIDRe.MatchString(r.ID) {
				continue
			}
			if normText(r.Title) == wantTitle || (linkURL != "" && sameLink(r.Link, linkURL)) {
				return map[string]interface{}{
					"id":     r.ID,
					"title":  r.Title,
					"url":    itemURL(r.ID),
					"link":   r.Link,
					"author": user,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("hackernews: submission not confirmed — no new item titled %q on /submitted; %s%s", title, noRetry, submittedURL(user))
}

// ---------------------------------------------------------------------------
// reply_to_comment
// ---------------------------------------------------------------------------

// buildReplyURL builds the URL for an item's reply form. itemID is passed
// through net/url's query encoding rather than being interpolated into the
// URL with fmt.Sprintf, so a hostile itemID (e.g. containing "&", "#", or
// other reserved URL characters) can never corrupt the query string or
// smuggle extra parameters — it is always confined to the "id" and "goto"
// query values, however it's spelled.
func buildReplyURL(itemID string) string {
	v := url.Values{}
	v.Set("id", itemID)
	v.Set("goto", "item?id="+itemID)
	return hnOrigin + "/reply?" + v.Encode()
}

type postedComment struct {
	ID string `json:"id"`
}

// findMyCommentJS finds, on the page HN redirected to after a reply, a
// comment by user whose normalised text contains needle and whose id is
// greater than the parent's (ids grow over time). The newest match wins.
const findMyCommentJS = `(user, needle, parent) => {
	const norm = s => s.replace(/[*\s]+/g, '').toLowerCase();
	let best = null;
	for (const row of document.querySelectorAll('tr.athing.comtr')) {
		const a = row.querySelector('a.hnuser');
		const body = row.querySelector('.commtext');
		if (!a || !body || a.textContent.trim() !== user) continue;
		if (!/^[0-9]+$/.test(row.id) || (parent && BigInt(row.id) <= BigInt(parent))) continue;
		const c = body.cloneNode(true);
		c.querySelectorAll('.reply').forEach(e => e.remove());
		if (!norm(c.textContent).includes(needle)) continue;
		if (!best || BigInt(row.id) > BigInt(best)) best = row.id;
	}
	return { id: best || '' };
}`

// ReplyToComment replies to an item (story or comment) through its reply
// form, then confirms the reply: after the submit HN redirects to the item,
// where a comment by the logged-in user with the typed text must appear.
// Rate-limit and login pages are reported as errors.
func (b *HackerNewsBot) ReplyToComment(ctx context.Context, page browser.PageInterface, itemID, text string) (map[string]interface{}, error) {
	id, err := checkItemID(itemID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("hackernews: reply text is required")
	}
	if err := open(page, buildReplyURL(id)); err != nil {
		return nil, err
	}
	st, err := readState(page)
	if err != nil {
		return nil, err
	}
	if st.User == "" {
		if p := st.refusal(); p != "" {
			return nil, fmt.Errorf("hackernews: reply page refused: %s", p)
		}
		return nil, fmt.Errorf("hackernews: not logged in (no #me on the reply page)")
	}
	if p := st.refusal(); p != "" {
		return nil, fmt.Errorf("hackernews: reply page refused: %s", p)
	}

	box, release, err := b.findControl(ctx, page, "form[action='comment'] textarea[name='text']",
		[]string{"form:has(input[name='parent']) textarea"}, controlWait, jevpick.Target{
			Kind: "fill", Intent: fmt.Sprintf("the reply text box on this Hacker News reply page (replying to item %s)", id),
		})
	if err != nil {
		return nil, fmt.Errorf("hackernews: reply box not found on %s: %w", st.URL, err)
	}
	defer release()
	if err := typeAndCheck(ctx, page, box, text, "reply"); err != nil {
		return nil, err
	}
	submit, rel, err := b.findControl(ctx, page, "form[action='comment'] input[type='submit']",
		[]string{"form:has(input[name='parent']) input[type='submit']"}, controlWait, jevpick.Target{
			Kind: "click", Intent: "the submit ('reply') button of the Hacker News reply form",
		})
	if err != nil {
		return nil, fmt.Errorf("hackernews: reply button not found: %w", err)
	}
	defer rel()

	navErr := botpkg.PageClickAndWaitNavigation(ctx, page, submit, 30*time.Second)
	after, err := readState(page)
	if err != nil {
		return nil, fmt.Errorf("hackernews: reply not confirmed: %w", err)
	}
	if p := after.refusal(); p != "" {
		return nil, fmt.Errorf("hackernews: Hacker News refused the reply: %s", p)
	}
	if navErr != nil {
		return nil, fmt.Errorf("hackernews: reply not confirmed: %w", navErr)
	}

	want := needle(text)
	deadline := time.Now().Add(confirmTimeout)
	for {
		var found postedComment
		if err := botpkg.EvalJSON(page, findMyCommentJS, &found, st.User, want, id); err == nil && found.ID != "" {
			return map[string]interface{}{
				"success":   true,
				"itemID":    id,
				"commentID": found.ID,
				"url":       itemURL(found.ID),
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("hackernews: reply not confirmed — no comment by %s with the reply text on %s", st.User, after.URL)
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, err
		}
	}
}

// ---------------------------------------------------------------------------
// list_comments
// ---------------------------------------------------------------------------

// Comment is one Hacker News comment.
type Comment struct {
	ID       string `json:"id"`
	Author   string `json:"author"`
	Text     string `json:"text"`
	Depth    int    `json:"depth"`
	ParentID string `json:"parentId"`
	Age      string `json:"age"`
	Deleted  bool   `json:"deleted"`
}

func (c Comment) toMap() map[string]interface{} {
	return map[string]interface{}{
		"id": c.ID, "author": c.Author, "text": c.Text, "depth": c.Depth,
		"parentId": c.ParentID, "age": c.Age, "deleted": c.Deleted,
	}
}

type commentPage struct {
	Comments []Comment `json:"comments"`
	Stack    []string  `json:"stack"`
	Next     string    `json:"next"`
	Found    bool      `json:"found"`
}

// commentsJS reads one item page's comment tree. HN renders the tree as a
// flat list of tr.athing.comtr rows whose td.ind[indent] is the depth; the
// parent is the closest previous row one level up. stack carries the open
// ancestors across "More" pages.
const commentsJS = `(itemID, stack) => {
	const found = !!document.getElementById(itemID);
	const out = [];
	stack = Array.isArray(stack) ? stack.slice() : [];
	for (const row of document.querySelectorAll('tr.athing.comtr')) {
		const ind = row.querySelector('td.ind');
		let depth = ind ? parseInt(ind.getAttribute('indent') || '', 10) : 0;
		if (isNaN(depth)) {
			const img = ind && ind.querySelector('img');
			depth = img ? Math.round((parseInt(img.getAttribute('width') || '0', 10) || 0) / 40) : 0;
		}
		stack.length = depth;
		const parentId = depth > 0 ? (stack[depth - 1] || '') : itemID;
		stack[depth] = row.id;
		const a = row.querySelector('a.hnuser');
		const body = row.querySelector('.commtext');
		let text = '';
		if (body) {
			const c = body.cloneNode(true);
			c.querySelectorAll('.reply').forEach(e => e.remove());
			c.querySelectorAll('p').forEach(p => p.prepend('\n\n'));
			text = c.textContent.replace(/[ \t]+\n/g, '\n').trim();
		}
		const age = row.querySelector('.age');
		out.push({ id: row.id, author: a ? a.textContent.trim() : '', text, depth, parentId,
			age: age ? (age.getAttribute('title') || '').split(' ')[0] : '', deleted: !body });
	}
	const more = document.querySelector('a.morelink');
	return { comments: out, stack: stack.map(x => x || ''), next: more ? more.href : '', found };
}`

// ListComments returns the comments on an item, in page order, following
// the "More" links (up to maxCommentPages pages). Every comment carries its
// depth (0 = top level) and parentId (the item id for top-level comments);
// topLevelOnly keeps depth-0 comments only.
func (b *HackerNewsBot) ListComments(ctx context.Context, page browser.PageInterface, itemID string, topLevelOnly bool) ([]map[string]interface{}, error) {
	id, err := checkItemID(itemID)
	if err != nil {
		return nil, err
	}
	out := []map[string]interface{}{}
	next := itemURL(id)
	var stack []string
	seen := map[string]bool{}
	for n := 0; n < maxCommentPages && next != ""; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := open(page, next); err != nil {
			return nil, err
		}
		var cp commentPage
		if err := botpkg.EvalJSON(page, commentsJS, &cp, id, stack); err != nil {
			return nil, fmt.Errorf("hackernews: reading comments: %w", err)
		}
		if n == 0 && !cp.Found {
			st, _ := readState(page)
			return nil, fmt.Errorf("hackernews: item %s not found on %s: %s", id, next, st.Text)
		}
		for _, c := range cp.Comments {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			if topLevelOnly && c.Depth != 0 {
				continue
			}
			out = append(out, c.toMap())
		}
		stack = cp.Stack
		next = ""
		if u, err := url.Parse(cp.Next); err == nil && cp.Next != "" &&
			u.Host == "news.ycombinator.com" && u.Path == "/item" && u.Query().Get("id") == id {
			next = u.String()
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// get_post_metrics
// ---------------------------------------------------------------------------

type metrics struct {
	Found    bool   `json:"found"`
	Points   *int   `json:"points"`
	Comments *int   `json:"comments"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Age      string `json:"age"`
	IsJob    bool   `json:"isJob"`
}

// postMetricsScript reads an item page's score and comment count from the
// subtext row under the item. itemID is a function parameter, passed as a
// JSON argument by EvalJSON — never interpolated into this source. The
// comment count is the item link reading "N comments" ("discuss" = 0); job
// posts have neither a score nor a comment link, and report null for both.
const postMetricsScript = `(itemID) => {
	const row = document.getElementById(itemID);
	if (!row || !row.classList.contains('athing')) return { found: false };
	const next = row.nextElementSibling;
	const sub = next ? next.querySelector('.subtext') : null;
	const scoreEl = document.getElementById('score_' + itemID);
	let comments = null;
	if (sub) {
		for (const a of sub.querySelectorAll('a')) {
			const href = a.getAttribute('href') || '';
			if (href !== 'item?id=' + itemID) continue;
			const t = a.textContent.replace(/ /g, ' ').trim();
			const m = t.match(/^([0-9]+)\s+comments?$/i);
			if (m) { comments = parseInt(m[1], 10); break; }
			if (/^discuss$/i.test(t)) { comments = 0; break; }
		}
	}
	const title = row.querySelector('.titleline > a');
	const author = sub ? sub.querySelector('a.hnuser') : null;
	const age = sub ? sub.querySelector('.age') : null;
	const points = scoreEl ? parseInt(scoreEl.textContent, 10) : null;
	return {
		found: true,
		points: Number.isNaN(points) ? null : points,
		comments,
		title: title ? title.textContent.trim() : '',
		author: author ? author.textContent.trim() : '',
		age: age ? (age.getAttribute('title') || '').split(' ')[0] : '',
		isJob: !!sub && !scoreEl && !author,
	};
}`

// postMetricsEvalArgs returns the EvalJSON script and arguments for reading
// a post's metrics; split out so the no-interpolation property is testable
// without a browser.
func postMetricsEvalArgs(itemID string) (string, []interface{}) {
	return postMetricsScript, []interface{}{itemID}
}

// GetPostMetrics reads an item's points and comment count. Points and
// comments are null when HN shows none (job posts).
func (b *HackerNewsBot) GetPostMetrics(ctx context.Context, page browser.PageInterface, itemID string) (map[string]interface{}, error) {
	id, err := checkItemID(itemID)
	if err != nil {
		return nil, err
	}
	if err := open(page, itemURL(id)); err != nil {
		return nil, err
	}
	js, args := postMetricsEvalArgs(id)
	var m metrics
	if err := botpkg.EvalJSON(page, js, &m, args...); err != nil {
		return nil, fmt.Errorf("hackernews: reading metrics: %w", err)
	}
	if !m.Found {
		st, _ := readState(page)
		return nil, fmt.Errorf("hackernews: item %s not found: %s", id, st.Text)
	}
	out := map[string]interface{}{
		"itemID": id, "title": m.Title, "author": m.Author, "age": m.Age, "isJob": m.IsJob,
		"points": nil, "comments": nil,
	}
	if m.Points != nil {
		out["points"] = *m.Points
	}
	if m.Comments != nil {
		out["comments"] = *m.Comments
	}
	return out, nil
}
