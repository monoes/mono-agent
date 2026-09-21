/**
 * MonoAgent Bridge — highlights as atomic notes (RCL-04)
 *
 * A highlight is a note in its own right. It has its own text, its own
 * comment, its own timestamp — and a link back to the page it was taken
 * from, which is what makes it worth keeping rather than a stray quote.
 *
 * This module is the model and the store. It runs in two worlds and touches
 * neither's APIs directly: the service worker (where it persists and builds
 * the capture artifact) and the page's isolated content-script world (where
 * highlight_page.js measures selections and paints them back). Storage is
 * always passed in, never reached for.
 *
 * THE ANCHOR is the part worth reading carefully. It is the same shape
 * citation.ts resolves — offsets plus a quote — but the offsets here are
 * measured against the PAGE'S VISIBLE TEXT, and the document that gets
 * indexed is `readable.md`, which is a Readability pass over that same DOM.
 * The two are close but not identical: boilerplate is stripped, markdown
 * syntax is added. So:
 *
 *   - `quote` is the TRUTH. The ingest side re-locates by it.
 *   - `startChar`/`endChar` are a HINT, used to disambiguate when the quote
 *     occurs more than once.
 *   - `prefix`/`suffix` disambiguate when the offsets have drifted too far
 *     to help.
 *   - `fragment` is a W3C `:~:text=` fragment, built the way citation.ts
 *     builds one, so the link lands on the sentence in a live page that has
 *     no id there.
 *
 * Writing the offsets down as if they were authoritative is how citations
 * rot. They are labelled as a hint here and treated as one on the other end.
 */

