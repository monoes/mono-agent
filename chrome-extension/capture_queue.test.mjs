// Tests for CLIP-08's UI half: what the popup lists, what the badge says,
// and the promise that nothing captured is dropped without someone saying
// so. `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoCaptureQueue: Queue, MonoCapture } = loadExtensionScripts([
  "capture_meta.js",
  "capture.js",
  "capture_queue.js",
]);

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (key) => (key in data ? { [key]: data[key] } : {}),
    set: async (values) => Object.assign(data, values),
  };
}

/** fakeBridge is the WebSocket half: connected or not, and a record of what went out. */
function fakeBridge(connected = true, throws = null) {
  const sent = [];
  return {
    sent,
    isConnected: () => connected,
    send: (envelope) => {
      if (throws) throw new Error(throws);
      sent.push(envelope);
    },
    connect: () => (connected = true),
    disconnect: () => (connected = false),
  };
}

const envelope = (id, title, bytes = 10) => ({
  id,
  meta: { url: `https://paper.test/${id}`, title, tags: [] },
  artifacts: [{ name: "readable.md", encoding: "base64", bytes: "eA==", rawBytes: bytes }],
  warnings: [],
});

test("a connected bridge sends and queues nothing", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(true);

  const result = await Queue.deliver(bridge, storage, envelope("a", "Lighthouse"));

  assert.deepEqual(result, { sent: true, queued: false });
  assert.equal(bridge.sent.length, 1);
  assert.deepEqual((await Queue.pending(storage)).counts, { queued: 0, failed: 0 });
});

test("a disconnected bridge queues the capture and the popup can see it", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(false);

  await Queue.deliver(bridge, storage, envelope("a", "Lighthouse"));
  const { queued, counts } = await Queue.pending(storage);

  assert.deepEqual(counts, { queued: 1, failed: 0 });
  assert.equal(queued[0].title, "Lighthouse");
  assert.equal(queued[0].url, "https://paper.test/a");
  assert.equal(queued[0].status, "queued");
  assert.deepEqual(queued[0].artifacts, ["readable.md"]);
  assert.ok(queued[0].at, "a queued capture says when it was taken");
});

test("a capture too large to queue becomes a visible failure, never a silent drop", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(false);
  const huge = envelope("big", "A very heavy page");
  huge.artifacts[0].bytes = "x".repeat(6 * 1024 * 1024);

  const result = await Queue.deliver(bridge, storage, huge);
  const { failed, counts } = await Queue.pending(storage);

  assert.equal(result.queued, false);
  assert.equal(result.failed, true);
  assert.deepEqual(counts, { queued: 0, failed: 1 });
  assert.equal(failed[0].title, "A very heavy page");
  assert.match(failed[0].reason, /too large to queue/);
});

test("a socket that dies mid-write falls back to the queue", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(true, "socket closed");

  const result = await Queue.deliver(bridge, storage, envelope("a", "Lighthouse"));

  assert.equal(result.queued, true);
  assert.deepEqual((await Queue.pending(storage)).counts, { queued: 1, failed: 0 });
});

test("retry-now sends one capture and removes only that one", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(false);
  await Queue.deliver(bridge, storage, envelope("a", "First"));
  await Queue.deliver(bridge, storage, envelope("b", "Second"));

  bridge.connect();
  const result = await Queue.retry(bridge, storage, "a");

  assert.deepEqual(result, { ok: true, title: "First" });
  assert.equal(bridge.sent.length, 1);
  assert.equal(bridge.sent[0].id, "a");
  const { queued } = await Queue.pending(storage);
  assert.deepEqual(queued.map((q) => q.title), ["Second"]);
});

test("a retry that fails keeps the capture and says why", async () => {
  const storage = fakeStorage();
  await Queue.deliver(fakeBridge(false), storage, envelope("a", "First"));

  const offline = await Queue.retry(fakeBridge(false), storage, "a");
  assert.equal(offline.ok, false);
  assert.match(offline.reason, /still disconnected/);
  assert.equal((await Queue.pending(storage)).counts.queued, 1);

  const broken = await Queue.retry(fakeBridge(true, "write failed"), storage, "a");
  assert.equal(broken.ok, false);
  assert.equal(broken.reason, "write failed");

  const { queued } = await Queue.pending(storage);
  assert.equal(queued.length, 1, "a failed retry never loses the capture");
  assert.equal(queued[0].reason, "write failed");
});

test("delete is the only way a capture leaves unsent", async () => {
  const storage = fakeStorage();
  const bridge = fakeBridge(false);
  await Queue.deliver(bridge, storage, envelope("a", "First"));
  await Queue.deliver(bridge, storage, envelope("b", "Second"));

  assert.deepEqual(await Queue.remove(storage, "a"), { ok: true });
  const { queued } = await Queue.pending(storage);
  assert.deepEqual(queued.map((q) => q.title), ["Second"]);

  const gone = await Queue.remove(storage, "a");
  assert.equal(gone.ok, false);
  assert.match(gone.reason, /no longer pending/);
});

test("a failure can be retried, and dismissed when it cannot", async () => {
  const storage = fakeStorage();
  await Queue.recordFailure(storage, envelope("a", "Retryable"), "bridge refused it");
  await Queue.recordFailure(storage, envelope("b", "Gone"), "too large", { keepEnvelope: false });

  const bridge = fakeBridge(true);
  assert.equal((await Queue.retry(bridge, storage, "a")).ok, true);

  const hopeless = await Queue.retry(bridge, storage, "b");
  assert.equal(hopeless.ok, false);
  assert.match(hopeless.reason, /cannot be resent/);

  assert.deepEqual(await Queue.clearFailures(storage), { ok: true, cleared: 1 });
  assert.deepEqual((await Queue.pending(storage)).counts, { queued: 0, failed: 0 });
});

test("the failure list is bounded so storage cannot fill", async () => {
  const storage = fakeStorage();
  for (let i = 0; i < Queue.MAX_FAILURES + 5; i++) {
    await Queue.recordFailure(storage, envelope(`e${i}`, `Page ${i}`), "nope");
  }
  const { failed } = await Queue.pending(storage);
  assert.equal(failed.length, Queue.MAX_FAILURES);
  assert.equal(failed[failed.length - 1].title, `Page ${Queue.MAX_FAILURES + 4}`);
});

test("the badge counts what needs attention, failures first", () => {
  assert.equal(Queue.badgeFor({ queued: 0, failed: 0 }).text, "");
  assert.equal(Queue.badgeFor({ queued: 3, failed: 0 }).text, "3");
  assert.equal(Queue.badgeFor({ queued: 3, failed: 0 }).color, "#c98a00");
  assert.match(Queue.badgeFor({ queued: 1, failed: 0 }).title, /1 capture waiting/);

  const failed = Queue.badgeFor({ queued: 3, failed: 2 });
  assert.equal(failed.text, "2");
  assert.equal(failed.color, "#c0392b");
  assert.match(failed.title, /2 captures failed/);

  assert.equal(Queue.badgeFor({ queued: 400, failed: 0 }).text, "99+");
});

test("the queue capture.js wrote is the queue the popup reads", async () => {
  // capture.js owns the write path; this file owns the read path. They
  // agree on the key and on the entry shape, or CLIP-08 is broken in a way
  // no test in either file would catch alone.
  const storage = fakeStorage();
  await MonoCapture.queueCapture(storage, envelope("a", "Written by capture.js"));

  const { queued } = await Queue.pending(storage);
  assert.equal(queued.length, 1);
  assert.equal(queued[0].title, "Written by capture.js");
  assert.equal(queued[0].key, "a");
});
