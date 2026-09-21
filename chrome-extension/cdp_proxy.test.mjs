// Tests for the raw CDP proxy: commands out through chrome.debugger, events
// back over the bridge unasked-for.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** A proxy installed against recordable fakes for everything it touches. */
function setup({ activeTab = 42, cdp, attach } = {}) {
  const sent = [];
  const record = { attached: [], detached: [], pinned: [], unpinned: [], commands: [] };
  const debuggerEvents = [];
  const debuggerDetaches = [];

  const { MonoCdpProxy } = loadExtensionScripts(["cdp_proxy.js"], {});
  MonoCdpProxy.install({
    send: (msg) => sent.push(msg),
    isConnected: () => true,
    activeTabId: async () => activeTab,
    attach:
      attach ??
      (async (tabId) => {
        record.attached.push(tabId);
      }),
    cdp:
      cdp ??
      (async (session, method, params) => {
        record.commands.push({ session, method, params });
        return { ok: method };
      }),
    detach: async (tabId) => {
      record.detached.push(tabId);
    },
    pin: (tabId) => record.pinned.push(tabId),
    unpin: (tabId) => record.unpinned.push(tabId),
    onDebuggerEvent: (fn) => debuggerEvents.push(fn),
    onDebuggerDetach: (fn) => debuggerDetaches.push(fn),
  });

  return {
    proxy: MonoCdpProxy,
    sent,
    record,
    emit: (...args) => debuggerEvents[0](...args),
    detachNotice: (...args) => debuggerDetaches[0](...args),
  };
}

test("attach resolves the active tab when none was named", async () => {
  const { proxy, record } = setup({ activeTab: 7 });
  const result = await proxy.handleCommand("cdp_attach", {});
  assert.deepEqual(result, { tabId: 7 });
  assert.deepEqual(record.attached, [7]);
});

test("attach uses the tab it was given", async () => {
  const { proxy, record } = setup({ activeTab: 7 });
  const result = await proxy.handleCommand("cdp_attach", { tabId: 99 });
  assert.deepEqual(result, { tabId: 99 });
  assert.deepEqual(record.attached, [99]);
});

test("an attached tab is pinned so the idle sweep cannot detach it mid-capture", async () => {
  const { proxy, record } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  assert.deepEqual(record.pinned, [42]);
  await proxy.handleCommand("cdp_detach", { tabId: 42 });
  assert.deepEqual(record.unpinned, [42]);
  assert.deepEqual(record.detached, [42]);
});

test("a cdp command reaches chrome.debugger and its result comes back", async () => {
  const { proxy, record } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  const result = await proxy.handleCommand("cdp", {
    tabId: 42,
    method: "Page.navigate",
    params: { url: "https://x.test" },
  });
  assert.deepEqual(result, { result: { ok: "Page.navigate" } });
  assert.deepEqual(record.commands, [
    { session: { tabId: 42 }, method: "Page.navigate", params: { url: "https://x.test" } },
  ]);
});

test("a command attaches on demand, so a client need not attach first", async () => {
  const { proxy, record } = setup();
  await proxy.handleCommand("cdp", { tabId: 42, method: "Runtime.enable", params: {} });
  assert.deepEqual(record.attached, [42]);
});

test("a child session id is carried into the debuggee, for out-of-process frames", async () => {
  const { proxy, record } = setup();
  await proxy.handleCommand("cdp", {
    tabId: 42,
    method: "Runtime.enable",
    params: {},
    sessionId: "OOPIF-1",
  });
  assert.deepEqual(record.commands[0].session, { tabId: 42, sessionId: "OOPIF-1" });
});

test("a command with no method is refused rather than passed to Chrome", async () => {
  const { proxy } = setup();
  await assert.rejects(() => proxy.handleCommand("cdp", { tabId: 42 }), /method/);
});

test("a debugger event on a subscribed tab is pushed over the bridge", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });

  emit({ tabId: 42 }, "Network.responseReceived", { requestId: "R1" });

  assert.deepEqual(sent, [
    {
      type: "cdp_event",
      success: true,
      data: {
        tabId: 42,
        method: "Network.responseReceived",
        params: { requestId: "R1" },
      },
    },
  ]);
});

test("an event carries its child session id when chrome.debugger reports one", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  emit({ tabId: 42, sessionId: "OOPIF-1" }, "Runtime.consoleAPICalled", {});
  assert.equal(sent[0].data.sessionId, "OOPIF-1");
});

test("events for tabs nobody subscribed to are not relayed", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  emit({ tabId: 99 }, "Network.responseReceived", {});
  assert.equal(sent.length, 0);
});

test("after detach, that tab's events stop", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  await proxy.handleCommand("cdp_detach", { tabId: 42 });
  emit({ tabId: 42 }, "Network.responseReceived", {});
  assert.equal(sent.length, 0);
});