(function (root) {
  "use strict";

  /** Bump when the shape of a stored record changes. The ingest side reads
   *  this before trusting anything else in the file. */
  const VERSION = 1;

  const ARTIFACT_NAME = "highlights.json";
  const STORAGE_PREFIX = "mono-hl:";
  const INDEX_KEY = "mono-hl-index";

  // Bounds. Highlights are small, but chrome.storage.local is not infinite
  // and a runaway page must not be able to fill it.
  const MAX_PER_PAGE = 300;
  const MAX_PAGES = 400;
  const MAX_TEXT = 4000;
  const MAX_COMMENT = 2000;
  const CONTEXT_CHARS = 48;

  const COLORS = ["yellow", "green", "blue", "pink"];
  const DEFAULT_COLOR = "yellow";

  // Deliberately not borrowed from capture.js: this file also loads into the
  // page's content-script world, where capture.js is not present, and a
  // module that works in only one of its two homes is worse than eight
  // duplicated lines.
  function utf8ToBase64(text) {
    const bytes = new TextEncoder().encode(text);
    let binary = "";
    for (let i = 0; i < bytes.length; i += 0x8000) {
      binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    }
    return btoa(binary);
  }

  /** identityUrl: the same rule as saved.js and the ingest side — the
   *  fragment goes, nothing else is rewritten. */
  function identityUrl(raw) {
    const s = String(raw || "").trim();
    const hash = s.indexOf("#");
    return (hash === -1 ? s : s.slice(0, hash)).trim();
  }

  function key(url) {
    return STORAGE_PREFIX + identityUrl(url);
  }

  function newId() {
    const uuid = globalThis.crypto && crypto.randomUUID && crypto.randomUUID();
    return `hl-${uuid || `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
  }

  const clip = (value, max) => String(value == null ? "" : value).slice(0, max);
  const collapse = (s) => String(s || "").replace(/\s+/g, " ").trim();

  // ── Text fragments ───────────────────────────────────────────────

  const FRAGMENT_WORDS = 8;

  /** `,` and `-` are syntax inside a text fragment, so they cannot be raw. */
  function encodeFragmentPart(text) {
    return encodeURIComponent(text).replace(/-/g, "%2D").replace(/,/g, "%2C");
  }

  /**
   * A W3C scroll-to-text fragment for a passage — the same construction as
   * citation.ts's textFragment, including the `textStart,textEnd` form for
   * long passages, which survives the site re-flowing its markup between
   * the highlight and the click.
   */
  function textFragment(passage) {
    const text = collapse(passage).slice(0, 2000);
    if (!text) return "";
    const words = text.split(" ").filter(Boolean);
    if (words.length <= FRAGMENT_WORDS * 2) return `:~:text=${encodeFragmentPart(text)}`;
    const start = words.slice(0, FRAGMENT_WORDS).join(" ");
    const end = words.slice(-FRAGMENT_WORDS).join(" ");
    return `:~:text=${encodeFragmentPart(start)},${encodeFragmentPart(end)}`;
  }

  /** The page URL pointed at this highlight. */
  function highlightUrl(url, text) {
    const base = identityUrl(url);
    if (!base) return "";
    const fragment = textFragment(text);
    return fragment ? `${base}#${fragment}` : base;
  }

  // ── Locating ─────────────────────────────────────────────────────

  /**
   * locate measures an anchor for `quote` against `fullText`.
   *
   * `hint` is where the caller believes the quote is (a selection's own
   * offset). When the quote occurs several times — a phrase like "see
   * below" — the occurrence nearest the hint is the one meant, which is why
   * the hint is taken at all.
   */
  function locate(fullText, quote, hint) {
    const text = String(fullText || "");
    const needle = String(quote || "");
    if (!needle) return null;

    let best = -1;
    let bestDistance = Infinity;
    for (let at = text.indexOf(needle); at !== -1; at = text.indexOf(needle, at + 1)) {
      const distance = Number.isFinite(hint) ? Math.abs(at - hint) : 0;
      if (distance < bestDistance) {
        best = at;
        bestDistance = distance;
      }
      if (!Number.isFinite(hint)) break;
    }
    if (best === -1) return null;

    return {
      startChar: best,
      endChar: best + needle.length,
      prefix: collapse(text.slice(Math.max(0, best - CONTEXT_CHARS), best)),
      suffix: collapse(text.slice(best + needle.length, best + needle.length + CONTEXT_CHARS)),
    };
  }

  /**
   * relocate finds a stored highlight in a page that has changed since —
   * which every page has, because a highlight outlives the visit that made
   * it. Returns the start offset, or -1.
   *
   * The order is the order of trustworthiness:
   *
   *  1. the recorded offsets, IF the quote is still sitting at them. This is
   *     the common case and costs a substring compare;
   *  2. prefix + quote + suffix, which survives text being inserted above
   *     (an ad, a banner, a cookie notice) — everything moved, but the
   *     neighbourhood did not change;
   *  3. the occurrence nearest the recorded offset, for a page that reworded
   *     its surroundings;
   *  4. nothing. A highlight that cannot be found is NOT painted somewhere
   *     approximate — a mark on the wrong sentence is a lie about what
   *     someone read.
   */
  function relocate(fullText, anchor) {
    const text = String(fullText || "");
    const a = anchor || {};
    const quote = String(a.quote || "");
    if (!quote) return -1;

    const start = Number.isFinite(a.startChar) ? a.startChar : null;
    if (start !== null && text.substr(start, quote.length) === quote) return start;

    if (a.prefix || a.suffix) {
      const window = `${a.prefix || ""}${quote}${a.suffix || ""}`;
      const at = text.indexOf(window);
      if (at !== -1) return at + (a.prefix || "").length;
    }

    const found = locate(text, quote, start === null ? undefined : start);
    return found ? found.startChar : -1;
  }

  // ── Records ──────────────────────────────────────────────────────

  /**
   * makeHighlight builds one record. `text` is required; everything else has
   * a sane default, because this is called from a content script reacting to
   * a selection and has no business failing there.
   */
  function makeHighlight(input) {
    const spec = input || {};
    const text = collapse(clip(spec.text, MAX_TEXT));
    if (!text) return null;
    const at = spec.createdAt || new Date(spec.now || Date.now()).toISOString();
    const anchor = spec.anchor || {};
    return {
      id: spec.id || newId(),
      text,
      anchor: {
        // A hint, not a promise — see the header.
        startChar: Number.isFinite(anchor.startChar) ? Math.max(0, Math.floor(anchor.startChar)) : null,
        endChar: Number.isFinite(anchor.endChar) ? Math.max(0, Math.floor(anchor.endChar)) : null,
        quote: text,
        prefix: collapse(clip(anchor.prefix, CONTEXT_CHARS)),
        suffix: collapse(clip(anchor.suffix, CONTEXT_CHARS)),
        fragment: textFragment(text),
      },
      createdAt: at,
      comment: clip(spec.comment, MAX_COMMENT),
      color: COLORS.indexOf(spec.color) === -1 ? DEFAULT_COLOR : spec.color,
    };
  }

  /**
   * normalize is the read side of the same defensiveness the ingest layer
   * applies to meta.json: a record out of storage was written by an earlier
   * version of this code, or half-written when the worker was suspended.
   * Returns null rather than throwing.
   */
  function normalize(raw) {
    if (!raw || typeof raw !== "object") return null;
    const text = collapse(clip(raw.text, MAX_TEXT));
    if (!text) return null;
    return makeHighlight({
      id: typeof raw.id === "string" && raw.id ? raw.id : undefined,
      text,
      anchor: raw.anchor && typeof raw.anchor === "object" ? raw.anchor : {},
      createdAt: typeof raw.createdAt === "string" ? raw.createdAt : undefined,
      comment: raw.comment,
      color: raw.color,
    });
  }

  function normalizeAll(list) {
    const out = [];
    const seen = new Set();
    for (const raw of Array.isArray(list) ? list : []) {
      const record = normalize(raw);
      if (!record || seen.has(record.id)) continue;
      seen.add(record.id);
      out.push(record);
    }
    return out.slice(0, MAX_PER_PAGE);
  }

  // ── Storage ──────────────────────────────────────────────────────

  async function load(storage, url) {
    const k = key(url);
    try {
      const bag = await storage.get(k);
      return normalizeAll(bag && bag[k]);
    } catch {
      // Storage unavailable is an empty page's worth of highlights, not an
      // error: the page is about to be rendered either way.
      return [];
    }
  }

  async function save(storage, url, records) {
    const k = key(url);
    const kept = normalizeAll(records);
    try {
      if (kept.length) await storage.set({ [k]: kept });
      else if (storage.remove) await storage.remove(k);
      else await storage.set({ [k]: [] });
      await touchIndex(storage, identityUrl(url), kept.length);
    } catch {
      // Best effort. The caller already has the records in hand.
    }
    return kept;
  }

  /**
   * touchIndex keeps a small directory of which pages have highlights: what
   * the popup lists, and what bounds the whole store. Without it, finding
   * "pages I have highlighted" means scanning every key in storage.
   */
  async function touchIndex(storage, url, count) {
    let index = {};
    try {
      const bag = await storage.get(INDEX_KEY);
      index = (bag && bag[INDEX_KEY]) || {};
      if (typeof index !== "object" || Array.isArray(index)) index = {};
    } catch {
      index = {};
    }
    if (count > 0) index[url] = { count, at: new Date().toISOString() };
    else delete index[url];

    const urls = Object.keys(index);
    if (urls.length > MAX_PAGES) {
      // Oldest-touched first. Their highlight keys go with them, or the
      // store keeps bytes nothing can find again.
      urls
        .sort((a, b) => String(index[a].at).localeCompare(String(index[b].at)))
        .slice(0, urls.length - MAX_PAGES)
        .forEach((old) => {
          delete index[old];
          if (storage.remove) storage.remove(STORAGE_PREFIX + old);
        });
    }
    await storage.set({ [INDEX_KEY]: index });
    return index;
  }

  async function listPages(storage) {
    try {
      const bag = await storage.get(INDEX_KEY);
      const index = (bag && bag[INDEX_KEY]) || {};
      return Object.keys(index)
        .map((url) => ({ url, count: index[url].count, at: index[url].at }))
        .sort((a, b) => String(b.at).localeCompare(String(a.at)));
    } catch {
      return [];
    }
  }

  /** add appends one highlight and persists. Returns the stored record and
   *  the full list, so the caller can repaint without re-reading. */
  async function add(storage, url, spec) {
    const record = makeHighlight(spec);
    if (!record) return { record: null, records: await load(storage, url) };
    const records = await load(storage, url);
    // The same passage highlighted twice is one highlight, updated — not a
    // duplicate. Selections get re-made by accident constantly.
    const existing = records.findIndex((r) => r.text === record.text);
    if (existing !== -1) {
      records[existing] = Object.assign({}, records[existing], {
        comment: record.comment || records[existing].comment,
        color: record.color,
      });
      return { record: records[existing], records: await save(storage, url, records) };
    }
    records.push(record);
    return { record, records: await save(storage, url, records) };
  }

  async function update(storage, url, id, patch) {
    const records = await load(storage, url);
    const at = records.findIndex((r) => r.id === id);
    if (at === -1) return { record: null, records };
    const merged = normalize(Object.assign({}, records[at], patch || {}, { id }));
    if (!merged) return { record: null, records };
    records[at] = merged;
    return { record: merged, records: await save(storage, url, records) };
  }

  async function remove(storage, url, id) {
    const records = await load(storage, url);
    const kept = records.filter((r) => r.id !== id);
    return { removed: kept.length !== records.length, records: await save(storage, url, kept) };
  }

  async function clear(storage, url) {
    return save(storage, url, []);
  }

  // ── The capture artifact ─────────────────────────────────────────

  /**
   * document is the parsed form of `highlights.json`: the contract between
   * this file and the ingest side (src/knowledge/highlights.ts).
   */
  function document(url, records) {
    const kept = normalizeAll(records);
    return {
      version: VERSION,
      url: identityUrl(url),
      highlights: kept.map((r) => Object.assign({}, r, { url: highlightUrl(url, r.text) })),
    };
  }

  /**
   * artifact is the envelope member (RCL-04). Returns null when there is
   * nothing to say — an empty `highlights.json` in every capture would be
   * noise in the inbox and a false signal to the ingest side.
   *
   * The name passes capture.ValidArtifactName's charset whitelist, so the
   * whole capture path carries it without knowing what it is.
   */
  function artifact(url, records) {
    const doc = document(url, records);
    if (!doc.highlights.length) return null;
    const bytes = utf8ToBase64(JSON.stringify(doc, null, 2));
    return { name: ARTIFACT_NAME, encoding: "base64", bytes, rawBytes: bytes.length };
  }

  root.MonoHighlights = {
    VERSION,
    ARTIFACT_NAME,
    STORAGE_PREFIX,
    INDEX_KEY,
    MAX_PER_PAGE,
    MAX_PAGES,
    COLORS,
    key,
    identityUrl,
    textFragment,
    highlightUrl,
    locate,
    relocate,
    makeHighlight,
    normalize,
    normalizeAll,
    load,
    save,
    add,
    update,
    remove,
    clear,
    listPages,
    document,
    artifact,
  };
})(globalThis);
