/**
 * MonoAgent Bridge — in-page capture half (CLIP-04, CLIP-05, CLIP-09)
 *
 * Injected into the target tab by capture.js, alongside markdown.js,
 * readable.js and capture_meta.js. Three entry points, called in order by the
 * service worker:
 *
 *   prepare()  — CLIP-09: make the page worth photographing. Force lazy
 *                images to load, wait for the network to go quiet, open
 *                <details>, and hide the sticky furniture that would
 *                otherwise be stamped across every page of the PDF.
 *   extract()  — CLIP-05/04: readable Markdown + provenance, scoped to the
 *                user's selection when there is one.
 *   restore()  — put the page back exactly as it was found.
 *
 * Everything with real logic lives in the pure modules; this file is the thin
 * browser-only shell around them.
 */

(function (root) {
  "use strict";

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // Banner patterns worth naming explicitly: these sit at z-index 9999 over
  // the article and are the single biggest cause of a useless archive.
  const CONSENT_SELECTORS = [
    "#onetrust-banner-sdk", "#onetrust-consent-sdk", ".cc-window", ".cookie-banner",
    "[id*='cookie-consent']", "[class*='cookie-consent']", "[id*='cookie-notice']",
    "[class*='cookie-notice']", "[aria-label*='cookie' i]", "[id*='gdpr']",
    "[class*='gdpr']", "#didomi-host", "#usercentrics-root", ".qc-cmp2-container",
  ];

  function state() {
    if (!root.__monoCaptureState) {
      root.__monoCaptureState = { hidden: [], details: [], scroll: null, bodyOverflow: null };
    }
    return root.__monoCaptureState;
  }

  function hide(el, store) {
    if (!el || store.hidden.some((h) => h.el === el)) return;
    store.hidden.push({ el, display: el.style.display, visibility: el.style.visibility });
    el.style.setProperty("display", "none", "important");
  }

  /**
   * prepare returns once the page is as loaded as it is going to get, or once
   * the budget runs out — never later. A capture that hangs on an infinite
   * feed is worse than one that misses the last screenful.
   */
  async function prepare(opts) {
    const o = Object.assign({ maxScrollPasses: 40, maxScrollMs: 6000, networkIdleMs: 600, maxNetworkMs: 3000 }, opts || {});
    const store = state();
    const started = Date.now();
    const stats = { scrollPasses: 0, imagesPromoted: 0, overlaysHidden: 0, detailsExpanded: 0 };

    store.scroll = { x: root.scrollX, y: root.scrollY };
    const body = document.body;
    if (body) {
      store.bodyOverflow = { body: body.style.overflow, html: document.documentElement.style.overflow };
      // Modal scroll-locks stop the auto-scroll pass dead.
      body.style.overflow = "visible";
      document.documentElement.style.overflow = "visible";
    }

    // Overlays first: a sticky consent bar can intercept the scroll itself.
    for (const el of document.querySelectorAll("body *")) {
      let style;
      try {
        style = getComputedStyle(el);
      } catch {
        continue;
      }
      if (style.position === "fixed" || style.position === "sticky") {
        // Leave tiny decorations alone; hide anything big enough to cover text.
        const rect = el.getBoundingClientRect();
        if (rect.width * rect.height > 4000) hide(el, store);
      }
    }
    for (const selector of CONSENT_SELECTORS) {
      let matches = [];
      try {
        matches = document.querySelectorAll(selector);
      } catch {
        continue;
      }
      for (const el of matches) hide(el, store);
    }
    stats.overlaysHidden = store.hidden.length;

    for (const d of document.querySelectorAll("details:not([open])")) {
      store.details.push(d);
      d.open = true;
      stats.detailsExpanded++;
    }

    stats.imagesPromoted = promoteLazyImages();

    // Auto-scroll: lazy loaders listen for scroll and IntersectionObserver,
    // and neither fires for a page nobody looked at.
    const scrollStart = Date.now();
    let lastHeight = -1;
    while (stats.scrollPasses < o.maxScrollPasses && Date.now() - scrollStart < o.maxScrollMs) {
      const height = document.documentElement.scrollHeight;
      root.scrollTo(0, Math.min(root.scrollY + root.innerHeight * 0.9, height));
      stats.scrollPasses++;
      await sleep(120);
      stats.imagesPromoted += promoteLazyImages();
      const atBottom = root.scrollY + root.innerHeight >= document.documentElement.scrollHeight - 2;
      if (atBottom && height === lastHeight) break;
      lastHeight = height;
    }

    stats.networkMs = await waitForNetworkIdle(o.maxNetworkMs, o.networkIdleMs);
    await settleImages(1500);

    root.scrollTo(store.scroll.x, store.scroll.y);
    await sleep(60);
    stats.durationMs = Date.now() - started;
    return stats;
  }

  function promoteLazyImages() {
    let promoted = 0;
    for (const img of document.querySelectorAll("img")) {
      const lazySrc = img.getAttribute("data-src") || img.getAttribute("data-lazy-src") || img.getAttribute("data-original");
      if (lazySrc && !img.getAttribute("src")) {
        img.setAttribute("src", lazySrc);
        promoted++;
      }
      const lazySet = img.getAttribute("data-srcset");
      if (lazySet && !img.getAttribute("srcset")) img.setAttribute("srcset", lazySet);
      if (img.loading === "lazy") {
        img.loading = "eager";
        promoted++;
      }
    }
    return promoted;
  }

  async function waitForNetworkIdle(maxMs, quietMs) {
    const started = Date.now();
    let lastChange = Date.now();
    let seen = performance.getEntriesByType("resource").length;
    while (Date.now() - started < maxMs) {
      await sleep(100);
      const now = performance.getEntriesByType("resource").length;
      if (now !== seen) {
        seen = now;
        lastChange = Date.now();
      } else if (Date.now() - lastChange >= quietMs) {
        break;
      }
    }
    return Date.now() - started;
  }

  async function settleImages(maxMs) {
    const started = Date.now();
    while (Date.now() - started < maxMs) {
      const pending = [...document.images].filter((i) => !i.complete).length;
      if (!pending) return;
      await sleep(100);
    }
  }

  function restore() {
    const store = state();
    for (const { el, display, visibility } of store.hidden) {
      el.style.display = display || "";
      el.style.visibility = visibility || "";
    }
    for (const d of store.details) d.open = false;
    if (store.bodyOverflow && document.body) {
      document.body.style.overflow = store.bodyOverflow.body || "";
      document.documentElement.style.overflow = store.bodyOverflow.html || "";
    }
    if (store.scroll) root.scrollTo(store.scroll.x, store.scroll.y);
    root.__monoCaptureState = null;
    return { restored: true };
  }

  // A stable-enough address for the selected region, so a later capture of
  // the same page can tell it is the same passage.
  function cssPath(el, maxDepth = 6) {
    const parts = [];
    let node = el;
    while (node && node.nodeType === 1 && parts.length < maxDepth) {
      if (node.id) {
        parts.unshift(`#${node.id}`);
        break;
      }
      const tag = node.tagName.toLowerCase();
      if (tag === "html" || tag === "body") {
        parts.unshift(tag);
        break;
      }
      const siblings = [...(node.parentElement ? node.parentElement.children : [])].filter(
        (s) => s.tagName === node.tagName
      );
      parts.unshift(siblings.length > 1 ? `${tag}:nth-of-type(${siblings.indexOf(node) + 1})` : tag);
      node = node.parentElement;
    }
    return parts.join(" > ");
  }

  function selectionInfo() {
    const sel = root.getSelection && root.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount) return null;
    const text = sel.toString().trim();
    if (!text) return null;
    const range = sel.getRangeAt(0);
    let ancestor = range.commonAncestorContainer;
    if (ancestor.nodeType !== 1) ancestor = ancestor.parentElement;
    return {
      element: ancestor,
      meta: {
        text: text.slice(0, 10000),
        truncated: text.length > 10000,
        path: ancestor ? cssPath(ancestor) : null,
        startOffset: range.startOffset,
        endOffset: range.endOffset,
      },
    };
  }

  /**
   * extract produces the readable artifact and the provenance. contentHash is
   * left null on purpose: crypto.subtle does not exist on a plain-http page,
   * and the service worker — always a secure context — fills it in.
   */
  function extract(opts) {
    const o = Object.assign({ selection: false, tables: false }, opts || {});
    const docTree = root.MonoReadable.snapshot(document.documentElement, root);
    const baseUrl = document.baseURI || location.href;

    let selection = null;
    let contentTree = docTree;
    let selectionOnly = false;
    if (o.selection) {
      const picked = selectionInfo();
      if (picked && picked.element) {
        selection = picked.meta;
        contentTree = root.MonoReadable.snapshot(picked.element, root);
        selectionOnly = true;
      }
    }

    const readable = root.MonoReadable.extract(contentTree, { baseUrl, selectionOnly });

    // CLIP-10: a per-site adapter gets first refusal on the page, unless the
    // user selected a region — a selection is already the answer. run()
    // returns null for a decline, a throw or an empty extraction, so an
    // adapter can only improve the result and can never cost the capture.
    let adapted = null;
    let adapterWarnings = [];
    if (!selectionOnly && root.MonoAdapters) {
      adapted = root.MonoAdapters.run({ url: location.href, tree: docTree, title: document.title, baseUrl, document });
      // Read the warnings here, not later: lastWarnings() survives until the
      // next run(), and this tab may have been captured before.
      adapterWarnings = adapted ? adapted.warnings : root.MonoAdapters.lastWarnings();
    }
    const markdown = adapted ? adapted.markdown : readable.markdown;
    const text = adapted ? root.MonoReadable.plainText(markdown) : readable.text;

    // CLIP-11: the tables under whatever was captured — the selection when
    // there is one, the page otherwise. A page with no data tables costs
    // nothing here and adds no artifacts.
    let tables = [];
    if (o.tables && root.MonoTables) {
      try {
        tables = root.MonoTables.extract(contentTree).tables;
      } catch {
        // A table that will not parse must not cost the user the capture.
      }
    }

    const meta = root.MonoMeta.buildSync({
      tree: docTree,
      url: location.href,
      title: (adapted && adapted.title) || document.title || readable.title,
      selection,
      note: o.note || null,
      tags: o.tags || [],
      collection: o.collection || null,
      excerpt: text.slice(0, 280),
      wordCount: text ? text.split(/\s+/).length : 0,
      capturedAt: o.capturedAt,
      source: "extension",
    });

    if (tables.length) meta.tables = root.MonoTables.metaEntries(tables);

    // Warnings accumulate even when no adapter produced anything — that is
    // how a page that *should* have had an adapter says why it did not.
    const artifacts = [];
    const warnings = adapterWarnings.slice();
    if (adapted) {
      for (const [key, value] of Object.entries(adapted.meta)) {
        if (value !== undefined) meta[key] = value;
      }
      artifacts.push(...adapted.artifacts);
    }

    // CLIP-12: rows the user extracted by picking an element ride along with
    // the next capture of this tab. takeStash() clears as it reads, so a
    // later capture cannot pick up yesterday's table.
    const picked = root.MonoExtractItems && root.MonoExtractItems.takeStash();
    if (picked) {
      artifacts.push(...root.MonoExtractItems.artifactsFor(picked));
      meta.items = { selector: picked.selector, count: picked.items.length, pages: picked.pagesFetched };
      warnings.push(...(picked.warnings || []));
    }

    return {
      meta,
      markdown,
      text,
      tables,
      artifacts,
      warnings,
      adapter: adapted ? adapted.adapter : null,
      selectionRequested: !!o.selection,
      selectionFound: !!selection,
    };
  }

  root.MonoCapturePage = { prepare, extract, restore, cssPath };
})(globalThis);
