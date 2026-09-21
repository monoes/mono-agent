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
        get: async (key) => ({ [key]: store[key] }),
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

function bridge({ connected = true } = {}) {
  const { chrome, store } = fakeChrome();
  const sent = [];
  const env = loadExtensionScripts(["capture_meta.js", "capture.js", "capture_bridge.js"], { chrome });
  env.MonoCaptureBridge.install({
    send: (message) => sent.push(message),
    isConnected: () => connected,
    attach: async () => {},
    cdp: async (tabId, method) =>
      method === "Page.getLayoutMetrics"
        ? { cssContentSize: { width: 1000, height: 2000 } }
        : { data: Buffer.from("artifact").toString("base64") },
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

test("a selection capture asks the page for the selection", async () => {
  const { chrome, sent } = bridge();
  chrome.listeners.menu({ menuItemId: "monoagent-capture-selection" }, { id: 42 });
  await settle(() => sent.length > 0);
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0].data.warnings, [], "the page reported a selection, so there is nothing to warn about");
});
