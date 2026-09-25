// Tests for the recorder's Chrome wiring (recorder_wiring.js) against a fake
// `chrome`: messages from the page and the panel reach the session, the
// panel's port closing stops the recording, webNavigation is routed, and
// analyze / verify / save go out as extension→Go requests over ask.js.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

function event() {
  const fns = [];
  return { addListener: (fn) => fns.push(fn), fire: (...a) => fns.map((fn) => fn(...a)), fns };
}

function fakeChrome() {
  const data = {};
  const scripts = [];
  const ports = [];
  const c = {
    data,
    scripts,
    ports,
    runtime: { onMessage: event(), onConnect: event() },
    tabs: {
      onCreated: event(),
      onRemoved: event(),
      get: async (id) => ({ id, url: `https://app.test/${id}`, title: "App" }),
      query: async () => [{ id: 7 }],
    },
    webNavigation: {
      onCommitted: event(),
      onHistoryStateUpdated: event(),
      onDOMContentLoaded: event(),
      onCreatedNavigationTarget: event(),
    },
    scripting: { executeScript: async (spec) => (scripts.push(spec), [{}]) },
    action: {
      setBadgeText: async () => {},
      setBadgeBackgroundColor: async () => {},
      setTitle: async () => {},
    },
    storage: {
      local: {
        get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, data[k]])),
        set: async (v) => Object.assign(data, JSON.parse(JSON.stringify(v))),
      },
    },
  };
  return c;
}

function load() {
  const chrome = fakeChrome();
  const wire = [];
  const g = loadExtensionScripts(["ask.js", "recorder_session.js", "recorder_wiring.js"], { chrome });
  const send = (f) => (wire.push(f), true);
  g.MonoAsk.install({ send, isConnected: () => true });
  g.MonoRecorderWiring.install({ send, isConnected: () => true, storage: chrome.storage.local });
  const call = (msg, sender = {}) =>
    new Promise((resolve) => {
      const handled = chrome.runtime.onMessage.fire(msg, sender, resolve);
      if (!handled.some(Boolean)) resolve(undefined);
    });
  const port = () => {
    const p = { name: g.MonoRecorderWiring.PORT_NAME, onMessage: event(), onDisconnect: event(), sent: [] };
    p.postMessage = (m) => p.sent.push(m);
    chrome.runtime.onConnect.fire(p);
    return p;
  };
  const settle = () => new Promise((r) => setTimeout(r, 200));
  return { g, chrome, wire, call, port, settle };
}

test("start injects the recorder files into every frame of the tab", async () => {
  const { chrome, wire, call } = load();
  const res = await call({ type: "record_start", goal: "demo" });
  assert.equal(res.ok, true, res.error);
  assert.equal(res.state.tabId, 7, "defaults to the active tab");
  assert.deepEqual(chrome.scripts[0].files, ["recorder_selectors.js", "recorder_list.js", "recorder.js"]);
  assert.deepEqual(chrome.scripts[0].target, { tabId: 7, allFrames: true });
  assert.equal(wire[0].op, "start");
});

test("page events, navigation and new documents are routed to the session", async () => {
  const { chrome, wire, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "recorder_event", event: { type: "click", url: "https://app.test/7", at: Date.now() } }, { tab: { id: 7 } });
  chrome.webNavigation.onCommitted.fire({ tabId: 7, frameId: 0, url: "https://app.test/b", transitionType: "link", transitionQualifiers: [] });
  chrome.webNavigation.onDOMContentLoaded.fire({ tabId: 7, frameId: 2 });
  await settle();
  const types = wire.filter((f) => f.op === "event").map((f) => f.event.type);
  assert.deepEqual(types, ["click", "navigated"]);
  assert.deepEqual(chrome.scripts.at(-1).target, { tabId: 7, frameIds: [2] }, "a frame that loaded later gets the recorder too");
});

test("closing the panel that started the recording stops it", async () => {
  const { wire, call, port, settle } = load();
  const p = port();
  assert.equal(p.sent[0].type, "record_state", "a new panel gets the state straight away");
  p.onMessage.fire({ type: "record_owner" });
  await call({ type: "record_start", tabId: 7 });
  p.onDisconnect.fire();
  await settle();
  assert.deepEqual([wire.at(-1).op, wire.at(-1).reason], ["stop", "panel_closed"]);
});

test("a new tab from the recorded tab stops the recording", async () => {
  const { chrome, wire, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  chrome.tabs.onCreated.fire({ id: 30, openerTabId: 7 });
  await settle();
  assert.equal(wire.at(-1).reason, "new_tab");
});

test("analyze, verify and save are record.* requests to Go", async () => {
  const { g, wire, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "record_stop" });
  const recordingId = wire[0].recordingId;

  const answer = (method, data) => {
    const req = wire.find((f) => f.kind === "request" && f.method === method);
    assert.ok(req, `${method} was asked`);
    g.MonoAsk.handleFrame({ kind: "reply", id: req.id, ok: true, data });
    return req;
  };

  const analyzing = call({ type: "record_analyze" });
  await settle();
  const req = answer("record.analyze", { draftDir: "/d", draft: { action: "x" } });
  assert.deepEqual(req.params, { recordingId });
  assert.deepEqual((await analyzing).result, { draftDir: "/d", draft: { action: "x" } });

  const verifying = call({ type: "record_verify", draftDir: "/d" });
  await settle();
  assert.deepEqual(answer("record.verify", { ok: true, steps: [] }).params, { draftDir: "/d" });
  assert.equal((await verifying).ok, true);

  const saving = call({ type: "record_save", draftDir: "/d", saveAs: "fragment", automation: "crm", name: "login" });
  await settle();
  assert.deepEqual(answer("record.save", { automation: "crm" }).params, {
    draftDir: "/d",
    saveAs: "fragment",
    automation: "crm",
    name: "login",
  });
  assert.equal((await saving).ok, true);
});

test("analyze refuses while the recording has not reached the bridge", async () => {
  const chrome = fakeChrome();
  const g = loadExtensionScripts(["ask.js", "recorder_session.js", "recorder_wiring.js"], { chrome });
  g.MonoAsk.install({ send: () => false, isConnected: () => false });
  g.MonoRecorderWiring.install({ send: () => false, isConnected: () => false, storage: chrome.storage.local });
  const call = (msg) => new Promise((resolve) => chrome.runtime.onMessage.fire(msg, {}, resolve));
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "record_stop" });
  const res = await call({ type: "record_analyze" });
  assert.equal(res.ok, false);
  assert.match(res.error, /waiting to reach the bridge/);
});
