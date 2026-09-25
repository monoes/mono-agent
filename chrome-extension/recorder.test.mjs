// Tests for the in-page recorder (recorder.js): how raw DOM events become
// recorded events — debounced final values, masked secrets, folded submits,
// dropped printable keys — driven through a fake document's capture
// listeners with a manual clock.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { h, fakeDocument, countingEnv, fakeTimers } from "./recorder_fake_dom.mjs";

const { MonoRecorder: R } = loadExtensionScripts(["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder_dom.js", "recorder.js"]);

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
  assert.deepEqual(events().map((e) => e.type), ["click"], "a submit with no action of the person's before it is the page's own");
  doc.dispatch("keydown", h("input", {}), { key: "Tab" });
  clock.advance(900);
  doc.dispatch("submit", form);
  assert.deepEqual(events().map((e) => e.type), ["click", "press_key", "submit"], "one right after the person acted is recorded");
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
  assert.equal(R.secretName(h("input", { id: ":r1:" }), "password", ""), "password");
});

// ── review fixes (H2, H4, M6, M11, LOW) ────────────────────────────────

test("picking a password or card field as data sends no value (H2)", () => {
  const pw = h("input", { type: "password", name: "pw" });
  pw.value = "hunter2";
  const cvv = h("input", { name: "card_cvv" });
  cvv.value = "123";
  const { doc, sent, events } = setup([pw, cvv]);
  doc.dispatch("click", pw, { altKey: true });
  doc.dispatch("click", cvv, { altKey: true });
  for (const e of events()) {
    assert.equal(e.type, "extract");
    assert.equal(e.masked, true);
    assert.deepEqual(e.extract.samples, []);
  }
  assert.ok(!JSON.stringify(sent).includes("hunter2"));
  assert.ok(!JSON.stringify(sent).includes('"123"'));
});

test("stopActive resets pick mode, so the next recording starts with clicks working (H4)", () => {
  const g = loadExtensionScripts(["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder_dom.js", "recorder.js"]);
  g.MonoRecorder.setPick(true);
  assert.equal(g.__monoRecorderPick, true);
  g.MonoRecorder.stopActive();
  assert.equal(g.__monoRecorderPick, false);
});

test("a stopped recorder is inert even if a listener survived (bfcache)", () => {
  const b = h("button", {}, "Go");
  const { doc, rec, events } = setup([b]);
  const handlers = rec.handlers;
  rec.stop();
  handlers.onClick({ type: "click", target: b, isTrusted: true, preventDefault() {} });
  handlers.onKeyDown({ key: "Enter", target: b });
  assert.equal(events().length, 0);
  assert.equal(rec.isStopped(), true);
  assert.equal(doc.listeners.click.length, 0);
});

test("targets inside an open shadow root come from composedPath (M6)", () => {
  const inner = h("button", {}, "Inside");
  const host = h("my-widget", {});
  const { doc, events } = setup([host]);
  doc.dispatch("click", host, { composedPath: () => [inner, host] });
  assert.equal(events()[0].target.text, "Inside");
});

test("change events inside a shadow root are heard through the root (M6)", () => {
  const box = h("input", { type: "checkbox", name: "agree" });
  const shadowListeners = {};
  const shadow = {
    host: h("x-form", {}),
    addEventListener: (t, fn) => ((shadowListeners[t] = shadowListeners[t] || []).push(fn)),
    removeEventListener: (t, fn) => (shadowListeners[t] = (shadowListeners[t] || []).filter((f) => f !== fn)),
  };
  box.getRootNode = () => shadow;
  const { doc, rec, events } = setup([shadow.host]);
  doc.dispatch("focusin", shadow.host, { composedPath: () => [box, shadow.host] });
  assert.equal(shadowListeners.change.length, 1, "the root is watched once touched");
  box.checked = true;
  shadowListeners.change[0]({ type: "change", target: box });
  assert.deepEqual(events().map((e) => [e.type, e.checked]), [["check", true]]);
  rec.stop();
  assert.equal(shadowListeners.change.length, 0, "and released on stop");
});

