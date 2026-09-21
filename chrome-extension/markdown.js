/**
 * MonoAgent Bridge — Markdown renderer (CLIP-05)
 *
 * Converts a node tree (see domlite.js for the shape, readable.js for how a
 * live DOM is snapshotted into one) into clean Markdown.
 *
 * Deliberately not a general HTML-to-Markdown library: it only emits
 * constructs a Markdown reader can round-trip — ATX headings, lists,
 * blockquotes, fenced code, pipe tables, links and images — and drops
 * everything else. That is also why the archive can never execute: no raw
 * HTML is ever passed through, and `javascript:`-style URLs are dropped
 * rather than linked (TRU-03).
 *
 * Runs unmodified in a page (injected by capture_page.js), in the service
 * worker, and in node under `node --test` — it touches no DOM API.
 */

(function (root) {
  "use strict";

  const HEADINGS = { h1: 1, h2: 2, h3: 3, h4: 4, h5: 5, h6: 6 };

  // Tags that force a block break. Anything not listed is treated as inline
  // and folded into the surrounding paragraph.
  const BLOCK = new Set([
    "address", "article", "aside", "blockquote", "details", "div", "dl", "dd",
    "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2",
    "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p",
    "pre", "section", "summary", "table", "tbody", "tfoot", "thead", "tr",
    "ul",
  ]);

  // The schemes a saved page may link to (TRU-03). An allowlist, checked
  // after the URL has been normalised — which is the whole point, because the
  // denylist this replaces was checked against the raw href and could be
  // walked past twice over: browsers strip ASCII control characters out of a
  // URL before parsing it, so a tab, a newline or a NUL spliced into
  // "javascript:" still navigated; and factoring the colon out of the
  // alternation meant `data:text/html;base64,...` never matched at all.
  //
  // This is the policy the Go crawler already applies — see resolveAgainst in
  // internal/crawlsite/extract.go — plus mailto:, which a saved article does
  // legitimately carry.
  const SAFE_SCHEMES = new Set(["http:", "https:", "mailto:"]);

  // Drop the ASCII control characters a browser would drop anyway, so the
  // scheme decision is made about the URL that would actually be followed.
  function stripControl(s) {
    let out = "";
    for (const ch of String(s)) {
      const code = ch.codePointAt(0);
      if (code > 31 && code !== 127) out += ch;
    }
    return out;
  }

  // Markdown ends a link target at the first ")", so a ")" inside an href
  // closes the link early and lets everything after it be read as markup:
  // `[safe](/ok) [CLICK ME](https://evil.test/steal)` out of a single <a>.
  // Percent-encode the characters that can end or reshape a target.
  const URL_BREAKERS = /[()<>"'`\\\s]/g;

  function encodeUrl(url) {
    if (!url) return "";
    return url.replace(URL_BREAKERS, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);
  }

  const SCHEME_LIKE = /^[a-zA-Z][a-zA-Z0-9+.\-]*:/;

  function isText(node) {
    return node && node.tag === "#text";
  }

  function childrenOf(node) {
    return (node && node.children) || [];
  }

  function attr(node, name) {
    return (node && node.attrs && node.attrs[name]) || "";
  }

  // Escape the characters that would otherwise be read as Markdown syntax.
  // Intentionally conservative: over-escaping makes prose unreadable, and the
  // output is consumed by a chunker and a human, not a strict parser.
  //
  // `(` and `)` are deliberately not escaped. They only carry meaning
  // immediately after a `]`, and a label cannot reach that position because
  // `[` and `]` are escaped here; the place a stray `)` really did break out
  // was the link *target*, and that is fixed where targets are written, by
  // encodeUrl(), rather than by escaping every parenthesis in every sentence.
  function escapeText(s) {
    return s.replace(/([\\`*_[\]])/g, "\\$1");
  }

  function collapse(s) {
    return s.replace(/\s+/g, " ");
  }

  function resolveUrl(href, base) {
    if (!href) return "";
    const raw = stripControl(href).trim();
    if (!raw) return "";
    let url;
    try {
      url = new URL(raw, base || undefined);
    } catch {
      // Unparseable and nothing to resolve against: a relative reference,
      // which names no scheme and so can execute nothing. Pass it on.
      return SCHEME_LIKE.test(raw) ? "" : raw;
    }
    return SAFE_SCHEMES.has(url.protocol) ? url.href : "";
  }

  // A data: URI is bytes, not an address. It can carry a whole document
  // (`data:image/svg+xml,<svg onload=...>`), and inlined base64 would bloat
  // readable.md past the point of usefulness — the bytes already live in
  // page.mhtml, which is where fidelity belongs.
  function imageSrc(src, base) {
    if (/^data:/i.test(stripControl(src || "").trim())) return "#embedded-image";
    return resolveUrl(src, base);
  }

  function inline(node, ctx) {
    if (isText(node)) return escapeText(collapse(node.text || ""));
    const tag = node.tag;
    if (tag === "br") return "  \n";
    if (tag === "img") {
      const src = imageSrc(attr(node, "src") || attr(node, "data-src"), ctx.baseUrl);
      const alt = collapse(attr(node, "alt")).trim();
      if (!src && !alt) return "";
      return `![${escapeText(alt)}](${encodeUrl(src)})`;
    }
    const inner = childrenOf(node).map((c) => inline(c, ctx)).join("");
    switch (tag) {
      case "strong":
      case "b":
        return inner.trim() ? `**${inner.trim()}**` : "";
      case "em":
      case "i":
        return inner.trim() ? `*${inner.trim()}*` : "";
      case "del":
      case "s":
        return inner.trim() ? `~~${inner.trim()}~~` : "";
      case "code": {
        const raw = collapse(textOf(node)).trim();
        if (!raw) return "";
        const fence = "`".repeat(longestBacktickRun(raw) + 1);
        return `${fence}${raw}${fence}`;
      }
      case "a": {
        const href = resolveUrl(attr(node, "href"), ctx.baseUrl);
        const label = inner.trim();
        if (!label) return "";
        return href ? `[${label}](${encodeUrl(href)})` : label;
      }
      default:
        return inner;
    }
  }

  function longestBacktickRun(s) {
    let best = 0;
    for (const run of s.match(/`+/g) || []) best = Math.max(best, run.length);
    return best;
  }

  function textOf(node) {
    if (isText(node)) return node.text || "";
    return childrenOf(node).map(textOf).join("");
  }

  function isBlock(node) {
    return !isText(node) && BLOCK.has(node.tag);
  }

  // Render a container's children: runs of inline nodes become paragraphs,
  // block children are rendered in place. This is what keeps `<div>text<p>x`
  // from losing the loose text.
  function renderChildren(node, ctx) {
    const blocks = [];
    let run = "";
    const flush = () => {
      const text = run.trim();
      if (text) blocks.push(text);
      run = "";
    };
    for (const child of childrenOf(node)) {
      if (isBlock(child)) {
        flush();
        const rendered = renderBlock(child, ctx);
        if (rendered) blocks.push(rendered);
      } else {
        run += inline(child, ctx);
      }
    }
    flush();
    return blocks;
  }

  function join(blocks) {
    return blocks.filter(Boolean).join("\n\n");
  }

  function prefixLines(text, first, rest) {
    return text
      .split("\n")
      .map((line, i) => (i === 0 ? first : rest) + line)
      .join("\n");
  }

  function renderList(node, ctx, ordered) {
    const items = [];
    let n = 1;
    for (const child of childrenOf(node)) {
      if (isText(child) || child.tag !== "li") continue;
      const body = join(renderChildren(child, ctx));
      if (!body.trim()) continue;
      const marker = ordered ? `${n++}. ` : "- ";
      items.push(prefixLines(body, marker, " ".repeat(marker.length)));
    }
    return items.join("\n");
  }

  function cell(node, ctx) {
    return childrenOf(node)
      .map((c) => (isBlock(c) ? collapse(textOf(c)) : inline(c, ctx)))
      .join("")
      .replace(/\|/g, "\\|")
      .replace(/\n/g, " ")
      .trim();
  }

  // Bounds for one rendered table. Without them 130k <tr> overflowed the
  // stack inside `Math.max(...rows)`, and 1 MB of HTML became 360 MB of
  // Markdown in 2.4 seconds. Past these sizes a pipe table has stopped being
  // something a reader can read anyway.
  const MAX_TABLE_ROWS = 2000;
  const MAX_TABLE_COLS = 100;

  function collectRows(node, rows, depth) {
    const d = depth || 0;
    for (const child of childrenOf(node)) {
      if (rows.length >= MAX_TABLE_ROWS) return rows;
      if (isText(child)) continue;
      if (child.tag === "tr") rows.push(child);
      else if (d < 64) collectRows(child, rows, d + 1);
    }
    return rows;
  }

  function renderTable(node, ctx) {
    const rows = collectRows(node, [], 0).map((tr) =>
      childrenOf(tr)
        .filter((c) => !isText(c) && (c.tag === "td" || c.tag === "th"))
        .slice(0, MAX_TABLE_COLS)
        .map((c) => cell(c, ctx))
    );
    if (!rows.length) return "";
    // A fold, not `Math.max(...rows)`: the spread is an argument list, and an
    // argument list that long is a stack overflow.
    let width = 0;
    for (const r of rows) width = Math.max(width, r.length);
    if (width === 0) return "";
    const pad = (r) => r.concat(Array(width - r.length).fill(""));
    const head = pad(rows[0]);
    const lines = [`| ${head.join(" | ")} |`, `| ${head.map(() => "---").join(" | ")} |`];
    for (const r of rows.slice(1)) lines.push(`| ${pad(r).join(" | ")} |`);
    return lines.join("\n");
  }

  function renderBlock(node, ctx) {
    const tag = node.tag;
    if (HEADINGS[tag]) {
      const text = collapse(childrenOf(node).map((c) => inline(c, ctx)).join("")).trim();
      return text ? `${"#".repeat(HEADINGS[tag])} ${text}` : "";
    }
    switch (tag) {
      case "hr":
        return "---";
      case "pre": {
        const code = textOf(node).replace(/\n+$/, "");
        if (!code.trim()) return "";
        const lang = codeLanguage(node);
        return `\`\`\`${lang}\n${code}\n\`\`\``;
      }
      case "ul":
        return renderList(node, ctx, false);
      case "ol":
        return renderList(node, ctx, true);
      case "table":
        return renderTable(node, ctx);
      case "blockquote": {
        const body = join(renderChildren(node, ctx));
        return body.trim() ? prefixLines(body, "> ", "> ") : "";
      }
      case "figcaption":
      case "summary": {
        const text = collapse(childrenOf(node).map((c) => inline(c, ctx)).join("")).trim();
        return text ? (tag === "summary" ? `**${text}**` : `*${text}*`) : "";
      }
      default:
        return join(renderChildren(node, ctx));
    }
  }

  function codeLanguage(pre) {
    const classes = `${attr(pre, "class")} ${childrenOf(pre)
      .filter((c) => !isText(c) && c.tag === "code")
      .map((c) => attr(c, "class"))
      .join(" ")}`;
    const m = classes.match(/(?:language|lang)[-:]([\w+#-]+)/i);
    return m ? m[1].toLowerCase() : "";
  }

  /**
   * toMarkdown renders a node (usually the extracted content root) as
   * Markdown. `opts.baseUrl` makes relative links and image sources absolute,
   * which matters because readable.md is read far from the page it came from.
   */
  function toMarkdown(node, opts) {
    const ctx = { baseUrl: (opts && opts.baseUrl) || "" };
    const body = isBlock(node) || node.tag === "#root"
      ? join(renderChildren(node, ctx))
      : join([inline(node, ctx)]);
    return body.replace(/\n{3,}/g, "\n\n").trim();
  }

  root.MonoMarkdown = { toMarkdown, textOf, escapeText, resolveUrl, encodeUrl };
})(globalThis);
