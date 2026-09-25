// Tests for the worker-side recording session (recorder_session.js): one
// tab only, the stop reasons, event numbering, navigation classification,
// side-panel marks, and the outbox that buffers frames while the bridge is
// down and flushes them in order — checked against the Go Frame/Event JSON
// names read from internal/recording/types.go.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { goJsonTags } from "./recorder_go_types.mjs";

const { MonoRecorderSession: RS } = loadExtensionScripts(["recorder_session.js"]);
const TAGS = goJsonTags();

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const out = {};
      for (const k of [].concat(keys)) if (k in data) out[k] = JSON.parse(JSON.stringify(data[k]));
      return out;
    },
    set: async (values) => Object.assign(data, JSON.parse(JSON.stringify(values))),
  };
}

function harness(opts = {}) {
  let connected = opts.connected !== false;
  let clock = 1_000_000;
  const wire = [];
  const injected = [];
  const badges = [];
  const notes = [];
  const storage = opts.storage || fakeStorage();
  const deps = {
    send: (f) => {
      if (!connected) return false;
      wire.push(JSON.parse(JSON.stringify(f)));
      return true;
    },
    isConnected: () => connected,
    storage,
    now: () => clock,
    settleMs: 0,
    inject: async (tabId, frameId) => {
      if (opts.injectFails) throw new Error("Cannot access a chrome:// URL");
      injected.push([tabId, frameId]);
    },
    uninject: async () => {},
    pick: async () => {},
    badge: (tabId, on) => badges.push([tabId, on]),
    notify: (st) => notes.push(st),
    tabInfo: async (tabId) => ({ url: `https://app.test/t${tabId}`, title: "App" }),
    profile: async () => opts.profile || "",
  };
  const s = RS.createSession(deps);
  return {
    s, wire, injected, badges, notes, storage, deps,
    tick: (ms) => (clock += ms),
    now: () => clock,
    online: () => (connected = true),
    offline: () => (connected = false),
  };
}

const fromTab = (id) => ({ tab: { id } });
const ev = (type, extra = {}) => ({ type: "recorder_event", event: Object.assign({ type, url: "https://app.test/t7" }, extra) });

test("start sends a start frame, injects into the tab and shows the badge", async () => {
  const hx = harness({ profile: "work" });
  const st = await hx.s.start({ tabId: 7, goal: "  add a contact " });
  assert.equal(st.recording, true);
  assert.match(st.id, /^rec-\d{8}-\d{6}-[a-z0-9]+$/);
  const [start] = hx.wire;
  assert.equal(start.kind, "recording");
  assert.equal(start.op, "start");
  assert.equal(start.recordingId, st.id);
  assert.equal(start.tabId, 7);
  assert.equal(start.url, "https://app.test/t7");
  assert.equal(start.goal, "add a contact");
  assert.equal(start.profile, "work");
  assert.equal(st.profile, "work", "kept, so analyze can look in the right inbox");
  assert.equal(start.startedAt, hx.now());
  assert.deepEqual(hx.injected, [[7, undefined]]);
  assert.deepEqual(hx.badges, [[7, true]]);
  await assert.rejects(hx.s.start({ tabId: 8 }), /already running/);
  hx.s.event(ev("click", { at: hx.now() }), fromTab(7));
  await hx.s.stop("user");
  assert.ok(hx.wire.every((f) => f.profile === "work"), "every frame names the profile, so a restarted bridge finds the spool");
});

test("events are numbered e1, e2… with seq and t, and only the recorded tab counts", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.tick(1500);
  hx.s.event(ev("click", { at: hx.now(), target: { tag: "button", candidates: [] } }), fromTab(7));
  hx.s.event(ev("click", { at: hx.now() }), fromTab(9)); // another tab: ignored
  hx.tick(500);
  hx.s.event(ev("type", { at: hx.now(), value: "Ann" }), fromTab(7));
  const events = hx.wire.filter((f) => f.op === "event").map((f) => f.event);
  assert.deepEqual(events.map((e) => [e.id, e.seq, e.t, e.type]), [
    ["e1", 1, 1500, "click"],
    ["e2", 2, 2000, "type"],
  ]);
  assert.ok(!("at" in events[0]));
});

test("a DOM snippet follows its event as a snapshot frame named dom-<id>.html", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(Object.assign(ev("click", { at: hx.now() }), { snippet: "<button>Go</button>" }), fromTab(7));
  const [, event, snap] = hx.wire;
  assert.equal(event.op, "event");
  assert.deepEqual(
    { op: snap.op, eventId: snap.eventId, name: snap.name, data: snap.data },
    { op: "snapshot", eventId: "e1", name: "dom-e1.html", data: "<button>Go</button>" }
  );
});

