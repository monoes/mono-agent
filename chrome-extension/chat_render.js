/**
 * MonoAgent Bridge — rendering an assistant reply (issue #451, slice 2)
 *
 * markdown.js goes the other way (HTML to Markdown), so this is the small
 * Markdown-to-nodes half the chat needs: paragraphs, headings, bullet and
 * numbered lists, quotes, fenced code, and inline code / bold / italic /
 * links. Nothing else is interpreted; everything unrecognised is text.
 *
 * SAFETY. The reply may contain whatever a hostile page talked the model
 * into saying, so the output is a tree of plain descriptions and the DOM is
 * built only with createElement / textContent / setAttribute of a vetted
 * href. There is no innerHTML anywhere, raw HTML in a reply is shown as
 * text, and a link is kept only for http(s) and mailto.
 *
 *   parse(text) -> blocks    pure, node-tested
 *   toDom(document, blocks)  builds a DocumentFragment
 */

(function (root) {
  "use strict";

  const SAFE_HREF = /^(https?:|mailto:)/i;

  /** safeHref returns the href to link, or "" when it must not be a link. */
  function safeHref(raw) {
    // Same reasoning as markdown.js: browsers drop control characters from a
    // URL before parsing it, so judge the URL that would be followed.
    // eslint-disable-next-line no-control-regex
    const href = String(raw || "").replace(/[\u0000- \u007f]+/g, "");
    if (!SAFE_HREF.test(href)) return "";
    try {
      return new URL(href).href;
    } catch {
      return "";
    }
  }

  /**
   * hostOf is the host the browser will go to for an href that safeHref
   * accepted (userinfo, escapes and IDN already resolved by URL), shown next
   * to a link so the label cannot claim a different site.
   */
  function hostOf(href) {
    try {
      const u = new URL(href);
      if (u.protocol === "mailto:") return decodeURIComponent(u.pathname).split("@").pop().toLowerCase();
      return u.host;
    } catch {
      return "";
    }
  }

  // Only an explicit Markdown link is a link. A bare address in a reply stays
  // text: a model talked into writing one with someone's data in its query
  // must not leave it one tap away.
  const INLINE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|(\*[^*\s][^*\n]*\*)|(\[[^\]\n]+\]\([^)\s]+\))/;

  /** inline splits one line of text into runs. */
  function inline(text) {
    const out = [];
    let rest = String(text);
    while (rest) {
      const m = INLINE.exec(rest);
      if (!m) {
        out.push({ t: "text", text: rest });
        break;
      }
      if (m.index > 0) out.push({ t: "text", text: rest.slice(0, m.index) });
      const tok = m[0];
      if (m[1]) {
        out.push({ t: "code", text: tok.slice(1, -1) });
      } else if (m[2]) {
        out.push({ t: "strong", text: tok.slice(2, -2) });
      } else if (m[3]) {
        out.push({ t: "em", text: tok.slice(1, -1) });
      } else {
        const close = tok.indexOf("](");
        const label = tok.slice(1, close);
        const href = safeHref(tok.slice(close + 2, -1));
        out.push(href ? { t: "link", text: label, href, host: hostOf(href) } : { t: "text", text: label });
      }
      rest = rest.slice(m.index + tok.length);
    }
    return out;
  }

  /** parse turns reply text into blocks. An unterminated fence is still code (the reply is streaming). */
  function parse(text) {
    const lines = String(text).replace(/\r\n?/g, "\n").split("\n");
    const blocks = [];
    let para = [];
    let list = null;

    const flushPara = () => {
      if (para.length) blocks.push({ type: "p", runs: inline(para.join(" ")) });
      para = [];
    };
    const flushList = () => {
      if (list) blocks.push(list);
      list = null;
    };

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      const fence = /^\s*```\s*([\w+-]*)\s*$/.exec(line);
      if (fence) {
        flushPara();
        flushList();
        const body = [];
        i++;
        while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) body.push(lines[i++]);
        blocks.push({ type: "code", lang: fence[1] || "", text: body.join("\n") });
        continue;
      }
      if (!line.trim()) {
        flushPara();
        flushList();
        continue;
      }
      const heading = /^(#{1,6})\s+(.*)$/.exec(line);
      if (heading) {
        flushPara();
        flushList();
        blocks.push({ type: "h", level: heading[1].length, runs: inline(heading[2]) });
        continue;
      }
      const item = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(line);
      if (item) {
        flushPara();
        const ordered = /\d/.test(item[1]);
        if (!list || list.ordered !== ordered) {
          flushList();
          list = { type: "list", ordered, items: [] };
        }
        list.items.push(inline(item[2]));
        continue;
      }
      const quote = /^\s*>\s?(.*)$/.exec(line);
      if (quote) {
        flushPara();
        flushList();
        const last = blocks[blocks.length - 1];
        if (last && last.type === "quote") last.runs.push({ t: "text", text: " " }, ...inline(quote[1]));
        else blocks.push({ type: "quote", runs: inline(quote[1]) });
        continue;
      }
      flushList();
      para.push(line.trim());
    }
    flushPara();
    flushList();
    return blocks;
  }

  function runsToDom(doc, runs, parent) {
    for (const r of runs) {
      let node;
      if (r.t === "text") {
        parent.appendChild(doc.createTextNode(r.text));
        continue;
      }
      if (r.t === "code") node = doc.createElement("code");
      else if (r.t === "strong") node = doc.createElement("strong");
      else if (r.t === "em") node = doc.createElement("em");
      else {
        node = doc.createElement("a");
        node.setAttribute("href", r.href);
        node.setAttribute("target", "_blank");
        node.setAttribute("rel", "noopener noreferrer");
      }
      node.textContent = r.text;
      parent.appendChild(node);
      // Where the link really goes, in plain text beside it.
      if (r.t === "link" && r.host) parent.appendChild(doc.createTextNode(` (${r.host})`));
    }
  }

  /** toDom builds the blocks as DOM nodes inside a fragment. */
  function toDom(doc, blocks) {
    const frag = doc.createDocumentFragment();
    for (const b of blocks) {
      let el;
      if (b.type === "code") {
        el = doc.createElement("pre");
        const code = doc.createElement("code");
        code.textContent = b.text;
        el.appendChild(code);
      } else if (b.type === "list") {
        el = doc.createElement(b.ordered ? "ol" : "ul");
        for (const runs of b.items) {
          const li = doc.createElement("li");
          runsToDom(doc, runs, li);
          el.appendChild(li);
        }
      } else if (b.type === "h") {
        // Panel-sized: every heading is a strong line, not a document outline.
        el = doc.createElement("p");
        el.className = "chat-h";
        runsToDom(doc, b.runs, el);
      } else {
        el = doc.createElement(b.type === "quote" ? "blockquote" : "p");
        runsToDom(doc, b.runs, el);
      }
      frag.appendChild(el);
    }
    return frag;
  }

  root.MonoChatRender = { parse, toDom, safeHref, inline };
})(globalThis);
