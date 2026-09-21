// Tests for the page_capture orchestration: the envelope, the size cap, the
// chunk protocol, and the failure paths that must degrade instead of throw.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoCapture } = loadExtensionScripts(["capture_meta.js", "capture.js"]);

const b64 = (s) => Buffer.from(s, "utf8").toString("base64");

/** fakeCtx stands in for Chrome: one page, scripted CDP answers. */
function fakeCtx(overrides = {}) {
  const calls = { injected: [], page: [], cdp: [], attached: 0, detached: 0 };
  const cdpAnswers = Object.assign(
    {
      "Page.enable": {},
      "Page.getLayoutMetrics": { cssContentSize: { width: 1200, height: 3000 } },
      "Page.captureScreenshot": { data: b64("PNGDATA") },
      "Page.printToPDF": { data: b64("%PDF-1.7") },
      "Page.captureSnapshot": { data: "From: <Snapshot>\r\nContent-Type: multipart/related" },
    },
    overrides.cdpAnswers || {}
  );
  const pageAnswers = Object.assign(
    {
      prepare: { scrollPasses: 4, overlaysHidden: 2, imagesPromoted: 9 },
      extract: {
        meta: {
          url: "https://paper.test/lighthouse",
          canonicalUrl: "https://paper.test/lighthouse",
          title: "The Lighthouse at Dunmore",
          capturedAt: "2026-09-21T10:00:00.000Z",
          contentHash: null,
          tags: [],
          source: "extension",
        },
        markdown: "# The Lighthouse at Dunmore\n\nThe keeper kept a ledger.",
        text: "The Lighthouse at Dunmore The keeper kept a ledger.",
        selectionRequested: false,
        selectionFound: false,
      },
      restore: { restored: true },
    },
    overrides.pageAnswers || {}
  );

  return {
    calls,
    resolveTabId: async () => overrides.tabId ?? 7,
    inject: async (tabId, files) => calls.injected.push(...files),
    callPage: async (tabId, fn, args) => {
      calls.page.push(fn);
      if (overrides.pageThrows && overrides.pageThrows[fn]) throw new Error(overrides.pageThrows[fn]);
      return pageAnswers[fn];
    },
    attach: async () => {
      calls.attached++;
      if (overrides.attachThrows) throw new Error(overrides.attachThrows);
    },
    cdp: async (tabId, method) => {
      calls.cdp.push(method);
      if (overrides.cdpThrows && overrides.cdpThrows[method]) throw new Error(overrides.cdpThrows[method]);
      return cdpAnswers[method];
    },
    detach: async () => {
      calls.detached++;
    },
  };
}

const names = (result) => result.artifacts.map((a) => a.name);

test("a capture produces the envelope the contract names", async () => {
  const ctx = fakeCtx();
  const result = await MonoCapture.pageCapture({ formats: ["mhtml", "pdf", "readable", "screenshot"] }, ctx);

  assert.deepEqual(names(result).sort(), ["page.mhtml", "page.pdf", "readable.md", "screenshot.png"]);
  for (const artifact of result.artifacts) {
    assert.equal(artifact.encoding, "base64");
    assert.ok(artifact.bytes.length > 0, `${artifact.name} carries bytes`);
    assert.ok(artifact.rawBytes > 0);
  }
  assert.match(result.meta.contentHash, /^sha256:[0-9a-f]{64}$/, "the worker hashes what the page could not");
  assert.deepEqual(result.warnings, []);
  assert.deepEqual(ctx.calls.page, ["prepare", "extract", "restore"], "the page is prepared, read, then put back");
  assert.equal(ctx.calls.detached, 1, "the debugger is always released");
});

test("readable.md is the Markdown the page extracted", async () => {
  const result = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx());
  const readable = result.artifacts.find((a) => a.name === "readable.md");
  assert.match(Buffer.from(readable.bytes, "base64").toString("utf8"), /^# The Lighthouse at Dunmore/);
});

test("one failed artifact is a warning, not a failed capture", async () => {
  const ctx = fakeCtx({ cdpThrows: { "Page.printToPDF": "PrintToPDF is not available" } });
  const result = await MonoCapture.pageCapture({ formats: ["mhtml", "pdf", "readable"] }, ctx);

  assert.deepEqual(names(result).sort(), ["page.mhtml", "readable.md"]);
  assert.match(result.warnings.join("\n"), /pdf skipped: PrintToPDF is not available/);
  assert.equal(ctx.calls.page.includes("restore"), true, "the page is still restored");
});

