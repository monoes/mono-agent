/**
 * MonoAgent Bridge — capture provenance (meta.json)
 *
 * Builds the envelope's meta object from the page's own claims about itself:
 * <link rel=canonical>, Open Graph, article metadata, JSON-LD. Pure functions
 * over the same node tree readable.js uses, so every rule here is testable
 * from an HTML string (capture_meta.test.mjs).
 *
 * The dedupe key downstream is canonicalUrl + contentHash, so those two
 * fields are the ones worth being fussy about: the canonical URL is taken
 * from the page when it declares one, and the hash is computed over the
 * readable prose, not the markup — a page that only changed its ad slots
 * must hash the same.
 */

(function (root) {
  "use strict";

  const isText = (n) => n.tag === "#text";
  const kids = (n) => n.children || [];
  const attr = (n, name) => (n.attrs && n.attrs[name]) || "";

  // Query parameters that identify the campaign that sent you, not the
  // document. They must not fork one article into five library entries.
  const TRACKING_PARAMS = /^(utm_|fbclid|gclid|mc_cid|mc_eid|igshid|ref_src|ref_url|s_cid|spm|_hsenc|_hsmi|yclid|msclkid)/i;

  function findAll(tree, predicate, limit) {
    const out = [];
    (function walk(node) {
      for (const child of kids(node)) {
        if (isText(child)) continue;
        if (predicate(child)) out.push(child);
        if (limit && out.length >= limit) return;
        walk(child);
      }
    })(tree);
    return out;
  }

  function textOf(node) {
    if (isText(node)) return node.text || "";
    return kids(node).map(textOf).join("");
  }

  const clean = (s) => String(s || "").replace(/\s+/g, " ").trim();

  function parseUrl(href, base) {
    if (!href) return null;
    try {
      return new URL(href, base || undefined);
    } catch {
      return null;
    }
  }

  function absolute(href, base) {
    const url = parseUrl(href, base);
    return url ? url.href : "";
  }

  /** metaContent returns the first non-empty <meta> value among `keys`. */
  function metaContent(tree, keys) {
    const wanted = keys.map((k) => k.toLowerCase());
    for (const node of findAll(tree, (n) => n.tag === "meta")) {
      const key = (attr(node, "property") || attr(node, "name") || attr(node, "itemprop")).toLowerCase();
      if (!key || !wanted.includes(key)) continue;
      const value = clean(attr(node, "content"));
      if (value) return value;
    }
    return "";
  }

  function linkHref(tree, relPattern) {
    for (const node of findAll(tree, (n) => n.tag === "link")) {
      const rel = attr(node, "rel").toLowerCase();
      if (rel && relPattern.test(rel)) {
        const href = attr(node, "href");
        if (href) return href;
      }
    }
    return "";
  }

  /** jsonLd returns every JSON-LD object on the page, @graph entries included. */
  function jsonLd(tree) {
    const out = [];
    const push = (value) => {
      if (!value || typeof value !== "object") return;
      if (Array.isArray(value)) {
        value.forEach(push);
        return;
      }
      out.push(value);
      if (value["@graph"]) push(value["@graph"]);
    };
    for (const node of findAll(tree, (n) => n.tag === "script" && /ld\+json/i.test(attr(n, "type")))) {
      try {
        push(JSON.parse(textOf(node)));
      } catch {
        // A page with malformed JSON-LD is still a page worth saving.
      }
    }
    return out;
  }

  function jsonLdValue(blocks, keys) {
    for (const block of blocks) {
      for (const key of keys) {
        const value = block[key];
        if (!value) continue;
        if (typeof value === "string") return clean(value);
        if (Array.isArray(value)) {
          const first = value.find((v) => typeof v === "string" || (v && v.name));
          if (first) return clean(typeof first === "string" ? first : first.name);
        }
        if (typeof value === "object" && value.name) return clean(value.name);
      }
    }
    return "";
  }

  function normalizeDate(value) {
    const raw = clean(value);
    if (!raw) return null;
    const parsed = new Date(raw);
    if (Number.isNaN(parsed.getTime())) return null;
    return parsed.toISOString();
  }

  /**
   * canonicalOf resolves the URL this capture is filed under. It is half the
   * dedupe key, and capture.js keys the reader's saved highlights on it
   * (capture.js:308) — so a page that declares someone else's URL as its
   * canonical would pull that page's annotations into its own capture. The
   * page's claim is therefore honoured only when it is
   *
   *   - addressable at all: http(s), not `javascript:` or `data:`, the same
   *     allowlist markdown.js applies to links; and
   *   - about a document on the same host, so an https upgrade or a path
   *     rewrite is fine and a hop to another site is not.
   *
   * Anything else falls through to the normalized address bar, which is the
   * one URL we watched the browser go to.
   */
  function canonicalOf(tree, url) {
    const declared = linkHref(tree, /(^|\s)canonical(\s|$)/) || metaContent(tree, ["og:url"]);
    const claimed = parseUrl(declared, url);
    const page = parseUrl(url);
    const addressable = claimed && (claimed.protocol === "https:" || claimed.protocol === "http:");
    if (addressable && (!page || page.hostname === claimed.hostname)) return claimed.href;
    // No usable canonical: normalize the address bar instead — drop the
    // fragment and the campaign parameters, keep everything that addresses
    // the document.
    try {
      const u = new URL(url);
      u.hash = "";
      for (const key of [...u.searchParams.keys()]) {
        if (TRACKING_PARAMS.test(key)) u.searchParams.delete(key);
      }
      return u.href;
    } catch {
      return url || "";
    }
  }

  function bylineOf(tree, blocks) {
    const declared = metaContent(tree, [
      "author", "article:author", "og:article:author", "twitter:creator",
      "dc.creator", "parsely-author", "sailthru.author",
    ]);
    if (declared && !/^https?:/i.test(declared)) return declared;
    const fromLd = jsonLdValue(blocks, ["author", "creator"]);
    if (fromLd) return fromLd;
    const marked = findAll(
      tree,
      (n) =>
        /(^|\s)author(\s|$)/i.test(attr(n, "rel")) ||
        attr(n, "itemprop").toLowerCase() === "author" ||
        /byline|author/i.test(`${attr(n, "class")} ${attr(n, "id")}`),
      1
    )[0];
    const text = marked ? clean(textOf(marked)).replace(/^by\s+/i, "") : "";
    return text && text.length <= 120 ? text : "";
  }

  function publishedAtOf(tree, blocks) {
    const declared = metaContent(tree, [
      "article:published_time", "og:article:published_time", "datepublished",
      "date", "pubdate", "publish-date", "publication_date", "dc.date",
      "dc.date.issued", "parsely-pub-date", "sailthru.date",
    ]);
    const fromLd = jsonLdValue(blocks, ["datePublished", "dateCreated", "uploadDate"]);
    const timeEl = findAll(tree, (n) => n.tag === "time" && attr(n, "datetime"), 1)[0];
    return (
      normalizeDate(declared) ||
      normalizeDate(fromLd) ||
      (timeEl ? normalizeDate(attr(timeEl, "datetime")) : null)
    );
  }

  function faviconOf(tree, url) {
    const href =
      linkHref(tree, /(^|\s)(shortcut\s+)?icon(\s|$)/) ||
      linkHref(tree, /apple-touch-icon/) ||
      linkHref(tree, /mask-icon/);
    const resolved = absolute(href, url);
    if (resolved) return resolved;
    return absolute("/favicon.ico", url);
  }

  function titleOf(tree, fallback) {
    const og = metaContent(tree, ["og:title", "twitter:title"]);
    if (og) return og;
    const titleEl = findAll(tree, (n) => n.tag === "title", 1)[0];
    const fromHead = titleEl ? clean(textOf(titleEl)) : "";
    if (fromHead) return fromHead;
    const h1 = findAll(tree, (n) => n.tag === "h1", 1)[0];
    return h1 ? clean(textOf(h1)) : clean(fallback);
  }

  /**
   * buildSync assembles everything except the content hash, which needs
   * WebCrypto and therefore an await. Split out so the page side (which may
   * not be a secure context, and so may have no crypto.subtle at all) can
   * produce the metadata and let the service worker do the hashing.
   */
  function buildSync(input) {
    const tree = input.tree || { tag: "#root", attrs: {}, children: [] };
    const url = input.url || "";
    const blocks = jsonLd(tree);
    return {
      url,
      canonicalUrl: canonicalOf(tree, url),
      title: titleOf(tree, input.title),
      byline: bylineOf(tree, blocks) || null,
      publishedAt: publishedAtOf(tree, blocks),
      capturedAt: input.capturedAt || new Date().toISOString(),
      httpStatus: typeof input.httpStatus === "number" ? input.httpStatus : null,
      contentHash: null,
      favicon: faviconOf(tree, url) || null,
      selection: input.selection || null,
      note: input.note || null,
      tags: Array.isArray(input.tags) ? input.tags.filter(Boolean).map(clean) : [],
      collection: input.collection || null,
      source: input.source || "extension",
      siteName: metaContent(tree, ["og:site_name", "application-name"]) || null,
      lang: clean(attr(findAll(tree, (n) => n.tag === "html", 1)[0] || {}, "lang")) || null,
      excerpt: clean(input.excerpt) || metaContent(tree, ["og:description", "description"]) || null,
      wordCount: typeof input.wordCount === "number" ? input.wordCount : null,
    };
  }

  async function sha256Hex(text) {
    const bytes = new TextEncoder().encode(String(text || ""));
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
  }

  /** build is buildSync plus the content hash of the readable text. */
  async function build(input) {
    const meta = buildSync(input);
    if (input.text !== undefined && input.text !== null) {
      meta.contentHash = `sha256:${await sha256Hex(input.text)}`;
    }
    return meta;
  }

  root.MonoMeta = { build, buildSync, sha256Hex, normalizeDate, jsonLd, metaContent, canonicalOf };
})(globalThis);
