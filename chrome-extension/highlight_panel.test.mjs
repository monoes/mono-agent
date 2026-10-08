// The floating panel's shell (highlight_panel.js). A page shares the DOM with
// the content script, so the panel must be out of its reach (a closed shadow
// root) and must not be pressable by its script (only a real click counts).
// Against a fake document; the real one is highlight_page.browser.test.mjs.
// `CHROME_PATH=/nonexistent node --test chrome-extension/highlight_panel.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { loadExtensionScripts } from "./test_helpers.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const { MonoHighlightPanel: UI } = loadExtensionScripts(["highlight_panel.js"]);

/** fakeDocument is the few DOM calls highlight_panel.js makes, and nothing else. */
function fakeDocument() {
  const byId = new Map();
  function element(tag) {
    return {
      tagName: tag.toUpperCase(),
      id: "",
      title: "",
      textContent: "",
      style: {},
      children: [],
      parent: null,
      listeners: {},
      shadowRoot: null,
      attached: null, // the shadow root, which only this test may hold
      addEventListener(type, fn) {
        (this.listeners[type] = this.listeners[type] || []).push(fn);
      },
      appendChild(child) {
        child.parent = this;
        this.children.push(child);
        if (child.id) byId.set(child.id, child);
        return child;
      },
      remove() {
        if (this.parent) this.parent.children = this.parent.children.filter((c) => c !== this);
        this.parent = null;
        if (byId.get(this.id) === this) byId.delete(this.id);
      },
      attachShadow(init) {
        const shadow = {
          mode: init.mode,
          children: [],
          appendChild(c) {
            this.children.push(c);
            return c;
          },
        };
        this.attached = shadow;
        this.shadowRoot = init.mode === "open" ? shadow : null; // what a page sees
        return shadow;
      },
      fire(type, event) {
        for (const fn of this.listeners[type] || []) fn.call(this, event);
      },
    };
  }
  const body = element("body");
  return { body, createElement: element, getElementById: (id) => byId.get(id) || null };
}

test("the panel lives in a closed shadow root: the page sees only an empty host", () => {
  const doc = fakeDocument();
  const box = UI.open(doc, 10.4, 20.6);
  const host = doc.getElementById(UI.UI_ID);
  assert.ok(host, "no host in the page");
  assert.equal(host.attached.mode, "closed");
  assert.equal(host.shadowRoot, null, "the page can reach the panel's buttons");
  assert.deepEqual(host.children, [], "the host holds nothing the page can find");
  assert.equal(host.attached.children[0], box);
  assert.equal(host.style.cssText, "all:initial;position:absolute;left:10px;top:21px;z-index:2147483647");
});

test("opening again replaces the panel; closing removes it", () => {
  const doc = fakeDocument();
  UI.open(doc, 0, 0);
  UI.open(doc, 5, 5);
  assert.equal(doc.body.children.length, 1);
  UI.close(doc);
  assert.equal(doc.body.children.length, 0);
  assert.equal(doc.getElementById(UI.UI_ID), null);
  UI.close(doc); // closing nothing is fine
});

test("a button runs only for a real click", () => {
  const doc = fakeDocument();
  let pressed = 0;
  const b = UI.button(doc, "x", "Highlight (yellow)", () => pressed++);
  assert.equal(b.title, "Highlight (yellow)");
  b.fire("click", { isTrusted: false }); // el.click() or dispatchEvent from the page's script
  b.fire("click", {}); // an event with no isTrusted at all
  b.fire("click", { isTrusted: "true" });
  b.fire("click", null);
  assert.equal(pressed, 0, "a click the page made up pressed the button");
  b.fire("click", { isTrusted: true });
  assert.equal(pressed, 1);
});

test("trusted passes a real event and its this through", () => {
  const seen = [];
  const handler = UI.trusted(function (event) {
    seen.push([this, event.type]);
    return "done";
  });
  const self = {};
  assert.equal(handler.call(self, { isTrusted: true, type: "mouseup" }), "done");
  assert.equal(handler.call(self, { isTrusted: false, type: "mouseup" }), undefined);
  assert.deepEqual(seen, [[self, "mouseup"]]);
});

test("a press inside the panel stops there, whatever made it", () => {
  const doc = fakeDocument();
  UI.open(doc, 0, 0);
  let stopped = 0;
  doc.getElementById(UI.UI_ID).fire("mousedown", { stopPropagation: () => stopped++ });
  assert.equal(stopped, 1);
});

test("capBytes cuts by UTF-8 bytes, never inside a character", () => {
  const e = String.fromCodePoint(0xe9); // two bytes
  assert.equal(UI.capBytes("abc", 3), "abc");
  assert.equal(UI.capBytes("abcd", 3), "abc");
  assert.equal(UI.capBytes(`a${e}${e}`, 2), "a");
  assert.equal(UI.capBytes(`a${e}${e}`, 3), `a${e}`);
});

test("the shell is loaded into the page before the highlighter", () => {
  const manifest = JSON.parse(readFileSync(join(HERE, "manifest.json"), "utf8"));
  const scripts = manifest.content_scripts.find((c) => c.js.includes("highlight_page.js")).js;
  assert.deepEqual(scripts, ["content.js", "highlights.js", "highlight_panel.js", "highlight_page.js"]);
});

// From the source, since no automated run drives highlight_page.js in a page
// (the browser suite skips in CI): the panel opens on a trusted mouseup only,
// and every click handler is highlight_panel.js's trusted button().
test("the highlighter opens the panel on a trusted mouseup and binds no click of its own", () => {
  const source = readFileSync(join(HERE, "highlight_page.js"), "utf8");
  assert.match(source, /document\.addEventListener\("mouseup", UI\.trusted\(/);
  assert.equal(source.includes('addEventListener("click"'), false, "a click listener outside button()");
});
