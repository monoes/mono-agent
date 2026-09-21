// RCL-04 — highlights as atomic notes: the model and the store.
//
// A highlight has to survive three trips: into storage and back when the page
// is revisited, into the capture envelope as `highlights.json`, and — on the
// other end — back onto the passage it was taken from. The first two are
// here; the third is pinned on the ingest side.
//
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoHighlights: H } = loadExtensionScripts(["highlights.js"]);

/** fakeStorage is chrome.storage.local's three methods, in a Map. */
function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (key) => (key in data ? { [key]: data[key] } : {}),
    set: async (values) => Object.assign(data, values),
    remove: async (key) => delete data[key],
  };
}

const URL_A = "https://example.com/post";

const selection = (text, extra = {}) =>
  Object.assign(
    {
      text,
      anchor: { startChar: 3200, endChar: 3200 + text.length, prefix: "…bench, ", suffix: " Then re-check." },
      comment: "",
    },
    extra
  );

test("a highlight carries its text, a resolvable anchor and a timestamp", () => {
  const h = H.makeHighlight(selection("torque the sprocket to 9 Nm", { now: 1758448800000 }));

  assert.match(h.id, /^hl-/);
  assert.equal(h.text, "torque the sprocket to 9 Nm");
  assert.equal(h.anchor.quote, h.text, "the quote is what the ingest side re-locates by");
  assert.equal(h.anchor.startChar, 3200);
  assert.equal(h.anchor.endChar, 3227);
  assert.equal(h.anchor.prefix, "…bench,");
  assert.match(h.anchor.fragment, /^:~:text=/);
  assert.equal(h.createdAt, "2025-09-21T10:00:00.000Z");
  assert.equal(h.color, "yellow");
});

test("the text fragment is built the way citation.ts builds one", () => {
  // Short passage: the whole thing, one part.
  assert.equal(H.textFragment("nine Nm exactly"), ":~:text=nine%20Nm%20exactly");

  // `-` and `,` are text-fragment syntax and must not be left raw, or the
  // link silently means something else.
  assert.equal(H.textFragment("well-known, really"), ":~:text=well%2Dknown%2C%20really");

  // Long passage: a start,end pair rather than the whole paragraph, so it
  // survives the site re-flowing its markup.
  const long = Array.from({ length: 30 }, (_, i) => `word${i}`).join(" ");
  const fragment = H.textFragment(long);
  // The separating `,` is literal syntax — only commas INSIDE a part are
  // escaped, which is exactly what citation.ts emits.
  const [start, end] = fragment.replace(":~:text=", "").split(",");
  assert.equal(start, "word0%20word1%20word2%20word3%20word4%20word5%20word6%20word7");
  assert.equal(end, "word22%20word23%20word24%20word25%20word26%20word27%20word28%20word29");

  assert.equal(H.textFragment("   "), "", "nothing to point at is no fragment at all");
});

test("a highlight's own URL lands on the passage", () => {
  const url = H.highlightUrl("https://example.com/post#already-here", "nine Nm exactly");
  assert.equal(url, "https://example.com/post#:~:text=nine%20Nm%20exactly");
});

test("an empty selection is not a highlight", () => {
  assert.equal(H.makeHighlight({ text: "   " }), null);
  assert.equal(H.makeHighlight({}), null);
  assert.equal(H.makeHighlight(null), null);
});

test("whitespace in a selection is collapsed", () => {
  // A DOM selection across elements is full of newlines and indentation
  // that were never words on the page.
  const h = H.makeHighlight({ text: "  torque\n\n  the   sprocket  " });
  assert.equal(h.text, "torque the sprocket");
});

test("highlights round-trip through storage", async () => {
  const storage = fakeStorage();

  await H.add(storage, URL_A, selection("torque the sprocket to 9 Nm"));
  await H.add(storage, URL_A, selection("the tolerance band is plus or minus 0.2 Nm"));

  const records = await H.load(storage, URL_A);
  assert.equal(records.length, 2);
  assert.equal(records[0].text, "torque the sprocket to 9 Nm");
  assert.ok(storage.data[H.key(URL_A)], "stored under the identity URL");
});

test("the fragment is not part of the storage key", async () => {
  const storage = fakeStorage();

  await H.add(storage, "https://example.com/post#intro", selection("first"));
  // Revisiting the same page at a different scroll position must find it.
  const records = await H.load(storage, "https://example.com/post#conclusion");
  assert.equal(records.length, 1);
});

test("highlighting the same passage twice updates it rather than duplicating", async () => {
  const storage = fakeStorage();

  await H.add(storage, URL_A, selection("torque the sprocket to 9 Nm"));
  const { records } = await H.add(
    storage,
    URL_A,
    selection("torque the sprocket to 9 Nm", { comment: "check on the bench", color: "green" })
  );

  assert.equal(records.length, 1);
  assert.equal(records[0].comment, "check on the bench");
  assert.equal(records[0].color, "green");
});

