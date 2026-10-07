// The task outbox (task board spec 11.3): queued first, sent later, never
// lost to two writes racing or to a storage read that failed, and bounded.
// `CHROME_PATH=/nonexistent node --test chrome-extension/task_outbox.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoTaskOutbox: O } = loadExtensionScripts(["task_outbox.js"]);

/** storage has chrome.storage.local's shape, and yields between a read and a write as the real one does. */
function storage(seed = {}) {
  const data = JSON.parse(JSON.stringify(seed));
  const tick = () => new Promise((r) => setTimeout(r, 1));
  return {
    data,
    failGet: null,
    failSet: null,
    async get(keys) {
      await tick();
      if (this.failGet) throw new Error(this.failGet);
      return Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, JSON.parse(JSON.stringify(data[k]))]));
    },
    async set(values) {
      await tick();
      if (this.failSet) throw new Error(this.failSet);
      Object.assign(data, JSON.parse(JSON.stringify(values)));
    },
  };
}

const entry = (id, extra) =>
  Object.assign({ client_id: id, text: `task ${id}`, url: "", title: "", kind: "note", profile: "p-work", at: "2026-10-06T10:00:00.000Z" }, extra || {});

test("a task stays queued until it is removed by its client id, oldest first", async () => {
  const s = storage();
  const box = O.create(s);
  assert.deepEqual(await box.enqueue(entry("t-1")), { queued: true, size: 1 });
  await box.enqueue(entry("t-2"));
  assert.deepEqual((await box.list()).map((e) => e.client_id), ["t-1", "t-2"]);
  assert.equal(await box.remove("t-1"), true);
  assert.equal(await box.remove("t-1"), false, "a second removal finds nothing");
  assert.deepEqual((await box.list()).map((e) => e.client_id), ["t-2"]);
  assert.equal(await O.count(s), 1);
});

test("adds and removals a moment apart all land", async () => {
  const s = storage({ [O.KEY]: [entry("t-old")] });
  const box = O.create(s);
  await Promise.all([box.enqueue(entry("t-a")), box.remove("t-old"), box.enqueue(entry("t-b")), box.enqueue(entry("t-c"))]);
  assert.deepEqual((await box.list()).map((e) => e.client_id), ["t-a", "t-b", "t-c"]);
});

test("a storage read that fails wipes nothing", async () => {
  const s = storage({ [O.KEY]: [entry("t-1"), entry("t-2")], [O.FAILED_KEY]: [] });
  const box = O.create(s);
  s.failGet = "IO error";
  const out = await box.enqueue(entry("t-3"));
  assert.deepEqual(out, { queued: false, reason: "could not read the waiting tasks: IO error" });
  await assert.rejects(box.remove("t-1"), /IO error/);
  await assert.rejects(box.fail(entry("t-1"), "gone"), /IO error/);
  assert.deepEqual(s.data[O.KEY].map((e) => e.client_id), ["t-1", "t-2"], "nothing was written over the waiting tasks");
  assert.deepEqual(s.data[O.FAILED_KEY], []);
  assert.equal(await O.count(s), 0, "the badge's count stays quiet: it must never fail a capture");
  s.failGet = null;
  assert.equal(await O.count(s), 2);
  assert.equal((await box.enqueue(entry("t-3"))).queued, true, "the chain goes on after a failure");
});

test("200 tasks wait at most; the 201st is refused, not half-written", async () => {
  const s = storage({ [O.KEY]: Array.from({ length: O.MAX_ENTRIES - 1 }, (_, i) => entry(`t-${i}`)) });
  const box = O.create(s);
  assert.equal((await box.enqueue(entry("t-last"))).queued, true, "the 200th fits");
  const over = await box.enqueue(entry("t-over"));
  assert.equal(over.queued, false);
  assert.match(over.reason, /^200 tasks are already waiting to sync$/);
  assert.equal(await O.count(s), O.MAX_ENTRIES);
});

test("the outbox holds at most 1 MiB: exactly the limit fits, one byte more does not", async () => {
  const overhead = new TextEncoder().encode(JSON.stringify([entry("t-big", { text: "" })])).length;
  const exact = entry("t-big", { text: "a".repeat(O.MAX_BYTES - overhead) });
  assert.equal((await O.create(storage()).enqueue(exact)).queued, true);
  const over = entry("t-big", { text: "a".repeat(O.MAX_BYTES - overhead + 1) });
  const refused = await O.create(storage()).enqueue(over);
  assert.equal(refused.queued, false);
  assert.match(refused.reason, /fill the space kept for them/);
});

test("storage that cannot save says so, and nothing claims to be queued", async () => {
  const s = storage();
  s.failSet = "QUOTA_BYTES quota exceeded";
  const out = await O.create(s).enqueue(entry("t-1"));
  assert.deepEqual(out, { queued: false, reason: "could not save it: QUOTA_BYTES quota exceeded" });
});

test("a refused task leaves the outbox for a short failures list that keeps its text", async () => {
  const s = storage();
  const box = O.create(s);
  for (let i = 0; i < O.MAX_FAILED + 3; i++) {
    const e = entry(`t-${i}`, { text: `  Line ${i}\n  more` });
    await box.enqueue(e);
    await box.fail(e, 'unknown profile "gone"');
  }
  assert.equal(await O.count(s), 0, "a refused task is not retried");
  const failures = await box.failures();
  assert.equal(failures.length, O.MAX_FAILED);
  const last = failures.at(-1);
  assert.deepEqual([last.title, last.text, last.reason, last.profile], [`Line ${O.MAX_FAILED + 2}`, `  Line ${O.MAX_FAILED + 2}\n  more`, 'unknown profile "gone"', "p-work"]);
  assert.equal(await O.failedCount(s), O.MAX_FAILED);
  await box.fail(entry("t-long", { text: "x".repeat(5000) }), "gone");
  assert.equal((await box.failures()).at(-1).text.length, O.FAILED_TEXT_BYTES, "2 KiB of a long text is kept to copy");
  await box.dismiss();
  assert.deepEqual(await box.failures(), []);
});

test("titleOf is a task's first line, short; a page task is named by its title or address", () => {
  assert.equal(O.titleOf({ text: "\n  Reply   to Sam \nmore" }), "Reply to Sam");
  assert.equal(O.titleOf({ text: "", title: "The page", url: "https://x.example/" }), "The page");
  assert.equal(O.titleOf({ text: "", title: "", url: "https://x.example/" }), "https://x.example/");
  assert.equal(O.titleOf({ text: "x".repeat(100) }), `${"x".repeat(77)}...`);
});

test("capBytes cuts by UTF-8 bytes, never inside a character", () => {
  const e = String.fromCodePoint(0xe9); // two bytes
  const smile = String.fromCodePoint(0x1f600); // four bytes
  assert.equal(O.capBytes("abc", 3), "abc", "exactly the limit is kept");
  assert.equal(O.capBytes("abcd", 3), "abc", "one over is cut");
  assert.equal(O.capBytes(`a${e}${e}`, 2), "a", "half a character is dropped, not mangled");
  assert.equal(O.capBytes(`a${e}${e}`, 3), `a${e}`);
  assert.equal(O.capBytes(smile, 3), "");
  assert.equal(O.capBytes(null, 3), "");
});
