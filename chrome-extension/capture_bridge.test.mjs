// Tests for the capture wiring: the three ways a person starts a capture, and
// what happens to one taken while the bridge is down.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** fakeChrome is the slice of the extension API the wiring actually touches. */
function fakeChrome() {
  const store = {};
  const listeners = {};
  const record = { menus: [], badges: [], injected: [] };
  const on = (name) => ({
    addListener: (fn) => {
      listeners[name] = fn;
    },
  });

  const chrome = {
    listeners,
    record,
    runtime: { onMessage: on("message"), lastError: null },
    commands: { onCommand: on("command") },
    contextMenus: {
      removeAll: (cb) => cb(),
      create: (item) => record.menus.push(item),
      onClicked: on("menu"),
    },
    tabs: { query: async () => [{ id: 42, url: "https://paper.test/lighthouse" }] },
    action: {
      setBadgeText: (o) => {
        record.badges.push(o.text);
        return Promise.resolve();
      },
      setBadgeBackgroundColor: () => Promise.resolve(),
    },
    storage: {
      local: {
        get: async (keys) => {
          const wanted = Array.isArray(keys) ? keys : [keys];
          const out = {};
          for (const k of wanted) if (k in store) out[k] = store[k];
          return out;
        },
        set: async (update) => Object.assign(store, update),
      },
    },
    scripting: {
      executeScript: async ({ files, args }) => {
        if (files) {
          record.injected.push(...files);
          return [];
        }
        const [fn] = args;
        if (fn === "prepare") return [{ result: { scrollPasses: 3 } }];
        if (fn === "restore") return [{ result: { restored: true } }];
        return [
          {
            result: {
              meta: {
                url: "https://paper.test/lighthouse",
                title: "The Lighthouse at Dunmore",
                capturedAt: "2026-09-21T10:00:00.000Z",
                tags: [],
              },
              markdown: "# The Lighthouse at Dunmore",
              text: "The Lighthouse at Dunmore",
              selectionFound: !!args[1][0].selection,
            },
          },
        ];
      },
    },
  };
  return { chrome, store };
}

function bridge({ connected = true, seed = {}, maxMessageBytes, artifact } = {}) {
  const { chrome, store } = fakeChrome();
  Object.assign(store, seed);
  const sent = [];
  const bytes = artifact || Buffer.from("artifact").toString("base64");
  const env = loadExtensionScripts(
    ["capture_meta.js", "capture.js", "capture_profile.js", "capture_bridge.js"],
    { chrome }
  );
  env.MonoCaptureBridge.install({
    send: (message) => {
      sent.push(message);
      return true;
    },
    isConnected: () => connected,
    // background.js hands the bridge its frame budget. A default here would
    // hide the case that matters — an install that leaves it out — so it is
    // passed only when a test asks for one.
    maxMessageBytes,
    attach: async () => {},
    cdp: async (tabId, method) =>
      method === "Page.getLayoutMetrics"
        ? { cssContentSize: { width: 1000, height: 2000 } }
        : { data: bytes },
    detach: async () => {},
  });
  return { env, chrome, store, sent };
}

/**
 * settle waits for the async capture chain to produce an observable effect.
 *
 * This was a fixed 20ms sleep, and that made the suite fail under load: 20ms
 * is plenty on an idle machine and nowhere near enough on a busy one, so the
 * run went red for a reason that had nothing to do with the code (the capture
 * simply had not finished yet, and `sent` was still empty). Polling the
 * condition the test actually means is fast when the machine is fast and
 * patient when it is not.
 */
async function settle(done, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    await new Promise((r) => setTimeout(r, 1));
    if (!done || done()) return;
    if (Date.now() > deadline) throw new Error("timed out waiting for the capture to settle");
  }
}

/**
 * collect reassembles what actually arrived on the wire.
 *
 * This is the receiver's job (internal/capture/chunks.go) done here, because
 * for a long time nothing in this suite ever looked inside an artifact: the
 * tests counted frames and compared names, both of which an envelope carrying
 * no content at all satisfies perfectly. An artifact is either inline `bytes`
 * on the envelope or a run of chunk frames the envelope references, and this
 * returns the decoded text either way — so a capture that carries nothing
 * cannot pass.
 */
