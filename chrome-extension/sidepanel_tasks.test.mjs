// The side panel's "Add a task" box (sidepanel_tasks.js) against a fake
// document: what the button says, when the box is cleared, the keys, the
// shortcut's request to open it, and the refused tasks.
// `CHROME_PATH=/nonexistent node --test chrome-extension/sidepanel_tasks.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const IDS = ["task-panel", "task-text", "task-add", "task-msg", "task-failed", "task-failed-text", "task-failed-body", "task-dismiss"];
const settle = () => new Promise((r) => setTimeout(r, 5));

/** setup loads the box against fakes; `replies` answers ask() by message type (a value or a function). */
function setup({ current = { id: "p-work", name: "Work" }, replies = {}, focusAt } = {}) {
  const doc = { focused: null, listeners: {}, els: {} };
  for (const id of IDS) {
    doc.els[id] = {
      id, value: "", textContent: "", disabled: false, hidden: false, open: false, dataset: {}, listeners: {},
      addEventListener(type, fn) {
        (this.listeners[type] = this.listeners[type] || []).push(fn);
      },
      fire(type, event) {
        for (const fn of this.listeners[type] || []) fn(Object.assign({ preventDefault() {} }, event));
      },
      focus() {
        doc.focused = this;
      },
    };
  }
  doc.getElementById = (id) => doc.els[id] || null;
  doc.addEventListener = (type, fn) => (doc.listeners[type] = doc.listeners[type] || []).push(fn);
  doc.fire = (type) => (doc.listeners[type] || []).forEach((fn) => fn({}));
  const sent = [];
  const ask = async (message) => {
    sent.push(message);
    const answer = replies[message.type];
    return typeof answer === "function" ? answer(message) : answer || { ok: true };
  };
  const profiles = { current };
  const chrome = {
    runtime: { onMessage: { addListener() {} } },
    storage: {
      onChanged: { addListener() {} },
      session: { get: async () => (focusAt === undefined ? {} : { taskFocusAt: focusAt }) },
    },
  };
  loadExtensionScripts(["sidepanel_status.js", "sidepanel_view.js", "sidepanel_tasks.js"], { document: doc, chrome, ask, profiles });
  return { els: doc.els, doc, sent, profiles };
}

test("the button names the profile, refuses the shared inbox, and follows the header", () => {
  const { els, doc, profiles } = setup({ current: { id: "", name: "Shared inbox" } });
  assert.deepEqual([els["task-add"].textContent, els["task-add"].disabled], ["Choose a profile first", true]);
  profiles.current = { id: "p-work", name: "Work" };
  doc.fire("panel:profiles");
  assert.deepEqual([els["task-add"].textContent, els["task-add"].disabled], ["Add to Work", false]);
});

test("an added or waiting task clears the box; one that was not added stays to be fixed", async () => {
  let status = "queued";
  const reply = () => ({ ok: true, status, feedback: { level: status === "queued" ? "warn" : "error", text: `answer: ${status}` } });
  const { els, sent } = setup({ replies: { task_add: reply } });
  els["task-text"].value = "  Call the bank ";
  els["task-add"].fire("click");
  await settle();
  assert.deepEqual(sent.find((m) => m.type === "task_add"), { type: "task_add", text: "Call the bank", profile: "p-work" });
  assert.equal(els["task-text"].value, "");
  assert.deepEqual([els["task-msg"].dataset.kind, els["task-msg"].textContent], ["warn", "answer: queued"]);
  for (status of ["refused", "full", "no_profile", "empty"]) {
    els["task-text"].value = "Call the bank";
    els["task-add"].fire("click");
    await settle();
    assert.equal(els["task-text"].value, "Call the bank", `${status} cleared the box`);
  }
  assert.equal(els["task-msg"].dataset.kind, "err", "an error is drawn as the panel's err");
});

test("Cmd or Ctrl+Enter adds; Enter alone does not", async () => {
  const { els, sent } = setup({ replies: { task_add: { ok: true, status: "added", feedback: { level: "ok", text: "Added to Inbox in Work (#3)" } } } });
  const adds = () => sent.filter((m) => m.type === "task_add").length;
  els["task-text"].value = "Book the flights";
  els["task-text"].fire("keydown", { key: "Enter" });
  await settle();
  assert.equal(adds(), 0);
  els["task-text"].fire("keydown", { key: "Enter", metaKey: true });
  await settle();
  els["task-text"].value = "Pay the invoice";
  els["task-text"].fire("keydown", { key: "Enter", ctrlKey: true });
  await settle();
  assert.equal(adds(), 2);
});

test("a fresh request from the shortcut opens the section on its box; an old one does not", async () => {
  const fresh = setup({ focusAt: Date.now() });
  await settle();
  assert.equal(fresh.els["task-panel"].open, true);
  assert.equal(fresh.doc.focused, fresh.els["task-text"]);
  const old = setup({ focusAt: Date.now() - 60000 });
  await settle();
  assert.equal(old.els["task-panel"].open, false);
});

test("the refused tasks show their count and the latest's text to copy, and go on Dismiss", async () => {
  let failures = [{ title: "Reply to Sam", text: "Reply to Sam\nabout the invoice", reason: 'unknown profile "gone"' }];
  const { els, sent } = setup({
    replies: {
      task_state: () => ({ ok: true, waiting: 0, failures }),
      task_dismiss: () => {
        failures = [];
        return { ok: true };
      },
    },
  });
  await settle();
  assert.equal(els["task-failed"].hidden, false);
  assert.equal(els["task-failed-text"].textContent, 'MonoAgent did not add 1 task. Latest: "Reply to Sam": unknown profile "gone"');
  assert.equal(els["task-failed-body"].value, "Reply to Sam\nabout the invoice");
  els["task-dismiss"].fire("click");
  await settle();
  assert.ok(sent.some((m) => m.type === "task_dismiss"));
  assert.equal(els["task-failed"].hidden, true);
});
