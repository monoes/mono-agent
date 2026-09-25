// Tests for the in-page recorder (recorder.js): how raw DOM events become
// recorded events — debounced final values, masked secrets, folded submits,
// dropped printable keys — driven through a fake document's capture
// listeners with a manual clock.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { h, fakeDocument, countingEnv, fakeTimers } from "./recorder_fake_dom.mjs";

const { MonoRecorder: R } = loadExtensionScripts(["recorder_selectors.js", "recorder_list.js", "recorder.js"]);

function setup(body, opts = {}) {
  const html = h("html", {}, h("body", {}, ...body));
  const doc = fakeDocument(html);
  const clock = fakeTimers();
  const sent = [];
  const rec = R.createRecorder({
    doc,
    send: (m) => sent.push(m),
    env: countingEnv({}, opts.labels || new Map()),
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
    url: () => "https://app.test/form",
  });
  rec.start();
  const events = () => sent.map((m) => m.event);
  return { doc, clock, sent, rec, events };
}

const typeInto = (doc, el, value) => {
  el.value = value;
  doc.dispatch("input", el);
};

test("typing is debounced to one event carrying only the final value", () => {
  const email = h("input", { type: "email", name: "email" });
  const { doc, clock, events } = setup([email]);
  for (const v of ["a", "an", "ann", "ann@x.test"]) {
    typeInto(doc, email, v);
    clock.advance(100);
  }
  assert.equal(events().length, 0, "nothing while still typing");
  clock.advance(R.DEBOUNCE_MS);
  assert.equal(events().length, 1);
  assert.equal(events()[0].type, "type");
  assert.equal(events()[0].value, "ann@x.test");
  assert.equal(events()[0].url, "https://app.test/form");
  assert.equal(events()[0].target.name, "email");
});

test("a click flushes the pending value first, so the order is the order it happened", () => {
  const name = h("input", { name: "name" });
  const save = h("button", {}, "Save");
  const { doc, events } = setup([h("form", {}, name, save)]);
  typeInto(doc, name, "Ann");
  doc.dispatch("click", save);
  assert.deepEqual(events().map((e) => e.type), ["type", "click"]);
  assert.equal(events()[1].target.text, "Save");
});

test("password, cc-* and card-number values are never sent", () => {
  const pw = h("input", { type: "password", name: "pass" });
  const cc = h("input", { name: "cardnum", autocomplete: "cc-number" });
  const plain = h("input", { name: "note" });
  const { doc, clock, sent, events } = setup([pw, cc, plain]);
  typeInto(doc, pw, "hunter2");
  typeInto(doc, cc, "4111 1111 1111 1111");
  typeInto(doc, plain, "4242424242424242"); // Luhn-valid, no autocomplete hint
  clock.advance(R.DEBOUNCE_MS);
  const [a, b, c] = events();
  assert.deepEqual([a.masked, a.secretAs, a.value], [true, "pass", undefined]);
  assert.deepEqual([b.masked, b.secretAs, b.value], [true, "cardnum", undefined]);
  assert.deepEqual([c.masked, c.value], [true, undefined]);
  assert.ok(!JSON.stringify(sent).includes("hunter2"));
  assert.ok(!JSON.stringify(sent).includes("4111"));
});

test("clicks land on the nearest actionable ancestor", () => {
  const icon = h("span", { class: "icon" });
  const button = h("button", { "aria-label": "Close" }, icon);
  const { doc, events } = setup([button]);
  doc.dispatch("click", icon);
  assert.equal(events()[0].target.tag, "button");
  assert.equal(events()[0].target.ariaName, "Close");
});

test("checkbox clicks become one check event with the state; selects become select_option", () => {
  const box = h("input", { type: "checkbox", name: "terms" });
  const sel = h("select", { name: "country" });
  const { doc, events } = setup([box, sel]);
  doc.dispatch("click", box);
  box.checked = true;
  doc.dispatch("change", box);
  sel.value = "NL";
  doc.dispatch("change", sel);
  assert.deepEqual(events().map((e) => e.type), ["check", "select_option"]);
  assert.equal(events()[0].checked, true);
  assert.equal(events()[1].value, "NL");
});

test("file inputs record file names only", () => {
  const file = h("input", { type: "file", name: "cv" });
  const { doc, events } = setup([file]);
  file.files = [{ name: "cv.pdf", size: 1 }, { name: "photo.png", size: 2 }];
  doc.dispatch("change", file);
  assert.equal(events()[0].type, "upload");
  assert.equal(events()[0].value, "cv.pdf, photo.png");
});

