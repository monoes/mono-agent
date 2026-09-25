// Tests for what the Record section says: the step sentences, how param and
// superseding extract events fold into the list, the personal-value flags,
// and reading the analyzer's draft.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoRecordView: V } = loadExtensionScripts(["sidepanel_record_view.js"]);

test("steps read the way a person would say them", () => {
  const cases = [
    [{ type: "click", target: { tag: "button", text: "Save" } }, "Click 'Save'"],
    [{ type: "click", note: "double", target: { tag: "td", text: "Row" } }, "Double-click 'Row'"],
    [{ type: "type", value: "Ann", target: { tag: "input", label: "Email" } }, "Type 'Ann' into 'Email'"],
    [{ type: "type", masked: true, secretAs: "password", target: { tag: "input", name: "pw" } }, "Type into 'pw' (hidden — becomes secret password)"],
    [{ type: "select_option", value: "NL", target: { tag: "select", label: "Country" } }, "Choose 'NL' in 'Country'"],
    [{ type: "check", checked: false, target: { tag: "input", label: "Remember me" } }, "Uncheck 'Remember me'"],
    [{ type: "press_key", key: "Control+k" }, "Press Control+k"],
    [{ type: "navigate", navCause: "typed", url: "https://www.app.test/contacts?x=1" }, "Go to app.test/contacts"],
    [{ type: "navigate", navCause: "reload", url: "https://app.test/" }, "Reload the page"],
    [{ type: "navigated", navCause: "link", url: "https://app.test/" }, "Page changed to app.test"],
    [{ type: "upload", value: "cv.pdf" }, "Upload 'cv.pdf'"],
    [{ type: "submit" }, "Submit the form"],
    [{ type: "extract", extract: { field: "title", list: true, samples: ["a", "b", "c", "d", "e"] } }, "Extract list 'title' (5 samples)"],
    [{ type: "extract", extract: { field: "price", list: false, samples: ["$3"] } }, "Extract 'price'"],
    [{ type: "click", target: { tag: "div" } }, "Click 'the div'"],
  ];
  for (const [step, text] of cases) assert.equal(V.describe(step), text);
});

test("param events fold into the step they mark; the latest mark wins", () => {
  const rows = V.rows([
    { id: "e1", type: "type", value: "shoes", target: { label: "Search" } },
    { id: "e2", type: "param", param: { refEvent: "e1", name: "query" } },
    { id: "e3", type: "type", value: "blue" },
    { id: "e4", type: "param", param: { refEvent: "e3" } },
    { id: "e5", type: "param", param: { refEvent: "e3" }, note: "unset" },
  ]);
  assert.deepEqual(rows.map((r) => r.id), ["e1", "e3"]);
  assert.deepEqual([rows[0].isParam, rows[0].paramName, rows[0].canParam], [true, "query", true]);
  assert.equal(rows[1].isParam, false);
});

test("an extract that supersedes another hides it", () => {
  const rows = V.rows([
    { id: "e1", type: "extract", extract: { field: "price", list: false, samples: ["$1"] } },
    { id: "e2", type: "extract", extract: { field: "price", list: true, samples: ["$1", "$2"] }, note: "supersedes e1" },
    { id: "e3", type: "extract", extract: { field: "unit_price", list: true, samples: ["$1", "$2"] }, note: "supersedes e2" },
  ]);
  assert.deepEqual(rows.map((r) => [r.id, r.field]), [["e3", "unit_price"]]);
  assert.equal(rows[0].isExtract, true);
});

test("personal-looking values are suggested as inputs; masked ones cannot be toggled", () => {
  assert.equal(V.sensitiveKind("ann@example.com"), "email");
  assert.equal(V.sensitiveKind("+31 6 1234 5678"), "phone");
  assert.equal(V.sensitiveKind("4111 1111 1111 1111"), "card");
  assert.equal(V.sensitiveKind("hello world"), "");
  assert.equal(V.sensitiveKind("2026"), "");
  const rows = V.rows([
    { id: "e1", type: "type", value: "ann@example.com" },
    { id: "e2", type: "param", param: { refEvent: "e1" } },
    { id: "e3", type: "type", value: "(555) 123-4567" },
    { id: "e4", type: "type", masked: true, secretAs: "password" },
  ]);
  assert.deepEqual(rows.map((r) => [r.id, r.sensitive, r.suggestInput, r.canParam]), [
    ["e1", "email", false, true],
    ["e3", "phone", true, true],
    ["e4", "", false, false],
  ]);
});

