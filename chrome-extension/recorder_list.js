/**
 * MonoAgent Bridge -- "mark as data" for the activity recorder (section 8.2)
 *
 * A person shows the recorder what to extract by picking an element. One
 * pick is one value. A second pick on a *similar* element -- the price of
 * the next product, the title of the next story -- means "all of these",
 * and this file turns the two picks into a list pattern:
 *
 *   container  the nearest common ancestor of the two picks
 *   item       the container's child holding each pick (same tag both times)
 *   field      the path from the item down to the pick, relative to the item
 *
 * Similar is structural, not visual: the two items must share a tag and the
 * two relative paths the same sequence of tags. Anything else is two
 * separate single values, and this returns null.
 *
 * Samples are read by walking the same relative path in every sibling item,
 * rather than by running the generated CSS, so the same code works on a
 * fake tree in node and on the page. The CSS strings are for replay.
 */

(function (root) {
  "use strict";

  const MAX_SAMPLES = 5;
  const tagOf = (el) => String((el && el.tagName) || "").toLowerCase();
  const attr = (el, name) => (el && el.getAttribute ? el.getAttribute(name) : null);
  const clean = (s) => String(s == null ? "" : s).replace(/\s+/g, " ").trim();
  const S = () => root.MonoRecorderSelectors;

  function classesOf(el) {
    return clean(attr(el, "class"))
      .split(" ")
      .filter((c) => c && !S().looksGeneratedClass(c));
  }

  /**
   * extractValue says what a picked element holds: a link's href, an
   * image's src, else text. A secret field or a card number holds nothing
   * the recorder may send, so its value comes back empty.
   */
  function extractValue(el) {
    const found = rawValue(el);
    if (root.MonoRecorderPrivacy && root.MonoRecorderPrivacy.maskReason(el, found.value)) {
      return { attribute: found.attribute, value: "" };
    }
    return found;
  }

  function rawValue(el) {
    const tag = tagOf(el);
    if (tag === "img") return { attribute: "src", value: el.src || attr(el, "src") || "" };
    if (tag === "a" && !clean(el.textContent)) return { attribute: "href", value: el.href || attr(el, "href") || "" };
    if (tag === "input" || tag === "textarea") return { attribute: "value", value: String(el.value || "") };
    return { attribute: "", value: clean(el.innerText != null ? el.innerText : el.textContent).slice(0, 500) };
  }

  const slug = (s) =>
    clean(s)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 40);

  /** suggestField proposes a field name; the side panel lets the person change it. */
  function suggestField(el, attribute) {
    if (attribute === "href") return "url";
    if (attribute === "src") return "image";
    const named = slug(attr(el, "itemprop") || attr(el, "aria-label") || attr(el, "name") || "");
    if (named) return named;
    const cls = classesOf(el).map(slug).find((c) => c && c.length > 2);
    if (cls) return cls;
    const tag = tagOf(el);
    if (/^h[1-6]$/.test(tag)) return "title";
    if (tag === "a") return "link_text";
    if (tag === "time") return "date";
    return "text";
  }

  function ancestors(el) {
    const out = [];
    for (let n = el; n; n = n.parentElement) out.push(n);
    return out;
  }

  function commonAncestor(a, b) {
    const seen = new Set(ancestors(a));
    for (let n = b; n; n = n.parentElement) if (seen.has(n)) return n;
    return null;
  }

  /** childOn is the child of `top` on the way down to `el`. */
  function childOn(top, el) {
    let n = el;
    while (n && n.parentElement !== top) n = n.parentElement;
    return n;
  }

  /** pathBelow is the element chain from (not including) `item` to `el`. */
  function pathBelow(item, el) {
    const chain = [];
    for (let n = el; n && n !== item; n = n.parentElement) chain.unshift(n);
    return chain;
  }

  const common = (x, y) => x.filter((c) => y.indexOf(c) !== -1);

  function stepSelector(tag, classes) {
    return tag + classes.map((c) => `.${S().cssEscape(c)}`).join("");
  }

  function matchesStep(el, step) {
    if (tagOf(el) !== step.tag) return false;
    const have = classesOf(el);
    return step.classes.every((c) => have.indexOf(c) !== -1);
  }

  /** resolve walks one item down the relative path; null when an item lacks it. */
  function resolve(item, steps) {
    let node = item;
    for (const step of steps) {
      node = Array.from(node.children || []).find((c) => matchesStep(c, step));
      if (!node) return null;
    }
    return node;
  }

  /**
   * proposeList turns two picks into a list ExtractMark (types.go), or null
   * when they are not two instances of the same thing.
   */
  function proposeList(a, b, env) {
    if (!a || !b || a === b) return null;
    const container = commonAncestor(a, b);
    if (!container || container === a || container === b) return null;
    const ia = childOn(container, a);
    const ib = childOn(container, b);
    if (!ia || !ib || ia === ib || tagOf(ia) !== tagOf(ib)) return null;
    const pa = pathBelow(ia, a);
    const pb = pathBelow(ib, b);
    if (pa.length !== pb.length) return null;
    for (let i = 0; i < pa.length; i++) if (tagOf(pa[i]) !== tagOf(pb[i])) return null;

    const itemStep = { tag: tagOf(ia), classes: common(classesOf(ia), classesOf(ib)) };
    const steps = pa.map((n, i) => ({ tag: tagOf(n), classes: common(classesOf(n), classesOf(pb[i])) }));
    const { attribute } = extractValue(a);

    const items = Array.from(container.children).filter((c) => matchesStep(c, itemStep));
    const samples = [];
    for (const item of items) {
      if (samples.length >= MAX_SAMPLES) break;
      const field = steps.length ? resolve(item, steps) : item;
      if (!field) continue;
      const v = extractValue(field);
      if (v.value) samples.push(v.value);
    }

    return {
      field: suggestField(a, attribute),
      list: true,
      containerSelector: S().bestCss(container, env),
      itemSelector: stepSelector(itemStep.tag, itemStep.classes),
      fieldSelector: steps.map((s) => stepSelector(s.tag, s.classes)).join(" > "),
      attribute,
      samples,
    };
  }

  /** single is the ExtractMark for one picked value. */
  function single(el) {
    const { attribute, value } = extractValue(el);
    return { field: suggestField(el, attribute), list: false, attribute, samples: value ? [value] : [] };
  }

  root.MonoRecorderList = { proposeList, single, extractValue, suggestField, commonAncestor, MAX_SAMPLES };
})(globalThis);