test("printable keys are dropped; Enter, Escape, Tab and shortcuts are kept", () => {
  const f = h("input", { name: "q" });
  const div = h("div", {});
  const inField = (key, mods = {}) => R.keyCombo(Object.assign({ key }, mods), true);
  assert.equal(inField("a"), "");
  assert.equal(inField("A", { shiftKey: true }), "");
  assert.equal(inField("Enter"), "Enter");
  assert.equal(inField("Tab", { shiftKey: true }), "Shift+Tab");
  assert.equal(inField("Escape"), "Escape");
  assert.equal(inField("ArrowDown"), "", "moving the caret is not a step");
  assert.equal(R.keyCombo({ key: "ArrowDown" }, false), "ArrowDown");
  assert.equal(inField("v", { ctrlKey: true }), "", "paste changes the value, which type records");
  assert.equal(R.keyCombo({ key: "k", ctrlKey: true }, false), "Control+k");
  assert.equal(R.keyCombo({ key: "S", metaKey: true, shiftKey: true }, false), "Meta+Shift+s");
  assert.equal(inField("Shift", { shiftKey: true }), "");

  const { doc, events } = setup([f, div]);
  typeInto(doc, f, "shoes");
  doc.dispatch("keydown", f, { key: "s" });
  doc.dispatch("keydown", f, { key: "Enter" });
  assert.deepEqual(events().map((e) => e.type), ["type", "press_key"]);
  assert.equal(events()[1].key, "Enter");
});

test("a submit right after its own click is folded into it", () => {
  const save = h("button", { type: "submit" }, "Save");
  const form = h("form", { id: "f" }, save);
  const { doc, clock, events } = setup([form]);
  doc.dispatch("click", save);
  doc.dispatch("submit", form);
  assert.deepEqual(events().map((e) => e.type), ["click"]);
  clock.advance(5000);
  doc.dispatch("submit", form);
  assert.deepEqual(events().map((e) => e.type), ["click", "submit"], "a later, script-driven submit is its own step");
});

test("Alt+click marks data and never reaches the page; a similar second pick proposes a list", () => {
  const items = [1, 2, 3].map((i) => h("li", { class: "product" }, h("span", { class: "price" }, `$${i}`)));
  const { doc, sent, events } = setup([h("ul", { id: "grid" }, ...items)]);
  const first = doc.dispatch("click", items[0].children[0], { altKey: true });
  assert.equal(first.defaultPrevented, true);
  doc.dispatch("click", items[1].children[0], { altKey: true });
  const [one, list] = events();
  assert.equal(one.type, "extract");
  assert.deepEqual(one.extract.samples, ["$1"]);
  assert.equal(list.extract.list, true);
  assert.equal(list.extract.fieldSelector, "span.price");
  assert.deepEqual(list.extract.samples, ["$1", "$2", "$3"]);
  assert.equal(sent[1].replacesLastExtract, true, "the worker notes the list supersedes the single pick");
});

test("Pick data mode turns plain clicks into extracts", () => {
  const title = h("h1", {}, "Order #42");
  const { doc, rec, events } = setup([title]);
  rec.setPick(true);
  const ev = doc.dispatch("click", title);
  assert.equal(ev.defaultPrevented, true);
  assert.equal(events()[0].type, "extract");
  assert.equal(events()[0].extract.field, "title");
  rec.setPick(false);
  doc.dispatch("click", title);
  assert.equal(events()[1].type, "click");
});

test("stop flushes the last typed value and removes every listener", () => {
  const f = h("input", { name: "q" });
  const { doc, rec, events } = setup([f]);
  typeInto(doc, f, "last words");
  rec.stop();
  assert.equal(events()[0].value, "last words");
  assert.ok(Object.values(doc.listeners).every((l) => l.length === 0));
});

test("partial events only use Event JSON keys (plus `at`, which the worker converts)", async () => {
  const { goJsonTags } = await import("./recorder_go_types.mjs");
  const allowed = new Set([...goJsonTags().Event, "at"]);
  const f = h("input", { type: "password", name: "p" });
  const b = h("button", {}, "Go");
  const { doc, events } = setup([f, b]);
  typeInto(doc, f, "x");
  doc.dispatch("click", b);
  doc.dispatch("keydown", b, { key: "Escape" });
  for (const e of events()) for (const k of Object.keys(e)) assert.ok(allowed.has(k), `Event.${k}`);
});

test("maskReason and secretName", () => {
  assert.equal(R.maskReason(h("input", { type: "hidden" }), "x"), "hidden");
  assert.equal(R.maskReason(h("input", { autocomplete: "one-time-code" }), "123456"), "otp");
  assert.equal(R.maskReason(h("input", { autocomplete: "new-password" }), "x"), "password");
  assert.equal(R.maskReason(h("input", {}), "hello"), "");
  assert.equal(R.luhn("4242 4242 4242 4242"), true);
  assert.equal(R.luhn("4242 4242 4242 4241"), false);
  assert.equal(R.secretName(h("input", { id: ":r1:" }), "password", countingEnv()), "password");
});
