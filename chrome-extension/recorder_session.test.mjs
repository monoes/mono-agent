// Tests for the worker-side recording session (recorder_session.js): one
// tab only, the stop reasons, event numbering, navigation classification,
// side-panel marks, listeners, pick mode and URL sanitising — frames
// checked against the Go Frame/Event JSON names read from
// internal/recording/types.go. Delivery is in recorder_session_delivery.test.mjs.

import test from "node:test";
import assert from "node:assert/strict";
import { RS, TAGS, fakeStorage, harness, fromTab, ev } from "./recorder_session_harness.mjs";

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


// ── listeners, pick, races (M10, H4, LOW) ──────────────────────────────

test("navigation listeners are registered for the recording only", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  await hx.s.stop("user");
  assert.deepEqual(hx.listening, [true, false]);
});

test("stopping turns pick mode off in the page first", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  await hx.s.setPick(true);
  await hx.s.stop("user");
  assert.deepEqual(hx.picks, [true, false]);
  assert.equal(hx.s.status().pick, false);
});

test("no injection or navigation event while stopping", async () => {
  const h2 = harness();
  await h2.s.start({ tabId: 7 });
  const stopping = h2.s.stop("user");
  const before = h2.injected.length;
  await h2.s.documentReady({ tabId: 7, frameId: 0 });
  assert.equal(h2.s.navigation({ tabId: 7, frameId: 0, url: "https://app.test/x", transitionType: "link" }), null);
  await stopping;
  assert.equal(h2.injected.length, before);
});

test("marks need the recording still open (Go refuses frames after stop)", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("type", { at: 1, value: "x" }), fromTab(7));
  await hx.s.stop("user");
  assert.throws(() => hx.s.mark({ kind: "param", refEvent: "e1" }), /has stopped/);
});

test("recorded URLs lose fragments and secret query values before they are queued (M5)", async () => {
  const hx = harness({ connected: false });
  hx.deps.tabInfo = async () => ({ url: "https://app.test/cb?code=abc123&state=xyz&page=2#access_token=t0k", title: "x".repeat(400) });
  await hx.s.start({ tabId: 7 });
  hx.s.navigation({ tabId: 7, frameId: 0, url: "https://app.test/reset?token=s3cret&u=ann#/inbox", transitionType: "link", transitionQualifiers: [] });
  const all = JSON.stringify(hx.s.outbox());
  for (const secret of ["abc123", "xyz", "t0k", "s3cret"]) assert.ok(!all.includes(secret), secret);
  const [start, nav] = hx.s.outbox();
  assert.equal(start.url, "https://app.test/cb?code=REDACTED&state=REDACTED&page=2");
  assert.equal(start.title.length, 300);
  assert.equal(nav.event.url, "https://app.test/reset?token=REDACTED&u=ann#/inbox", "a plain hash route is kept");
});