function collect(sent) {
  assert.ok(sent.length, "something was written to the socket");
  const envelope = sent[sent.length - 1];
  assert.equal(envelope.data.final, true, "the envelope is always the last frame");

  const chunks = new Map();
  for (const frame of sent.slice(0, -1)) {
    const chunk = frame.data.chunk;
    assert.ok(chunk, "every frame before the envelope is a chunk frame");
    if (!chunks.has(chunk.of)) chunks.set(chunk.of, []);
    chunks.get(chunk.of)[chunk.index] = frame.data.bytes;
  }

  const out = {};
  for (const artifact of envelope.data.artifacts) {
    let base64;
    if (artifact.chunked) {
      const parts = chunks.get(artifact.name) || [];
      assert.equal(parts.length, artifact.chunks, `${artifact.name}: every chunk it listed arrived`);
      assert.ok(parts.length > 0, `${artifact.name} is listed as chunked but no chunks arrived`);
      assert.ok(parts.every((p) => typeof p === "string"), `${artifact.name}: no gaps in the chunk run`);
      base64 = parts.join("");
    } else {
      assert.ok(artifact.bytes, `${artifact.name} carries bytes`);
      base64 = artifact.bytes;
    }
    out[artifact.name] = Buffer.from(base64, "base64").toString("utf8");
  }
  return out;
}

test("install offers both a page and a selection context menu (CLIP-04)", () => {
  const { chrome } = bridge();
  assert.deepEqual(
    chrome.record.menus.map((m) => [m.title, m.contexts[0]]),
    [
      ["Save page to monomind", "page"],
      ["Save selection to monomind", "selection"],
    ]
  );
});

test("the keyboard shortcut captures the tab in front of you and pushes it", async () => {
  const { chrome, sent } = bridge();
  chrome.listeners.command("capture-page");
  await settle(() => sent.length > 0);

  assert.equal(sent.length, 1, "one frame for a small capture");
  const push = sent[0];
  // Extension-initiated: there is no pending command on the Go side to match
  // this to, so it has to be routed by type instead.
  assert.equal(push.type, "page_capture");
  assert.match(push.id, /^ext-/);
  assert.equal(push.data.final, true);
  assert.equal(push.data.meta.title, "The Lighthouse at Dunmore");
  assert.match(push.data.meta.contentHash, /^sha256:/);
  assert.deepEqual(push.data.artifacts.map((a) => a.name).sort(), [
    "page.mhtml",
    "readable.md",
    "screenshot.png",
  ]);

  // The names are the cheap half. install() above is background.js's own
  // signature, so this is the envelope a real shortcut capture produces —
  // and an envelope whose artifacts have no bytes is a capture that never
  // happened, which the Go receiver rejects outright.
  const got = collect(sent);
  assert.equal(got["readable.md"], "# The Lighthouse at Dunmore");
  // The screenshot path passes CDP's already-base64 `data` through untouched;
  // the MHTML path base64s the text CDP handed it. So the fake's one string
  // arrives decoded at two different depths, and both have to survive.
  assert.equal(got["screenshot.png"], "artifact");
  assert.equal(got["page.mhtml"], Buffer.from("artifact").toString("base64"));
});

test("a capture too big for one frame round-trips through the chunk protocol", async () => {
  // 300KB of base64 against a 64KB frame budget: several chunk frames, then
  // the envelope referencing them. The bytes that come out the far end have
  // to be the bytes that went in.
  const payload = "M".repeat(300 * 1024);
  const big = Buffer.from(payload, "utf8").toString("base64");
  const { chrome, sent } = bridge({ maxMessageBytes: 64 * 1024, artifact: big });
  chrome.listeners.command("capture-page");
  await settle(() => sent.some((m) => m.data.final));

  assert.ok(sent.length > 1, "a 400KB artifact does not fit in a 64KB frame");
  const got = collect(sent);
  assert.equal(got["screenshot.png"], payload, "every chunk, in order, with nothing lost at the joins");
});

test("the page modules are injected before the page is asked to do anything", async () => {
  const { chrome } = bridge();
  chrome.listeners.command("capture-page");
  await settle(() => chrome.record.injected.includes("capture_page.js"));

  const injected = chrome.record.injected;
  // Every module capture_page.js reaches for has to already be there, and
  // capture_page.js itself has to be last — it is the one that gets called.
  // Asserted as an ordering invariant rather than as a literal list, so that
  // adding a module (tables.js, the adapters) is not a test edit.
  for (const required of ["markdown.js", "readable.js", "capture_meta.js", "tables.js"]) {
    assert.ok(injected.includes(required), `${required} is injected`);
    assert.ok(injected.indexOf(required) < injected.indexOf("capture_page.js"), `${required} precedes capture_page.js`);
  }
  assert.equal(injected[injected.length - 1], "capture_page.js");
  // The adapter registry must load before the adapters that register into it.
  assert.ok(
    injected.indexOf("adapters/registry.js") < injected.indexOf("adapters/youtube.js"),
    "the registry precedes the adapters"
  );
});