test("a comment can be added after the fact", async () => {
  const storage = fakeStorage();
  const { record } = await H.add(storage, URL_A, selection("torque the sprocket to 9 Nm"));

  const { record: updated } = await H.update(storage, URL_A, record.id, {
    comment: "this is the number",
  });
  assert.equal(updated.comment, "this is the number");
  assert.equal(updated.id, record.id, "the note keeps its identity");

  const reloaded = await H.load(storage, URL_A);
  assert.equal(reloaded[0].comment, "this is the number");
});

test("updating a highlight that is gone is a no-op, not a crash", async () => {
  const storage = fakeStorage();
  const { record } = await H.update(storage, URL_A, "hl-nope", { comment: "x" });
  assert.equal(record, null);
});

test("removing the last highlight clears the page's key", async () => {
  const storage = fakeStorage();
  const { record } = await H.add(storage, URL_A, selection("torque the sprocket"));

  const { removed } = await H.remove(storage, URL_A, record.id);
  assert.equal(removed, true);
  assert.equal(storage.data[H.key(URL_A)], undefined);
  assert.deepEqual(await H.load(storage, URL_A), []);
});

test("a half-written record out of storage is dropped, not thrown over", async () => {
  const storage = fakeStorage({
    [H.key(URL_A)]: [
      { id: "hl-1", text: "a real one", anchor: { startChar: 0, endChar: 10 }, createdAt: "2026-09-21T10:00:00.000Z" },
      { id: "hl-2" }, // no text: the worker was suspended mid-write
      null,
      "not even an object",
      { id: "hl-1", text: "a duplicate id" },
    ],
  });

  const records = await H.load(storage, URL_A);
  assert.equal(records.length, 1);
  assert.equal(records[0].text, "a real one");
});

test("a record from an older shape is normalized rather than rejected", async () => {
  const storage = fakeStorage({
    [H.key(URL_A)]: [{ id: "hl-old", text: "no anchor at all" }],
  });

  const [record] = await H.load(storage, URL_A);
  assert.equal(record.text, "no anchor at all");
  assert.equal(record.anchor.quote, "no anchor at all");
  assert.equal(record.anchor.startChar, null, "an absent offset is null, never a guess");
  assert.match(record.anchor.fragment, /^:~:text=/);
});

test("storage that is unavailable reads as an empty page", async () => {
  const broken = {
    get: async () => {
      throw new Error("storage is gone");
    },
    set: async () => {
      throw new Error("storage is gone");
    },
  };
  assert.deepEqual(await H.load(broken, URL_A), []);
  // And a save against it does not throw either: the page still renders.
  await H.save(broken, URL_A, [H.makeHighlight(selection("x"))]);
});

test("the store is bounded per page", async () => {
  const storage = fakeStorage();
  const many = Array.from({ length: H.MAX_PER_PAGE + 40 }, (_, i) =>
    H.makeHighlight({ text: `highlight number ${i}` })
  );

  const kept = await H.save(storage, URL_A, many);
  assert.equal(kept.length, H.MAX_PER_PAGE);
});

test("the index lists highlighted pages, newest first, and is bounded", async () => {
  const storage = fakeStorage();

  await H.add(storage, "https://a.test/one", selection("first"));
  await H.add(storage, "https://b.test/two", selection("second"));

  const pages = await H.listPages(storage);
  assert.equal(pages.length, 2);
  assert.equal(pages[0].count, 1);

  // A page whose highlights are all removed leaves the index.
  const records = await H.load(storage, "https://a.test/one");
  await H.remove(storage, "https://a.test/one", records[0].id);
  const after = await H.listPages(storage);
  assert.deepEqual(
    after.map((p) => p.url),
    ["https://b.test/two"]
  );
});

test("evicting a page from the index takes its highlights with it", async () => {
  const storage = fakeStorage();
  // Seed an index that is already full, with one old entry to push out.
  const index = {};
  for (let i = 0; i < H.MAX_PAGES; i++) {
    index[`https://old.test/${i}`] = { count: 1, at: `2020-01-01T00:00:${String(i).padStart(2, "0")}.000Z` };
  }
  storage.data[H.INDEX_KEY] = index;
  storage.data[H.STORAGE_PREFIX + "https://old.test/0"] = [{ id: "hl-x", text: "stale" }];

  await H.add(storage, "https://new.test/page", selection("fresh"));

  assert.equal(
    storage.data[H.STORAGE_PREFIX + "https://old.test/0"],
    undefined,
    "an evicted page's bytes must go too, or nothing can ever find them again"
  );
});

// ── The capture artifact ───────────────────────────────────────────

