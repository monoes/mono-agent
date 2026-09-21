// Tests for the in-page half of a capture (capture_page.js).
//
// This file exists because for a long time nothing loaded capture_page.js at
// all — the module the whole capture runs through had no test of any kind.
// What it covers is bounded on purpose: `cssPath` and `restore` are the two
// entry points that need elements rather than a browser, and a handful of
// plain objects is a truer stand-in for them than a DOM engine would be.
//
// `prepare` and `extract` are still uncovered here and need more than fakes;
// see the note at the foot of test_helpers.mjs.
//
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** el builds the slice of an Element that cssPath and restore actually read. */
function el(tagName, props = {}) {
  const node = Object.assign(
    { nodeType: 1, tagName: tagName.toUpperCase(), id: "", parentElement: null, children: [], style: {} },
    props
  );
  for (const child of node.children) child.parentElement = node;
  return node;
}

/** page loads capture_page.js against a fake document and window. */
function page({ body = el("body"), scrolled = [] } = {}) {
  const documentElement = el("html", { style: {} });
  const document = { body, documentElement, title: "A Page", baseURI: "https://x.test/a" };
  const sandbox = loadExtensionScripts(["capture_page.js"], {
    document,
    location: { href: "https://x.test/a" },
    getComputedStyle: () => ({ position: "static", display: "block" }),
  });
  sandbox.scrollX = 0;
  sandbox.scrollY = 0;
  sandbox.scrollTo = (x, y) => scrolled.push([x, y]);
  return { sandbox, page: sandbox.MonoCapturePage, document, scrolled };
}

test("capture_page.js loads at all", () => {
  const { page: mono } = page();
  assert.deepEqual(Object.keys(mono).sort(), ["cssPath", "extract", "prepare", "restore"]);
});

test("cssPath stops at the nearest id, because that is the stable part", () => {
  const { page: mono } = page();
  const article = el("article", { id: "main" });
  const p = el("p", { parentElement: article });
  article.children = [p];
  assert.equal(mono.cssPath(p), "#main > p");
});

test("cssPath numbers a tag only when it has to", () => {
  const { page: mono } = page();
  const body = el("body");
  const one = el("p");
  const two = el("p");
  const only = el("h1");
  body.children = [one, two, only];
  for (const c of body.children) c.parentElement = body;

  assert.equal(mono.cssPath(two), "body > p:nth-of-type(2)", "two siblings share a tag, so both are numbered");
  assert.equal(mono.cssPath(only), "body > h1", "a lone tag is not numbered");
});

test("cssPath gives up rather than emitting an unbounded selector", () => {
  const { page: mono } = page();
  // A deeply nested node with no id anywhere: the path is capped, so a
  // pathological page cannot produce a selector megabytes long.
  let node = el("body");
  for (let i = 0; i < 40; i++) {
    const child = el("div", { parentElement: node });
    node.children = [child];
    node = child;
  }
  const path = mono.cssPath(node);
  assert.equal(path.split(" > ").length, 6, "six segments, the documented maxDepth");
  assert.equal(path.includes("body"), false, "the cap bit before it reached the top");
});

test("cssPath does not walk off a detached node", () => {
  const { page: mono } = page();
  assert.equal(mono.cssPath(el("div")), "div");
  assert.equal(mono.cssPath(null), "");
  assert.equal(mono.cssPath({ nodeType: 3 }), "", "a text node has no path");
});

test("restore puts back exactly what prepare changed, and nothing else", () => {
  const scrolled = [];
  const body = el("body", { style: { overflow: "hidden" } });
  const { sandbox, page: mono, document } = page({ body, scrolled });

  const banner = el("div", { style: { display: "flex", visibility: "visible" } });
  const untouched = el("div", { style: { display: "grid" } });
  const details = { open: true };

  // The shape prepare() leaves behind, written directly so restore is tested
  // without a browser in the way.
  sandbox.__monoCaptureState = {
    hidden: [{ el: banner, display: "flex", visibility: "visible" }],
    details: [details],
    scroll: { x: 12, y: 340 },
    bodyOverflow: { body: "hidden", html: "" },
  };
  banner.style.display = "none";

  assert.deepEqual(mono.restore(), { restored: true });
  assert.equal(banner.style.display, "flex", "the overlay's own display is back");
  assert.equal(untouched.style.display, "grid", "an element prepare never touched is left alone");
  assert.equal(details.open, false);
  assert.equal(document.body.style.overflow, "hidden");
  assert.equal(document.documentElement.style.overflow, "");
  assert.deepEqual(scrolled, [[12, 340]], "the reader is put back where they were");
  assert.equal(sandbox.__monoCaptureState, null, "the state is cleared, so a second restore is a no-op");
});

test("restore on a page that was never prepared is harmless", () => {
  const { page: mono, scrolled } = page();
  assert.deepEqual(mono.restore(), { restored: true });
  assert.deepEqual(scrolled, [], "nothing was moved, so nothing is put back");
});

test("an element whose display was never set comes back to the empty string, not 'undefined'", () => {
  const { sandbox, page: mono } = page();
  const overlay = el("div", { style: { display: "none" } });
  sandbox.__monoCaptureState = {
    hidden: [{ el: overlay, display: undefined, visibility: undefined }],
    details: [],
    scroll: null,
    bodyOverflow: null,
  };
  mono.restore();
  assert.equal(overlay.style.display, "", "back to the stylesheet's value, not a literal undefined");
  assert.equal(overlay.style.visibility, "");
});
