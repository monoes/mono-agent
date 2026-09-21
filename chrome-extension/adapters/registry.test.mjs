// Tests for the per-site adapter registry (CLIP-10).
//
// The contract that matters most is negative: an adapter must never be able
// to make a page fail to capture. Declining, throwing, and returning rubbish
// all have to land in the same place — the generic readable pipeline, with a
// warning recorded.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "../test_helpers.mjs";

function fresh() {
  const g = loadExtensionScripts([
    "domlite.js",
    "markdown.js",
    "adapters/util.js",
    "adapters/registry.js",
  ]);
  return g.MonoAdapters;
}

const ctx = (over) => Object.assign({ url: "https://x.test/a", tree: { tag: "#root", attrs: {}, children: [] }, title: "T" }, over);

test("the first matching adapter wins and is named in the result", () => {
  const reg = fresh();
  reg.register({ name: "second", match: () => true, extract: () => ({ markdown: "# second" }) });
  reg.register({ name: "first", match: () => true, extract: () => ({ markdown: "# first" }) });

  const out = reg.run(ctx());
  assert.equal(out.adapter, "second");
  assert.equal(out.markdown, "# second");
  assert.equal(out.meta.adapter, "second");
});

test("a declining adapter falls through to the next one", () => {
  const reg = fresh();
  reg.register({ name: "picky", match: () => true, extract: () => null });
  reg.register({ name: "willing", match: () => true, extract: () => ({ markdown: "# ok" }) });

  const out = reg.run(ctx());
  assert.equal(out.adapter, "willing");
  assert.deepEqual(out.warnings, ["adapter picky declined this page"]);
});

test("a throwing adapter is a warning, never a failed capture", () => {
  const reg = fresh();
  reg.register({
    name: "broken",
    match: () => true,
    extract: () => {
      throw new Error("site changed its markup");
    },
  });

  const out = reg.run(ctx());
  assert.equal(out, null, "no adapter produced anything, so the generic pipeline runs");
  assert.deepEqual(reg.lastWarnings(), ["adapter broken failed: site changed its markup"]);
});

test("a throwing match() cannot take the capture down either", () => {
  const reg = fresh();
  reg.register({
    name: "explodes",
    match: () => {
      throw new Error("bad url");
    },
    extract: () => ({ markdown: "never" }),
  });
  reg.register({ name: "calm", match: () => true, extract: () => ({ markdown: "# calm" }) });

  const out = reg.run(ctx());
  assert.equal(out.adapter, "calm");
  assert.match(out.warnings[0], /adapter explodes failed to match: bad url/);
});

test("an empty or whitespace-only result is treated as a decline", () => {
  const reg = fresh();
  reg.register({ name: "hollow", match: () => true, extract: () => ({ markdown: "   \n  " }) });

  assert.equal(reg.run(ctx()), null);
  assert.deepEqual(reg.lastWarnings(), ["adapter hollow declined this page"]);
});

test("meta and artifacts come back normalized", () => {
  const reg = fresh();
  reg.register({
    name: "rich",
    match: () => true,
    extract: () => ({
      markdown: "# body",
      title: "A Title",
      meta: { resolvedPdfUrl: "https://x.test/a.pdf" },
      artifacts: [{ name: "items.csv", text: "a,b\n1,2\n" }],
      warnings: ["transcript unavailable"],
    }),
  });

  const out = reg.run(ctx());
  assert.equal(out.title, "A Title");
  assert.equal(out.meta.adapter, "rich");
  assert.equal(out.meta.resolvedPdfUrl, "https://x.test/a.pdf");
  assert.deepEqual(out.artifacts, [{ name: "items.csv", text: "a,b\n1,2\n" }]);
  assert.deepEqual(out.warnings, ["transcript unavailable"]);
});

test("artifacts with names the Go receiver would reject are dropped", () => {
  const reg = fresh();
  reg.register({
    name: "sloppy",
    match: () => true,
    extract: () => ({
      markdown: "# body",
      artifacts: [
        { name: "../escape.csv", text: "x" },
        { name: "meta.json", text: "{}" },
        { name: "items.json", text: "[]" },
      ],
    }),
  });

  const out = reg.run(ctx());
  assert.deepEqual(out.artifacts.map((a) => a.name), ["items.json"]);
  assert.equal(out.warnings.length, 2);
  assert.match(out.warnings[0], /rejected artifact name/);
});

test("a non-matching adapter is never asked to extract", () => {
  const reg = fresh();
  let asked = false;
  reg.register({
    name: "elsewhere",
    match: (c) => c.url.includes("other.test"),
    extract: () => {
      asked = true;
      return { markdown: "# no" };
    },
  });

  assert.equal(reg.run(ctx()), null);
  assert.equal(asked, false);
  assert.deepEqual(reg.lastWarnings(), []);
});

test("register rejects a malformed adapter rather than storing it", () => {
  const reg = fresh();
  assert.throws(() => reg.register({ name: "x", match: () => true }), /extract/);
  assert.throws(() => reg.register({ match: () => true, extract: () => null }), /name/);
  assert.equal(reg.all().length, 0);
});
