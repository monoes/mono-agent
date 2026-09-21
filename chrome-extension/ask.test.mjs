// The extension→Go request channel, from the extension's side.
//
// The promise this module hands a caller must always settle. Every test here
// is a way that could fail: the socket is down before the question is asked,
// it dies after, the backend never answers, or it answers something for a
// different question entirely.
//
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** fakeSocket records the frames the channel writes, and can be knocked over. */
function fakeSocket(connected = true) {
  const sent = [];
  let throws = null;
  let swallow = false;
  return {
    sent,
    isConnected: () => connected,
    send: (frame) => {
      if (throws) throw new Error(throws);
      if (swallow) return false;
      sent.push(frame);
      return true;
    },
    swallowSend: () => (swallow = true),
    disconnect: () => (connected = false),
    breakSend: (message) => (throws = message),
    last: () => sent[sent.length - 1],
  };
}

test("a question written to a socket that closed under it fails now, not in 25 seconds", async () => {
  const { MonoAsk, socket } = freshAsk();
  // isConnected() still says yes — that is the whole shape of the race — and
  // background.js's send reports false rather than throwing.
  socket.swallowSend();

  const err = await MonoAsk.request("doc.lookup", { url: "https://x.test" }).then(
    () => null,
    (e) => e
  );
  assert.ok(err, "the promise settles");
  assert.equal(err.code, MonoAsk.CODE_OFFLINE);
  assert.equal(MonoAsk.inFlight(), 0, "nothing is left in the table to time out later");
});

function freshAsk(connected = true) {
  const { MonoAsk } = loadExtensionScripts(["ask.js"]);
  const socket = fakeSocket(connected);
  MonoAsk.install(socket);
  return { MonoAsk, socket };
}

const reply = (id, extra) => Object.assign({ kind: "reply", id }, extra);

test("a request goes out as a request frame and resolves on its reply", async () => {
  const { MonoAsk, socket } = freshAsk();

  const promise = MonoAsk.request("doc.lookup", { url: "https://example.com/post" });
  const frame = socket.last();

  assert.equal(frame.kind, "request");
  assert.equal(frame.method, "doc.lookup");
  assert.deepEqual(frame.params, { url: "https://example.com/post" });
  assert.match(frame.id, /^req-/);

  assert.equal(MonoAsk.handleFrame(reply(frame.id, { ok: true, data: { saved: true } })), true);
  assert.deepEqual(await promise, { saved: true });
  assert.equal(MonoAsk.inFlight(), 0);
});

test("two questions in flight cannot take each other's answer", async () => {
  const { MonoAsk, socket } = freshAsk();

  const a = MonoAsk.request("doc.lookup", { url: "a" });
  const b = MonoAsk.request("doc.lookup", { url: "b" });
  const [idA, idB] = socket.sent.map((f) => f.id);
  assert.notEqual(idA, idB);

  // Answered out of order, on purpose.
  MonoAsk.handleFrame(reply(idB, { ok: true, data: "B" }));
  MonoAsk.handleFrame(reply(idA, { ok: true, data: "A" }));

  assert.equal(await a, "A");
  assert.equal(await b, "B");
});

test("a failed reply rejects with the backend's code", async () => {
  const { MonoAsk, socket } = freshAsk();

  const promise = MonoAsk.request("doc.lookup", {});
  MonoAsk.handleFrame(
    reply(socket.last().id, { ok: false, error: "monomind not found", code: "unavailable" })
  );

  const err = await promise.then(
    () => null,
    (e) => e
  );
  assert.equal(err.code, "unavailable");
  assert.match(err.message, /monomind not found/);
  // "unavailable" is an absence, not a failure — a UI renders nothing.
  assert.equal(MonoAsk.isOffline(err), true);
});

test("asking while the bridge is down fails immediately and sends nothing", async () => {
  const { MonoAsk, socket } = freshAsk(false);

  const err = await MonoAsk.request("doc.lookup", {}).then(
    () => null,
    (e) => e
  );
  assert.equal(err.code, "offline");
  assert.equal(socket.sent.length, 0, "a question must not be queued: the tab has moved on by the time it could be answered");
  assert.equal(MonoAsk.inFlight(), 0);
});

test("a send that throws settles the promise rather than leaking it", async () => {
  const { MonoAsk, socket } = freshAsk();
  socket.breakSend("socket closed mid-write");

  const err = await MonoAsk.request("doc.ask", {}).then(
    () => null,
    (e) => e
  );
  assert.equal(err.code, "offline");
  assert.equal(MonoAsk.inFlight(), 0);
});

test("the socket closing settles everything in flight", async () => {
  const { MonoAsk } = freshAsk();

  const a = MonoAsk.request("doc.ask", { q: "one" });
  const b = MonoAsk.request("doc.ask", { q: "two" });
  assert.equal(MonoAsk.inFlight(), 2);

  MonoAsk.disconnected("the bridge disconnected");

  for (const p of [a, b]) {
    const err = await p.then(
      () => null,
      (e) => e
    );
    assert.equal(err.code, "offline");
  }
  assert.equal(MonoAsk.inFlight(), 0);
});

test("a request that is never answered times out", async () => {
  const { MonoAsk } = freshAsk();

  const err = await MonoAsk.request("doc.ask", {}, { timeoutMs: 10, idleTimeoutMs: 10 }).then(
    () => null,
    (e) => e
  );
  assert.equal(err.code, "timeout");
  assert.equal(MonoAsk.inFlight(), 0);
});

