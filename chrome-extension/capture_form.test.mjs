// Tests for CLIP-07: the note, the tags and the collection a capture is
// saved with. `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoCaptureForm: Form } = loadExtensionScripts(["capture_form.js"]);

/** fakeStorage is chrome.storage.local, minus Chrome. */
function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const wanted = Array.isArray(keys) ? keys : [keys];
      const out = {};
      for (const k of wanted) if (k in data) out[k] = data[k];
      return out;
    },
    set: async (values) => Object.assign(data, values),
  };
}

test("tags are typed however people type them", () => {
  assert.deepEqual(Form.parseTags("ml, deep learning"), ["ml", "deep", "learning"]);
  assert.deepEqual(Form.parseTags("#ml #papers"), ["ml", "papers"]);
  assert.deepEqual(Form.parseTags("  ml ,, papers ,  "), ["ml", "papers"]);
  assert.deepEqual(Form.parseTags(["ml", "papers"]), ["ml", "papers"]);
  assert.deepEqual(Form.parseTags(""), []);
  assert.deepEqual(Form.parseTags(null), []);
});

test("two spellings of one tag are one tag", () => {
  assert.deepEqual(Form.parseTags("ML ml Ml"), ["ML"]);
});

test("the tag list is bounded", () => {
  const many = Array.from({ length: 40 }, (_, i) => `t${i}`).join(" ");
  assert.equal(Form.parseTags(many).length, Form.MAX_TAGS);
  assert.equal(Form.parseTags("x".repeat(200))[0].length, 48);
});

test("recent tags are most-recently-used first", () => {
  const recent = Form.rememberTags(["papers", "ml", "rust"], "rust, new");
  assert.deepEqual(recent, ["rust", "new", "papers", "ml"]);
  assert.equal(Form.rememberTags(recent, "", 3).length, 3);
});

test("suggestions prefer a prefix match and skip what is already chosen", () => {
  const recent = ["machine-learning", "ml", "papers", "html"];
  assert.deepEqual(Form.suggestTags(recent, "m", ""), ["machine-learning", "ml", "html"]);
  assert.deepEqual(Form.suggestTags(recent, "m", "ml"), ["machine-learning", "html"]);
  assert.deepEqual(Form.suggestTags(recent, "#pap", ""), ["papers"]);
  assert.deepEqual(Form.suggestTags(recent, "", "", 2), ["machine-learning", "ml"]);
  assert.deepEqual(Form.suggestTags([], "m", ""), []);
});

test("a note is one line, a collection is one name", () => {
  assert.equal(Form.normalizeNote("  why this\n matters  "), "why this matters");
  assert.equal(Form.normalizeNote("   "), null);
  assert.equal(Form.normalizeNote("x".repeat(900)).length, Form.MAX_NOTE_CHARS);
  assert.equal(Form.normalizeCollection("  Lighthouse \n reading "), "Lighthouse reading");
  assert.equal(Form.normalizeCollection(""), null);
});

test("an untouched form produces exactly what a shortcut capture sends", () => {
  assert.deepEqual(Form.toMeta({}), { note: null, tags: [], collection: null });
  assert.deepEqual(Form.toMeta({ note: "  ", tags: " , ", collection: "" }), {
    note: null,
    tags: [],
    collection: null,
  });
  assert.equal(Form.isEmpty({ note: " ", tags: "" }), true);
  assert.equal(Form.isEmpty({ tags: "ml" }), false);
});

test("the form fills the three envelope fields", () => {
  assert.deepEqual(Form.toMeta({ note: "for the talk", tags: "#ml, papers", collection: "Reading" }), {
    note: "for the talk",
    tags: ["ml", "papers"],
    collection: "Reading",
  });
});

test("applying the form to a finished capture only moves those three fields", () => {
  const meta = {
    url: "https://paper.test/x",
    title: "X",
    contentHash: "sha256:abc",
    tags: ["existing"],
    note: null,
    collection: null,
  };
  const after = Form.applyToMeta(meta, { note: "read on the train", tags: "ml, EXISTING" });

  assert.equal(after.note, "read on the train");
  assert.deepEqual(after.tags, ["existing", "ml"]);
  assert.equal(after.collection, null);
  assert.equal(after.contentHash, "sha256:abc", "the capture itself is untouched");
});

test("an empty form leaves a capture exactly as taken", () => {
  const meta = { title: "X", tags: [], note: null, collection: null };
  assert.deepEqual(Form.applyToMeta(meta, {}), { title: "X", tags: [], note: null, collection: null });
});

test("recall round-trips through storage", async () => {
  const storage = fakeStorage();
  assert.deepEqual(await Form.load(storage), { recentTags: [], collections: [] });

  await Form.remember(storage, { tags: "ml, papers", collection: "Reading" });
  await Form.remember(storage, { tags: "rust", collection: "Work" });

  const loaded = await Form.load(storage);
  assert.deepEqual(loaded.recentTags, ["rust", "ml", "papers"]);
  assert.deepEqual(loaded.collections, ["Work", "Reading"]);
});

test("a profile with no storage still lets the form work", async () => {
  const broken = {
    get: async () => {
      throw new Error("storage is gone");
    },
    set: async () => {
      throw new Error("storage is gone");
    },
  };
  assert.deepEqual(await Form.load(broken), { recentTags: [], collections: [] });
  assert.deepEqual(await Form.remember(broken, { tags: "ml" }), {
    recentTags: ["ml"],
    collections: [],
  });
});
