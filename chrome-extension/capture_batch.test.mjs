// Tests for CLIP-06: a window or a tab group saved as one collection, one
// tab at a time, with every skip accounted for.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoCaptureBatch: Batch } = loadExtensionScripts(["capture_form.js", "capture_batch.js"]);

const tab = (id, url, extra = {}) =>
  Object.assign({ id, url, title: `Tab ${id}`, status: "complete" }, extra);

test("tabs no extension can reach are skipped with a reason", () => {
  const cases = [
    [tab(1, "chrome://settings"), /closed to extensions/],
    [tab(2, "chrome-extension://abc/page.html"), /closed to extensions/],
    [tab(3, "https://chromewebstore.google.com/detail/x"), /Chrome Web Store/],
    [tab(4, "https://chrome.google.com/webstore/detail/x"), /Chrome Web Store/],
    [tab(5, "https://paper.test/report.pdf"), /Chrome's viewer/],
    [tab(6, "https://paper.test/report.pdf?v=2"), /Chrome's viewer/],
    [tab(7, "https://paper.test/x", { discarded: true }), /discarded/],
    [tab(8, "https://paper.test/x", { status: "unloaded" }), /not loaded/],
    [tab(9, "file:///home/me/notes.html"), /file-access permission/],
    [tab(10, "about:blank"), /closed to extensions/],
    [tab(11, undefined), /no address yet/],
    [tab(12, "view-source:https://paper.test"), /closed to extensions/],
  ];
  for (const [t, pattern] of cases) {
    assert.match(Batch.skipReason(t), pattern, `tab ${t.id} (${t.url})`);
  }
});

test("an ordinary page is not skipped", () => {
  assert.equal(Batch.skipReason(tab(1, "https://paper.test/lighthouse")), "");
  assert.equal(Batch.skipReason(tab(2, "http://localhost:3000/app")), "");
  // A URL that merely mentions pdf is not a PDF.
  assert.equal(Batch.skipReason(tab(3, "https://paper.test/pdf-guide")), "");
});

test("the plan separates what will be captured from what will not", () => {
  const plan = Batch.planBatch([
    tab(1, "https://paper.test/a"),
    tab(2, "chrome://extensions"),
    tab(3, "https://paper.test/b"),
  ]);

  assert.equal(plan.total, 2);
  assert.deepEqual(plan.items.map((i) => i.tabId), [1, 3]);
  assert.equal(plan.skipped.length, 1);
  assert.equal(plan.skipped[0].tabId, 2);
  assert.match(plan.skipped[0].reason, /closed to extensions/);
});

test("a batch is named after where it came from, unless it is named", () => {
  const now = new Date("2026-09-21T08:00:00Z");
  assert.equal(Batch.planBatch([], { now }).collection, "Window — 2026-09-21");
  assert.equal(
    Batch.planBatch([], { scope: { kind: "group", groupTitle: "Reading" }, now }).collection,
    "Reading — 2026-09-21"
  );
  assert.equal(Batch.planBatch([], { scope: { kind: "group" }, now }).collection, "Tab group — 2026-09-21");
  assert.equal(Batch.planBatch([], { collection: "  My picks  ", now }).collection, "My picks");
});

/** recorder is the capture half: one call per tab, in order. */
function recorder(behaviour = {}) {
  const calls = [];
  let running = 0;
  return {
    calls,
    capture: async ({ tabId, collection }) => {
      running++;
      assert.equal(running, 1, "captures must not overlap");
      calls.push({ tabId, collection });
      await new Promise((r) => setTimeout(r, 1));
      running--;
      if (behaviour[tabId] instanceof Error) throw behaviour[tabId];
      return behaviour[tabId] || { ok: true, queued: false, warnings: [] };
    },
  };
}

test("every tab is captured, one at a time, into the one collection", async () => {
  const plan = Batch.planBatch([tab(1, "https://paper.test/a"), tab(2, "https://paper.test/b")], {
    collection: "Reading",
  });
  const rec = recorder();

  const report = await Batch.runBatch(plan, { capture: rec.capture });

  assert.deepEqual(rec.calls, [
    { tabId: 1, collection: "Reading" },
    { tabId: 2, collection: "Reading" },
  ]);
  assert.equal(report.done.length, 2);
  assert.equal(report.failed.length, 0);
  assert.equal(report.collection, "Reading");
});

test("one tab failing costs only that tab", async () => {
  const plan = Batch.planBatch([
    tab(1, "https://paper.test/a"),
    tab(2, "https://paper.test/b"),
    tab(3, "https://paper.test/c"),
  ]);
  const rec = recorder({ 2: new Error("debugger refused to attach") });

  const report = await Batch.runBatch(plan, { capture: rec.capture });

  assert.deepEqual(report.done.map((d) => d.tabId), [1, 3]);
  assert.deepEqual(report.failed.map((f) => [f.tabId, f.reason]), [[2, "debugger refused to attach"]]);
});

test("a capture that reports failure rather than throwing is still a failure", async () => {
  const plan = Batch.planBatch([tab(1, "https://paper.test/a")]);
  const rec = recorder({ 1: { ok: false, error: "no tab to capture" } });

  const report = await Batch.runBatch(plan, { capture: rec.capture });

  assert.equal(report.done.length, 0);
  assert.deepEqual(report.failed[0].reason, "no tab to capture");
});

test("progress counts up and is visible before each tab, not only after", async () => {
  const plan = Batch.planBatch([tab(1, "https://paper.test/a"), tab(2, "https://paper.test/b")]);
  const seen = [];

  await Batch.runBatch(plan, {
    capture: recorder().capture,
    onProgress: (s) => seen.push(`${s.phase}:${s.completed}/${s.total}${s.title ? ` ${s.title}` : ""}`),
  });

  assert.deepEqual(seen, [
    "start:0/2",
    "capturing:0/2 Tab 1",
    "captured:1/2 Tab 1",
    "capturing:1/2 Tab 2",
    "captured:2/2 Tab 2",
    "done:2/2",
  ]);
});

test("cancelling stops before the next tab and accounts for the rest", async () => {
  const plan = Batch.planBatch([
    tab(1, "https://paper.test/a"),
    tab(2, "https://paper.test/b"),
    tab(3, "https://paper.test/c"),
  ]);
  const rec = recorder();
  let stop = false;

  const report = await Batch.runBatch(plan, {
    capture: async (args) => {
      const out = await rec.capture(args);
      stop = true; // the user hits Cancel while the first capture is running
      return out;
    },
    cancelled: () => stop,
  });

  assert.equal(rec.calls.length, 1, "no capture starts after cancel");
  assert.equal(report.cancelled, true);
  assert.deepEqual(report.done.map((d) => d.tabId), [1]);
  assert.deepEqual(report.skipped.map((s) => [s.tabId, s.reason]), [
    [2, "cancelled before this tab was captured"],
    [3, "cancelled before this tab was captured"],
  ]);
});

test("a forty-tab window stays sequential and reports every skip", async () => {
  const tabs = [];
  for (let i = 1; i <= 40; i++) {
    tabs.push(i % 10 === 0 ? tab(i, `chrome://internal-${i}`) : tab(i, `https://paper.test/${i}`));
  }
  const plan = Batch.planBatch(tabs, { collection: "Everything" });
  const rec = recorder();

  const report = await Batch.runBatch(plan, { capture: rec.capture });

  assert.equal(plan.total, 36);
  assert.equal(rec.calls.length, 36);
  assert.equal(report.done.length, 36);
  assert.equal(report.skipped.length, 4);
  assert.equal(report.done.length + report.failed.length + report.skipped.length, 40);
});

test("the summary line says what happened", async () => {
  const plan = Batch.planBatch([tab(1, "https://paper.test/a"), tab(2, "chrome://x")], {
    collection: "Reading",
  });
  const report = await Batch.runBatch(plan, { capture: recorder().capture });
  assert.equal(Batch.summarize(report), "1 of 1 saved to “Reading” · 1 skipped");
});
