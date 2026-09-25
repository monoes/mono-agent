// Tests for the recorder's Chrome wiring (recorder_wiring.js) against a fake
// `chrome`: messages from the page and the panel reach the session, the
// panel's port closing stops the recording, webNavigation is routed, and
// analyze / verify / save go out as extension→Go requests over ask.js.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

function event() {
  let fns = [];
  return {
    addListener: (fn) => fns.push(fn),
    removeListener: (fn) => (fns = fns.filter((f) => f !== fn)),
    hasListener: (fn) => fns.includes(fn),
    fire: (...a) => fns.map((fn) => fn(...a)),
    get fns() {
      return fns;
    },
  };
}

function area(data = {}) {
  return {
    data,
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, data[k]])),
    set: async (v) => Object.assign(data, JSON.parse(JSON.stringify(v))),
    remove: async (keys) => [].concat(keys).forEach((k) => delete data[k]),
  };
}

// Who is asking: the side panel (an extension page) or a page recorder (a tab).
const PANEL = { id: "ext", url: "chrome-extension://ext/sidepanel.html" };
const PAGE = (tabId) => ({ id: "ext", tab: { id: tabId }, url: "https://app.test/" });

function fakeChrome() {
  const data = {};
  const scripts = [];
  const ports = [];
  const c = {
    data,
    scripts,
    ports,
    runtime: {
      id: "ext",
      getURL: (p) => `chrome-extension://ext/${p}`,
      onMessage: event(),
      onConnect: event(),
      onStartup: event(),
    },
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
      onReferenceFragmentUpdated: event(),
    },
    scripting: { executeScript: async (spec) => (scripts.push(spec), [{}]) },
    action: {
      setBadgeText: async () => {},
      setBadgeBackgroundColor: async () => {},
      setTitle: async () => {},
    },
    storage: { local: area(data), session: area() },
  };
  return c;
}

function load() {
  const chrome = fakeChrome();
  const wire = [];
  const g = loadExtensionScripts(["ask.js", "recorder_privacy.js", "recorder_outbox.js", "recorder_session.js", "recorder_wiring.js"], { chrome });
  const send = (f) => (wire.push(f), true);
  g.MonoAsk.install({ send, isConnected: () => true });
  g.MonoRecorderWiring.install({ send, isConnected: () => true, storage: chrome.storage.local });
  const call = (msg, sender = PANEL) =>
    new Promise((resolve) => {
      const handled = chrome.runtime.onMessage.fire(msg, sender, resolve);
      if (!handled.some(Boolean)) resolve(undefined);
    });
  const port = () => {
    const p = { name: g.MonoRecorderWiring.PORT_NAME, sender: PANEL, onMessage: event(), onDisconnect: event(), sent: [] };
    p.postMessage = (m) => p.sent.push(m);
    chrome.runtime.onConnect.fire(p);
    return p;
  };
  const settle = () => new Promise((r) => setTimeout(r, 200));
  return { g, chrome, wire, call, port, settle };
}

test("start injects the recorder files into every frame of the tab", async () => {
  const { chrome, wire, call } = load();
  chrome.data.captureProfile = "work";
  const res = await call({ type: "record_start", goal: "demo" });
  assert.equal(res.ok, true, res.error);
  assert.equal(res.state.tabId, 7, "defaults to the active tab");
  assert.deepEqual(chrome.scripts[0].files, ["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder_dom.js", "recorder.js"]);
  assert.deepEqual(chrome.scripts[0].target, { tabId: 7, allFrames: true });
  assert.equal(wire[0].op, "start");
});

test("page events, navigation and new documents are routed to the session", async () => {
  const { chrome, wire, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "recorder_event", event: { type: "click", url: "https://app.test/7", at: Date.now() } }, PAGE(7));
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
  await settle();
  assert.equal(p.sent[0].type, "record_state", "a new panel gets the state once the worker has restored it");
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
  const { g, chrome, wire, call, settle } = load();
  chrome.data.captureProfile = "work";
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "record_stop" });
  const recordingId = wire[0].recordingId;
  // Go acks every frame; the stop ack says the envelope is written.
  for (const f of wire.filter((x) => x.kind === "recording")) {
    g.MonoRecorderWiring.handleFrame({ id: f.id, success: true, type: "recording" });
  }

  const answer = (method, data) => {
    const req = wire.find((f) => f.kind === "request" && f.method === method);
    assert.ok(req, `${method} was asked`);
    g.MonoAsk.handleFrame({ kind: "reply", id: req.id, ok: true, data });
    return req;
  };

  const analyzing = call({ type: "record_analyze" });
  await settle();
  const req = answer("record.analyze", { draftDir: "/d", draft: { action: "x" } });
  assert.deepEqual(req.params, { recordingId, profile: "work" }, "analyze looks in the profile's inbox");
  assert.deepEqual((await analyzing).result, { draftDir: "/d", draft: { action: "x" } });

  const verifying = call({ type: "record_verify", draftDir: "/d", inputs: { password: "pw-for-this-run" } });
  await settle();
  assert.deepEqual(answer("record.verify", { ok: true, steps: [] }).params, {
    draftDir: "/d",
    inputs: { password: "pw-for-this-run" },
    profile: "work",
  });
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

  const creating = call({ type: "record_save", draftDir: "/e", automation: "newcrm", isNew: true });
  await settle();
  const req2 = wire.filter((f) => f.method === "record.save").at(-1);
  assert.deepEqual(req2.params, { draftDir: "/e", saveAs: "action", new: "newcrm" }, "a new automation is --new");
  g.MonoAsk.handleFrame({ kind: "reply", id: req2.id, ok: true, data: {} });
  await creating;
});

