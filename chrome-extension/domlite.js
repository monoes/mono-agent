/**
 * MonoAgent Bridge — domlite
 *
 * A tolerant, dependency-free HTML -> node-tree parser.
 *
 * The readable extractor (readable.js) works on a plain node tree rather
 * than on a live DOM, so that the scoring and the Markdown conversion — the
 * parts that decide what a saved page actually says — can be unit-tested in
 * node from an HTML string, with no browser and no jsdom. In Chrome the same
 * tree comes from MonoReadable.snapshot(document); here it comes from text.
 *
 * Node shape (shared with readable.js / markdown.js):
 *   element: { tag, attrs: {}, children: [] }
 *   text:    { tag: "#text", text: "..." }
 */

(function (root) {
  "use strict";

  const VOID = new Set([
    "area", "base", "br", "col", "embed", "hr", "img", "input", "link",
    "meta", "param", "source", "track", "wbr",
  ]);

  // Elements whose content is text, not markup — never descend into them.
  const RAW_TEXT = new Set(["script", "style", "textarea", "title"]);

  // Tags that implicitly close an open tag of the same family. Without this
  // a real-world `<li>a<li>b` nests instead of listing.
  const IMPLIED_CLOSE = {
    li: ["li"],
    tr: ["tr", "td", "th"],
    td: ["td", "th"],
    th: ["td", "th"],
    option: ["option"],
    dt: ["dt", "dd"],
    dd: ["dt", "dd"],
    p: ["p"],
  };

  const BLOCK_CLOSES_P = new Set([
    "address", "article", "aside", "blockquote", "div", "dl", "fieldset",
    "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header",
    "hr", "main", "nav", "ol", "p", "pre", "section", "table", "ul",
  ]);

  const NAMED_ENTITIES = {
    amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ",
    mdash: "—", ndash: "–", hellip: "…", rsquo: "’",
    lsquo: "‘", ldquo: "“", rdquo: "”", copy: "©",
    reg: "®", trade: "™", middot: "·", times: "×",
  };

  function decodeEntities(s) {
    if (s.indexOf("&") < 0) return s;
    return s.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (whole, body) => {
      if (body[0] === "#") {
        const code = body[1] === "x" || body[1] === "X"
          ? parseInt(body.slice(2), 16)
          : parseInt(body.slice(1), 10);
        return Number.isFinite(code) && code > 0 ? String.fromCodePoint(code) : whole;
      }
      const named = NAMED_ENTITIES[body.toLowerCase()];
      return named === undefined ? whole : named;
    });
  }

  // Find the ">" that ends a tag, skipping any inside a quoted attribute
  // value (`<a title="a > b">` is legal and common in prose-heavy pages).
  function findTagEnd(html, start) {
    let quote = null;
    for (let i = start + 1; i < html.length; i++) {
      const ch = html[i];
      if (quote) {
        if (ch === quote) quote = null;
      } else if (ch === '"' || ch === "'") {
        quote = ch;
      } else if (ch === ">") {
        return i;
      }
    }
    return -1;
  }

  const ATTR_RE = /([^\s"'=/>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'`=<>]+)))?/g;

  function parseOpenTag(raw) {
    const m = raw.match(/^([a-zA-Z][^\s/>]*)/);
    if (!m) return null;
    const tag = m[1].toLowerCase();
    const selfClose = /\/\s*$/.test(raw);
    const attrs = {};
    ATTR_RE.lastIndex = m[0].length;
    let a;
    while ((a = ATTR_RE.exec(raw))) {
      const name = a[1].toLowerCase();
      if (name === "/") continue;
      const value = a[2] !== undefined ? a[2] : a[3] !== undefined ? a[3] : a[4] !== undefined ? a[4] : "";
      if (!(name in attrs)) attrs[name] = decodeEntities(value);
    }
    return { tag, attrs, selfClose };
  }

  function parse(html) {
    const document = { tag: "#root", attrs: {}, children: [] };
    const stack = [document];
    const top = () => stack[stack.length - 1];
    const addText = (text) => {
      if (!text) return;
      top().children.push({ tag: "#text", text: decodeEntities(text) });
    };

    let i = 0;
    while (i < html.length) {
      const lt = html.indexOf("<", i);
      if (lt < 0) {
        addText(html.slice(i));
        break;
      }
      if (lt > i) addText(html.slice(i, lt));

      if (html.startsWith("<!--", lt)) {
        const end = html.indexOf("-->", lt);
        i = end < 0 ? html.length : end + 3;
        continue;
      }
      if (html[lt + 1] === "!" || html[lt + 1] === "?") {
        const end = html.indexOf(">", lt);
        i = end < 0 ? html.length : end + 1;
        continue;
      }

      const gt = findTagEnd(html, lt);
      if (gt < 0) {
        addText(html.slice(lt));
        break;
      }
      const raw = html.slice(lt + 1, gt);
      i = gt + 1;

      if (raw[0] === "/") {
        const name = raw.slice(1).trim().toLowerCase();
        for (let s = stack.length - 1; s > 0; s--) {
          if (stack[s].tag === name) {
            stack.length = s;
            break;
          }
        }
        continue;
      }

      const open = parseOpenTag(raw);
      if (!open) continue;

      const implied = IMPLIED_CLOSE[open.tag];
      if (implied && implied.includes(top().tag)) stack.pop();
      else if (top().tag === "p" && BLOCK_CLOSES_P.has(open.tag)) stack.pop();

      const node = { tag: open.tag, attrs: open.attrs, children: [] };
      top().children.push(node);

      if (VOID.has(open.tag) || open.selfClose) continue;

      if (RAW_TEXT.has(open.tag)) {
        const close = html.toLowerCase().indexOf(`</${open.tag}`, i);
        const text = html.slice(i, close < 0 ? html.length : close);
        if (text) node.children.push({ tag: "#text", text });
        if (close < 0) {
          i = html.length;
        } else {
          const end = html.indexOf(">", close);
          i = end < 0 ? html.length : end + 1;
        }
        continue;
      }

      stack.push(node);
    }
    return document;
  }

  root.MonoDomLite = { parse, decodeEntities };
})(globalThis);