test("AltGr, alt-only typing in a field and key repeat are not steps (M11)", () => {
  const altGr = { key: "@", ctrlKey: true, altKey: true, getModifierState: (m) => m === "AltGraph" };
  assert.equal(R.keyCombo(altGr, true), "");
  assert.equal(R.keyCombo({ key: "e", altKey: true }, true), "", "Option+e on a Mac types a character");
  assert.equal(R.keyCombo({ key: "e", altKey: true }, false), "Alt+e", "outside a field it is a shortcut");
  const f = h("input", { name: "q" });
  const { doc, events } = setup([f]);
  doc.dispatch("keydown", f, { key: "Enter" });
  doc.dispatch("keydown", f, { key: "Enter", repeat: true });
  assert.equal(events().length, 1);
});

test("the mask is latched at the first keystroke: show-password does not unmask (LOW)", () => {
  const pw = h("input", { type: "password", name: "pw" });
  const { doc, clock, sent } = setup([pw]);
  typeInto(doc, pw, "hun");
  pw.attrs.type = "text"; // the page's "show password" toggle
  typeInto(doc, pw, "hunter2");
  clock.advance(R.DEBOUNCE_MS);
  assert.equal(sent[0].event.masked, true);
  assert.ok(!JSON.stringify(sent).includes("hunter"));
});

test("pagehide flushes the value being typed (LOW)", () => {
  const f = h("input", { name: "q" });
  const html = h("html", {}, h("body", {}, f));
  const doc = fakeDocument(html);
  const winListeners = {};
  const win = { addEventListener: (t, fn) => (winListeners[t] = fn), removeEventListener() {}, location: { href: "https://a.test/" } };
  const sent = [];
  const clock = fakeTimers();
  const rec = R.createRecorder({ doc, win, send: (m) => sent.push(m), env: countingEnv(), now: clock.now, setTimer: clock.setTimer, clearTimer: clock.clearTimer });
  rec.start();
  typeInto(doc, f, "leaving");
  winListeners.pagehide({ type: "pagehide" });
  assert.equal(sent[0].event.value, "leaving");
});

// ── security review (H3, H6, M5) ───────────────────────────────────────

test("synthetic (untrusted) events of any kind are never recorded (H3)", () => {
  const f = h("input", { name: "q" });
  const b = h("button", {}, "Go");
  const form = h("form", {}, f, b);
  const { doc, clock, events } = setup([form]);
  const fake = { isTrusted: false };
  f.value = "injected";
  doc.dispatch("input", f, fake);
  doc.dispatch("change", f, fake);
  doc.dispatch("click", b, fake);
  doc.dispatch("click", b, Object.assign({ altKey: true }, fake));
  doc.dispatch("keydown", f, Object.assign({ key: "Enter" }, fake));
  doc.dispatch("submit", form, fake);
  clock.advance(R.DEBOUNCE_MS);
  assert.deepEqual(events(), []);
});

test("a secret field's fingerprint says sensitive; an ordinary one does not (H6)", () => {
  const token = h("input", { name: "api_token" });
  const plain = h("input", { name: "city" });
  const { doc, clock, events } = setup([token, plain]);
  typeInto(doc, token, "tk_live_1");
  typeInto(doc, plain, "Utrecht");
  clock.advance(R.DEBOUNCE_MS);
  const [a, b] = events();
  assert.deepEqual([a.masked, a.target.sensitive, a.value], [true, true, undefined]);
  assert.deepEqual([b.value, b.target.sensitive], ["Utrecht", undefined]);
});

test("a field that was ever a password stays masked after the page makes it text (H6)", () => {
  const pw = h("input", { type: "password", name: "p1" });
  const { doc, clock, events } = setup([pw]);
  doc.dispatch("focusin", pw);
  pw.attrs.type = "text"; // "show password", before a single key
  typeInto(doc, pw, "hunter2");
  clock.advance(R.DEBOUNCE_MS);
  assert.equal(events()[0].masked, true);
  assert.equal(events()[0].target.sensitive, true);
});

test("event URLs and link hrefs are sanitised in the page (M5)", () => {
  const link = h("a", { href: "https://app.test/magic?magic=abc&x=1#frag" }, "Open");
  const html = h("html", {}, h("body", {}, link));
  const doc = fakeDocument(html);
  const sent = [];
  const rec = R.createRecorder({ doc, send: (m) => sent.push(m), env: countingEnv(), url: () => "https://app.test/p?session=zzz#top" });
  rec.start();
  doc.dispatch("click", link);
  const e = sent[0].event;
  assert.equal(e.url, "https://app.test/p?session=REDACTED");
  assert.equal(e.target.href, "https://app.test/magic?magic=REDACTED&x=1");
});