test("highlights become a highlights.json artifact", async () => {
  const storage = fakeStorage();
  await H.add(storage, URL_A, selection("torque the sprocket to 9 Nm", { comment: "the number" }));
  const records = await H.load(storage, URL_A);

  const artifact = H.artifact(URL_A, records);
  assert.equal(artifact.name, "highlights.json");
  assert.equal(artifact.encoding, "base64");

  const doc = JSON.parse(Buffer.from(artifact.bytes, "base64").toString("utf8"));
  assert.equal(doc.version, 1);
  assert.equal(doc.url, URL_A);
  assert.equal(doc.highlights.length, 1);

  const h = doc.highlights[0];
  assert.equal(h.text, "torque the sprocket to 9 Nm");
  assert.equal(h.comment, "the number");
  assert.equal(h.anchor.quote, h.text);
  assert.ok(h.createdAt);
  // Each highlight carries the link that lands on it, so a note can point
  // back at the live page and not only at the archive.
  assert.match(h.url, /#:~:text=/);
});

test("a page with no highlights produces no artifact", () => {
  assert.equal(H.artifact(URL_A, []), null);
  assert.equal(H.artifact(URL_A, [{ id: "x" }]), null, "and not one made of junk either");
});

test("the artifact name passes the envelope's charset whitelist", () => {
  // Mirrors capture.ValidArtifactName in internal/capture/envelope.go: no
  // separators, no leading dot, nothing outside [A-Za-z0-9._-].
  assert.match(H.ARTIFACT_NAME, /^[A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9]$/);
  assert.ok(H.ARTIFACT_NAME.length <= 64);
  assert.notEqual(H.ARTIFACT_NAME, "meta.json");
});

// ── Finding a highlight again ──────────────────────────────────────
//
// A highlight outlives the visit that made it, so every page it is restored
// onto has changed. These are the ways it changes, and what each one costs.

const PAGE = [
  "Sprocket Calibration",
  "Torque the sprocket to 9 Nm on the bench, then re-check.",
  "The tolerance band is plus or minus 0.2 Nm across the whole range.",
  "See below for the full procedure.",
  "Procedure",
  "See below for the full procedure.",
].join("\n");

test("locate measures an anchor and its neighbourhood", () => {
  const anchor = H.locate(PAGE, "9 Nm on the bench");

  assert.equal(PAGE.slice(anchor.startChar, anchor.endChar), "9 Nm on the bench");
  assert.match(anchor.prefix, /Torque the sprocket to$/);
  assert.match(anchor.suffix, /^, then re-check/);
});

test("locate picks the occurrence nearest the hint when a phrase repeats", () => {
  const second = PAGE.lastIndexOf("See below for the full procedure.");
  const anchor = H.locate(PAGE, "See below for the full procedure.", second - 2);

  assert.equal(anchor.startChar, second, "the one the reader actually highlighted");
});

test("an unchanged page is relocated straight off the offsets", () => {
  const anchor = H.locate(PAGE, "9 Nm on the bench");
  assert.equal(H.relocate(PAGE, Object.assign({ quote: "9 Nm on the bench" }, anchor)), anchor.startChar);
});

test("text inserted above a highlight does not lose it", () => {
  const anchor = H.locate(PAGE, "9 Nm on the bench");
  const stored = Object.assign({ quote: "9 Nm on the bench" }, anchor);

  // A cookie banner, an ad, a "related posts" strip: everything shifts.
  const shifted = `We use cookies. Accept all? Manage preferences.\n\n${PAGE}`;
  const at = H.relocate(shifted, stored);

  assert.notEqual(at, -1);
  assert.equal(shifted.slice(at, at + stored.quote.length), stored.quote);
  assert.notEqual(at, stored.startChar, "the offsets alone would have been wrong");
});

test("a reworded neighbourhood still finds the passage itself", () => {
  const stored = {
    quote: "9 Nm on the bench",
    startChar: 9999,
    prefix: "a sentence that no longer exists",
    suffix: "nor does this one",
  };
  const at = H.relocate(PAGE, stored);
  assert.equal(PAGE.slice(at, at + stored.quote.length), stored.quote);
});

test("a passage that is gone is not painted somewhere approximate", () => {
  assert.equal(
    H.relocate(PAGE, { quote: "torque it to 40 Nm", startChar: 20 }),
    -1,
    "a mark on the wrong sentence is a lie about what someone read"
  );
  assert.equal(H.relocate(PAGE, { quote: "" }), -1);
  assert.equal(H.relocate("", { quote: "anything" }), -1);
});

test("relocate survives an anchor with nothing useful in it", () => {
  assert.equal(H.relocate(PAGE, null), -1);
  assert.equal(H.relocate(PAGE, {}), -1);
  // Offsets past the end of the text must not throw.
  const at = H.relocate(PAGE, { quote: "Procedure", startChar: 10_000_000 });
  assert.equal(PAGE.slice(at, at + 9), "Procedure");
});
