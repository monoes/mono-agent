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
  const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEXTAREA", "SELECT"]);
  /** Elements whose text starts a new line. Crossing one reads as a space
   *  even when the markup has no whitespace there. */
  const BLOCKS =
    "address,article,aside,blockquote,dd,details,div,dl,dt,fieldset,figcaption," +
    "figure,footer,form,h1,h2,h3,h4,h5,h6,header,li,main,nav,ol,p,pre,section," +
    "summary,table,td,th,tr,ul";
  const WS = /\s/;
  /** The common cases without a regex call: this runs per character. */
  function isSpace(value, at) {
    const code = value.charCodeAt(at);
    if (code === 32 || (code >= 9 && code <= 13)) return true;
    return code > 127 && WS.test(value[at]);
  }
  const NEXT = Symbol("the space belongs to whatever comes next");
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

  /** hidden: text the reader cannot see is text they cannot have selected. */
  function hidden(el) {
    if (typeof el.checkVisibility !== "function") return false;
    return !el.checkVisibility({ visibilityProperty: true, contentVisibilityAuto: true });
  }

  /**
   * textMap walks the visible text once and returns the page as a single
   * string, plus where each run of it came from.
   *
   * The string is WHITESPACE-COLLAPSED, and that is the whole point. A
   * quote is stored collapsed (highlights.js `collapse`s it, because that
   * is what the reader saw, not what the markup happened to indent), and
   * `locate`/`relocate` match it by plain substring search. So the haystack
   * has to be in the same shape as the needle: a page whose source wraps a
   * sentence across two lines has "a\n  selection" in its text nodes and
   * "a selection" in the selection, and matching raw node data against a
   * collapsed quote finds nothing — every multi-line highlight silently
   * fails to paint, which is exactly what it did.
   *
   * `segments` maps back: each is a run of characters contiguous in both
   * the collapsed text and its source node, so painting can find the nodes
   * again and split them at the right offsets.
   */
  function textMap() {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        if (node.nodeType === Node.TEXT_NODE) {
          return node.nodeValue ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
        }
        // REJECT on an element skips its subtree, which is what these want.
        if (SKIP_TAGS.has(node.tagName) || node.id === UI_ID) return NodeFilter.FILTER_REJECT;
        if (node.ownerSVGElement || node.tagName === "svg") return NodeFilter.FILTER_REJECT;
        if (hidden(node)) return NodeFilter.FILTER_REJECT;
        if (node.tagName === "BR") return NodeFilter.FILTER_ACCEPT; // a line break is a space
        return NodeFilter.FILTER_SKIP; // descend, but emit nothing of its own
      },
    });

    const segments = [];
    const blocks = new Map(); // one closest() per parent, not per text node
    let text = "";
    let pending = null; // whitespace seen but not yet emitted
    let block = null;

    function push(node, offset, ch) {
      const last = segments[segments.length - 1];
      if (last && last.node === node && last.nodeEnd === offset) last.nodeEnd = offset + 1;
      else segments.push({ node, nodeStart: offset, nodeEnd: offset + 1, textStart: text.length });
      text += ch;
    }

    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (node.nodeType === Node.ELEMENT_NODE) {
        if (!pending) pending = NEXT; // <br>
        continue;
      }
      const parent = node.parentElement;
      if (parent && !blocks.has(parent)) blocks.set(parent, parent.closest(BLOCKS));
      const owner = parent ? blocks.get(parent) : null;
      if (block !== null && owner !== block && !pending) pending = NEXT;
      block = owner;

      const value = node.nodeValue;
      for (let i = 0; i < value.length; i++) {
        const ch = value[i];
        if (isSpace(value, i)) {
          if (!pending) pending = { node, offset: i };
          continue;
        }
        if (pending) {
          // Leading whitespace is dropped, the way `collapse` trims it.
          if (text) {
            const at = pending === NEXT ? { node, offset: i } : pending;
            push(at.node, at.offset, " ");
          }
          pending = null;
        }
        push(node, i, ch);
      }
    }
    return { text, segments };
  }

  /**
   * offsetOf places a Range boundary in the map's text. A boundary that
   * landed in collapsed whitespace, or on an element, answers with the
   * nearest mapped position rather than giving up: the map is what the
   * reader saw, and that is the space the quote has to be measured in.
   */
  function offsetOf(map, node, offset) {
    if (!node) return NaN;
    if (node.nodeType === Node.ELEMENT_NODE) {
      // A boundary on an element sits BEFORE childNodes[offset].
      const child = node.childNodes[offset];
      return child ? edgeOf(map, child, "start") : edgeOf(map, node, "end");
    }
    let after = NaN;
    for (const seg of map.segments) {
      if (seg.node !== node) continue;
      if (offset < seg.nodeStart) return seg.textStart;
      if (offset < seg.nodeEnd) return seg.textStart + (offset - seg.nodeStart);
      after = seg.textStart + (seg.nodeEnd - seg.nodeStart);
    }
    return after;
  }

  /** Where a subtree begins or ends in the map's text. */
  function edgeOf(map, root, which) {
    let end = NaN;
    for (const seg of map.segments) {
      if (seg.node !== root && !root.contains(seg.node)) continue;
      if (which === "start") return seg.textStart;
      end = seg.textStart + (seg.nodeEnd - seg.nodeStart);
    }
    return end;
  }

  // ── Painting ─────────────────────────────────────────────────────

  /**
   * rangesIn turns [start, end) of the mapped text into the node ranges it
   * covers — which nodes to cut, and where.
   */
  function rangesIn(map, start, end) {
    // At most one range per text node, so a passage becomes as few marks
    // as the markup allows rather than one per whitespace run.
    const ranges = [];
    for (const seg of map.segments) {
      const length = seg.nodeEnd - seg.nodeStart;
      const from = Math.max(start, seg.textStart);
      const to = Math.min(end, seg.textStart + length);
      if (from >= to) continue;
      const last = ranges[ranges.length - 1];
      const nodeTo = seg.nodeStart + (to - seg.textStart);
      if (last && last.node === seg.node) last.to = Math.max(last.to, nodeTo);
      else ranges.push({ node: seg.node, from: seg.nodeStart + (from - seg.textStart), to: nodeTo });
    }
    return ranges;
  }

  /**
   * paint puts a <mark> around each of those ranges. The page's characters
   * are never rewritten: nodes are split, which only changes which node
   * holds them.
   */
  function paint(ranges, record) {
    const marks = [];
    for (const range of ranges) {
      // The formatting whitespace between two blocks is inside the passage
      // but has nothing to show. Wrapping it would put an element between
      // two siblings and quietly break the page's own `p + p` rules.
      if (!/\S/.test(range.node.nodeValue)) continue;
      let node = range.node;
      if (range.from > 0) node = node.splitText(range.from);
      if (range.to - range.from < node.nodeValue.length) node.splitText(range.to - range.from);

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
    let map = null;
    const split = new Set(); // text nodes an earlier highlight has already cut

    for (const record of records || []) {
      // Already on the page. The worker re-sends the list on navigation and
      // `add` returns a record that may already be painted; painting it a
      // second time would nest a mark inside itself.
      if (painted.some((p) => p.id === record.id)) continue;
      if (!map) map = textMap();

      let at = H.relocate(map.text, record.anchor);
      if (at === -1) continue; // gone from the page: draw nothing
      let ranges = rangesIn(map, at, at + record.anchor.quote.length);

      // Painting does not change a single character, so the map's TEXT
      // stays true for the whole pass — only the offsets into a node that
      // has been split go stale. Rebuilding for a passage that lands in
      // one of those, and only then, is what keeps a page with three
      // hundred highlights from rebuilding the map three hundred times.
      if (ranges.some((range) => split.has(range.node))) {
        map = textMap();
        split.clear();
        at = H.relocate(map.text, record.anchor);
        if (at === -1) continue;
        ranges = rangesIn(map, at, at + record.anchor.quote.length);
      }

      const nodes = paint(ranges, record);
      if (nodes.length) painted.push({ id: record.id, nodes });
      for (const range of ranges) split.add(range.node);
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
    const end = offsetOf(map, range.endContainer, range.endOffset);

    // The quote is read off the MAP, not off `range.toString()`. The range
    // gives the raw data of the text nodes it spans, which keeps text the
    // reader cannot see (a display:none span between two words) and drops
    // the breaks they can (a <br>, or two blocks whose markup has no
    // whitespace between them) — so the quote would be a sentence nobody
    // read, and one that no later visit can find again.
    const mapped = Number.isNaN(start) || Number.isNaN(end) ? "" : map.text.slice(start, end).trim();
    const quote = mapped || String(range.toString() || "").replace(/\s+/g, " ").trim();
    dismiss();
    if (!quote) return;

    // Measured against the same walk the restore path will use.
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
