/**
 * MonoAgent Bridge -- what the recorder sends of the page itself (section 8.2)
 *
 * The DOM that travels with an event: a ~2KB snippet around its target, or,
 * for a list picked as data, the list's container (under Go's 64KB cap).
 * Both are serialised from a COPY that has had everything sensitive taken
 * out (cleanHtml). Also the frame path, so an event in an iframe says which.
 *
 * Page script: plain ASCII (recorder_encoding.test.mjs).
 */

(function (root) {
  "use strict";

  const SNIPPET_MAX = 2048;
  const SNIPPET_WALK = 6; // ancestor levels considered for the snippet
  const SNIPPET_NODES = 80; // an ancestor with more descendants is never serialised
  const CONTAINER_MAX = 60000; // a list container's snapshot; Go refuses > 64KB

  const Priv = () => root.MonoRecorderPrivacy;
  const tagOf = (el) => String((el && el.tagName) || "").toLowerCase();
  const attr = (el, name) => (el && el.getAttribute ? el.getAttribute(name) : null);

  /** fits is true when an element is small enough to serialise for a snippet. */
  function fits(el) {
    if (el.getElementsByTagName && el.getElementsByTagName("*").length > SNIPPET_NODES) return false;
    return el.outerHTML.length <= SNIPPET_MAX;
  }

  /**
   * snippetOf is ~2KB of outerHTML around the element: the largest nearby
   * ancestor that still fits, with scripts dropped and secrets removed. The
   * walk is capped, and a large ancestor is judged by its node count before
   * anything is serialised, so a click deep in a big page stays cheap.
   */
  function snippetOf(el, label) {
    if (!el || !el.cloneNode || el.outerHTML == null) return "";
    let node = el;
    for (let i = 0; i < SNIPPET_WALK; i++) {
      const up = node.parentElement;
      if (!up || tagOf(up) === "body" || tagOf(up) === "html" || !fits(up)) break;
      node = up;
    }
    const html = cleanHtml(node, el, label);
    return html.length > SNIPPET_MAX ? html.slice(0, SNIPPET_MAX) : html;
  }

  /**
   * cleanHtml serialises a copy of `node` with nothing sensitive left in it:
   * no scripts or styles, no data-* attributes, no values of secret fields
   * (password, cc-*, cvv, ever-password...), sanitised href/src/action URLs,
   * and no card numbers anywhere in the text.
   */
  function cleanHtml(node, el, label) {
    const base = (node.ownerDocument && node.ownerDocument.baseURI) || "https://invalid.invalid/";
    const copy = node.cloneNode(true);
    if (copy.querySelectorAll) {
      for (const s of copy.querySelectorAll("script,style,noscript,template")) s.remove();
      // The live elements, in the same document order as their copies, so a
      // field that was ever a password is judged by the real element.
      const live = [node].concat(Array.from(node.querySelectorAll("*")));
      const copies = [copy].concat(Array.from(copy.querySelectorAll("*")));
      const liveFields = live.filter((f) => /^(input|textarea)$/.test(tagOf(f)));
      const copyFields = copies.filter((f) => /^(input|textarea)$/.test(tagOf(f)));
      copyFields.forEach((f, i) => {
        const real = liveFields[i] || f;
        const withheld =
          Priv().sensitiveField(real, real === el ? label : "") || Priv().maskReason(f, attr(f, "value") || f.textContent, "");
        if (withheld) {
          f.removeAttribute("value");
          if (tagOf(f) === "textarea") f.textContent = "";
        }
      });
      for (const n of [copy].concat(Array.from(copy.querySelectorAll("*")))) {
        for (const a of Array.from(n.attributes || [])) {
          if (/^data-/i.test(a.name)) n.removeAttribute(a.name);
          else if (/^(href|src|action|formaction)$/i.test(a.name)) n.setAttribute(a.name, Priv().sanitizeUrl(a.value, base));
        }
      }
    }
    return Priv().scrubCards(copy.outerHTML || "");
  }

  /** containerHtml is a list container's cleaned HTML, capped under Go's 64KB. */
  function containerHtml(container) {
    if (!container || !container.cloneNode || container.outerHTML == null) return "";
    let html = cleanHtml(container, null, "");
    if (html.length > CONTAINER_MAX) html = html.slice(0, CONTAINER_MAX);
    const enc = typeof TextEncoder === "function" ? new TextEncoder() : null;
    while (enc && enc.encode(html).length > 64000) html = html.slice(0, Math.floor(html.length * 0.9));
    return html;
  }

  /** framePath is this frame's index path from the top window; [] at the top. */
  function framePath(win) {
    const path = [];
    try {
      let w = win;
      while (w && w.parent && w !== w.parent) {
        const p = w.parent;
        let at = -1;
        for (let i = 0; i < p.frames.length; i++) {
          if (p.frames[i] === w) {
            at = i;
            break;
          }
        }
        path.unshift(at);
        w = p;
      }
    } catch {
      // an unreachable parent: keep what was found
    }
    return path;
  }

  root.MonoRecorderDom = { snippetOf, cleanHtml, containerHtml, framePath, SNIPPET_MAX, CONTAINER_MAX };
})(globalThis);