test("the popup's button answers with what was actually saved", async () => {
  const { chrome } = bridge();
  const response = await new Promise((resolve) => {
    chrome.listeners.message({ type: "capture_active_tab" }, {}, resolve);
  });
  assert.equal(response.ok, true);
  assert.equal(response.queued, false);
  assert.equal(response.title, "The Lighthouse at Dunmore");
});

test("a capture taken with the bridge down is queued, not lost (CLIP-08)", async () => {
  const { chrome, store, env, sent } = bridge({ connected: false });
  const response = await new Promise((resolve) => {
    chrome.listeners.message({ type: "capture_active_tab" }, {}, resolve);
  });

  assert.equal(response.queued, true);
  assert.equal(sent.length, 0, "nothing is written to a socket that is not there");
  assert.equal(store.captureQueue.length, 1);
  assert.equal(store.captureQueue[0].envelope.meta.title, "The Lighthouse at Dunmore");
  assert.ok(chrome.record.badges.includes("q"), "the badge says the capture is waiting");

  // flush() is what background.js calls on reconnect.
  const flushed = [];
  env.MonoCaptureBridge.install({
    send: (m) => flushed.push(m),
    isConnected: () => true,
    attach: async () => {},
    cdp: async () => ({ data: "" }),
    detach: async () => {},
  });
  assert.deepEqual(await env.MonoCaptureBridge.flush(), { flushed: 1 });
  assert.equal(flushed[0].type, "page_capture");
  assert.equal(flushed[0].data.meta.title, "The Lighthouse at Dunmore");
  assert.deepEqual(store.captureQueue, []);
});

test("a flush interrupted by a dying socket keeps what it could not send (CLIP-08)", async () => {
  const { chrome, store, env } = bridge({ connected: false });
  for (let i = 0; i < 3; i++) {
    await new Promise((resolve) => chrome.listeners.message({ type: "capture_active_tab" }, {}, resolve));
  }
  assert.equal(store.captureQueue.length, 3, "three captures taken while the bridge was down");

  // background.js's real send writes only while the socket is OPEN and says
  // nothing at all when it is not — it never throws. An MV3 socket torn down
  // seconds after ws.onopen called flush() is the ordinary case, so a flush
  // that empties the queue up front and relies on a throw to put it back
  // loses every capture after the first.
  let open = true;
  const wire = [];
  env.MonoCaptureBridge.install({
    send: (message) => {
      if (!open) return false;
      wire.push(message);
      if (message.data && message.data.final) open = false;
      return true;
    },
    isConnected: () => true,
    attach: async () => {},
    cdp: async () => ({ data: "" }),
    detach: async () => {},
  });

  const result = await env.MonoCaptureBridge.flush();
  assert.equal(result.flushed, 1, "only what reached the wire counts as flushed");
  assert.equal(wire.length, 1, "one envelope actually went out");
  assert.equal(store.captureQueue.length, 2, "the two that did not are still queued, not reported as sent");
});

test("a selection capture asks the page for the selection", async () => {
  const { chrome, sent } = bridge();
  chrome.listeners.menu({ menuItemId: "monoagent-capture-selection" }, { id: 42 });
  await settle(() => sent.length > 0);
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0].data.warnings, [], "the page reported a selection, so there is nothing to warn about");
});

test("the keyboard shortcut files into the sticky profile, with no interaction", async () => {
  const { env, chrome, sent } = bridge({ seed: { captureProfile: "p-work" } });
  await chrome.listeners.command("capture-page");
  await new Promise((r) => setTimeout(r, 30));

  assert.equal(sent.length, 1, "one envelope");
  assert.equal(sent[0].data.meta.profile, "p-work");
  assert.ok(env.MonoCaptureProfile, "the profile module is loaded in the worker");
});

test("with nothing chosen, a shortcut capture is unprofiled — as it always was", async () => {
  const { chrome, sent } = bridge();
  await chrome.listeners.command("capture-page");
  await new Promise((r) => setTimeout(r, 30));

  assert.equal(sent.length, 1);
  assert.equal("profile" in sent[0].data.meta, false);
});

test("a remembered profile that is not a usable id is ignored, not sent", async () => {
  const { chrome, sent } = bridge({ seed: { captureProfile: "../evil" } });
  await chrome.listeners.command("capture-page");
  await new Promise((r) => setTimeout(r, 30));

  assert.equal(sent.length, 1, "the capture still lands");
  assert.equal("profile" in sent[0].data.meta, false);
});
