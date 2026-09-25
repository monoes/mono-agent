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
// REL_TIME is a relative-time label: "22 s", "3h", "5 min", "2 w", "1 y".
const REL_TIME = /^\d+\s*(s|m|h|d|w|y|sec|secs|min|mins|hr|hrs|wk|wks)$/i;
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
			// Never the relative-time label ("22 s", "3 h": a <time>, usually
			// inside the comment's permalink) nor any other link's text — the
			// body span holds mention links, it is never inside one.
			if (s.closest('time') || s.querySelector('time')) continue;
			const cb = s.closest('button, [role="button"], a[href]');
			if ((cb && body.contains(cb)) || (authorLink && (authorLink.contains(s) || s.contains(authorLink)))) continue;
			const t = T(s);
			if (!t || t === author || REL_TIME.test(t) || /^(\d[\d,]* (likes?|repl(y|ies))|see translation|reply|like|edited|verified|•)$/i.test(t)) continue;
			if (t.length > text.length) text = t;
		}
		const time = body.querySelector('time[datetime]');
		out.push({ el, body, reply: rb, like: likeSvg, author, text, liked: !!likeSvg && lbl(likeSvg) === 'Unlike',
			timestamp: time ? time.getAttribute('datetime') : '' });
	}
	return out;
};

// embeddedMedia returns the post object for shortcode sc from the JSON
// Instagram embeds in the page (script[type="application/json"]: the
// logged-in xdt_api__v1__media__shortcode__web_info items and the logged-out
// xig_polaris_media alike carry code, media_type, product_type, caption,
// user, carousel_media, image_versions2, video_versions), or null. Of the
// objects with that code the one with the most post fields wins (the
// "more posts" grid repeats a trimmed copy).
const embeddedMedia = (sc) => {
	if (!sc) return null;
	const keys = ['media_type', 'product_type', 'caption', 'user', 'owner', 'carousel_media', 'image_versions2',
		'video_versions', 'display_uri', 'display_url', 'edge_sidecar_to_children', 'edge_media_to_caption'];
	let best = null, score = 0;
	const walk = (o, depth) => {
		if (!o || typeof o !== 'object' || depth > 80) return;
		if (Array.isArray(o)) { for (const v of o) walk(v, depth + 1); return; }
		if (o.code === sc || o.shortcode === sc) {
			const n = keys.filter((k) => o[k] != null).length;
			if (n > score) { best = o; score = n; }
		}
		for (const k in o) walk(o[k], depth + 1);
	};
	for (const s of document.querySelectorAll('script[type="application/json"]')) {
		const t = s.textContent || '';
		if (!t.includes('"' + sc + '"')) continue;
		try { walk(JSON.parse(t), 0); } catch (e) {}
	}
	return best;
};
// mediaKind classifies an embedded post object: carousel, reel, video, image.
const mediaKind = (o) => {
	const tn = String(o.__typename || '');
	if (o.media_type === 8 || o.product_type === 'carousel_container' || /carousel|sidecar/i.test(tn) ||
		(o.carousel_media || []).length > 1 || o.edge_sidecar_to_children) return 'carousel';
	if (o.product_type === 'clips' || /clips|reel/i.test(tn)) return 'reel';
	if (o.media_type === 2 || o.is_video === true || /video/i.test(tn)) return 'video';
	if (o.media_type === 1 || /image|photo/i.test(tn)) return 'image';
	return '';
};
// mediaURLs lists an embedded post's own media: per item its video, else its
// largest image.
const mediaURLs = (o) => {
	const items = (o.carousel_media && o.carousel_media.length && o.carousel_media) ||
		(o.edge_sidecar_to_children && (o.edge_sidecar_to_children.edges || []).map((e) => e.node)) || [o];
	const out = [];
	for (const it of items) {
		if (!it) continue;
		const v = (it.video_versions || [])[0];
		const c = ((it.image_versions2 && it.image_versions2.candidates) || [])[0];
		const u = (v && v.url) || it.video_url || (c && c.url) || it.display_uri || it.display_url || '';
		if (u && !/^blob:/.test(u)) out.push(u);
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
// STAT is a profile header count: "1,234 followers", "32.2k followers",
// "56 following", "12 posts".
const STAT = /^([\d.,]+\s*[kmb]?)\s*(posts?|followers?|following)$/i;
// headerStats finds the profile header's counts, whatever the layout (a
// <ul><li> list, or — the 2026 layout — bare <div>s holding
// <a role="link" href="#">): {posts|followers|following: {el, target, num}}.
// el is the innermost element reading "<n> <kind>", target the link/button
// to click (the header's own element when there is none), num the exact
// count from a title attribute when the page carries one.
const headerStats = (h) => {
	const out = {};
	if (!h) return out;
	for (const el of h.querySelectorAll('*')) {
		if (el.closest('[role="menu"], svg')) continue;
		const m = T(el).match(STAT);
		if (!m) continue;
		const kind = /^post/i.test(m[2]) ? 'posts' : (/^follower/i.test(m[2]) ? 'followers' : 'following');
		const titled = el.matches('[title]') ? el : el.querySelector('[title]');
		const exact = titled && /^[\d.,\s]+$/.test(titled.getAttribute('title') || '') ? titled.getAttribute('title') : '';
		const c = el.closest('a, [role="link"], [role="button"], button');
		// Document order visits ancestors first: a descendant that still
		// reads "<n> <kind>" replaces its ancestor.
		out[kind] = { el, target: c && h.contains(c) ? c : el, num: exact || m[1].trim() };
	}
	return out;
};
// linkDest resolves a link through Instagram's l.instagram.com shim.
const linkDest = (href) => {
	try {
		const u = new URL(href, location.href);
		const d = /(^|\.)l\.instagram\.com$/i.test(u.hostname) && u.searchParams.get('u');
		return d ? new URL(d) : u;
	} catch (e) { return null; }
};
// isThreadsBadge: the header's Threads badge links to threads.net/.com.
const isThreadsBadge = (a) => {
	const d = linkDest(a.getAttribute('href') || '');
	return !!(d && /(^|\.)threads\.(net|com)$/i.test(d.hostname)) || /threads/i.test(lbl(a)) ||
		!!a.querySelector('svg[aria-label*="Threads" i], [aria-label*="Threads" i]');
};
const dialogs = () => Array.from(document.querySelectorAll('[role="dialog"]')).filter(vis);
`

// js wraps body (a function source) so it runs with jsLib in scope.
func js(body string) string {
	return "(...args) => {\n" + jsLib + "\nreturn (" + body + ")(...args);\n}"
}
