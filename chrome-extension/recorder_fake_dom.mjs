// A few-dozen-line element fake for the recorder's node tests.
//
// recorder_selectors.js, recorder_list.js and recorder.js only touch the
// element surface — tagName, getAttribute, parentElement, children,
// textContent, closest — so that is all this is. It is NOT a DOM: nothing
// here parses CSS beyond what `closest` needs, and anything that needs a
// real selector engine or layout is covered by recorder.browser.test.mjs.

/** matchesSimple handles "tag", "[a]", '[a="v"]', 'tag[a="v"]' and comma lists. */
function matchesSimple(el, selector) {
  return selector.split(",").some((raw) => {
    const part = raw.trim();
    const m = /^([a-z0-9]*)(?:\[([a-z-]+)(?:=(?:"([^"]*)"|([^\]]*)))?\])?$/i.exec(part);
    if (!m) return false;
    const [, tag, name, v1, v2] = m;
    if (tag && el.tagName.toLowerCase() !== tag.toLowerCase()) return false;
    if (name) {
      const have = el.getAttribute(name);
      if (have == null) return false;
      const want = v1 != null ? v1 : v2;
      if (want != null && have !== want) return false;
    }
    return !!(tag || name);
  });
}

export class FakeElement {
  constructor(tag, attrs = {}, children = []) {
    this.tagName = tag.toUpperCase();
    this.attrs = Object.assign({}, attrs);
    this.children = [];
    this.parentElement = null;
    this.ownText = "";
    this.listeners = {};
    for (const c of children) {
      if (typeof c === "string") this.ownText += c;
      else this.append(c);
    }
    if ("value" in attrs) this.value = attrs.value;
  }
  append(child) {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }
  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attrs, name) ? String(this.attrs[name]) : null;
  }
  setAttribute(name, value) {
    this.attrs[name] = String(value);
  }
  get id() {
    return this.getAttribute("id") || "";
  }
  get textContent() {
    return this.ownText + this.children.map((c) => c.textContent).join(" ");
  }
  get isContentEditable() {
    const v = this.getAttribute("contenteditable");
    return v === "" || v === "true";
  }
  closest(selector) {
    for (let n = this; n; n = n.parentElement) if (matchesSimple(n, selector)) return n;
    return null;
  }
  get ownerDocument() {
    let n = this;
    while (n.parentElement) n = n.parentElement;
    return n.doc || null;
  }
}

/** h builds a tree: h("div", {class: "x"}, h("a", {href: "/"}, "text")). */
export const h = (tag, attrs, ...children) => new FakeElement(tag, attrs || {}, children);

/** fakeDocument wraps a root <html> and records capture-phase listeners. */
export function fakeDocument(rootEl) {
  const listeners = {};
  const doc = {
    documentElement: rootEl,
    listeners,
    addEventListener(type, fn, capture) {
      (listeners[type] = listeners[type] || []).push({ fn, capture });
    },
    removeEventListener(type, fn) {
      listeners[type] = (listeners[type] || []).filter((l) => l.fn !== fn);
    },
    getElementById(id) {
      const walk = (n) => {
        if (n.id === id) return n;
        for (const c of n.children) {
          const f = walk(c);
          if (f) return f;
        }
        return null;
      };
      return walk(rootEl);
    },
    /** dispatch fires a fake event at the document's capture listeners. */
    dispatch(type, target, extra = {}) {
      const event = Object.assign(
        {
          type,
          target,
          isTrusted: true,
          defaultPrevented: false,
          preventDefault() {
            this.defaultPrevented = true;
          },
          stopImmediatePropagation() {},
        },
        extra
      );
      for (const l of listeners[type] || []) l.fn(event);
      return event;
    },
  };
  rootEl.doc = doc;
  return doc;
}

/** countingEnv is a selector env whose counts come from a table (default 1). */
export function countingEnv(table = {}, labels = new Map()) {
  const look = (key) => (key in table ? table[key] : 1);
  return {
    labelOf: (el) => labels.get(el) || "",
    countCss: (sel) => look(`css:${sel}`),
    countXPath: (xp) => look(`xpath:${xp}`),
    countAria: (role, name) => look(`aria:${role}:${name}`),
    countText: (text) => look(`text:${text}`),
  };
}

/** fakeTimers is a manual clock for debounce tests. */
export function fakeTimers(start = 1000) {
  let now = start;
  let seq = 0;
  const timers = new Map();
  return {
    now: () => now,
    setTimer(fn, ms) {
      seq += 1;
      timers.set(seq, { fn, at: now + ms });
      return seq;
    },
    clearTimer(id) {
      timers.delete(id);
    },
    advance(ms) {
      now += ms;
      for (const [id, t] of [...timers].sort((a, b) => a[1].at - b[1].at)) {
        if (t.at <= now && timers.has(id)) {
          timers.delete(id);
          t.fn();
        }
      }
    },
    pending: () => timers.size,
  };
}