test("progress frames report, restart the silence timer, and do not settle", async () => {
  const { MonoAsk, socket } = freshAsk();

  const seen = [];
  const promise = MonoAsk.request(
    "doc.ask",
    { q: "torque" },
    { timeoutMs: 2000, idleTimeoutMs: 60, onProgress: (p) => seen.push(p.stage) }
  );
  const id = socket.last().id;

  // Two progress frames, each past the 60ms silence budget. Without the
  // restart the request would already have been killed.
  for (const stage of ["searching", "citing"]) {
    await new Promise((r) => setTimeout(r, 40));
    MonoAsk.handleFrame(reply(id, { progress: { stage, detail: "…" } }));
  }
  await new Promise((r) => setTimeout(r, 40));
  MonoAsk.handleFrame(reply(id, { ok: true, data: { answers: [] } }));

  assert.deepEqual(await promise, { answers: [] });
  assert.deepEqual(seen, ["searching", "citing"]);
});

test("silence past the idle budget times out even when the overall one is generous", async () => {
  const { MonoAsk } = freshAsk();

  const started = Date.now();
  const err = await MonoAsk.request("doc.ask", {}, { timeoutMs: 5000, idleTimeoutMs: 20 }).then(
    () => null,
    (e) => e
  );
  assert.equal(err.code, "timeout");
  assert.ok(Date.now() - started < 1000, "the idle budget, not the overall one, should have fired");
});

test("a throwing progress callback cannot fail the request", async () => {
  const { MonoAsk, socket } = freshAsk();

  const promise = MonoAsk.request(
    "doc.ask",
    {},
    {
      onProgress: () => {
        throw new Error("the panel blew up");
      },
    }
  );
  const id = socket.last().id;
  MonoAsk.handleFrame(reply(id, { progress: { stage: "searching" } }));
  MonoAsk.handleFrame(reply(id, { ok: true, data: "fine" }));

  assert.equal(await promise, "fine");
});

test("handleFrame claims replies and leaves commands alone", () => {
  const { MonoAsk } = freshAsk();

  // A command from Go. If this were consumed here, page_capture would stop
  // working the moment the channel shipped.
  assert.equal(MonoAsk.handleFrame({ id: "1", type: "page_capture", params: {} }), false);
  assert.equal(MonoAsk.handleFrame({ type: "pong" }), false);
  assert.equal(MonoAsk.handleFrame(null), false);
  // A reply to a request that already timed out: still ours, still consumed.
  assert.equal(MonoAsk.handleFrame(reply("req-gone", { ok: true })), true);
});

test("probe caches what the backend advertised", async () => {
  const { MonoAsk, socket } = freshAsk();

  const first = MonoAsk.probe();
  MonoAsk.handleFrame(
    reply(socket.last().id, { ok: true, data: { pong: true, methods: ["ping", "doc.lookup"] } })
  );
  assert.deepEqual(await first, ["ping", "doc.lookup"]);

  const sentSoFar = socket.sent.length;
  assert.deepEqual(await MonoAsk.probe(), ["ping", "doc.lookup"]);
  assert.equal(socket.sent.length, sentSoFar, "probe should be cached for the life of the connection");

  assert.equal(await MonoAsk.supports("doc.lookup"), true);
  assert.equal(await MonoAsk.supports("doc.ask"), false);
});

test("a backend with no request channel probes empty instead of hanging", async () => {
  const { MonoAsk } = freshAsk();

  // An older backend ignores the frame entirely: nothing ever comes back.
  const probe = MonoAsk.probe();
  MonoAsk.disconnected("gone");
  assert.deepEqual(await probe, [], "an unanswerable probe must resolve empty, never reject");
});

test("a disconnect during a probe does not poison the cache for the worker's life", async () => {
  const { MonoAsk, socket } = freshAsk();

  // recall_bridge.js probes on every ws.onopen, so a reconnect that lands
  // while a probe is still in flight is ordinary. disconnected() rejects the
  // pending request and then clears the cache — but the rejection handler is
  // a microtask, so probe()'s catch runs afterwards and writes [] back over
  // the cleared cache. An empty array is truthy, so probe() serves it for
  // the life of the worker, supports() is false forever, and every panel
  // that gates on it renders as permanently absent.
  const interrupted = MonoAsk.probe();
  MonoAsk.disconnected("the bridge disconnected");
  assert.deepEqual(await interrupted, [], "the interrupted probe still settles, and settles empty");

  const before = socket.sent.length;
  const second = MonoAsk.probe();
  assert.equal(socket.sent.length, before + 1, "the reconnected backend is asked, not answered from a stale cache");
  MonoAsk.handleFrame(reply(socket.last().id, { ok: true, data: { methods: ["ping", "doc.lookup"] } }));
  assert.deepEqual(await second, ["ping", "doc.lookup"]);
  assert.equal(await MonoAsk.supports("doc.lookup"), true, "supports() recovers with the connection");
});

test("disconnecting forgets the probe, so a new backend is asked again", async () => {
  const { MonoAsk, socket } = freshAsk();

  const first = MonoAsk.probe();
  MonoAsk.handleFrame(reply(socket.last().id, { ok: true, data: { methods: ["ping"] } }));
  await first;

  MonoAsk.disconnected("restarted");
  const before = socket.sent.length;
  const second = MonoAsk.probe();
  assert.equal(socket.sent.length, before + 1, "a reconnect must re-probe");
  MonoAsk.handleFrame(reply(socket.last().id, { ok: true, data: { methods: ["ping", "doc.ask"] } }));
  assert.deepEqual(await second, ["ping", "doc.ask"]);
});
