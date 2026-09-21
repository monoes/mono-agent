/**
 * MonoAgent Bridge — point-and-extract for repeating elements (CLIP-12)
 *
 * The user clicks one row of a listing. From that single click this works out
 * what "one of these" means, finds the rest, and turns them into structured
 * rows — `items.csv` and `items.json` as envelope artifacts.
 *
 * The whole inference is pure: it runs on the node tree (domlite.js /
 * MonoReadable.snapshot), and the live DOM contributes exactly one thing —
 * a `data-mono-pick` attribute set on the clicked element just before the
 * snapshot and removed immediately after. That keeps every heuristic below
 * testable from a fixture, which matters because heuristics are the part
 * that goes wrong.
 *
 * Three judgements, in order:
 *
 *  1. Which ancestor is the item? Walk up from the click; at each level count
 *     siblings of the same *kind* (same tag sharing at least one stable
 *     class, so a `card featured` still groups with its plain `card`
 *     siblings). Prefer the largest group, and among groups within half of
 *     the best, the outermost — which is why clicking a tag inside a product
 *     card extracts cards rather than tags.
 *  2. Which children are fields? Anything that repeats at the same relative
 *     path in at least 60% of the items: a link, an image, a `<time>`, or a
 *     run of text. Named by what it looks like (title / url / image / price /
 *     date) rather than by position, so the CSV header reads.
 *  3. Where does it end? Pagination follows a same-origin next link, stops
 *     at a hard cap, and stops the moment a page yields no new rows.
 *
 * Note on the one network exception: following pagination does fetch pages
 * the tab had not loaded. That is the feature, it is user-initiated, it is
 * same-origin only, and it is bounded. Nothing else here touches the network.
 *
 * The element-picking UI is not here — see `fromElement` for the entry point
 * the popup/content-script side calls.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  const PICK_ATTR = "data-mono-pick";
  const FIELD_PRESENCE = 0.6;
  const DEFAULT_MAX_PAGES = 5;
  // A bound the caller cannot raise: a runaway crawl from a browser tab is
  // indistinguishable from abuse, whatever the caller intended.
  const HARD_MAX_PAGES = 20;

  const HEADINGS = new Set(["h1", "h2", "h3", "h4", "h5", "h6"]);
  const INLINE = new Set([
    "span", "b", "i", "em", "strong", "small", "code", "mark", "u", "sub",
    "sup", "abbr", "br", "wbr", "bdi", "bdo", "q", "cite", "s", "del", "ins",
  ]);

  // Build-generated class names carry no meaning and differ between siblings.
  const UNSTABLE_CLASS = /^(css|sc|jsx|emotion|svelte|styles?)[-_][0-9a-z]{4,}$|^[a-z]{0,3}[0-9a-f]{7,}$/i;

  const PRICE = /[$£€¥₹]\s?\d|^\d[\d.,]*\s?(USD|EUR|GBP|CAD|AUD|JPY|CHF|SEK|PLN)$|\b(USD|EUR|GBP)\s?\d/i;
  const DATEISH = /^\d{4}-\d{2}-\d{2}|^\d{1,2}[/.-]\d{1,2}[/.-]\d{2,4}$|^\d{1,2}\s+\w{3,}\s+\d{4}$/;

  const NEXT_TEXT = /^(next|older|more|›|»|→|>>)\b|^next\s*(page)?\s*(›|»|→)?$/i;

  function stableClasses(node) {
    return U.classes(node).filter((c) => c.length <= 40 && !UNSTABLE_CLASS.test(c));
  }

  /** sameKind decides whether two siblings are two of the same thing. */
  function sameKind(a, b) {
    if (a.tag !== b.tag) return false;
    const ca = stableClasses(a);
    const cb = stableClasses(b);
    if (!ca.length && !cb.length) return true;
    if (!ca.length || !cb.length) return false;
    return ca.some((c) => cb.includes(c));
  }

  const elementChildren = (node) => U.kids(node).filter((n) => !U.isText(n));

  /** selectorOf names a group by its tag plus the classes all of it shares. */
  function selectorOf(group) {
    const [first] = group;
    const shared = stableClasses(first).filter((c) => group.every((n) => stableClasses(n).includes(c)));
    return first.tag + shared.map((c) => `.${c}`).join("");
  }

  function parseSelector(spec) {
    const m = String(spec).trim().match(/^([a-zA-Z][\w-]*)?((?:\.[\w-]+)*)$/);
    if (!m) return null;
    return { tag: (m[1] || "").toLowerCase(), classes: (m[2] || "").split(".").filter(Boolean) };
  }

  function matchesSelector(node, parsed) {
    if (parsed.tag && node.tag !== parsed.tag) return false;
    const classes = U.classes(node);
    return parsed.classes.every((c) => classes.includes(c));
  }

  /** parentsOf indexes the tree once so the walk up from the click is cheap. */
  function parentsOf(tree) {
    const parents = new Map();
    U.walk(tree, (node) => {
      for (const child of elementChildren(node)) parents.set(child, node);
    });
    for (const child of elementChildren(tree)) parents.set(child, tree);
    return parents;
  }

  /**
   * inferItems walks up from the picked node and returns the best repeating
   * group, or null when the page has no repetition to find.
   */
  function inferItems(tree, picked) {
    const parents = parentsOf(tree);
    const candidates = [];
    let node = picked;
    let depth = 0;
    while (node && node !== tree) {
      const parent = parents.get(node);
      if (!parent) break;
      const group = elementChildren(parent).filter((sibling) => sameKind(node, sibling));
      if (group.length >= 2) candidates.push({ group, depth });
      node = parent;
      depth++;
    }
    if (!candidates.length) return null;

    const best = Math.max(...candidates.map((c) => c.group.length));
    const threshold = Math.max(2, best / 2);
    // Outermost of the groups that are still roughly as populous as the best
    // one: clicking a tag inside a card should give you cards.
    const viable = candidates.filter((c) => c.group.length >= threshold);
    return viable.reduce((a, b) => (b.depth > a.depth ? b : a));
  }

  // --- fields -------------------------------------------------------------

  function isTextLeaf(node) {
    if (!U.text(node)) return false;
    return U.kids(node).every((c) => U.isText(c) || INLINE.has(c.tag) || !U.text(c));
  }

  /** valuesOf reads what a single element contributes, and whether to stop. */
  function valuesOf(node, baseUrl) {
    if (node.tag === "img" || node.tag === "source") {
      const src = U.absolute(U.attr(node, "src") || U.attr(node, "data-src") || U.attr(node, "srcset").split(/[\s,]/)[0], baseUrl);
      return src ? [{ kind: "image", value: src, stop: true }] : [];
    }
    if (node.tag === "time" && U.attr(node, "datetime")) {
      return [{ kind: "date", value: U.clean(U.attr(node, "datetime")), stop: true }];
    }
    const out = [];
    if (node.tag === "a" && U.attr(node, "href")) {
      const href = U.absolute(U.attr(node, "href"), baseUrl);
      if (href && !/^javascript:/i.test(href)) out.push({ kind: "link", value: href });
    }
    if (isTextLeaf(node)) out.push({ kind: "text", value: U.text(node), stop: true });
    return out;
  }

  const stepName = (node) => {
    const [first] = stableClasses(node);
    return first ? `${node.tag}.${first}` : node.tag;
  };

  /** collect flattens one item into keyed entries, keyed by relative path. */
  function collect(item, baseUrl) {
    const entries = [];
    const counts = new Map();
    const keyFor = (base) => {
      const n = (counts.get(base) || 0) + 1;
      counts.set(base, n);
      return n === 1 ? base : `${base}@${n}`;
    };

    (function descend(node, path) {
      for (const child of elementChildren(node)) {
        const here = path ? `${path}>${stepName(child)}` : stepName(child);
        let stop = false;
        for (const value of valuesOf(child, baseUrl)) {
          entries.push({
            key: keyFor(`${here}#${value.kind}`),
            kind: value.kind,
            tag: child.tag,
            value: value.value,
          });
          if (value.stop) stop = true;
        }
        if (!stop) descend(child, here);
      }
    })(item, "");

    // An item that *is* its content — a bare <li>text</li> — still has one.
    if (!entries.length && U.text(item)) {
      entries.push({ key: "self#text", kind: "text", tag: item.tag, value: U.text(item) });
    }
    return entries;
  }

  const majority = (values, re) => values.length > 0 && values.filter((v) => re.test(v)).length >= values.length * 0.6;

  /**
   * buildFields decides the columns from every item at once, keeps those
   * present in most of them, and names each by what it appears to hold.
   */
  function buildFields(rawItems) {
    const order = [];
    const seen = new Map();
    for (const entries of rawItems) {
      for (const entry of entries) {
        if (!seen.has(entry.key)) {
          seen.set(entry.key, { kind: entry.kind, tag: entry.tag, values: [], count: 0 });
          order.push(entry.key);
        }
        const info = seen.get(entry.key);
        info.count++;
        info.values.push(entry.value);
      }
    }

    const min = Math.max(1, Math.ceil(rawItems.length * FIELD_PRESENCE));
    const kept = order.filter((key) => seen.get(key).count >= min);
    const firstText = kept.find((key) => seen.get(key).kind === "text");

    const used = new Map();
    const uniq = (base) => {
      const n = (used.get(base) || 0) + 1;
      used.set(base, n);
      return n === 1 ? base : `${base}${n}`;
    };

    let titled = false;
    const fields = kept.map((key) => {
      const info = seen.get(key);
      if (info.kind === "link") return { key, name: uniq("url"), type: "url" };
      if (info.kind === "image") return { key, name: uniq("image"), type: "image" };
      if (info.kind === "date") return { key, name: uniq("date"), type: "date" };
      if (majority(info.values, PRICE)) return { key, name: uniq("price"), type: "price" };
      if (majority(info.values, DATEISH)) return { key, name: uniq("date"), type: "date" };
      if (!titled && (HEADINGS.has(info.tag) || key === firstText)) {
        titled = true;
        return { key, name: uniq("title"), type: "title" };
      }
      return { key, name: uniq("text"), type: "text" };
    });

    const PRIORITY = { title: 0, url: 1, image: 2, price: 3, date: 4, text: 5 };
    fields.sort((a, b) => (PRIORITY[a.type] ?? 5) - (PRIORITY[b.type] ?? 5));

    const items = rawItems.map((entries) => {
      const byKey = new Map(entries.map((e) => [e.key, e.value]));
      const row = {};
      for (const field of fields) row[field.name] = byKey.has(field.key) ? byKey.get(field.key) : "";
      return row;
    });

    return { fields: fields.map(({ name, type }) => ({ name, type })), items };
  }

  // --- CSV ----------------------------------------------------------------

  function csvCell(value) {
    const s = value === null || value === undefined ? "" : String(value);
    return /[",\n\r]|^\s|\s$/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  }

  function toCsv(items, fields) {
    const names = fields.map((f) => f.name);
    const lines = [names.map(csvCell).join(",")];
    for (const item of items) lines.push(names.map((n) => csvCell(item[n])).join(","));
    return `${lines.join("\n")}\n`;
  }

  function artifactsFor(result) {
    if (!result || !result.items || !result.items.length) return [];
    return [
      { name: "items.csv", text: toCsv(result.items, result.fields) },
      { name: "items.json", text: `${JSON.stringify(result.items, null, 2)}\n` },
    ];
  }

  // --- the pure entry point -----------------------------------------------

  function resolve(tree, opts) {
    const warnings = [];
    if (opts.selector) {
      const parsed = parseSelector(opts.selector);
      if (!parsed) {
        warnings.push(`extract-items: ${JSON.stringify(opts.selector)} is not a tag/class selector`);
        return { selector: null, nodes: [], warnings };
      }
      const nodes = U.findAll(tree, (n) => matchesSelector(n, parsed));
      if (!nodes.length) warnings.push(`extract-items: nothing on this page matches ${opts.selector}`);
      return { selector: opts.selector, nodes, warnings };
    }

    const picked = U.find(tree, (n) => U.attr(n, PICK_ATTR));
    if (!picked) {
      warnings.push("extract-items: no element was marked as picked, and no selector was given");
      return { selector: null, nodes: [], warnings };
    }
    const inferred = inferItems(tree, picked);
    if (!inferred) {
      warnings.push("extract-items: no repeating structure was found around the picked element");
      return { selector: null, nodes: [], warnings };
    }
    return { selector: selectorOf(inferred.group), nodes: inferred.group, warnings };
  }

  /**
   * fromTree is the pure core: a tree in, rows out. `opts.selector` skips the
   * inference; otherwise the tree must carry a `data-mono-pick` element.
   */
  function fromTree(tree, opts) {
    const o = opts || {};
    const baseUrl = o.baseUrl || o.url || "";
    const { selector, nodes, warnings } = resolve(tree, o);
    const raw = nodes.map((node) => collect(node, baseUrl));
    const built = buildFields(raw);
    return {
      selector,
      fields: built.fields,
      items: built.items,
      pages: [o.url || ""],
      pagesFetched: 1,
      warnings,
      raw,
    };
  }

  // --- pagination ---------------------------------------------------------

  function nextLinkOf(tree, currentUrl) {
    const link =
      U.pick(tree, [
        (n) => n.tag === "a" && /(^|\s)next(\s|$)/i.test(U.attr(n, "rel")),
        (n) => n.tag === "link" && /(^|\s)next(\s|$)/i.test(U.attr(n, "rel")),
        (n) => n.tag === "a" && /next/i.test(U.attr(n, "aria-label")) && U.attr(n, "href"),
        (n) => n.tag === "a" && U.attr(n, "href") && NEXT_TEXT.test(U.text(n)),
      ]) || null;
    const href = link ? U.attr(link, "href") : "";
    if (!href || /^#/.test(href)) return "";
    return U.absolute(href, currentUrl);
  }

  function sameOrigin(a, b) {
    try {
      return new URL(a).origin === new URL(b).origin;
    } catch {
      return false;
    }
  }

  const rowKey = (entries) => JSON.stringify(entries.map((e) => [e.key, e.value]));

  function defaultParse(html) {
    if (root.MonoDomLite) return root.MonoDomLite.parse(html);
    const doc = new DOMParser().parseFromString(html, "text/html");
    return root.MonoReadable.snapshot(doc.documentElement);
  }

  function defaultFetch(url) {
    return fetch(url, { credentials: "include", redirect: "follow" }).then((r) => {
      if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
      return r.text();
    });
  }

  /**
   * fromTreeWithPagination extracts the first page, then follows next links
   * until the cap, a dead end, an off-origin hop, a failed fetch, or a page
   * that adds nothing. Every one of those is recorded rather than silent.
   */
  async function fromTreeWithPagination(tree, opts) {
    const o = opts || {};
    const first = fromTree(tree, o);
    const warnings = first.warnings.slice();
    const cap = Math.min(Math.max(1, o.maxPages || DEFAULT_MAX_PAGES), HARD_MAX_PAGES);
    const parseHtml = o.parseHtml || defaultParse;
    const fetchPage = o.fetchPage || defaultFetch;

    const raw = first.raw.slice();
    const keys = new Set(raw.map(rowKey));
    const pages = [o.url || ""];
    const visited = new Set(pages);

    let currentTree = tree;
    let currentUrl = o.url || "";
    let capped = false;

    while (first.selector) {
      if (pages.length >= cap) {
        capped = !!nextLinkOf(currentTree, currentUrl);
        break;
      }
      const next = nextLinkOf(currentTree, currentUrl);
      if (!next || visited.has(next)) break;
      if (!sameOrigin(next, o.url || next)) {
        warnings.push(`extract-items: stopped at an off-origin next link (${next})`);
        break;
      }

      let html;
      try {
        html = await fetchPage(next);
      } catch (err) {
        warnings.push(`extract-items: stopped after ${next} failed: ${err.message}`);
        break;
      }
      visited.add(next);
      pages.push(next);

      currentTree = parseHtml(html);
      currentUrl = next;
      const page = fromTree(currentTree, Object.assign({}, o, { url: next, baseUrl: next, selector: first.selector }));
      const fresh = page.raw.filter((entries) => !keys.has(rowKey(entries)));
      for (const entries of fresh) {
        keys.add(rowKey(entries));
        raw.push(entries);
      }
      if (!fresh.length) {
        warnings.push(`extract-items: ${next} added no new items, so pagination stopped there`);
        break;
      }
    }

    if (capped) warnings.push(`extract-items: stopped at the ${cap}-page limit with more pages still offered`);

    const built = buildFields(raw);
    return {
      selector: first.selector,
      fields: built.fields,
      items: built.items,
      pages,
      pagesFetched: pages.length,
      warnings,
      raw,
    };
  }

  // --- the DOM entry point (for ext-ux) -----------------------------------

  /**
   * fromElement is the one function the element picker calls. It marks the
   * clicked element, snapshots the document around it, and hands off to the
   * pure pipeline — the DOM is touched for exactly two statements.
   */
  async function fromElement(element, options) {
    const o = Object.assign({ paginate: false, maxPages: DEFAULT_MAX_PAGES }, options || {});
    if (!element || element.nodeType !== 1) throw new Error("extract-items: pick an element first");

    const had = element.hasAttribute(PICK_ATTR);
    element.setAttribute(PICK_ATTR, "1");
    let tree;
    try {
      tree = root.MonoReadable.snapshot(document.documentElement, root);
    } finally {
      if (!had) element.removeAttribute(PICK_ATTR);
    }

    const opts = Object.assign({ url: location.href, baseUrl: document.baseURI || location.href }, o);
    const result = o.paginate ? await fromTreeWithPagination(tree, opts) : fromTree(tree, opts);
    return Object.assign({}, result, { raw: undefined, artifacts: artifactsFor(result) });
  }

  /**
   * stash parks a result where capture_page.extract() will find it, so the
   * rows ride along with the next capture of this tab as envelope artifacts.
   * Both halves run in the same isolated world, which is what makes this
   * work; it is cleared on read so a later capture cannot pick up stale rows.
   */
  function stash(result) {
    root.__monoPendingItems = result && result.items && result.items.length ? result : null;
    return !!root.__monoPendingItems;
  }

  function takeStash() {
    const pending = root.__monoPendingItems || null;
    root.__monoPendingItems = null;
    return pending;
  }

  root.MonoExtractItems = {
    fromElement, fromTree, fromTreeWithPagination,
    toCsv, artifactsFor, stash, takeStash,
    inferItems, buildFields, nextLinkOf, selectorOf,
    PICK_ATTR, DEFAULT_MAX_PAGES, HARD_MAX_PAGES,
  };
})(globalThis);
