// Tests for asking the bridge whether it is running.
//
// The case that matters most here is the adversarial one: Chrome run with
// --remote-debugging-port=9222 answers HTTP on the port the bridge prefers,
// so "something replied with a 200" must not be mistaken for "the bridge is
// up". Several of these exist only to pin that down.
//
// `node --test 'chrome-extension/*.test.mjs'`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoBridgeHealth: H } = loadExtensionScripts(["bridge_health.js"]);

const doc = (over = {}) =>
  Object.assign(
    {
      service: "monoagent-bridge",
      status: "waiting",
      connected: false,
      addr: "127.0.0.1:9477",
      wsUrl: "ws://127.0.0.1:9477/monoagent",
      pid: 2307236,
      uptimeSec: 1,
      version: "dev",
    },
    over
  );

/**
 * fakeFetch answers each URL from a table; anything absent is refused.
 *
 * It honours `signal` the way a real fetch does — rejecting when aborted —
 * because a double that ignores it would let a hang in the code under test
 * pass as a pass. That is exactly the bug this file is here to catch.
 */
function fakeFetch(table) {
  const calls = [];
  const impl = async (url, init = {}) => {
    calls.push(url);
    const entry = table[url];
    if (!entry) throw new Error("ECONNREFUSED");
    if (entry.hang) {
      await new Promise((_, reject) => {
        const signal = init.signal;
        if (!signal) return; // never settles, as a signal-less hang would
        if (signal.aborted) reject(new Error("aborted"));
        signal.addEventListener("abort", () => reject(new Error("aborted")));
      });
    }
    return {
      ok: entry.ok !== false,
      json: async () => {
        if (entry.notJson) throw new Error("not JSON");
        return entry.body;
      },
    };
  };
  impl.calls = calls;
  return impl;
}

// ── identity ─────────────────────────────────────────────────────────

test("a document is the bridge only if it says so", () => {
  assert.equal(H.isBridge(doc()), true);
  assert.equal(H.isBridge({ status: "connected" }), false, "status alone proves nothing");
  assert.equal(H.isBridge({ service: "something-else", status: "connected" }), false);
  assert.equal(H.isBridge(null), false);
  assert.equal(H.isBridge("monoagent-bridge"), false);
  assert.equal(H.isBridge([]), false);
});

test("Chrome's own devtools endpoint on 9222 is not the bridge", () => {
  // What a browser with --remote-debugging-port=9222 actually serves. It is
  // a 200, it is JSON, and it means nothing to us.
  const devtools = {
    Browser: "Chrome/141.0.0.0",
    "Protocol-Version": "1.3",
    webSocketDebuggerUrl: "ws://127.0.0.1:9222/devtools/browser/abc",
  };
  assert.equal(H.isBridge(devtools), false);
  assert.equal(H.read(devtools), null);
});

// ── reading ──────────────────────────────────────────────────────────

test("a health document is read down to the fields that are used", () => {
  const health = H.read(doc({ status: "connected", connected: true }));
  assert.deepEqual(health, {
    status: "connected",
    connected: true,
    wsUrl: "ws://127.0.0.1:9477/monoagent",
    addr: "127.0.0.1:9477",
    uptimeSec: 1,
    version: "dev",
  });
});

test("a missing field never becomes undefined downstream", () => {
  const health = H.read({ service: "monoagent-bridge" });
  assert.equal(health.status, "");
  assert.equal(health.wsUrl, "");
  assert.equal(health.uptimeSec, 0);
  assert.equal(health.connected, false);
});

// ── the mapping onto popup states ────────────────────────────────────

test("nothing answering is the bridge not running — stated, not inferred", () => {
  assert.deepEqual(H.toStatus(null), { status: "disconnected", reason: "no_bridge" });
});

test("each health status maps onto the state the popup draws", () => {
  assert.equal(H.toStatus(H.read(doc({ status: "connected", connected: true }))).status, "connected");
  assert.equal(H.toStatus(H.read(doc({ status: "waiting" }))).status, "waiting");
  assert.equal(H.toStatus(H.read(doc({ status: "unpaired" }))).status, "unpaired");
});

test("waiting is never reported as an error", () => {
  const mapped = H.toStatus(H.read(doc({ status: "waiting" })));
  // A bridge that is up with nothing attached is the resting state of a
  // suspended service worker. It must not carry a failure reason.
  assert.equal(mapped.reason, "");
  assert.notEqual(mapped.status, "disconnected");
});

test("the socket to use comes from the bridge, not from a guess", () => {
  const mapped = H.toStatus(H.read(doc({ status: "connected", wsUrl: "ws://127.0.0.1:9477/monoagent" })));
  assert.equal(mapped.wsUrl, "ws://127.0.0.1:9477/monoagent");
});

test("an unrecognised status from a newer bridge is passed through", () => {
  const mapped = H.toStatus(H.read(doc({ status: "draining" })));
  assert.equal(mapped.status, "draining");
  assert.equal(mapped.reason, "");
});

// ── candidate URLs ───────────────────────────────────────────────────

test("a websocket URL becomes the health URL on the same origin", () => {
  assert.equal(H.healthUrl("ws://127.0.0.1:9222/monoagent"), "http://127.0.0.1:9222/monoagent/health");
  assert.equal(H.healthUrl("wss://127.0.0.1:9443/monoagent"), "https://127.0.0.1:9443/monoagent/health");
  assert.equal(H.healthUrl(""), "");
  assert.equal(H.healthUrl("nonsense"), "", "junk skips the candidate instead of throwing");
  assert.equal(H.healthUrl(null), "");
});

