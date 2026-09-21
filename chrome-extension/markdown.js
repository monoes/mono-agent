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

  // URL schemes that are executable rather than addressable. A saved page is
  // a photograph, not a program, so these never become links (TRU-03).
  const UNSAFE_URL = /^\s*(javascript|vbscript|data:text\/html|file):/i;

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
  function escapeText(s) {
    return s.replace(/([\\`*_[\]])/g, "\\$1");
  }

  function collapse(s) {
    return s.replace(/\s+/g, " ");
  }

  function resolveUrl(href, base) {
    if (!href) return "";
    if (UNSAFE_URL.test(href)) return "";
    if (!base) return href.trim();
    try {
      return new URL(href, base).href;
    } catch {
      return href.trim();
    }
  }

  // A long data: URI (a base64 image inlined in the markup) would bloat
  // readable.md past the point of usefulness — the bytes already live in
  // page.mhtml, which is where fidelity belongs.
  function imageSrc(src, base) {
    if (/^data:/i.test(src) && src.length > 512) return "#embedded-image";
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
      return `![${escapeText(alt)}](${src})`;
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
        return href ? `[${label}](${href})` : label;
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

  function collectRows(node, rows) {
    for (const child of childrenOf(node)) {
      if (isText(child)) continue;
      if (child.tag === "tr") rows.push(child);
      else collectRows(child, rows);
    }
    return rows;
  }

  function renderTable(node, ctx) {
    const rows = collectRows(node, []).map((tr) =>
      childrenOf(tr)
        .filter((c) => !isText(c) && (c.tag === "td" || c.tag === "th"))
        .map((c) => cell(c, ctx))
    );
    if (!rows.length) return "";
    const width = Math.max(...rows.map((r) => r.length));
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

  root.MonoMarkdown = { toMarkdown, textOf, escapeText, resolveUrl };
})(globalThis);
