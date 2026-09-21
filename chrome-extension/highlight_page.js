/**
 * MonoAgent Bridge — the highlighter, in the page (RCL-04)
 *
 * A content script on every page. It does three things and nothing else:
 * turn a selection into a highlight, paint stored highlights back when the
 * page is revisited, and let a highlight be commented on or removed.
 *
 * All the judgement lives in highlights.js, which is loaded into this same
 * isolated world (see manifest.json) and is node-tested: the anchor shape,
 * the fragment construction, and — the part that actually decides whether
 * this works — `relocate`, which finds a stored passage in a page that has
 * changed since. This file is the DOM around it.
 *
 * TWO RULES it must not break:
 *
 *  - the page's own text is never modified, only wrapped. Highlights are
 *    <mark> elements around existing text nodes; nothing is inserted,
 *    removed or reordered, so a site's own scripts see the DOM they expect.
 *  - a highlight that cannot be found again is NOT painted. `relocate`
 *    returns -1 and this file draws nothing, because a mark on the wrong
 *    sentence is a lie about what someone read.
 */

(function () {
  "use strict";

  if (window.__monoagentHighlighter) return; // the script was injected twice
  window.__monoagentHighlighter = true;

  const H = globalThis.MonoHighlights;
  if (!H) return; // highlights.js did not load: do nothing rather than half-work

  const MARK_CLASS = "monoagent-highlight";
  const UI_ID = "monoagent-highlight-ui";
  const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEXTAREA", "SELECT", "SVG"]);
  const TINT = {
    yellow: "rgba(255, 214, 0, .38)",
    green: "rgba(46, 139, 87, .30)",
    blue: "rgba(56, 132, 255, .28)",
    pink: "rgba(233, 84, 160, .28)",
  };

  let painted = []; // { id, nodes: [<mark>] }

  // ── Talking to the worker ────────────────────────────────────────

  function ask(type, payload) {
    return new Promise((resolve) => {
      try {
        chrome.runtime.sendMessage(Object.assign({ type, url: location.href }, payload), (reply) => {
          void chrome.runtime.lastError; // a suspended worker is not an error here
          resolve(reply || null);
        });
      } catch {
        resolve(null);
      }
    });
  }

  // ── The page as text ─────────────────────────────────────────────

  /**
   * textMap walks the visible text nodes once and returns the page's text
   * plus, for each node, where it starts in that text. Offsets measured and
   * resolved against the same walk always agree, which is why the map is
   * built here rather than read off innerText.
   */
  function textMap() {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        const parent = node.parentElement;
        if (!parent || SKIP_TAGS.has(parent.tagName)) return NodeFilter.FILTER_REJECT;
        if (parent.closest(`#${UI_ID}`)) return NodeFilter.FILTER_REJECT;
        if (!node.nodeValue || !node.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    const nodes = [];
    let text = "";
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      nodes.push({ node, start: text.length, length: node.nodeValue.length });
      text += node.nodeValue;
    }
    return { text, nodes };
  }

  /** offsetOf gives the position of a (node, offset) pair in the map's text. */
  function offsetOf(map, node, offset) {
    for (const entry of map.nodes) {
      if (entry.node === node) return entry.start + offset;
    }
    return NaN;
  }

  // ── Painting ─────────────────────────────────────────────────────

  /** wrap puts a <mark> around [start, end) of the mapped text, across as
   *  many text nodes as the range spans. */
  function wrap(map, start, end, record) {
    const marks = [];
    for (const entry of map.nodes) {
      const from = Math.max(start, entry.start);
      const to = Math.min(end, entry.start + entry.length);
      if (from >= to) continue;
      let node = entry.node;
      // splitText leaves the page's characters exactly as they were; it
      // only changes which node holds them.
      if (from > entry.start) node = node.splitText(from - entry.start);
      if (to - from < node.nodeValue.length) node.splitText(to - from);

      const mark = document.createElement("mark");
      mark.className = MARK_CLASS;
      mark.dataset.monoagentHighlight = record.id;
      mark.style.background = TINT[record.color] || TINT.yellow;
      mark.style.color = "inherit";
      mark.style.cursor = "pointer";
      if (record.comment) mark.title = record.comment;
      node.parentNode.replaceChild(mark, node);
      mark.appendChild(node);
      marks.push(mark);
    }
    return marks;
  }

  /** unpaint removes a highlight's marks, putting the text back where it
   *  was. The page ends up with the characters it started with. */
  function unpaint(id) {
    for (const entry of painted.filter((p) => p.id === id)) {
      for (const mark of entry.nodes) {
        const parent = mark.parentNode;
        if (!parent) continue;
        while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
        parent.removeChild(mark);
        parent.normalize();
      }
    }
    painted = painted.filter((p) => p.id !== id);
  }

  /**
   * restore paints every stored highlight for this page. Painting changes
   * the DOM, so the map is rebuilt between highlights rather than reused —
   * cheap at the counts involved, and correct, which reusing it is not.
   */
  function restore(records) {
    for (const record of records || []) {
      const map = textMap();
      const at = H.relocate(map.text, record.anchor);
      if (at === -1) continue; // gone from the page: draw nothing
      const nodes = wrap(map, at, at + record.anchor.quote.length, record);
      if (nodes.length) painted.push({ id: record.id, nodes });
    }
  }

  // ── The floating UI ──────────────────────────────────────────────

  function dismiss() {
    const existing = document.getElementById(UI_ID);
    if (existing) existing.remove();
  }

  function panel(x, y) {
    dismiss();
    const box = document.createElement("div");
    box.id = UI_ID;
    box.style.cssText = [
      "position:absolute",
      `left:${Math.round(x)}px`,
      `top:${Math.round(y)}px`,
      "z-index:2147483647",
      "background:#0b0f14",
      "color:#e2e8f0",
      "font:13px/1.4 -apple-system,system-ui,sans-serif",
      "border-radius:8px",
      "padding:6px",
      "box-shadow:0 6px 24px rgba(0,0,0,.4)",
      "display:flex",
      "gap:6px",
      "align-items:center",
    ].join(";");
    box.addEventListener("mousedown", (e) => e.stopPropagation());
    document.body.appendChild(box);
    return box;
  }

  function button(label, title, onClick) {
    const el = document.createElement("button");
    el.textContent = label;
    el.title = title || label;
    el.style.cssText =
      "background:#1e293b;color:#e2e8f0;border:0;border-radius:6px;padding:4px 8px;cursor:pointer;font:inherit";
    el.addEventListener("click", onClick);
    return el;
  }

  /** offer shows the "highlight this" button next to a live selection. */
  function offer(selection) {
    const range = selection.getRangeAt(0);
    const rect = range.getBoundingClientRect();
    const box = panel(rect.left + window.scrollX, rect.bottom + window.scrollY + 6);

    for (const color of H.COLORS) {
      const swatch = button(" ", `Highlight (${color})`, () => saveSelection(range, color));
      swatch.style.background = TINT[color];
      swatch.style.width = "20px";
      box.appendChild(swatch);
    }
    box.appendChild(
      button("＋ note", "Highlight and add a note", () => saveSelection(range, "yellow", true))
    );
  }

  async function saveSelection(range, color, withNote) {
    const map = textMap();
    const start = offsetOf(map, range.startContainer, range.startOffset);
    const quote = String(range.toString() || "").replace(/\s+/g, " ").trim();
    dismiss();
    if (!quote) return;

    // The selection's own offset is only a hint — locate re-measures it
    // against the same walk the restore path will use.
    const anchor = H.locate(map.text, quote, Number.isNaN(start) ? undefined : start) || {};
    const comment = withNote ? window.prompt("Note for this highlight:", "") || "" : "";

    const reply = await ask("highlight_add", {
      highlight: { text: quote, anchor, comment, color },
    });
    window.getSelection().removeAllRanges();
    if (reply && reply.record) restore([reply.record]);
  }

  /** edit is the panel on an existing highlight: comment, or remove. */
  function edit(id, mark) {
    const rect = mark.getBoundingClientRect();
    const box = panel(rect.left + window.scrollX, rect.bottom + window.scrollY + 6);

    const input = document.createElement("input");
    input.value = mark.title || "";
    input.placeholder = "note…";
    input.style.cssText =
      "background:#1e293b;color:#e2e8f0;border:0;border-radius:6px;padding:4px 8px;font:inherit;width:200px";
    box.appendChild(input);

    box.appendChild(
      button("save", "Save this note", async () => {
        const comment = input.value;
        dismiss();
        await ask("highlight_update", { id, patch: { comment } });
        for (const entry of painted.filter((p) => p.id === id)) {
          for (const node of entry.nodes) node.title = comment;
        }
      })
    );
    box.appendChild(
      button("remove", "Remove this highlight", async () => {
        dismiss();
        await ask("highlight_remove", { id });
        unpaint(id);
      })
    );
    input.focus();
  }

  // ── Wiring ───────────────────────────────────────────────────────

  document.addEventListener("mouseup", (event) => {
    if (event.target && event.target.closest && event.target.closest(`#${UI_ID}`)) return;
    // A click on an existing highlight edits it rather than offering to
    // make another one on top.
    const mark = event.target && event.target.closest && event.target.closest(`mark.${MARK_CLASS}`);
    if (mark) {
      edit(mark.dataset.monoagentHighlight, mark);
      return;
    }
    setTimeout(() => {
      const selection = window.getSelection();
      if (!selection || selection.isCollapsed || !selection.rangeCount) {
        dismiss();
        return;
      }
      offer(selection);
    }, 0);
  });

  document.addEventListener("mousedown", (event) => {
    if (event.target && event.target.closest && event.target.closest(`#${UI_ID}`)) return;
    dismiss();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") dismiss();
  });

  // The worker relays two things here: the page's stored highlights on
  // load, and a request to highlight the selection from the keyboard.
  chrome.runtime.onMessage.addListener((msg, _sender, respond) => {
    if (!msg || typeof msg.type !== "string") return false;
    if (msg.type === "highlight_restore") {
      restore(msg.records);
      respond({ ok: true, painted: painted.length });
      return false;
    }
    if (msg.type === "highlight_selection") {
      const selection = window.getSelection();
      if (selection && !selection.isCollapsed && selection.rangeCount) {
        saveSelection(selection.getRangeAt(0), "yellow", false);
        respond({ ok: true });
      } else {
        respond({ ok: false, error: "nothing is selected" });
      }
      return false;
    }
    return false;
  });

  // Ask for this page's highlights once the document has settled. The
  // worker may be asleep; a null reply just means nothing is painted, and
  // the next visit tries again.
  function loadStored() {
    ask("highlight_list", {}).then((reply) => {
      if (reply && Array.isArray(reply.records)) restore(reply.records);
    });
  }
  if (document.readyState === "complete") loadStored();
  else window.addEventListener("load", loadStored, { once: true });
})();
