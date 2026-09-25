// Tests for the recorder's element fingerprints (§8.2): which selectors are
// offered, in what order, and how uniqueness in the live page moves them.
// Counting is faked (countingEnv) — the real counting against a document is
// exercised in recorder.browser.test.mjs.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { h, countingEnv } from "./recorder_fake_dom.mjs";

const { MonoRecorderSelectors: S } = loadExtensionScripts(["recorder_selectors.js"]);

const kinds = (fp) => fp.candidates.map((c) => `${c.kind}:${c.value || `${c.role}/${c.name}`}`);

test("generated-looking ids are not offered as selectors", () => {
  for (const id of [":r3:", ":R1a2:", "ember1234", "react-select-5-input", "radix-:r0:-trigger", "a1b2c3d4e5f6", "item-4f9a2c-8b1e7d-x", "u_0_1a_9f"]) {
    assert.equal(S.looksGenerated(id), true, id);
  }
  for (const id of ["email", "login-form", "submit_button", "main-nav", "search2"]) {
    assert.equal(S.looksGenerated(id), false, id);
  }
});

test("a test id beats everything, and a stable id comes next", () => {
  const button = h("button", { "data-testid": "save", id: "save-btn", class: "btn" }, "Save");
  h("html", {}, h("body", {}, h("form", {}, button)));
  const fp = S.fingerprint(button, countingEnv());
  assert.equal(fp.tag, "button");
  assert.equal(fp.testId, "save");
  assert.equal(fp.role, "button");
  assert.equal(fp.ariaName, "Save");
  assert.equal(fp.text, "Save");
  assert.deepEqual(kinds(fp).slice(0, 4), [
    'css:[data-testid="save"]',
    "css:#save-btn",
    "aria:button/Save",
    "text:Save",
  ]);
  assert.ok(fp.candidates.every((c) => c.unique && c.count === 1));
  assert.equal(fp.candidates.at(-1).kind, "xpath", "the absolute XPath is the last resort");
});

test("a generated id is recorded on the fingerprint but never as a candidate", () => {
  const input = h("input", { id: ":r7:", name: "email", type: "email", placeholder: "you@example.com" });
  h("html", {}, h("body", {}, input));
  const labels = new Map([[input, "Email address"]]);
  const fp = S.fingerprint(input, countingEnv({}, labels));
  assert.equal(fp.id, ":r7:");
  assert.equal(fp.inputType, "email");
  assert.equal(fp.label, "Email address");
  assert.equal(fp.ariaName, "Email address");
  assert.ok(!kinds(fp).some((k) => k.includes(":r7:") && k.startsWith("css:#")));
  assert.deepEqual(kinds(fp).slice(0, 4), [
    "aria:textbox/Email address",
    'css:input[name="email"]',
    'xpath://input[@id=//label[normalize-space(.)=\'Email address\']/@for]',
    'css:input[placeholder="you@example.com"]',
  ]);
});

test("a selector matching several elements is scored below a unique one", () => {
  const link = h("a", { href: "/next", class: "more" }, "More");
  h("html", {}, h("body", {}, h("ul", {}, h("li", {}, link))));
  const env = countingEnv({ "aria:link:More": 12, "text:More": 12 });
  const fp = S.fingerprint(link, env);
  const aria = fp.candidates.find((c) => c.kind === "aria");
  assert.equal(aria.unique, false);
  assert.equal(aria.count, 12);
  assert.ok(aria.score < S.STABILITY.css, "a 12-way match ranks under even the structural path");
  assert.equal(fp.candidates[0].kind, "css", "the structural path wins when nothing better is unique");
  assert.equal(fp.candidates[0].value, fp.css);
});

test("a selector that matches nothing scores zero", () => {
  assert.equal(S.scoreOf("testid", 0), 0);
  assert.equal(S.scoreOf("testid", 1), 1);
  assert.ok(S.scoreOf("testid", 2) < S.scoreOf("text", 1));
});

test("the structural path stops at a stable id and uses nth-of-type only when needed", () => {
  const second = h("li", {}, "b");
  h("html", {}, h("body", {}, h("div", { id: "results" }, h("ul", {}, h("li", {}, "a"), second))));
  assert.equal(S.cssPath(second), "#results > ul > li:nth-of-type(2)");
  assert.equal(S.xpathOf(second), "/html/body/div/ul/li[2]");
});

test("xpath literals with both quote kinds use concat", () => {
  assert.equal(S.xpathString("plain"), "'plain'");
  assert.equal(S.xpathString("it's"), `"it's"`);
  assert.equal(S.xpathString(`say "it's"`), `concat('say "it', "'", 's"')`);
});

test("text candidates are only offered for things people name by their text", () => {
  const div = h("div", { class: "card" }, "Some long paragraph");
  h("html", {}, h("body", {}, div));
  const fp = S.fingerprint(div, countingEnv());
  assert.ok(!fp.candidates.some((c) => c.kind === "text"));
});

test("fingerprint JSON keys are exactly the Go Fingerprint/Candidate tags", async () => {
  const { goJsonTags } = await import("./recorder_go_types.mjs");
  const tags = goJsonTags();
  const input = h("input", { id: "q", name: "q", type: "search", autocomplete: "off", "data-qa": "search" });
  h("html", {}, h("body", {}, input));
  input.getBoundingClientRect = () => ({ x: 1, y: 2, width: 30, height: 10 });
  const fp = S.fingerprint(input, countingEnv({}, new Map([[input, "Search"]])));
  for (const k of Object.keys(fp)) assert.ok(tags.Fingerprint.has(k), `Fingerprint.${k}`);
  for (const c of fp.candidates) for (const k of Object.keys(c)) assert.ok(tags.Candidate.has(k), `Candidate.${k}`);
  assert.deepEqual(Object.keys(fp.rect).sort(), ["H", "W", "X", "Y"], "Rect has no json tags, so Go field names");
});
