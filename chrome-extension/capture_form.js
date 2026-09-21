/**
 * MonoAgent Bridge — the save form's model (CLIP-07)
 *
 * A capture is worth more with a line saying why it was taken. meta.note,
 * meta.tags and meta.collection already ride the envelope; this file is the
 * small amount of thinking between what a person types into the popup and
 * what those three fields should hold.
 *
 * All of it is pure, so the awkward cases — "#ml, deep learning" typed with
 * a trailing comma, the same tag in two casings, a collection named with a
 * stray newline — are settled in node (capture_form.test.mjs) rather than by
 * clicking around a browser.
 *
 * Recall of recently-used tags lives in chrome.storage.local under
 * RECENT_TAGS_KEY; the popup reads and writes it through these functions so
 * the ordering rule (most recently used first) is in one place.
 */

(function (root) {
  "use strict";

  const RECENT_TAGS_KEY = "captureRecentTags";
  const COLLECTIONS_KEY = "captureCollections";

  const MAX_TAG_CHARS = 48;
  const MAX_TAGS = 16;
  const MAX_RECENT_TAGS = 40;
  const MAX_COLLECTION_CHARS = 64;
  const MAX_COLLECTIONS = 30;
  // A note is a line, not an essay — the essay belongs in the document.
  const MAX_NOTE_CHARS = 500;

  const collapse = (s) => String(s === undefined || s === null ? "" : s).replace(/\s+/g, " ").trim();

  /**
   * parseTags accepts whatever tag syntax someone happens to use: commas,
   * spaces, or both, with or without a leading #. Case is preserved as
   * typed, but two spellings that differ only in case are one tag.
   */
  function parseTags(input) {
    if (Array.isArray(input)) return parseTags(input.join(","));
    const out = [];
    const seen = new Set();
    for (const raw of String(input || "").split(/[,\s]+/)) {
      const tag = raw.replace(/^#+/, "").trim().slice(0, MAX_TAG_CHARS);
      if (!tag) continue;
      const key = tag.toLowerCase();
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(tag);
      if (out.length >= MAX_TAGS) break;
    }
    return out;
  }

  /**
   * rememberTags returns the recent-tag list with `used` moved to the
   * front. Most-recently-used ordering is what makes the suggestions worth
   * reading after a week of captures.
   */
  function rememberTags(recent, used, max) {
    const limit = max || MAX_RECENT_TAGS;
    const out = [];
    const seen = new Set();
    for (const tag of parseTags(used).concat(Array.isArray(recent) ? recent : [])) {
      const clean = String(tag || "").trim();
      if (!clean) continue;
      const key = clean.toLowerCase();
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(clean);
      if (out.length >= limit) break;
    }
    return out;
  }

  /**
   * suggestTags ranks recent tags against what is being typed: a prefix
   * match first, then a substring match, with anything already chosen
   * dropped. An empty query just offers the most recent.
   */
  function suggestTags(recent, query, chosen, limit) {
    const max = limit || 8;
    const taken = new Set(parseTags(chosen).map((t) => t.toLowerCase()));
    const q = String(query || "").replace(/^#+/, "").trim().toLowerCase();
    const prefix = [];
    const contains = [];
    for (const tag of Array.isArray(recent) ? recent : []) {
      const key = String(tag || "").toLowerCase();
      if (!key || taken.has(key)) continue;
      if (!q) prefix.push(tag);
      else if (key.startsWith(q)) prefix.push(tag);
      else if (key.includes(q)) contains.push(tag);
    }
    return prefix.concat(contains).slice(0, max);
  }

  /** normalizeCollection is a display name, or null for "no collection". */
  function normalizeCollection(name) {
    const clean = collapse(name).slice(0, MAX_COLLECTION_CHARS);
    return clean || null;
  }

  /** rememberCollection keeps the collection list most-recently-used first. */
  function rememberCollection(collections, name, max) {
    const limit = max || MAX_COLLECTIONS;
    const clean = normalizeCollection(name);
    const out = clean ? [clean] : [];
    const seen = new Set(out.map((c) => c.toLowerCase()));
    for (const existing of Array.isArray(collections) ? collections : []) {
      const value = normalizeCollection(existing);
      if (!value || seen.has(value.toLowerCase())) continue;
      seen.add(value.toLowerCase());
      out.push(value);
      if (out.length >= limit) break;
    }
    return out;
  }

  /** normalizeNote collapses a note to one line, or null when there is none. */
  function normalizeNote(note) {
    const clean = collapse(note).slice(0, MAX_NOTE_CHARS);
    return clean || null;
  }

  /**
   * toMeta is the whole form as the three envelope fields. A form nobody
   * filled in produces { note: null, tags: [], collection: null }, which is
   * exactly what a zero-interaction shortcut capture sends — the note path
   * and the fast path cannot drift apart.
   */
  function toMeta(form) {
    const f = form || {};
    return {
      note: normalizeNote(f.note),
      tags: parseTags(f.tags),
      collection: normalizeCollection(f.collection),
    };
  }

  /** isEmpty reports whether the form would add nothing to the envelope. */
  function isEmpty(form) {
    const meta = toMeta(form);
    return !meta.note && !meta.tags.length && !meta.collection;
  }

  /**
   * applyToMeta stamps a finished capture's meta with the form. It is what
   * makes "capture now, annotate while the snapshot is taken" safe: the
   * envelope is already built, and only these three fields move.
   */
  function applyToMeta(meta, form) {
    const target = meta || {};
    const values = toMeta(form);
    if (values.note) target.note = values.note;
    if (values.tags.length) {
      const merged = parseTags((target.tags || []).concat(values.tags));
      target.tags = merged;
    }
    if (values.collection) target.collection = values.collection;
    return target;
  }

  /** load reads the recall lists out of storage, tolerating a cold profile. */
  async function load(storage) {
    let stored = {};
    try {
      stored = (await storage.get([RECENT_TAGS_KEY, COLLECTIONS_KEY])) || {};
    } catch {
      // No storage (private window, wiped profile) — the form still works,
      // it just cannot suggest anything.
    }
    return {
      recentTags: Array.isArray(stored[RECENT_TAGS_KEY]) ? stored[RECENT_TAGS_KEY] : [],
      collections: Array.isArray(stored[COLLECTIONS_KEY]) ? stored[COLLECTIONS_KEY] : [],
    };
  }

  /** remember records what a capture just used, and returns the new lists. */
  async function remember(storage, form) {
    const current = await load(storage);
    const values = toMeta(form);
    const next = {
      recentTags: rememberTags(current.recentTags, values.tags),
      collections: rememberCollection(current.collections, values.collection),
    };
    try {
      await storage.set({ [RECENT_TAGS_KEY]: next.recentTags, [COLLECTIONS_KEY]: next.collections });
    } catch {
      // Recall is a convenience; failing to store it must not fail a capture.
    }
    return next;
  }

  root.MonoCaptureForm = {
    parseTags, rememberTags, suggestTags, normalizeCollection, rememberCollection,
    normalizeNote, toMeta, isEmpty, applyToMeta, load, remember,
    RECENT_TAGS_KEY, COLLECTIONS_KEY, MAX_TAGS, MAX_NOTE_CHARS,
  };
})(globalThis);