test("a detach Chrome initiated is reported as Inspector.detached with its reason", async () => {
  const { proxy, sent, detachNotice } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });

  // The user clicked Cancel on Chrome's debugging banner.
  detachNotice({ tabId: 42 }, "canceled_by_user");

  assert.deepEqual(sent, [
    {
      type: "cdp_event",
      success: true,
      data: {
        tabId: 42,
        method: "Inspector.detached",
        params: { reason: "canceled_by_user" },
      },
    },
  ]);
});

test("an oversized result is refused, not sent into a frame limit that kills the socket", async () => {
  const { proxy } = setup({
    cdp: async () => ({ data: "x".repeat(31 * 1024 * 1024) }),
  });
  await assert.rejects(
    () => proxy.handleCommand("cdp", { tabId: 42, method: "Page.captureScreenshot", params: {} }),
    /too large/,
  );
});

test("an oversized event is dropped, and the drop is reported rather than silent", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  emit({ tabId: 42 }, "Tracing.dataCollected", { value: "x".repeat(31 * 1024 * 1024) });

  assert.equal(sent.length, 1);
  assert.equal(sent[0].data.method, "MonoAgent.eventDropped");
  assert.equal(sent[0].data.params.method, "Tracing.dataCollected");
  assert.ok(sent[0].data.params.bytes > 31 * 1024 * 1024);
});

// 11M CJK characters: 11M UTF-16 units, so `.length` says ~11MB and sails
// under the 30MB guard — while the frame actually weighs ~33MB, over the Go
// server's 32MiB read limit. JSON.stringify does not escape non-ASCII, so
// there is nothing between the two numbers but the unit they are counted in.
const CJK = "\u6f22".repeat(11 * 1024 * 1024);

test("the result guard counts UTF-8 bytes, not UTF-16 units", async () => {
  const { proxy } = setup({ cdp: async () => ({ value: CJK }) });
  // Sending this would not fail the command — it would drop the whole
  // extension connection, which is the exact outcome the guard exists for.
  await assert.rejects(
    () => proxy.handleCommand("cdp", { tabId: 42, method: "Runtime.evaluate", params: {} }),
    /too large/,
  );
});

test("the event guard counts UTF-8 bytes, not UTF-16 units", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  emit({ tabId: 42 }, "Tracing.dataCollected", { value: CJK });

  assert.equal(sent.length, 1);
  assert.equal(sent[0].data.method, "MonoAgent.eventDropped");
  assert.ok(
    sent[0].data.params.bytes > 32 * 1024 * 1024,
    `the reported size is the wire size, got ${sent[0].data.params.bytes}`
  );
});

test("a relay disconnect releases every tab it was holding", async () => {
  const { proxy, record } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  await proxy.handleCommand("cdp_attach", { tabId: 7 });
  assert.deepEqual(proxy.subscribedTabs(), [42, 7]);

  // ws.onclose. The subscriptions were to a socket that is gone, and the
  // pins they took are the only thing keeping the 30s idle sweep off two
  // tabs nothing is listening to — so Chrome's debugging banner stays up
  // until the user intervenes.
  proxy.disconnected("the bridge disconnected");

  assert.deepEqual(proxy.subscribedTabs(), [], "nothing is still subscribed");
  assert.deepEqual(record.unpinned, [42, 7], "the sweep is free to reclaim both tabs");
});

test("events stop being relayed once the socket that asked for them is gone", async () => {
  const { proxy, sent, emit } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  proxy.disconnected("the bridge disconnected");
  sent.length = 0;

  emit({ tabId: 42 }, "Network.responseReceived", { requestId: "1" });
  assert.deepEqual(sent, [], "a closed socket is not formatted for");
});

test("attach failure surfaces Chrome's own message", async () => {
  const { proxy } = setup({
    attach: async () => {
      throw new Error("debugger attach: Cannot access a chrome:// URL");
    },
  });
  await assert.rejects(
    () => proxy.handleCommand("cdp_attach", { tabId: 42 }),
    /chrome:\/\/ URL/,
  );
});

test("detaching a tab that was never attached is not an error", async () => {
  const { proxy, record } = setup();
  const result = await proxy.handleCommand("cdp_detach", { tabId: 42 });
  assert.deepEqual(result, { tabId: 42 });
  assert.deepEqual(record.detached, [42]);
});

test("handles returns the tab ids it is relaying for", async () => {
  const { proxy } = setup();
  await proxy.handleCommand("cdp_attach", { tabId: 42 });
  await proxy.handleCommand("cdp_attach", { tabId: 43 });
  assert.deepEqual(proxy.subscribedTabs().sort(), [42, 43]);
});
