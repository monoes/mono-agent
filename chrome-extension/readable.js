/**
 * MonoAgent Bridge — readable extraction (CLIP-05)
 *
 * A compact, dependency-free article extractor: score block elements by text
 * density and link density, pick the best content root, strip the furniture
 * (nav / header / footer / aside / ads / cookie banners), and hand the
 * surviving subtree to markdown.js.
 *
 * It works on a node tree, not a live DOM, for two reasons. It is testable
 * from an HTML string in plain node (see readable.test.mjs). And the
 * snapshot step — the only part that needs Chrome — is where live-DOM signals
 * that no post-hoc HTML parse can recover get recorded: whether an element is
 * actually visible, and whether it is a sticky/fixed overlay. Those flags then
 * steer the pure scoring below.
 */

(function (root) {
  "use strict";

  // Never content, whatever the scoring says. `script`/`noscript` lead the
  // list: an archive is a photograph, not a program (TRU-03).
  const DROP_TAGS = new Set([
    "script", "noscript", "style", "link", "meta", "template", "svg", "canvas",
    "iframe", "object", "embed", "form", "input", "select", "textarea",
    "button", "dialog", "audio", "video", "map", "base",
  ]);

  // Structural furniture. `header` is spared inside an article/main, where it
  // usually holds the headline and byline rather than the site chrome.
  const FURNITURE = new Set(["nav", "aside", "footer"]);
  const SHELTERS_HEADER = new Set(["article", "main"]);

  const UNLIKELY = /-ad-|\bads?\b|advert|banner|breadcrumb|combx|comment|consent|cookie|disqus|extra|footer|gdpr|masthead|menu|newsletter|paywall|pager|pagination|popup|promo|related|remark|rss|share|shoutbox|sidebar|skyscraper|social|sponsor|subscribe|supplemental|toolbar|utility|widget/i;
  const MAYBE = /and|article|body|column|content|main|shadow|story/i;
  const POSITIVE = /article|blog|body|content|entry|hentry|h-entry|main|page|post|story|text/i;
  const NEGATIVE = /hidden|banner|combx|comment|com-|contact|foot|footnote|gdpr|masthead|media|meta|outbrain|promo|related|scroll|share|shoutbox|sidebar|skyscraper|sponsor|shopping|tags|widget|cookie|consent|newsletter|subscribe/i;
  const HIDDEN_ROLES = new Set(["banner", "navigation", "complementary", "contentinfo", "dialog", "alertdialog", "search", "menu", "menubar", "toolbar", "tablist"]);

  // Elements dense enough to carry an article's weight.
  const PARAGRAPH_TAGS = new Set(["p", "pre", "td", "blockquote", "article", "section", "div"]);
  const MIN_PARAGRAPH = 25;

  const isText = (n) => n.tag === "#text";
  const kids = (n) => n.children || [];
  const attr = (n, name) => (n.attrs && n.attrs[name]) || "";
  const hasAttr = (n, name) => !!(n.attrs && Object.prototype.hasOwnProperty.call(n.attrs, name));

  // Every walk here recurses, and pages really do nest a few thousand
  // wrappers. The cap turns a stack overflow into a slightly flatter capture.
  // domlite.js caps its own nesting lower, so this is a backstop for trees
  // that arrive from somewhere else.
  const MAX_DEPTH = 1024;

  // --- measurements --------------------------------------------------------
  //
  // textOf used to rebuild a node's entire subtree string every time it was
  // asked, and the scorer asks once per node: quadratic, and a 189 KiB page
  // took 58 seconds inside a content script. Each node's measurements are now
  // computed once and cached here.
  //
  // The cache is only sound because it is dropped in step with the tree: the
  // two functions that mutate children — prune() and cleanup() — forget a
  // node *after* they have finished rewriting it, bottom-up, so no cached
  // value is ever read across a change to the subtree it describes.
  let measures = new WeakMap();

  function measureOf(node) {
    let m = measures.get(node);
    if (!m) {
      m = {};
      measures.set(node, m);
    }
    return m;
  }

  const forget = (node) => measures.delete(node);

  function textOf(node) {
    if (isText(node)) return node.text || "";
    const m = measureOf(node);
    if (m.text === undefined) m.text = kids(node).map(textOf).join("");
    return m.text;
  }

  // Memoizing textOf() alone is not enough: building each node's string once
  // still copies every character once per level of nesting, which for a deeply
  // wrapped page is the same quadratic in a quieter coat. Almost every caller
  // only wants the *length*, so measure that without ever building the string.
  //
  // A span describes `textOf(node).replace(/\s+/g, " ")` — its length, and
  // whether it begins or ends with the single space a run of whitespace
  // collapses to. Two spans concatenate by adding their lengths and merging
  // one space where the join has a space on both sides, which is what makes
  // the measurement exactly equal to collapsing the built string.
  const EMPTY_SPAN = { len: 0, lead: false, trail: false };

  function collapsedSpan(node) {
    if (isText(node)) {
      const text = (node.text || "").replace(/\s+/g, " ");
      if (!text) return EMPTY_SPAN;
      return { len: text.length, lead: text[0] === " ", trail: text[text.length - 1] === " " };
    }
    const m = measureOf(node);
    if (m.span === undefined) {
      let span = EMPTY_SPAN;
      for (const child of kids(node)) {
        const next = collapsedSpan(child);
        if (!next.len) continue;
        span = span.len
          ? {
              len: span.len + next.len - (span.trail && next.lead ? 1 : 0),
              lead: span.lead,
              trail: next.trail,
            }
          : next;
      }
      m.span = span;
    }
    return m.span;
  }

  function textLength(node) {
    const span = collapsedSpan(node);
    return Math.max(0, span.len - (span.lead ? 1 : 0) - (span.trail ? 1 : 0));
  }

  // The text inside this node that sits in a link. Nested <a> is not counted
  // twice, which is what the hand-written walk did before.
  function linkedLength(node) {
    if (isText(node)) return 0;
    const m = measureOf(node);
    if (m.linked === undefined) {
      let linked = 0;
      for (const c of kids(node)) {
        if (isText(c)) continue;
        linked += c.tag === "a" ? textLength(c) : linkedLength(c);
      }
      m.linked = linked;
    }
    return m.linked;
  }

  function linkDensity(node) {
    const total = textLength(node);
    if (!total) return 0;
    return Math.min(1, linkedLength(node) / total);
  }

  /** hasImageWithin is firstOf(node, img) without the per-node subtree walk. */
  function hasImageWithin(node) {
    if (isText(node)) return false;
    const m = measureOf(node);
    if (m.img === undefined) {
      m.img = false;
      for (const c of kids(node)) {
        if (isText(c)) continue;
        if (c.tag === "img" || hasImageWithin(c)) {
          m.img = true;
          break;
        }
      }
    }
    return m.img;
  }

  function signature(node) {
    return `${attr(node, "class")} ${attr(node, "id")}`;
  }

  function classWeight(node) {
    const sig = signature(node);
    let weight = 0;
    if (POSITIVE.test(sig)) weight += 25;
    if (NEGATIVE.test(sig)) weight -= 25;
    if (node.tag === "article" || node.tag === "main") weight += 30;
    return weight;
  }

  function cloneTree(node, depth) {
    const d = depth || 0;
    if (isText(node)) return { tag: "#text", text: node.text };
    return {
      tag: node.tag,
      attrs: Object.assign({}, node.attrs),
      children: d >= MAX_DEPTH ? [] : kids(node).map((c) => cloneTree(c, d + 1)),
      hidden: node.hidden,
      fixed: node.fixed,
    };
  }

  // --- pruning ------------------------------------------------------------

  function shouldDrop(node, parentTag, opts) {
    if (DROP_TAGS.has(node.tag)) return true;
    if (node.hidden) return true;
    // A sticky/fixed element is a floating overlay (cookie bar, share rail,
    // newsletter nag) — never the article body.
    if (node.fixed) return true;
    if (attr(node, "aria-hidden") === "true" && textLength(node) < 500) return true;
    // `hidden` is a boolean attribute: its presence hides the element,
    // whatever the value — except `until-found`, which only collapses it.
    // The old test was `attr(node, "hidden") !== ""`, which is true for an
    // *absent* attribute and false for the commonest form of all, `<div
    // hidden>`, so the one case that matters was the one it let through.
    if (hasAttr(node, "hidden") && attr(node, "hidden").toLowerCase() !== "until-found") return true;
    if (HIDDEN_ROLES.has(attr(node, "role").toLowerCase())) return true;
    if (FURNITURE.has(node.tag)) return true;
    if (node.tag === "header" && !SHELTERS_HEADER.has(parentTag)) return true;
    if (!opts.keepUnlikely) {
      const sig = signature(node);
      if (sig.trim() && UNLIKELY.test(sig) && !MAYBE.test(sig)) return true;
    }
    return false;
  }

  function prune(node, opts, parentTag) {
    node.children = kids(node).filter((child) => {
      if (isText(child)) return true;
      if (shouldDrop(child, parentTag || node.tag, opts)) return false;
      prune(child, opts, node.tag);
      return true;
    });
    // This node's children just changed, and prune() has already forgotten
    // every descendant on its way back up, so anything measured during the
    // pass above is discarded rather than believed.
    forget(node);
    return node;
  }

  // --- scoring ------------------------------------------------------------

  function scoreParagraphs(tree) {
    const scores = new Map();
    const bump = (node, amount) => {
      if (!node || isText(node)) return;
      if (!scores.has(node)) scores.set(node, classWeight(node));
      scores.set(node, scores.get(node) + amount);
    };

    const stack = [];
    (function walk(node) {
      for (const child of kids(node)) {
        if (isText(child)) continue;
        if (PARAGRAPH_TAGS.has(child.tag)) {
          // `div`/`section` only count for their own loose text, otherwise a
          // wrapper would be scored once per nesting level — and the subtree
          // string they used to build first was then thrown away unread.
          const loose = child.tag === "div" || child.tag === "section" || child.tag === "article";
          const own = loose
            ? kids(child).filter(isText).map((t) => t.text).join(" ").replace(/\s+/g, " ").trim()
            : textOf(child).replace(/\s+/g, " ").trim();
          if (own.length >= MIN_PARAGRAPH) {
            const base = 1 + (own.match(/[,，、]/g) || []).length + Math.min(Math.floor(own.length / 100), 3);
            bump(child, base);
            bump(stack[stack.length - 1], base);
            bump(stack[stack.length - 2], base / 2);
            bump(stack[stack.length - 3], base / 3);
          }
        }
        stack.push(child);
        walk(child);
        stack.pop();
      }
    })(tree);

    return scores;
  }

  function firstOf(tree, predicate) {
    let found = null;
    (function walk(node) {
      if (found) return;
      for (const child of kids(node)) {
        if (found) return;
        if (isText(child)) continue;
        if (predicate(child)) {
          found = child;
          return;
        }
        walk(child);
      }
    })(tree);
    return found;
  }

  function pickTop(tree, scores) {
    let best = null;
    let bestScore = 0;
    for (const [node, score] of scores) {
      const final = score * (1 - linkDensity(node));
      if (final > bestScore) {
        best = node;
        bestScore = final;
      }
    }
    if (best) return { node: best, score: bestScore, scores };
    const fallback =
      firstOf(tree, (n) => n.tag === "article") ||
      firstOf(tree, (n) => n.tag === "main" || attr(n, "role") === "main") ||
      firstOf(tree, (n) => n.tag === "body") ||
      tree;
    return { node: fallback, score: 0, scores };
  }

  // Readability's trick: the top candidate is often one column of the article,
  // with the lede or a pull quote as a sibling. Pull in siblings that score
  // well or read like body copy.
  function withSiblings(tree, top) {
    const parent = parentOf(tree, top.node);
    if (!parent || parent === tree) return top.node;
    const threshold = Math.max(10, top.score * 0.2);
    const kept = [];
    for (const sibling of kids(parent)) {
      if (isText(sibling)) continue;
      if (sibling === top.node) {
        kept.push(sibling);
        continue;
      }
      const score = (top.scores.get(sibling) || 0) * (1 - linkDensity(sibling));
      const bodyish = sibling.tag === "p" && textLength(sibling) > 80 && linkDensity(sibling) < 0.25;
      if (score >= threshold || bodyish) kept.push(sibling);
    }
    if (kept.length <= 1) return top.node;
    return { tag: "div", attrs: {}, children: kept };
  }

  function parentOf(tree, target) {
    let found = null;
    (function walk(node) {
      if (found) return;
      for (const child of kids(node)) {
        if (child === target) {
          found = node;
          return;
        }
        if (!isText(child)) walk(child);
        if (found) return;
      }
    })(tree);
    return found;
  }

  // --- post-extraction cleanup -------------------------------------------

  function cleanup(node) {
    node.children = kids(node).filter((child) => {
      if (isText(child)) return true;
      // cleanup() forgets `child` on its way out, so these three measurements
      // are recomputed from the subtree as it now stands, not as it arrived.
      cleanup(child);
      if (child.tag === "a" || child.tag === "img" || child.tag === "br" || child.tag === "hr") return true;
      const len = textLength(child);
      if (!len && !hasImageWithin(child)) return false;
      // A dense block of links inside the article is a "related stories" rail
      // that survived pruning.
      if (len < 120 && linkDensity(child) > 0.5) return false;
      return true;
    });
    forget(node);
    return node;
  }

  function headingTitle(node) {
    const h = firstOf(node, (n) => n.tag === "h1") || firstOf(node, (n) => n.tag === "h2");
    return h ? textOf(h).replace(/\s+/g, " ").trim() : "";
  }

  /**
   * extract turns a node tree into Markdown plus the plain text the content
   * hash is computed over.
   *
   * opts.selectionOnly skips scoring entirely — when the user selected a
   * region, that region *is* the answer (CLIP-04).
   */
  function extract(tree, opts) {
    const options = Object.assign({ baseUrl: "", minLength: 250, selectionOnly: false }, opts || {});
    const run = (keepUnlikely) => {
      measures = new WeakMap();
      const working = prune(cloneTree(tree), { keepUnlikely });
      const contentRoot = options.selectionOnly
        ? working
        : withSiblings(working, pickTop(working, scoreParagraphs(working)));
      cleanup(contentRoot);
      const markdown = root.MonoMarkdown.toMarkdown(contentRoot, { baseUrl: options.baseUrl });
      return { markdown, contentRoot };
    };

    let result = run(false);
    // A too-short result usually means the unlikely-candidate filter ate the
    // article (single-page apps love class names like "content-widget").
    // Retry once with that filter off rather than returning a stub.
    if (!options.selectionOnly && plainText(result.markdown).length < options.minLength) {
      const relaxed = run(true);
      if (plainText(relaxed.markdown).length > plainText(result.markdown).length) result = relaxed;
    }

    const text = plainText(result.markdown);
    return {
      markdown: result.markdown,
      text,
      title: headingTitle(result.contentRoot),
      excerpt: text.slice(0, 280),
      wordCount: text ? text.split(/\s+/).length : 0,
    };
  }

  // The hash and the excerpt should describe the prose, not the syntax, so
  // strip the Markdown scaffolding back out before measuring.
  function plainText(markdown) {
    return markdown
      .replace(/```[\s\S]*?```/g, " ")
      .replace(/!\[[^\]]*\]\([^)]*\)/g, " ")
      .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
      .replace(/^[#>\s-]+/gm, "")
      .replace(/[*_`\\|]/g, "")
      .replace(/\s+/g, " ")
      .trim();
  }

  function fromHTML(html, opts) {
    return extract(root.MonoDomLite.parse(html), opts);
  }

  /**
   * snapshot converts a live DOM element into the node tree the extractor
   * consumes, recording the two things only a rendered page knows: what is
   * invisible, and what floats above the content.
   */
  function snapshot(el, win) {
    const view = win || (typeof window !== "undefined" ? window : null);
    const ELEMENT_NODE = 1;
    const TEXT_NODE = 3;

    function convert(node, depth) {
      if (node.nodeType === TEXT_NODE) {
        return node.nodeValue ? { tag: "#text", text: node.nodeValue } : null;
      }
      if (node.nodeType !== ELEMENT_NODE) return null;
      const tag = node.tagName.toLowerCase();
      const out = { tag, attrs: {}, children: [] };
      for (const a of node.attributes || []) out.attrs[a.name.toLowerCase()] = a.value;
      // JSON-LD is the best source of byline and publication date, so it is
      // carried through the snapshot even though DROP_TAGS removes scripts
      // before any content scoring happens.
      if (tag === "script") {
        if (/ld\+json/i.test(out.attrs.type || "")) out.children.push({ tag: "#text", text: node.textContent || "" });
        return out;
      }
      if (tag === "style" || tag === "template") return out;

      if (view && depth < 400) {
        let style = null;
        try {
          style = view.getComputedStyle(node);
        } catch {
          style = null;
        }
        if (style) {
          if (style.display === "none" || style.visibility === "hidden" || style.opacity === "0") out.hidden = true;
          if (style.position === "fixed" || style.position === "sticky") out.fixed = true;
        }
      }
      if (!out.hidden && depth < MAX_DEPTH) {
        for (const child of node.childNodes) {
          const converted = convert(child, depth + 1);
          if (converted) out.children.push(converted);
        }
      }
      return out;
    }

    return convert(el, 0) || { tag: "#root", attrs: {}, children: [] };
  }

  /**
   * defangHtmlSource deletes the obvious executable constructs from an HTML
   * *string*, by pattern. It is a source-level scrub, not a sanitizer, and
   * the difference is the whole reason it is no longer called stripScripts —
   * that name read as a guarantee it has never been able to make.
   *
   * What it does remove, as it appears literally in the source: `<script>`
   * and `<noscript>` elements, `on*=` handler attributes, and `javascript:`
   * in an href/src/action.
   *
   * What it does not, each verified and pinned in readable.test.mjs:
   *
   *   - it never decodes entities, so any part of a scheme written as one
   *     walks past — `jav&#x09;ascript:`, `&#106;avascript:`,
   *     `java&#115;cript:`, `javascript&colon;`;
   *   - it only looks at href/src/action, so `style="…url(javascript:…)"`
   *     and every other attribute that resolves a URL are untouched;
   *   - it matches attribute *names*, so SVG's indirection —
   *     `<set attributeName="onload" to="…">` — says nothing it recognises;
   *   - it treats attribute values as opaque, so markup carried inside one
   *     (`<iframe srcdoc="&lt;script&gt;…&lt;/script&gt;">`) survives whole.
   *
   * So: fine for tidying markup already trusted, nowhere near enough to
   * render attacker-controlled HTML. Anything that must actually be safe has
   * to be rebuilt from the parsed tree (domlite.js) rather than patched in
   * source — which is exactly what the capture path does. It emits Markdown
   * and hands no HTML on at all, which is why nothing here calls this.
   */
  function defangHtmlSource(html) {
    return String(html)
      .replace(/<script\b[\s\S]*?<\/script\s*>/gi, "")
      .replace(/<script\b[^>]*\/?>/gi, "")
      .replace(/<noscript\b[\s\S]*?<\/noscript\s*>/gi, "")
      .replace(/\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, "")
      .replace(/(href|src|action)\s*=\s*("\s*javascript:[^"]*"|'\s*javascript:[^']*'|javascript:[^\s>]*)/gi, '$1="#"');
  }

  root.MonoReadable = {
    extract, fromHTML, snapshot, defangHtmlSource, plainText,
    textOf, textLength, linkDensity,
  };
})(globalThis);
