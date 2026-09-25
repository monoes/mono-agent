/**
 * MonoAgent Bridge -- element fingerprints for the activity recorder (section 8.2)
 *
 * Every recorded event names the element it happened to, and a recording is
 * only worth replaying if that name still finds the element next month. So
 * the fingerprint is not one selector but a ranked list of them, each tried
 * against the live page at the moment of recording: a selector that matched
 * three elements then will not match the right one later.
 *
 * The ranking is the stability argument, best first:
 *
 *   data-testid / data-test / data-qa   written for exactly this purpose
 *   id                                  unless it looks generated (":r3:", "ember123")
 *   ARIA role + accessible name         what a person would call it
 *   name attribute, placeholder         form fields
 *   label-based XPath                   "the input labelled Email"
 *   visible text                        buttons and links
 *   structural CSS path                 last resort -- breaks on any layout change
 *   absolute XPath                      even more so
 *
 * Counting is done through an `env` so node can test the ranking without a
 * DOM: env.countCss / countXPath / countAria / countText / labelOf. In the
 * page, browserEnv(document) supplies the real ones. Elements only need the
 * DOM's element surface (tagName, getAttribute, parentElement, children,
 * textContent), which a test can fake in a few lines.
 *
 * The output is the Fingerprint shape of internal/recording/types.go.
 */