test("frames use only Go Frame / Event JSON names", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7, goal: "g" });
  hx.s.event(Object.assign(ev("type", { at: hx.now(), value: "x", frame: [0] }), { snippet: "<i>" }), fromTab(7));
  hx.s.navigation({ tabId: 7, frameId: 0, url: "https://app.test/next", transitionType: "link", transitionQualifiers: [], timeStamp: hx.now() });
  hx.s.mark({ kind: "param", refEvent: "e1", name: "query", on: true });
  await hx.s.stop("user");
  for (const f of hx.wire) {
    for (const k of Object.keys(f)) assert.ok(TAGS.Frame.has(k), `Frame.${k} (${f.op})`);
    if (f.event) {
      for (const k of Object.keys(f.event)) assert.ok(TAGS.Event.has(k), `Event.${k}`);
      if (f.event.param) for (const k of Object.keys(f.event.param)) assert.ok(TAGS.ParamMark.has(k), `ParamMark.${k}`);
    }
  }
  assert.ok(hx.wire.every((f) => f.id && f.kind === "recording"), "every frame carries an id, so Go acks it");
});

test("navigation: typed/reload/back-forward are navigate; link/form/script are navigated", () => {
  const cases = [
    [["typed", []], "navigate", "typed"],
    [["link", ["from_address_bar"]], "navigate", "typed"],
    [["reload", []], "navigate", "reload"],
    [["link", ["forward_back"]], "navigate", "back_forward"],
    [["link", []], "navigated", "link"],
    [["link", ["server_redirect"]], "navigated", "link"],
    [["link", ["client_redirect"]], "navigated", "script"],
    [["form_submit", []], "navigated", "form"],
    [["auto_subframe", []], "navigated", "script"],
  ];
  for (const [[t, q], type, cause] of cases) {
    assert.deepEqual(RS.navKind(t, q, false), { type, navCause: cause }, `${t} ${q}`);
  }
  assert.deepEqual(RS.navKind("link", [], true), { type: "navigated", navCause: "script" }, "pushState");
});

test("only the top frame of the recorded tab produces navigation events", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  assert.equal(hx.s.navigation({ tabId: 7, frameId: 3, url: "https://ads.test/", transitionType: "auto_subframe" }), null);
  assert.equal(hx.s.navigation({ tabId: 8, frameId: 0, url: "https://x.test/", transitionType: "typed" }), null);
  assert.equal(hx.s.navigation({ tabId: 7, frameId: 0, url: "chrome://newtab/", transitionType: "typed" }), null);
  const nav = hx.s.navigation({ tabId: 7, frameId: 0, url: "https://app.test/b", transitionType: "typed", timeStamp: hx.now() + 10 });
  assert.equal(nav.type, "navigate");
  assert.equal(nav.t, 10);
  assert.equal(hx.s.status().url, "https://app.test/b");
});

test("a tab opened from the recorded tab stops with new_tab; closing it stops with tab_closed", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  assert.equal(hx.s.tabCreated({ id: 20, openerTabId: 3 }), null, "unrelated tabs do not stop it");
  await hx.s.tabCreated({ id: 21, openerTabId: 7 });
  const stop = hx.wire.at(-1);
  assert.deepEqual([stop.op, stop.reason], ["stop", "new_tab"]);
  assert.equal(hx.s.status().recording, false);
  assert.deepEqual(hx.badges.at(-1), [7, false]);

  const hy = harness();
  await hy.s.start({ tabId: 7 });
  await hy.s.navigationTarget({ sourceTabId: 7, tabId: 22 });
  assert.equal(hy.wire.at(-1).reason, "new_tab", "window.open / target=_blank");

  const hz = harness();
  await hz.s.start({ tabId: 7 });
  await hz.s.tabRemoved(7);
  assert.equal(hz.wire.at(-1).reason, "tab_closed");

  const hp = harness();
  await hp.s.start({ tabId: 7 });
  await hp.s.panelClosed();
  assert.equal(hp.wire.at(-1).reason, "panel_closed");
  assert.equal(hp.s.event(ev("click", { at: 1 }), fromTab(7)), null, "nothing is recorded after stop");
});

test("stopping twice sends one stop frame", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  await Promise.all([hx.s.stop("user"), hx.s.stop("user"), hx.s.tabRemoved(7)]);
  assert.equal(hx.wire.filter((f) => f.op === "stop").length, 1);
});

test("a page that cannot be injected fails the start and ends the recording with error", async () => {
  const hx = harness({ injectFails: true });
  await assert.rejects(hx.s.start({ tabId: 7 }), /cannot be recorded/);
  assert.deepEqual(hx.wire.map((f) => f.op), ["start", "stop"]);
  assert.equal(hx.wire[1].reason, "error");
});