test("the stopped summary says how many steps, where, and why it stopped", () => {
  const steps = [{ id: "e1", type: "click" }, { id: "e2", type: "param", param: { refEvent: "e1" } }];
  assert.equal(V.summary({ steps, url: "https://app.test/x", stopReason: "user" }), "1 step on app.test/x");
  assert.equal(
    V.summary({ steps: [], url: "", stopReason: "new_tab", queued: 3 }),
    "0 steps — stopped because a new tab was opened — recording follows one tab. 3 updates waiting for the bridge"
  );
});

test("a draft is read from record analyze --json", () => {
  const d = V.describeDraft({
    draftDir: "/h/.monoagent/recording-drafts/rec-1",
    draft: {
      recordingId: "rec-1",
      targetAutomation: "crm",
      isNew: false,
      action: "create_contact",
      saveAs: "action",
      names: { automation: "crm", action: "create_contact" },
      lint: [{ severity: "warning", code: "script_used", message: "page_script used" }],
      actionDef: {
        inputs: { required: [{ name: "email" }], optional: ["note"] },
        steps: [
          { id: "open", type: "navigate", url: "https://crm.test/new" },
          { id: "save", type: "click", configKey: "save_button", sideEffect: "write" },
        ],
      },
    },
  });
  assert.equal(d.action, "create_contact");
  assert.equal(d.automation, "crm");
  assert.deepEqual(d.inputs.map((i) => [i.name, i.required]), [["email", true], ["note", false]]);
  assert.deepEqual(d.steps.map((s) => [s.id, s.detail, s.sideEffect]), [
    ["open", "https://crm.test/new", false],
    ["save", "save_button", true],
  ]);
  assert.deepEqual(d.lint, [{ level: "warning", message: "page_script used" }]);
  assert.deepEqual(V.describeDraft(null).steps, [], "an empty answer draws an empty draft, not a crash");
});

test("verify results become one line per step", () => {
  const v = V.describeVerify({
    ok: false,
    steps: [
      { id: "open", type: "navigate", status: "pass" },
      { id: "save", type: "click", status: "stopped_before_side_effect", message: "safe mode" },
    ],
    stoppedAt: { step: "save" },
  });
  assert.equal(v.ok, false);
  assert.deepEqual(v.steps.map((s) => s.text), ["open navigate — pass", "save click — stopped_before_side_effect: safe mode"]);
  assert.ok(v.stoppedAt);
});

test("the real analyze draft: inputs that verify needs a value for (M13)", () => {
  const d = V.describeDraft({
    draftDir: "/d",
    draft: {
      action: "login",
      names: { automation: "crm", action: "login" },
      isNew: true,
      recordedInputs: { email: "ann@x.test" },
      inputs: [
        { name: "email", type: "string", required: true },
        { name: "password", type: "string", required: true, default: "{{secret:password}}" },
        { name: "otp", type: "secret", required: true },
        { name: "note", type: "string", required: false },
        { name: "team", type: "string", required: false, default: "sales" },
      ],
      actionDef: { steps: [{ id: "go", type: "navigate", url: "https://crm.test" }] },
      lint: [],
    },
  });
  assert.equal(d.isNew, true);
  assert.deepEqual(
    d.inputs.map((i) => [i.name, i.secret, i.needsValue, i.recorded]),
    [
      ["email", false, false, "ann@x.test"],
      ["password", true, true, ""],
      ["otp", true, true, ""],
      ["note", false, true, ""],
      ["team", false, false, ""],
    ]
  );
});

test("draft review: boolean side effects, error lint, scripts in full, Go's needsValue (D8, security)", () => {
  const d = V.describeDraft({
    draftDir: "/d",
    draft: {
      action: "post",
      names: { action: "post" },
      lint: [{ severity: "error", message: "selector save_button matches nothing" }],
      scripts: { "b.js": "return 2;", "a.js": "return document.title;" },
      inputs: [
        { name: "body", type: "string", required: true, secret: false, needsValue: false },
        { name: "key", type: "string", required: true, secret: true, needsValue: true },
      ],
      actionDef: { steps: [{ id: "s", type: "click", sideEffect: true }, { id: "r", type: "extract_text" }] },
    },
  });
  assert.deepEqual(d.steps.map((s) => s.sideEffect), [true, false]);
  assert.equal(V.hasErrors(d), true);
  assert.equal(V.hasErrors(V.describeDraft({ draft: { lint: [{ severity: "warning", message: "w" }] } })), false);
  assert.deepEqual(d.scripts, [{ name: "a.js", source: "return document.title;" }, { name: "b.js", source: "return 2;" }]);
  assert.deepEqual(d.inputs.map((i) => [i.name, i.secret, i.needsValue]), [["body", false, false], ["key", true, true]]);
});

test("a discarded recording says nothing was saved", () => {
  assert.equal(V.summary({ discarded: true, steps: [] }), "Nothing was saved: the recording had no steps.");
});
