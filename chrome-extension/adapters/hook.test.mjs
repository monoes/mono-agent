// The hook that joins the adapters to the capture envelope (CLIP-10/12).
//
// The adapters themselves are tested from fixtures; what is tested here is
// the wiring — that an adapter's artifacts and warnings reach the envelope,
// that `meta.adapter` records which one ran, and that none of it is required
// for a capture to succeed.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "../test_helpers.mjs";

const { MonoCapture } = loadExtensionScripts(["capture_meta.js", "capture.js"]);

const b64 = (s) => Buffer.from(s, "utf8").toString("base64");
const decode = (a) => Buffer.from(a.bytes, "base64").toString("utf8");

function fakeCtx(extractAnswer) {
  const calls = { injected: [] };
  return {
    calls,
    resolveTabId: async () => 7,
    inject: async (tabId, files) => calls.injected.push(...files),
    callPage: async (tabId, fn) =>
      ({
        prepare: { scrollPasses: 1 },
        extract: extractAnswer,
        restore: { restored: true },
      })[fn],
    attach: async () => {},
    detach: async () => {},
    cdp: async (tabId, method) =>
      ({
        "Page.enable": {},
        "Page.getLayoutMetrics": { cssContentSize: { width: 800, height: 600 } },
        "Page.captureScreenshot": { data: b64("PNG") },
        "Page.captureSnapshot": { data: "From: <Snapshot>" },
      })[method],
  };
}

const baseAnswer = (over) =>
  Object.assign(
    {
      meta: {
        url: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
        canonicalUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
        title: "How a lighthouse keeper's ledger survived",
        capturedAt: "2026-09-21T10:00:00.000Z",
        contentHash: null,
        tags: [],
        source: "extension",
      },
      markdown: "# How a lighthouse keeper's ledger survived\n\n## Transcript\n\n[0:00](u&t=0s) The drawer.",
      text: "How a lighthouse keeper's ledger survived Transcript The drawer.",
      tables: [],
      artifacts: [],
      warnings: [],
      adapter: null,
      selectionRequested: false,
      selectionFound: false,
    },
    over
  );

test("the adapter's name is recorded in meta", async () => {
  const out = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx(baseAnswer({ adapter: "youtube" })));
  assert.equal(out.meta.adapter, "youtube");
  assert.match(decode(out.artifacts[0]), /## Transcript/);
});

test("an adapter's warnings reach the envelope", async () => {
  const answer = baseAnswer({
    adapter: "youtube",
    warnings: ["youtube: no transcript panel was open on this page"],
  });
  const out = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx(answer));

  assert.ok(out.warnings.some((w) => /no transcript panel/.test(w)));
});

test("extracted rows are packed as their own artifacts", async () => {
  const answer = baseAnswer({
    artifacts: [
      { name: "items.csv", text: "title,price\nBrass lamp,£42.00\n" },
      { name: "items.json", text: '[{"title":"Brass lamp"}]\n' },
    ],
  });
  const out = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx(answer));

  assert.deepEqual(out.artifacts.map((a) => a.name).sort(), ["items.csv", "items.json", "readable.md"]);
  const csv = out.artifacts.find((a) => a.name === "items.csv");
  assert.equal(decode(csv), "title,price\nBrass lamp,£42.00\n");
  assert.equal(csv.encoding, "base64");
  // rawBytes is what the size cap and the frame planner both budget against.
  assert.equal(csv.rawBytes, Buffer.byteLength("title,price\nBrass lamp,£42.00\n", "utf8"));
});

test("the content hash follows the adapter's text, so a re-capture versions correctly", async () => {
  const generic = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx(baseAnswer({})));
  const adapted = await MonoCapture.pageCapture(
    { formats: ["readable"] },
    fakeCtx(baseAnswer({ adapter: "youtube", text: "the transcript, now that the panel was open" }))
  );
  assert.notEqual(generic.meta.contentHash, adapted.meta.contentHash);
  assert.match(adapted.meta.contentHash, /^sha256:/);
});

test("a page half that knows nothing about adapters still captures", async () => {
  // The old shape: no `artifacts`, no `warnings`, no `adapter` key at all.
  const legacy = baseAnswer({});
  delete legacy.artifacts;
  delete legacy.warnings;
  delete legacy.adapter;

  const out = await MonoCapture.pageCapture({ formats: ["readable"] }, fakeCtx(legacy));
  assert.deepEqual(out.artifacts.map((a) => a.name), ["readable.md"]);
  assert.equal(out.meta.adapter, undefined);
  assert.deepEqual(out.warnings, []);
});

test("every adapter file is injected, registry first", async () => {
  const ctx = fakeCtx(baseAnswer({}));
  await MonoCapture.pageCapture({ formats: ["readable"] }, ctx);

  const injected = ctx.calls.injected;
  for (const file of ["util", "registry", "youtube", "arxiv", "twitter", "github", "chat", "pdf", "extract_items"]) {
    assert.ok(injected.includes(`adapters/${file}.js`), `adapters/${file}.js is injected`);
  }
  assert.ok(injected.indexOf("adapters/registry.js") < injected.indexOf("adapters/pdf.js"));
  assert.ok(injected.indexOf("adapters/util.js") < injected.indexOf("adapters/registry.js"));
  // pdf.js is the catch-all and must get last refusal, after every adapter
  // that can say something more specific about the page.
  for (const file of ["youtube", "arxiv", "twitter", "github", "chat"]) {
    assert.ok(injected.indexOf(`adapters/${file}.js`) < injected.indexOf("adapters/pdf.js"), `${file} precedes pdf`);
  }
});