test("analyze refuses while the recording has not reached the bridge", async () => {
  const chrome = fakeChrome();
  const g = loadExtensionScripts(["ask.js", "recorder_privacy.js", "recorder_outbox.js", "recorder_session.js", "recorder_wiring.js"], { chrome });
  g.MonoAsk.install({ send: () => false, isConnected: () => false });
  g.MonoRecorderWiring.install({ send: () => false, isConnected: () => false, storage: chrome.storage.local });
  const call = (msg) => new Promise((resolve) => chrome.runtime.onMessage.fire(msg, PANEL, resolve));
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "record_stop" });
  const res = await call({ type: "record_analyze" });
  assert.equal(res.ok, false);
  assert.match(res.error, /waiting to reach the bridge/);
});

test("navigation and tab listeners exist only while recording", async () => {
  const { chrome, call, settle } = load();
  const count = () => chrome.webNavigation.onCommitted.fns.length + chrome.tabs.onCreated.fns.length;
  await settle();
  assert.equal(count(), 0, "idle: no listener on anyone's browsing");
  await call({ type: "record_start", tabId: 7 });
  assert.equal(count(), 2);
  assert.equal(chrome.webNavigation.onReferenceFragmentUpdated.fns.length, 1, "hash routes are heard");
  await call({ type: "record_stop" });
  assert.equal(count(), 0);
});

test("a hash-route change is recorded as navigation", async () => {
  const { chrome, wire, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  chrome.webNavigation.onReferenceFragmentUpdated.fire({ tabId: 7, frameId: 0, url: "https://app.test/7#/inbox", transitionType: "link", transitionQualifiers: [] });
  await settle();
  const nav = wire.find((f) => f.event && f.event.url.endsWith("#/inbox"));
  assert.equal(nav.event.type, "navigated");
});

test("a page from the back/forward cache is told whether to keep recording", async () => {
  const { call } = load();
  await call({ type: "record_start", tabId: 7 });
  assert.deepEqual(await call({ type: "recorder_alive" }, PAGE(7)), { recording: true });
  assert.deepEqual(await call({ type: "recorder_alive" }, PAGE(8)), { recording: false });
  await call({ type: "record_stop" });
  assert.deepEqual(await call({ type: "recorder_alive" }, PAGE(7)), { recording: false });
});

test("the recording state lives in storage.session", async () => {
  const { chrome, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  await settle();
  assert.ok(chrome.storage.session.data.recordingState);
  assert.equal(chrome.data.recordingState, undefined);
});

test("a browser restart (runtime.onStartup) closes the old recording", async () => {
  const { chrome, call, settle } = load();
  await call({ type: "record_start", tabId: 7 });
  chrome.runtime.onStartup.fire();
  await settle();
  const res = await call({ type: "record_status" });
  assert.equal(res.state.recording, false);
});

test("only this extension's pages may drive a recording, and only tabs send events", async () => {
  const { wire, call } = load();
  // An ignored message is not answered at all (call resolves undefined).
  const nobody = (msg, sender) => call(msg, sender).then((r) => (r === undefined ? "ignored" : r));
  assert.equal(await nobody({ type: "record_start", tabId: 7 }, { id: "other-extension", url: "chrome-extension://other/x.html" }), "ignored");
  assert.equal(await nobody({ type: "record_start", tabId: 7 }, PAGE(7)), "ignored", "a content script cannot start one");
  assert.equal(wire.length, 0);
  await call({ type: "record_start", tabId: 7 });
  await call({ type: "recorder_event", event: { type: "click", url: "https://app.test/7", at: 1 } }, { id: "ext", url: "chrome-extension://ext/sidepanel.html" });
  await call({ type: "recorder_event", event: { type: "click", url: "https://app.test/7", at: 1 } }, { id: "evil", tab: { id: 7 } });
  assert.equal(wire.filter((f) => f.op === "event").length, 0, "events come only from this extension's page recorders");
});