(function (root) {
  "use strict";

  const TEXT_MAX = 120;

  /** Base stability per candidate source, before uniqueness is applied. */
  const STABILITY = {
    testid: 1.0,
    id: 0.9,
    aria: 0.85,
    name: 0.8,
    label: 0.75,
    placeholder: 0.7,
    text: 0.65,
    css: 0.35,
    xpath: 0.25,
  };

  const TEST_ATTRS = ["data-testid", "data-test-id", "data-test", "data-qa", "data-cy"];

  const tagOf = (el) => String((el && el.tagName) || "").toLowerCase();
  const attr = (el, name) => (el && el.getAttribute ? el.getAttribute(name) : null);
  const clean = (s) => String(s == null ? "" : s).replace(/\s+/g, " ").trim();
  const clip = (s, n) => (s.length > n ? s.slice(0, n) : s);

  /**
   * looksGenerated is true for ids a framework minted per render: React's
   * ":r3:", Ember's "ember123", long digit runs, hash-chunked ids. Such an
   * id is unique today and gone on the next reload.
   */
  function looksGenerated(id) {
    const s = String(id || "");
    if (!s) return true;
    if (/^:r[0-9a-z]*:$/i.test(s) || /^:[^:]+:$/.test(s)) return true; // React useId
    if (/^(ember|react-|radix-|headlessui-|mui-|rc-|__next|yui_|ext-gen)/i.test(s)) return true;
    const digits = (s.match(/[0-9]/g) || []).length;
    if (digits >= 4 && digits / s.length > 0.3) return true;
    const chunks = s.split(/[-_]/);
    const hashy = chunks.filter((c) => c.length >= 4 && /[0-9]/.test(c) && /[a-z]/i.test(c));
    if (chunks.length > 2 && hashy.length >= 2) return true;
    const numbered = chunks.filter((c) => /[0-9]/.test(c));
    if (chunks.length > 2 && numbered.length >= chunks.length - 1) return true;
    if (/^[a-f0-9]{8,}$/i.test(s)) return true;
    return false;
  }

  /** looksGeneratedClass drops CSS-in-JS class names (css-1x2y3z, sc-abc12). */
  function looksGeneratedClass(c) {
    return (
      !c ||
      /^(css|sc|jsx|emotion|styled|svelte)-/i.test(c) ||
      /^_?[a-z0-9]{5,}_[a-z0-9]{3,}$/i.test(c) ||
      looksGenerated(c)
    );
  }

  function cssEscape(s) {
    if (root.CSS && root.CSS.escape) return root.CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_\u00A0-\uFFFF-]/g, (ch) => `\\${ch}`).replace(/^(\d)/, "\\3$1 ");
  }

  const cssString = (s) => `"${String(s).replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;

  /** xpathString quotes a literal for XPath 1.0, which has no escape. */
  function xpathString(s) {
    const v = String(s);
    if (v.indexOf("'") === -1) return `'${v}'`;
    if (v.indexOf('"') === -1) return `"${v}"`;
    return `concat('${v.split("'").join(`', "'", '`)}')`;
  }

  // -- what the element is ------------------------------------------

  const INPUT_ROLES = {
    button: "button", submit: "button", reset: "button", image: "button",
    checkbox: "checkbox", radio: "radio", range: "slider", search: "searchbox",
    email: "textbox", tel: "textbox", text: "textbox", url: "textbox", "": "textbox",
    number: "spinbutton",
  };
  const TAG_ROLES = {
    button: "button", select: "combobox", textarea: "textbox", nav: "navigation",
    h1: "heading", h2: "heading", h3: "heading", h4: "heading", h5: "heading", h6: "heading",
    li: "listitem", ul: "list", ol: "list", table: "table", tr: "row", td: "cell",
    img: "img", form: "form", dialog: "dialog", option: "option", main: "main",
  };

  /** roleOf is the explicit role, else the implicit one for common tags. */
  function roleOf(el) {
    const explicit = clean(attr(el, "role")).split(" ")[0];
    if (explicit) return explicit.toLowerCase();
    const tag = tagOf(el);
    if (tag === "a") return attr(el, "href") != null ? "link" : "";
    if (tag === "input") {
      const type = String(attr(el, "type") || "").toLowerCase();
      return INPUT_ROLES[type] || "";
    }
    return TAG_ROLES[tag] || "";
  }

  /** visibleText is the element's trimmed text, capped. */
  const visibleText = (el) => clip(clean(el && (el.innerText != null ? el.innerText : el.textContent)), TEXT_MAX);

  /**
   * accessibleName is a reduced accname: aria-label, aria-labelledby,
   * a <label>, alt/title/placeholder, then own text for roles that take it.
   */
  function accessibleName(el, env) {
    const label = clean(attr(el, "aria-label"));
    if (label) return clip(label, TEXT_MAX);
    const by = clean(attr(el, "aria-labelledby"));
    const doc = el && el.ownerDocument;
    if (by && doc && doc.getElementById) {
      const text = by
        .split(" ")
        .map((id) => doc.getElementById(id))
        .filter(Boolean)
        .map((n) => clean(n.textContent))
        .join(" ");
      if (text) return clip(text, TEXT_MAX);
    }
    const tag = tagOf(el);
    if (tag === "input" || tag === "textarea" || tag === "select") {
      const lab = env && env.labelOf ? clean(env.labelOf(el)) : "";
      if (lab) return clip(lab, TEXT_MAX);
      if (tag === "input" && /^(submit|button|reset)$/i.test(attr(el, "type") || "")) {
        return clip(clean(attr(el, "value")), TEXT_MAX);
      }
      return clip(clean(attr(el, "title") || attr(el, "placeholder")), TEXT_MAX);
    }
    if (tag === "img") return clip(clean(attr(el, "alt") || attr(el, "title")), TEXT_MAX);
    return visibleText(el) || clip(clean(attr(el, "title")), TEXT_MAX);
  }

  /** siblingIndex is the 1-based nth-of-type position, 0 when the tag is alone. */
  function siblingIndex(el) {
    const parent = el.parentElement;
    if (!parent) return 0;
    const same = Array.from(parent.children).filter((c) => tagOf(c) === tagOf(el));
    return same.length > 1 ? same.indexOf(el) + 1 : 0;
  }

  /** cssPath is a structural path from the nearest stable-id ancestor (or html). */
  function cssPath(el) {
    const parts = [];
    let node = el;
    while (node && node.tagName && tagOf(node) !== "html") {
      const id = attr(node, "id");
      if (id && !looksGenerated(id) && node !== el) {
        parts.unshift(`#${cssEscape(id)}`);
        break;
      }
      const n = siblingIndex(node);
      parts.unshift(n ? `${tagOf(node)}:nth-of-type(${n})` : tagOf(node));
      node = node.parentElement;
    }
    return parts.join(" > ");
  }

  /** xpathOf is the absolute positional XPath. */
  function xpathOf(el) {
    const parts = [];
    let node = el;
    while (node && node.tagName) {
      const n = siblingIndex(node);
      parts.unshift(n ? `${tagOf(node)}[${n}]` : tagOf(node));
      node = node.parentElement;
    }
    return `/${parts.join("/")}`;
  }

  // -- candidates ---------------------------------------------------

  /**
   * specsFor lists the candidate selectors for an element, unscored. Each
   * carries its `source` so scoring knows how stable it is.
   */
  function specsFor(el, env, info) {
    const tag = tagOf(el);
    const out = [];
    for (const a of TEST_ATTRS) {
      const v = attr(el, a);
      if (v) out.push({ source: "testid", kind: "css", value: `[${a}=${cssString(v)}]` });
    }
    const id = attr(el, "id");
    if (id && !looksGenerated(id)) out.push({ source: "id", kind: "css", value: `#${cssEscape(id)}` });
    if (info.role && info.ariaName) out.push({ source: "aria", kind: "aria", role: info.role, name: info.ariaName });
    const name = attr(el, "name");
    if (name) out.push({ source: "name", kind: "css", value: `${tag}[name=${cssString(name)}]` });
    if (info.label && /^(input|textarea|select)$/.test(tag)) {
      const lit = xpathString(info.label);
      out.push({
        source: "label",
        kind: "xpath",
        value: id
          ? `//${tag}[@id=//label[normalize-space(.)=${lit}]/@for]`
          : `//label[normalize-space(.)=${lit}]//${tag}`,
      });
    }
    const ph = attr(el, "placeholder");
    if (ph) out.push({ source: "placeholder", kind: "css", value: `${tag}[placeholder=${cssString(ph)}]` });
    const texty = /^(a|button|summary|label|option)$|^h[1-6]$/.test(tag) || /^(button|link|tab|menuitem)$/.test(info.role);
    if (info.text && texty) {
      out.push({ source: "text", kind: "text", value: info.text });
    }
    out.push({ source: "css", kind: "css", value: info.css });
    out.push({ source: "xpath", kind: "xpath", value: info.xpath });
    return out;
  }

  function countOf(spec, env) {
    try {
      if (spec.kind === "css") return env.countCss(spec.value);
      if (spec.kind === "xpath") return env.countXPath(spec.value);
      if (spec.kind === "aria") return env.countAria(spec.role, spec.name);
      if (spec.kind === "text") return env.countText(spec.value);
    } catch {
      // an invalid selector matches nothing
    }
    return 0;
  }

  /**
   * scoreOf: stability, scaled down hard when the selector is not unique.
   * A selector that matches nothing (the element is in a shadow root, say)
   * scores zero -- it cannot find anything later either.
   */
  function scoreOf(source, count) {
    const base = STABILITY[source] || 0.1;
    if (count <= 0) return 0;
    if (count === 1) return base;
    return Math.round((base * 0.4) / count * 1000) / 1000;
  }

  /** rank scores specs with live counts and sorts best first (stable). */
  // Counting an aria or text candidate walks every matching element and
  // reads its text (layout, on a big page). Once a unique candidate this
  // stable is in hand, those are skipped rather than counted.
  const GOOD_ENOUGH = 0.8;
  const EXPENSIVE = new Set(["aria", "text"]);

  function rank(specs, env) {
    const order = specs
      .map((spec, i) => ({ spec, i }))
      .sort((a, b) => (STABILITY[b.spec.source] || 0) - (STABILITY[a.spec.source] || 0) || a.i - b.i);
    let settled = false;
    const kept = [];
    for (const { spec, i } of order) {
      if (settled && EXPENSIVE.has(spec.kind)) continue;
      const count = countOf(spec, env);
      if (count === 1 && (STABILITY[spec.source] || 0) >= GOOD_ENOUGH) settled = true;
      kept.push({ spec, i, count });
    }
    const scored = kept.map(({ spec, i, count }) => {
      const c = { kind: spec.kind };
      if (spec.kind === "aria") {
        c.role = spec.role;
        c.name = spec.name;
      } else {
        c.value = spec.value;
      }
      c.unique = count === 1;
      c.count = count;
      c.score = scoreOf(spec.source, count);
      return { c, i };
    });
    scored.sort((a, b) => b.c.score - a.c.score || a.i - b.i);
    const seen = new Set();
    return scored
      .map((s) => s.c)
      .filter((c) => {
        const key = `${c.kind}|${c.value || ""}|${c.role || ""}|${c.name || ""}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  }

  /** fingerprint builds the full Fingerprint for one element. */
  function fingerprint(el, env) {
    const tag = tagOf(el);
    const role = roleOf(el);
    const label = env && env.labelOf ? clip(clean(env.labelOf(el)), TEXT_MAX) : "";
    const isField = /^(input|textarea|select)$/.test(tag);
    const info = {
      role,
      ariaName: accessibleName(el, env),
      label,
      text: isField ? "" : visibleText(el),
      css: cssPath(el),
      xpath: xpathOf(el),
    };
    const fp = { tag };
    const id = attr(el, "id");
    if (id) fp.id = id;
    const name = attr(el, "name");
    if (name) fp.name = name;
    const testId = TEST_ATTRS.map((a) => attr(el, a)).find(Boolean);
    if (testId) fp.testId = testId;
    if (role) fp.role = role;
    if (info.ariaName) fp.ariaName = info.ariaName;
    if (label) fp.label = label;
    const ph = attr(el, "placeholder");
    if (ph) fp.placeholder = ph;
    if (info.text) fp.text = info.text;
    const href = attr(el, "href");
    if (href) fp.href = el.href || href;
    if (tag === "input") fp.inputType = String(attr(el, "type") || "text").toLowerCase();
    const auto = attr(el, "autocomplete");
    if (auto) fp.autocomplete = auto;
    fp.css = info.css;
    fp.xpath = info.xpath;
    if (el.getBoundingClientRect) {
      const r = el.getBoundingClientRect();
      fp.rect = { X: Math.round(r.x), Y: Math.round(r.y), W: Math.round(r.width), H: Math.round(r.height) };
    }
    fp.candidates = rank(specsFor(el, env, info), env);
    return fp;
  }

  /** bestCss is the highest-ranked unique CSS candidate, else the structural path. */
  function bestCss(el, env) {
    const info = { role: "", ariaName: "", label: "", text: "", css: cssPath(el), xpath: xpathOf(el) };
    const ranked = rank(specsFor(el, env, info).filter((s) => s.kind === "css"), env);
    const unique = ranked.find((c) => c.unique);
    return unique ? unique.value : info.css;
  }

  // -- the live page ------------------------------------------------

  const ROLE_SELECTORS = {
    button: 'button,input[type=button],input[type=submit],input[type=reset],input[type=image],[role~="button"]',
    link: 'a[href],[role~="link"]',
    textbox: 'input:not([type]),input[type=text],input[type=email],input[type=tel],input[type=url],textarea,[role~="textbox"]',
    checkbox: 'input[type=checkbox],[role~="checkbox"]',
    radio: 'input[type=radio],[role~="radio"]',
    combobox: 'select,[role~="combobox"]',
  };

  /** browserEnv counts against a real document. */
  function browserEnv(doc) {
    const labelOf = (el) => {
      if (el.labels && el.labels.length) return el.labels[0].textContent;
      const id = el.getAttribute && el.getAttribute("id");
      if (id) {
        const lab = doc.querySelector(`label[for=${cssString(id)}]`);
        if (lab) return lab.textContent;
      }
      const wrap = el.closest && el.closest("label");
      return wrap ? wrap.textContent : "";
    };
    const env = {
      labelOf,
      countCss: (sel) => doc.querySelectorAll(sel).length,
      countXPath: (xp) =>
        doc.evaluate(xp, doc, null, 7 /* ORDERED_NODE_SNAPSHOT_TYPE */, null).snapshotLength,
      countAria: (role, name) => {
        const sel = ROLE_SELECTORS[role] || `[role~=${cssString(role)}],${role === "heading" ? "h1,h2,h3,h4,h5,h6" : "_none_"}`;
        let n = 0;
        for (const el of doc.querySelectorAll(sel)) {
          if (roleOf(el) === role && accessibleName(el, env) === name) n++;
        }
        return n;
      },
      countText: (text) => {
        let n = 0;
        for (const el of doc.querySelectorAll("a,button,summary,label,option,h1,h2,h3,h4,h5,h6,[role]")) {
          if (visibleText(el) === text) n++;
        }
        return n;
      },
    };
    return env;
  }

  root.MonoRecorderSelectors = {
    fingerprint,
    bestCss,
    browserEnv,
    looksGenerated,
    looksGeneratedClass,
    roleOf,
    accessibleName,
    visibleText,
    cssPath,
    xpathOf,
    xpathString,
    cssString,
    cssEscape,
    scoreOf,
    rank,
    STABILITY,
  };
})(globalThis);
