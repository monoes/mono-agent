//go:build social

package instagram

// Page-side JavaScript. Every script is a function source run through
// botpkg.EvalJSON (CDP Runtime.evaluate when the page offers it, so
// Instagram's CSP does not block it); values reach the script as JSON call
// arguments, never spliced into the source. jsLib is prepended to every
// script by js().

// jsLib holds the helpers shared by the page scripts below.
const jsLib = `
const MARK = 'data-monoagent-ig';
const norm = (s) => (s || '').replace(/[\s ]+/g, ' ').trim();
// T is an element's text without the <title> text of its SVG icons (Instagram
// puts e.g. "Down chevron icon" inside the Following button).
const T = (el) => {
	if (!el) return '';
	const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
	let s = '', n;
	while ((n = w.nextNode())) {
		if (n.parentElement && n.parentElement.closest('svg')) continue;
		s += n.nodeValue;
	}
	return norm(s);
};
// VT is the page's visible text (hidden fixtures/templates excluded).
const VT = () => norm(document.body ? document.body.innerText : '');
const vis = (el) => !!(el && el.isConnected && (el.offsetWidth || el.offsetHeight || el.getClientRects().length));
const lbl = (el) => (el && el.getAttribute && el.getAttribute('aria-label')) || '';
const btnOf = (el) => (el ? el.closest('button, [role="button"], a') : null);
const btns = (root) => Array.from((root || document).querySelectorAll('button, [role="button"]')).filter(vis);
const isDisabled = (el) => !!(el && (el.disabled || el.getAttribute('aria-disabled') === 'true'));
const mark = (el, m) => { if (el && m) el.setAttribute(MARK, m); return el; };
const byText = (root, re) => btns(root).filter((b) => re.test(T(b)));
const RESERVED = new Set(['explore', 'reels', 'reel', 'direct', 'accounts', 'p', 'stories', 'tv', 'about', 'legal',
	'developer', 'web', 'api', 'challenge', 'emails', 'session', 'your_activity', 'privacy', 'create', 'notifications']);
// userOf returns the username a same-site profile link points at, or ''.
const userOf = (href) => {
	try {
		const u = new URL(href, location.href);
		if (!/(^|\.)instagram\.com$/.test(u.hostname) && u.hostname !== location.hostname) return '';
		const m = u.pathname.match(/^\/([A-Za-z0-9._]{1,30})\/?$/);
		return m && !RESERVED.has(m[1].toLowerCase()) ? m[1] : '';
	} catch (e) { return ''; }
};
// postRef parses a post/reel link: {url, shortcode, author}.
const postRef = (href) => {
	try {
		const u = new URL(href, location.href);
		const m = u.pathname.match(/^\/(?:([A-Za-z0-9._]{1,30})\/)?(p|reel|tv)\/([A-Za-z0-9_-]+)\/?/);
		if (!m || (m[1] && RESERVED.has(m[1].toLowerCase()))) return null;
		return { url: 'https://www.instagram.com' + m[0].replace(/\/?$/, '/'), shortcode: m[3], kind: m[2], author: m[1] || '' };
	} catch (e) { return null; }
};
const isReply = (b) => /^reply$/i.test(T(b)) && !b.querySelector('svg');
const LIKE_SVG = 'svg[aria-label="Like"], svg[aria-label="Unlike"]';
const svgSize = (s) => { const r = s.getBoundingClientRect(); return Math.max(r.width, r.height, +s.getAttribute('height') || 0); };

// postBars returns the post action bars: innermost <section>s holding a
// Comment icon and a Like/Unlike icon that is not inside a comment list.
const postBars = () => {
	const own = (sec) => Array.from(sec.querySelectorAll(LIKE_SVG)).filter((s) => !s.closest('li, ul') && s.closest('section') === sec);
	const secs = Array.from(document.querySelectorAll('section')).filter((s) => s.querySelector('svg[aria-label="Comment"]') && own(s).length);
	return secs.filter((s) => !secs.some((o) => o !== s && s.contains(o))).map((s) => ({ sec: s, likes: own(s) }));
};
// postLike returns the like icon of the post's action bar ({svg, count}).
const postLike = () => {
	const bars = postBars();
	if (!bars.length) return { svg: null, count: 0 };
	const likes = bars[0].likes.slice().sort((a, b) => svgSize(b) - svgSize(a));
	return { svg: likes[0], count: bars.length };
};

// comments lists the rendered comments (not the caption, which has no Reply
// button) as {el, body, reply, like, author, text, liked}.
const comments = () => {
	const out = [];
	const seen = new Set();
	for (const rb of Array.from(document.querySelectorAll('button, [role="button"]')).filter(isReply)) {
		let body = rb.parentElement;
		while (body && body !== document.body) {
			if (body.querySelector('time[datetime]') && Array.from(body.querySelectorAll('a[href]')).some((a) => userOf(a.getAttribute('href')))) break;
			body = body.parentElement;
		}
		if (!body || body === document.body || seen.has(body)) continue;
		seen.add(body);
		// Grow to the row that also holds this comment's like button, without
		// swallowing a neighbouring comment (a second Reply button).
		let el = body;
		while (!el.querySelector(LIKE_SVG) && el.parentElement && el.parentElement !== document.body) {
			const p = el.parentElement;
			if (Array.from(p.querySelectorAll('button, [role="button"]')).filter(isReply).length > 1) break;
			el = p;
		}
		const likeSvg = el.querySelector(LIKE_SVG);
		const authorLink = Array.from(body.querySelectorAll('a[href]')).find((a) => userOf(a.getAttribute('href')));
		const author = authorLink ? userOf(authorLink.getAttribute('href')) : '';
		let text = '';
		for (const s of body.querySelectorAll('span, h1, h2, h3, div')) {
			if (s.querySelector('div, span, time') && !s.matches('span[dir="auto"]')) continue;
			const cb = s.closest('button, [role="button"], time');
			if ((cb && body.contains(cb)) || (authorLink && (authorLink.contains(s) || s.contains(authorLink)))) continue;
			const t = T(s);
			if (!t || t === author || /^(\d+[smhdw]|\d+ (likes?|repl(y|ies))|see translation|reply|edited|verified)$/i.test(t)) continue;
			if (t.length > text.length) text = t;
		}
		const time = body.querySelector('time[datetime]');
		out.push({ el, body, reply: rb, like: likeSvg, author, text, liked: !!likeSvg && lbl(likeSvg) === 'Unlike',
			timestamp: time ? time.getAttribute('datetime') : '' });
	}
	return out;
};

// commentBox finds the post's "Add a comment…" input.
const commentBox = () => {
	const sels = ['textarea[aria-label^="Add a comment"]', 'textarea[placeholder^="Add a comment"]',
		'[contenteditable="true"][aria-label^="Add a comment"]', 'form textarea'];
	for (const sel of sels) {
		const el = Array.from(document.querySelectorAll(sel)).find(vis);
		if (el) return el;
	}
	return null;
};
const boxValue = (el) => (el ? ('value' in el ? el.value : T(el)) : '');
// postButtonFor finds the "Post" button that submits box.
const postButtonFor = (box) => {
	let root = box;
	for (let i = 0; i < 6 && root; i++, root = root.parentElement) {
		const b = btns(root).find((x) => /^post$/i.test(T(x)));
		if (b) return b;
	}
	return null;
};
const errorToast = () => {
	const t = VT();
	const m = t.match(/(couldn't post comment|could not post comment|couldn't send|not delivered|try again later|something went wrong|action blocked)/i);
	return m ? m[1] : '';
};

// hdr is the profile header; profileButton finds a header button whose text
// matches re.
const hdr = () => document.querySelector('main header') || document.querySelector('header');
const profileButton = (re) => { const h = hdr(); return h ? byText(h, re)[0] || null : null; };
const followState = () => {
	const h = hdr();
	if (!h) return 'no_header';
	const texts = btns(h).map(T);
	if (texts.some((t) => /^(following|requested)$/i.test(t))) return texts.some((t) => /^requested$/i.test(t)) ? 'requested' : 'following';
	if (texts.some((t) => /^follow( back)?$/i.test(t))) return 'not_following';
	return 'unknown';
};
const dialogs = () => Array.from(document.querySelectorAll('[role="dialog"]')).filter(vis);
`

// js wraps body (a function source) so it runs with jsLib in scope.
func js(body string) string {
	return "(...args) => {\n" + jsLib + "\nreturn (" + body + ")(...args);\n}"
}
