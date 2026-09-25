// Tests for recorder_outbox.js on its own: chunked persistence across a
// reload, the move from the old single-blob layout, and purge.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoRecorderOutbox: O } = loadExtensionScripts(["recorder_outbox.js"]);

function store(seed = {}) {
  const data = JSON.parse(JSON.stringify(seed));
  return {
    data,
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, JSON.parse(JSON.stringify(data[k]))])),
    set: async (v) => Object.assign(data, JSON.parse(JSON.stringify(v))),
    remove: async (keys) => [].concat(keys).forEach((k) => delete data[k]),
  };
}

const frame = (i, op = "event", rec = "rec-a") => ({ kind: "recording", id: `${rec}-f${i}`, op, recordingId: rec });
const offline = { send: () => false, isConnected: () => false };

test("a reloaded outbox has the same frames in the same order", async () => {
  const storage = store();
  const a = O.createOutbox(Object.assign({ storage }, offline));
  for (let i = 1; i <= 60; i++) a.push(frame(i));
  a.ack("rec-a-f1");
  await a.saved();

  const b = O.createOutbox(Object.assign({ storage }, offline));
  assert.equal(await b.load(), 59);
  assert.deepEqual(b.frames().map((f) => f.id).slice(0, 2), ["rec-a-f2", "rec-a-f3"]);
  assert.equal(b.frames().at(-1).id, "rec-a-f60");
});

test("the old single-key outbox is moved into chunks once", async () => {
  const storage = store({ recordingOutbox: [frame(1, "start"), frame(2)] });
  const box = O.createOutbox(Object.assign({ storage }, offline));
  assert.equal(await box.load(), 2);
  await box.saved();
  assert.equal(storage.data.recordingOutbox, undefined);
  assert.ok(storage.data[O.META_KEY]);
});

test("purge removes one recording's frames and, when empty, every stored key", async () => {
  const storage = store();
  const box = O.createOutbox(Object.assign({ storage }, offline));
  for (let i = 1; i <= 30; i++) box.push(frame(i));
  box.push(frame(1, "start", "rec-b"));
  box.purge("rec-a");
  assert.deepEqual(box.frames().map((f) => f.recordingId), ["rec-b"]);
  box.purge("rec-b");
  await box.saved();
  assert.deepEqual(Object.keys(storage.data).filter((k) => k.startsWith(O.CHUNK_PREFIX)), []);
  assert.equal(box.size(), 0);
});

test("resend stops at the first refused send and remembers it owes one", () => {
  let up = true;
  const wire = [];
  const box = O.createOutbox({
    storage: store(),
    isConnected: () => up,
    send: (f) => (up ? (wire.push(f.id), true) : false),
  });
  box.push(frame(1));
  up = false;
  box.push(frame(2));
  up = true;
  box.push(frame(3));
  assert.deepEqual(wire, ["rec-a-f1"], "f3 waits behind f2");
  assert.equal(box.resend(), 3);
  assert.deepEqual(wire, ["rec-a-f1", "rec-a-f1", "rec-a-f2", "rec-a-f3"]);
});