test("what the user configured is tried before the defaults", () => {
  const urls = H.candidates({ wsUrl: "ws://127.0.0.1:9477/monoagent" });
  assert.equal(urls[0], "http://127.0.0.1:9477/monoagent/health");
  assert.ok(urls.includes("http://127.0.0.1:9222/monoagent/health"));
  assert.ok(urls.includes("http://127.0.0.1:9323/monoagent/health"));
});

test("the learned port outranks the defaults but not the configured one", () => {
  const urls = H.candidates({
    wsUrl: "ws://127.0.0.1:9001/monoagent",
    pairedWsUrl: "ws://127.0.0.1:9002/monoagent",
    workingWsUrl: "ws://127.0.0.1:9003/monoagent",
  });
  assert.deepEqual(urls.slice(0, 3), [
    "http://127.0.0.1:9001/monoagent/health",
    "http://127.0.0.1:9002/monoagent/health",
    "http://127.0.0.1:9003/monoagent/health",
  ]);
});

test("a candidate is never probed twice", () => {
  const urls = H.candidates({
    wsUrl: "ws://127.0.0.1:9222/monoagent",
    workingWsUrl: "ws://127.0.0.1:9222/monoagent",
  });
  assert.equal(new Set(urls).size, urls.length);
  assert.equal(urls.filter((u) => u.includes(":9222")).length, 1);
});

test("both known ports are always covered", () => {
  const urls = H.candidates({});
  assert.deepEqual(urls, [
    "http://127.0.0.1:9222/monoagent/health",
    "http://127.0.0.1:9323/monoagent/health",
  ]);
});

// ── probing ──────────────────────────────────────────────────────────

test("a bridge on the fallback port is found", async () => {
  const fetchImpl = fakeFetch({
    "http://127.0.0.1:9323/monoagent/health": { body: doc({ status: "connected", connected: true }) },
  });
  const health = await H.probe({ fetch: fetchImpl, known: {} });
  assert.equal(health.status, "connected");
});

test("nothing listening anywhere resolves to null, not an error", async () => {
  const health = await H.probe({ fetch: fakeFetch({}), known: {} });
  assert.equal(health, null);
});

test("a stranger on the bridge's favourite port does not count as the bridge", async () => {
  // The real scenario: Chrome holds 9222, the bridge bound 9323.
  const fetchImpl = fakeFetch({
    "http://127.0.0.1:9222/monoagent/health": { body: { Browser: "Chrome/141.0.0.0" } },
    "http://127.0.0.1:9323/monoagent/health": { body: doc({ status: "waiting" }) },
  });
  const health = await H.probe({ fetch: fetchImpl, known: {} });
  assert.equal(health.status, "waiting");
  assert.equal(health.addr, "127.0.0.1:9477");
});

test("when two ports both answer, the preferred one wins", async () => {
  const fetchImpl = fakeFetch({
    "http://127.0.0.1:9222/monoagent/health": { body: doc({ status: "connected", addr: "a" }) },
    "http://127.0.0.1:9323/monoagent/health": { body: doc({ status: "waiting", addr: "b" }) },
  });
  const health = await H.probe({ fetch: fetchImpl, known: {} });
  assert.equal(health.addr, "a", "ranked by candidate order, not by who replied first");
});

test("a port that accepts and then says nothing cannot hang the popup", async () => {
  const fetchImpl = fakeFetch({
    "http://127.0.0.1:9222/monoagent/health": { hang: true },
    "http://127.0.0.1:9323/monoagent/health": { body: doc({ status: "waiting" }) },
  });
  const started = Date.now();
  const health = await H.probe({ fetch: fetchImpl, known: {}, timeoutMs: 80 });
  // The healthy port still answers, and the hung one is abandoned rather
  // than waited on in turn.
  assert.equal(health.status, "waiting");
  assert.ok(Date.now() - started < 1000, "must not wait out the hung port serially");
});

test("everything hanging still resolves, as no bridge", async () => {
  const fetchImpl = fakeFetch({
    "http://127.0.0.1:9222/monoagent/health": { hang: true },
    "http://127.0.0.1:9323/monoagent/health": { hang: true },
  });
  const health = await H.probe({ fetch: fetchImpl, known: {}, timeoutMs: 60 });
  assert.equal(health, null);
  assert.deepEqual(H.toStatus(health), { status: "disconnected", reason: "no_bridge" });
});

test("a non-200 and unparseable body are both simply 'not the bridge'", async () => {
  const notOk = await H.probe({
    fetch: fakeFetch({ "http://127.0.0.1:9222/monoagent/health": { ok: false, body: doc() } }),
    known: {},
  });
  assert.equal(notOk, null);

  const garbage = await H.probe({
    fetch: fakeFetch({ "http://127.0.0.1:9222/monoagent/health": { notJson: true } }),
    known: {},
  });
  assert.equal(garbage, null);
});

test("every candidate is asked at once rather than in turn", async () => {
  const fetchImpl = fakeFetch({});
  await H.probe({ fetch: fetchImpl, known: { wsUrl: "ws://127.0.0.1:9001/monoagent" } });
  assert.equal(fetchImpl.calls.length, 3, "all three candidates were tried");
});

test("no fetch at all is a clean null rather than a crash", async () => {
  assert.equal(await H.probe({ fetch: null, urls: [], known: {} }), null);
});
