/**
 * MonoAgent Bridge — adapter toolkit (CLIP-10)
 *
 * Tree helpers shared by every per-site adapter. Adapters work on the same
 * node tree readable.js and capture_meta.js use (see domlite.js for the
 * shape), never on a live DOM, so an adapter is exercised in node from a
 * trimmed HTML fixture and behaves identically in Chrome.
 *
 * The one thing worth explaining is `matcher`. Site markup changes weekly, so
 * an adapter that pins one CSS selector is an adapter that silently breaks.
 * Everything here matches loosely — a tag plus a *substring or pattern* of a
 * class, a `data-testid`, an ARIA role — and every adapter tries several
 * shapes in turn, so a renamed wrapper costs one signal rather than the page.
 */

(function (root) {
  "use strict";

  const isText = (n) => !!n && n.tag === "#text";
  const kids = (n) => (n && n.children) || [];
  const attr = (n, name) => (n && n.attrs && n.attrs[name]) || "";
  const clean = (s) => String(s || "").replace(/\s+/g, " ").trim();

  function textOf(node) {
    if (isText(node)) return node.text || "";
    return kids(node).map(textOf).join("");
  }

  /** text is textOf with whitespace collapsed — what a reader would see. */
  const text = (node) => clean(textOf(node));

  /** walk visits every element node, depth-first, in document order. */
  function walk(node, visit) {
    for (const child of kids(node)) {
      if (isText(child)) continue;
      if (visit(child) === false) return false;
      if (walk(child, visit) === false) return false;
    }
    return true;
  }

  function findAll(tree, predicate, limit) {
    const out = [];
    walk(tree, (node) => {
      if (!predicate(node)) return;
      out.push(node);
      if (limit && out.length >= limit) return false;
    });
    return out;
  }

  function find(tree, predicate) {
    return findAll(tree, predicate, 1)[0] || null;
  }

  function classes(node) {
    return attr(node, "class").split(/\s+/).filter(Boolean);
  }

  function hasClass(node, name) {
    return classes(node).includes(name);
  }

  function test(pattern, value) {
    if (pattern === undefined || pattern === null) return true;
    if (pattern === true) return !!value;
    if (pattern instanceof RegExp) return pattern.test(value);
    return String(value).toLowerCase().includes(String(pattern).toLowerCase());
  }

  /**
   * matcher builds a predicate from a loose spec:
   *
   *   { tag: "div" | /^h[12]$/, class: "tweetText" | /segment/, id, testid,
   *     role, attrs: { "data-x": true | "value" | /re/ }, text: /re/ }
   *
   * A string matches as a case-insensitive substring; a RegExp matches as a
   * pattern; `true` means "present and non-empty". Every key given must hold.
   */
  function matcher(spec) {
    const s = spec || {};
    return (node) => {
      if (s.tag !== undefined && !test(s.tag, node.tag)) return false;
      if (s.class !== undefined && !test(s.class, attr(node, "class"))) return false;
      if (s.id !== undefined && !test(s.id, attr(node, "id"))) return false;
      if (s.testid !== undefined && !test(s.testid, attr(node, "data-testid"))) return false;
      if (s.role !== undefined && !test(s.role, attr(node, "role"))) return false;
      if (s.text !== undefined && !test(s.text, text(node))) return false;
      for (const [name, want] of Object.entries(s.attrs || {})) {
        if (!test(want, attr(node, name))) return false;
      }
      return true;
    };
  }

  /** pick returns the first node matching any spec, trying them in order. */
  function pick(tree, specs) {
    for (const spec of specs) {
      const found = find(tree, typeof spec === "function" ? spec : matcher(spec));
      if (found) return found;
    }
    return null;
  }

  /** pickAll returns the matches of the first spec that matches anything. */
  function pickAll(tree, specs) {
    for (const spec of specs) {
      const found = findAll(tree, typeof spec === "function" ? spec : matcher(spec));
      if (found.length) return found;
    }
    return [];
  }

  /** pickText returns the trimmed text of the first spec that yields any. */
  function pickText(tree, specs) {
    for (const spec of specs) {
      const found = find(tree, typeof spec === "function" ? spec : matcher(spec));
      const value = found ? text(found) : "";
      if (value) return value;
    }
    return "";
  }

  function absolute(href, base) {
    if (!href) return "";
    try {
      return new URL(href, base || undefined).href;
    } catch {
      return String(href).trim();
    }
  }

  /** metaContent reads a <meta> value by property/name/itemprop. */
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

  /** metaAll reads every <meta> value for a key — citation_author repeats. */
  function metaAll(tree, key) {
    const wanted = key.toLowerCase();
    const out = [];
    for (const node of findAll(tree, (n) => n.tag === "meta")) {
      const name = (attr(node, "property") || attr(node, "name") || attr(node, "itemprop")).toLowerCase();
      if (name !== wanted) continue;
      const value = clean(attr(node, "content"));
      if (value) out.push(value);
    }
    return out;
  }

  /**
   * markdownOf renders a subtree through the shared Markdown renderer, which
   * is what keeps code fences, lists and tables intact inside an adapter's
   * output instead of flattening them to text.
   */
  function markdownOf(node, baseUrl) {
    if (!node) return "";
    try {
      return root.MonoMarkdown.toMarkdown(node, { baseUrl: baseUrl || "" }).trim();
    } catch {
      return text(node);
    }
  }

  /** blocks joins rendered pieces with blank lines, dropping the empties. */
  function blocks(parts) {
    return parts.filter((p) => p && String(p).trim()).join("\n\n").replace(/\n{3,}/g, "\n\n").trim();
  }

  /** hostOf is url.hostname without "www.", and "" for anything unparseable. */
  function hostOf(url) {
    try {
      return new URL(url).hostname.replace(/^www\./i, "").toLowerCase();
    } catch {
      return "";
    }
  }

  function pathOf(url) {
    try {
      return new URL(url).pathname;
    } catch {
      return "";
    }
  }

  root.MonoAdapterUtil = {
    isText, kids, attr, clean, textOf, text, walk, findAll, find,
    classes, hasClass, matcher, pick, pickAll, pickText, absolute,
    metaContent, metaAll, markdownOf, blocks, hostOf, pathOf,
  };
})(globalThis);