test("while the bridge is down frames are buffered, persisted, and flushed in order", async () => {
  const hx = harness({ connected: false });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 3; i++) hx.s.event(ev("click", { at: hx.now() + i }), fromTab(7));
  await hx.s.stop("user");
  assert.equal(hx.wire.length, 0);
  assert.equal(hx.s.status().queued, 5);
  assert.equal(hx.s.status().delivered, false, "analyze waits for delivery");
  await hx.s.saved();
  assert.equal(hx.storage.data[RS.OUTBOX_KEY].length, 5, "the outbox survives the worker");

  hx.online();
  assert.equal(hx.s.flush(), 5);
  assert.deepEqual(hx.wire.map((f) => f.op), ["start", "event", "event", "event", "stop"]);
  assert.deepEqual(hx.wire.filter((f) => f.event).map((f) => f.event.seq), [1, 2, 3]);
  assert.equal(hx.s.status().delivered, true);
});

test("a socket that drops mid-flush keeps the rest in order", async () => {
  const hx = harness({ connected: false });
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("click", { at: 1 }), fromTab(7));
  hx.s.event(ev("click", { at: 2 }), fromTab(7));
  hx.online();
  let n = 0;
  const realSend = hx.deps.send;
  hx.deps.send = (f) => (++n <= 2 ? realSend(f) : false);
  assert.equal(hx.s.flush(), 2);
  assert.equal(hx.s.status().queued, 1);
  hx.deps.send = realSend;
  hx.s.flush();
  assert.deepEqual(hx.wire.map((f) => f.event ? f.event.id : f.op), ["start", "e1", "e2"]);
});

test("a restarted worker picks the recording and its outbox back up", async () => {
  const storage = fakeStorage();
  const a = harness({ connected: false, storage });
  await a.s.start({ tabId: 7 });
  a.s.event(ev("click", { at: 1 }), fromTab(7));
  await a.s.saved();

  const b = harness({ connected: false, storage });
  const st = await b.s.restore();
  assert.equal(st.recording, true);
  assert.equal(st.tabId, 7);
  assert.equal(st.queued, 2);
  b.s.event(ev("click", { at: 2 }), fromTab(7));
  b.online();
  b.s.flush();
  assert.deepEqual(b.wire.map((f) => f.event ? f.event.id : f.op), ["start", "e1", "e2"], "numbering continues");
  assert.deepEqual(b.badges, [[7, true]]);
});

test("marks: a typed value becomes an input; an extract can be renamed; a list supersedes the single pick", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("type", { at: 1, value: "ann@x.test" }), fromTab(7)); // e1
  hx.s.event(ev("extract", { at: 2, extract: { field: "price", list: false, samples: ["$1"] } }), fromTab(7)); // e2
  hx.s.event(Object.assign(ev("extract", { at: 3, extract: { field: "price", list: true, samples: ["$1", "$2"] } }), { replacesLastExtract: true }), fromTab(7)); // e3
  assert.equal(hx.wire.at(-1).event.note, "supersedes e2");

  const p = hx.s.mark({ kind: "param", refEvent: "e1", name: "email", on: true });
  assert.deepEqual([p.type, p.param], ["param", { refEvent: "e1", name: "email" }]);
  const off = hx.s.mark({ kind: "param", refEvent: "e1", on: false });
  assert.equal(off.note, "unset");
  const renamed = hx.s.mark({ kind: "extract", refEvent: "e3", field: "unit_price" });
  assert.deepEqual([renamed.type, renamed.extract.field, renamed.extract.list, renamed.note], ["extract", "unit_price", true, "supersedes e3"]);
  assert.throws(() => hx.s.mark({ kind: "param", refEvent: "e2" }), /only typed/);
  assert.throws(() => hx.s.mark({ kind: "extract", refEvent: "e1", field: "x" }), /not an extract/);
  assert.throws(() => hx.s.mark({ kind: "param", refEvent: "e99" }), /no step/);
});

test("Go's acks are consumed, other frames are not", () => {
  const hx = harness();
  assert.equal(hx.s.handleFrame({ id: "rec-1-f1", success: true, type: "recording" }), true);
  assert.equal(hx.s.handleFrame({ id: "c1", type: "click", params: {} }), false);
  assert.equal(hx.s.handleFrame({ kind: "reply", id: "req-1", ok: true }), false);
});

test("the panel is told about every change and steps carry no candidates or snippets", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(Object.assign(ev("click", { at: 1, target: { tag: "button", text: "Save", candidates: [{ kind: "css", value: "#s" }] } }), { snippet: "<b>" }), fromTab(7));
  const last = hx.notes.at(-1);
  assert.equal(last.steps.length, 1);
  assert.deepEqual(last.steps[0].target, { tag: "button", text: "Save" });
});

test("the outbox cap drops DOM snapshots before any event", async () => {
  const hx = harness({ connected: false });
  await hx.s.start({ tabId: 7 });
  const half = Math.ceil(RS.MAX_OUTBOX / 2);
  for (let i = 0; i < half; i++) hx.s.event(Object.assign(ev("click", { at: i }), { snippet: "<i>" }), fromTab(7));
  const box = hx.s.outbox();
  assert.equal(box.length, RS.MAX_OUTBOX);
  assert.equal(box.filter((f) => f.op === "event").length, half, "no event was dropped");
  assert.equal(box[0].op, "start");
});