test("a page that will not attach still yields readable text", async () => {
  const result = await MonoCapture.pageCapture(
    { formats: ["mhtml", "readable"] },
    fakeCtx({ attachThrows: "Cannot access a chrome:// URL" })
  );
  assert.deepEqual(names(result), ["readable.md"]);
  assert.match(result.warnings.join("\n"), /debugger unavailable.*chrome:\/\//);
});

test("failed page preparation degrades the capture instead of ending it", async () => {
  const result = await MonoCapture.pageCapture(
    { formats: ["readable"] },
    fakeCtx({ pageThrows: { prepare: "frame detached" } })
  );
  assert.deepEqual(names(result), ["readable.md"]);
  assert.match(result.warnings.join("\n"), /page preparation skipped: frame detached/);
});

test("asking for a selection that is not there says so", async () => {
  const result = await MonoCapture.pageCapture({ formats: ["readable"], selection: true }, fakeCtx());
  assert.match(result.warnings.join("\n"), /selection requested but nothing was selected/);
});

test("an oversized artifact is dropped, named, and measured (TRU-04)", () => {
  const { kept, warnings } = MonoCapture.applySizeCaps(
    [
      { name: "readable.md", bytes: "x", rawBytes: 4 * 1024 },
      { name: "page.mhtml", bytes: "x", rawBytes: 31 * 1024 * 1024 },
    ],
    25 * 1024 * 1024
  );
  assert.deepEqual(kept.map((a) => a.name), ["readable.md"]);
  assert.deepEqual(warnings, ["page.mhtml skipped: 31.0MB exceeds the 25.0MB artifact cap"]);
});

test("a capture that fits is exactly one message", () => {
  const meta = { url: "https://x.test/a" };
  const messages = MonoCapture.planMessages(
    "cmd-1",
    meta,
    [{ name: "readable.md", encoding: "base64", bytes: b64("hello"), rawBytes: 5 }],
    [],
    {}
  );

  assert.equal(messages.length, 1);
  assert.deepEqual(messages[0].id, "cmd-1");
  assert.equal(messages[0].success, true);
  assert.equal(messages[0].data.final, true);
  assert.equal(messages[0].data.artifacts[0].bytes, b64("hello"));
  assert.equal(messages[0].data.artifacts[0].chunked, undefined);
});

test("an artifact too big for one frame is chunked, and the envelope comes last", () => {
  const big = "A".repeat(300 * 1024);
  const messages = MonoCapture.planMessages(
    "cmd-2",
    { url: "https://x.test/a" },
    [
      { name: "readable.md", encoding: "base64", bytes: b64("small"), rawBytes: 5 },
      { name: "page.mhtml", encoding: "base64", bytes: big, rawBytes: 225 * 1024 },
    ],
    ["pdf skipped: too large"],
    { maxMessageBytes: 64 * 1024 }
  );

  const envelope = messages[messages.length - 1];
  const chunks = messages.slice(0, -1);
  assert.ok(chunks.length >= 4, `expected several chunks, got ${chunks.length}`);

  chunks.forEach((msg, i) => {
    assert.equal(msg.id, "cmd-2");
    assert.equal(msg.success, true);
    assert.equal(msg.data.chunk.of, "page.mhtml");
    assert.equal(msg.data.chunk.index, i);
    assert.equal(msg.data.chunk.total, chunks.length);
    assert.equal(msg.data.final, undefined, "a chunk never looks like the terminal message");
  });

  // The receiver's job, done here to prove the protocol closes.
  assert.equal(chunks.map((m) => m.data.bytes).join(""), big);

  assert.equal(envelope.data.final, true);
  const listed = envelope.data.artifacts.find((a) => a.name === "page.mhtml");
  assert.deepEqual(listed, { name: "page.mhtml", encoding: "base64", rawBytes: 225 * 1024, chunked: true, chunks: chunks.length });
  assert.equal(envelope.data.artifacts.find((a) => a.name === "readable.md").bytes, b64("small"));
  assert.deepEqual(envelope.data.warnings, ["pdf skipped: too large"]);
});

test("a frame budget that arrives as undefined falls back to the default, not to NaN", () => {
  // capture_bridge.js builds its options object as {type, maxMessageBytes:
  // deps.maxMessageBytes}. When the install left that out, the property is
  // still there — its value is just undefined — and Object.assign copies an
  // explicit undefined straight over a default. Everything downstream is
  // then arithmetic on NaN: no comparison is ever true, the chunk loop runs
  // zero times, and the envelope lists artifacts with no bytes anywhere.
  for (const bad of [undefined, null, NaN, 0, "", "lots"]) {
    const messages = MonoCapture.planMessages(
      "cmd-0",
      { url: "https://x.test/a" },
      [{ name: "readable.md", encoding: "base64", bytes: b64("hello"), rawBytes: 5 }],
      [],
      { type: null, maxMessageBytes: bad }
    );
    const label = `maxMessageBytes: ${String(bad)}`;
    assert.equal(messages.length, 1, `${label}: one envelope, no chunk frames`);
    assert.equal(messages[0].data.artifacts[0].bytes, b64("hello"), `${label}: the artifact carries its bytes`);
    assert.equal(messages[0].data.artifacts[0].chunked, undefined, `${label}: nothing was chunked`);
  }
});

test("an artifact is never listed as chunked without its chunks", () => {
  // The invariant the Go receiver enforces (internal/capture/chunks.go), and
  // the one every capture broke: an envelope either carries the bytes or
  // names the frames that do.
  const artifacts = [
    { name: "readable.md", encoding: "base64", bytes: b64("hello"), rawBytes: 5 },
    { name: "page.mhtml", encoding: "base64", bytes: "Z".repeat(300 * 1024), rawBytes: 225 * 1024 },
  ];
  for (const opts of [{}, { maxMessageBytes: undefined }, { maxMessageBytes: 64 * 1024 }]) {
    const messages = MonoCapture.planMessages("cmd-x", {}, artifacts, [], opts);
    const envelope = messages[messages.length - 1];
    const frames = messages.slice(0, -1);
    for (const listed of envelope.data.artifacts) {
      const mine = frames.filter((m) => m.data.chunk.of === listed.name);
      if (listed.chunked) {
        assert.equal(mine.length, listed.chunks, `${listed.name}: chunk count matches the frames sent`);
        assert.ok(mine.length > 0, `${listed.name}: listed as chunked but no chunks arrived`);
        assert.equal(mine.map((m) => m.data.bytes).join(""), artifacts.find((a) => a.name === listed.name).bytes);
      } else {
        assert.ok(listed.bytes, `${listed.name}: inline artifacts carry bytes`);
      }
    }
  }
});

test("an extension-initiated push is tagged so the bridge can route it", () => {
  const messages = MonoCapture.planMessages("ext-1", {}, [], [], { type: "capture_push" });
  assert.equal(messages[0].type, "capture_push");
  assert.equal(messages[0].data.final, true);
});

test("a capture taken with the bridge down is queued, then flushed in order", async () => {
  const store = {};
  const storage = {
    get: async (key) => ({ [key]: store[key] }),
    set: async (update) => Object.assign(store, update),
  };

  assert.deepEqual(await MonoCapture.queueCapture(storage, { id: "a" }), { queued: true, depth: 1 });
  assert.deepEqual(await MonoCapture.queueCapture(storage, { id: "b" }), { queued: true, depth: 2 });

  const sent = [];
  assert.deepEqual(await MonoCapture.flushQueue(storage, (e) => sent.push(e.id)), { flushed: 2 });
  assert.deepEqual(sent, ["a", "b"]);
  assert.deepEqual(await MonoCapture.flushQueue(storage, () => {}), { flushed: 0 }, "the queue is drained, not replayed");
});

test("a flush stops at the first frame that does not reach the wire", async () => {
  const store = {};
  const storage = {
    get: async (key) => ({ [key]: store[key] }),
    set: async (update) => Object.assign(store, update),
  };
  await MonoCapture.queueCapture(storage, { id: "a" });
  await MonoCapture.queueCapture(storage, { id: "b" });
  await MonoCapture.queueCapture(storage, { id: "c" });

  const sent = [];
  const result = await MonoCapture.flushQueue(storage, (envelope) => {
    if (sent.length >= 1) throw new Error("the bridge disconnected mid-send");
    sent.push(envelope.id);
  });

  assert.equal(result.flushed, 1);
  assert.deepEqual(sent, ["a"]);
  // Emptying the queue before sending means this recovery can never run: the
  // entries are already gone by the time the send fails.
  assert.deepEqual(store.captureQueue.map((e) => e.envelope.id), ["b", "c"]);
});

test("the queue's size cap counts bytes on the wire, not UTF-16 units", async () => {
  const storage = { get: async () => ({}), set: async () => {} };
  // 2M CJK characters: 2M UTF-16 units — comfortably under the 4MB cap by
  // `.length` — and 6MB of UTF-8, which is what chrome.storage.local's
  // byte-counted bucket actually sees.
  const envelope = { id: "cjk", meta: {}, artifacts: [{ name: "readable.md", bytes: "\u6f22".repeat(2 * 1024 * 1024) }] };
  const result = await MonoCapture.queueCapture(storage, envelope);
  assert.equal(result.queued, false, "6MB is over the 4MB queue cap however you spell it");
  assert.match(result.reason, /too large to queue offline/);
});

test("a capture too big for the storage bucket is refused rather than half-written", async () => {
  const storage = { get: async () => ({}), set: async () => { throw new Error("QUOTA_BYTES exceeded"); } };
  const huge = { id: "x", data: { bytes: "A".repeat(5 * 1024 * 1024) } };
  const result = await MonoCapture.queueCapture(storage, huge);
  assert.equal(result.queued, false);
  assert.match(result.reason, /too large to queue offline/);
});
