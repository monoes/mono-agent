// Tests for how the recording session delivers frames (recorder_session.js
// with recorder_outbox.js): acks and resend, the frame and byte bounds,
// chunked persistence, restore after a worker or browser restart, and
// discarded recordings.

import test from "node:test";
import assert from "node:assert/strict";
import { RS, TAGS, fakeStorage, harness, fromTab, ev } from "./recorder_session_harness.mjs";

// ── delivery: acks, resend, finalize (H5c) ─────────────────────────────

test("frames leave the outbox only when Go acks them, and are resent on reconnect", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("click", { at: 1 }), fromTab(7));
  assert.equal(hx.s.status().queued, 2, "sent is not delivered");
  const firstTry = hx.wire.splice(0);
  assert.deepEqual(firstTry.map((f) => f.op), ["start", "event"]);

  // The socket died with both in flight: a reconnect sends both again, in order.
  hx.offline();
  hx.online();
  assert.equal(hx.s.flush(), 2);
  assert.deepEqual(hx.wire.map((f) => f.id), firstTry.map((f) => f.id), "same ids, so Go can dedupe");
  hx.ackAll();
  assert.equal(hx.s.status().queued, 0);
});

test("frames made while offline wait, then go out in order behind the resend", async () => {
  const hx = harness({ connected: false });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 3; i++) hx.s.event(ev("click", { at: i }), fromTab(7));
  assert.equal(hx.wire.length, 0);
  hx.online();
  hx.s.event(ev("click", { at: 9 }), fromTab(7)); // socket back, but the flush has not run yet
  assert.equal(hx.wire.length, 0, "a new frame never overtakes queued ones");
  hx.s.flush();
  assert.deepEqual(hx.wire.map((f) => (f.event ? f.event.id : f.op)), ["start", "e1", "e2", "e3", "e4"]);
});

test("a recording is delivered only when its stop frame is acked; then storage.local forgets it", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("type", { at: 1, value: "private words" }), fromTab(7));
  await hx.s.stop("user");
  await hx.s.saved();
  assert.equal(hx.s.status().delivered, false, "sent but not acked");
  assert.ok(JSON.stringify(hx.storage.data).includes("private words"), "buffered until acked");

  hx.ackAll({ envelope: "20260925-rec" });
  await hx.s.saved();
  const st = hx.s.status();
  assert.equal(st.delivered, true);
  assert.equal(st.envelopeId, "20260925-rec");
  assert.equal(st.queued, 0);
  assert.ok(!JSON.stringify(hx.storage.data).includes("private words"), "no typed value left in storage.local");
  assert.deepEqual(Object.keys(hx.storage.data).filter((k) => k.startsWith("recordingOutbox.")), []);
});

test("a stop refused as 'already finished' counts as delivered; other refusals are reported", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  hx.s.event(ev("click", { at: 1 }), fromTab(7));
  const [, event] = hx.wire;
  hx.s.handleFrame({ id: event.id, success: false, type: "recording", error: "invalid event id" });
  assert.match(hx.s.status().warning, /refused a event frame: invalid event id/);
  assert.equal(hx.s.outbox().some((f) => f.id === event.id), false, "a refused frame is not resent forever");
  await hx.s.stop("user");
  const stop = hx.wire.at(-1);
  hx.s.handleFrame({ id: stop.id, success: false, type: "recording", error: "recording rec-x already finished" });
  assert.equal(hx.s.status().delivered, true);
});

// ── bounds (H5a, H5b) ──────────────────────────────────────────────────

test("over the frame cap only snapshots are evicted; start and events never are", async () => {
  const hx = harness({ connected: false, maxFrames: 20 });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 12; i++) hx.s.event(Object.assign(ev("click", { at: i }), { snippet: "<i>" }), fromTab(7));
  const box = hx.s.outbox();
  assert.equal(box.length, 20);
  assert.equal(box[0].op, "start");
  assert.equal(box.filter((f) => f.op === "event").length, 12, "every event kept");
  assert.equal(hx.s.status().recording, true);
});

test("when only events are left and the cap is hit, the recording stops with a visible reason", async () => {
  const hx = harness({ connected: false, maxFrames: 5 });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 6; i++) hx.s.event(ev("click", { at: i }), fromTab(7));
  await new Promise((r) => setTimeout(r, 10));
  const st = hx.s.status();
  assert.equal(st.recording, false);
  assert.equal(st.stopReason, "error");
  assert.equal(st.error, RS.OVERFLOW);
  const box = hx.s.outbox();
  assert.deepEqual(box.map((f) => (f.event ? f.event.id : f.op)), ["start", "e1", "e2", "e3", "e4", "stop"], "the stop frame is always kept");
  assert.equal(st.steps.length, 4, "the rejected event is not shown as recorded");
});

test("the byte cap works the same way", async () => {
  const hx = harness({ connected: false, maxBytes: 4000 });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 20; i++) hx.s.event(Object.assign(ev("click", { at: i }), { snippet: "x".repeat(300) }), fromTab(7));
  const box = hx.s.outbox();
  assert.ok(JSON.stringify(box).length <= 4000 + 400);
  assert.equal(box.filter((f) => f.op === "snapshot").length < 20, true);
});

test("the outbox is stored in chunks and only changed chunks are rewritten", async () => {
  const storage = fakeStorage();
  const writes = [];
  const realSet = storage.set;
  storage.set = async (v) => (writes.push(Object.keys(v)), realSet(v));
  const hx = harness({ connected: false, storage });
  await hx.s.start({ tabId: 7 });
  for (let i = 0; i < 60; i++) {
    hx.s.event(ev("click", { at: i }), fromTab(7));
    await hx.s.saved();
  }
  const chunkKeys = Object.keys(storage.data).filter((k) => k.startsWith("recordingOutbox."));
  assert.equal(chunkKeys.length, 3, "61 frames in chunks of 25");
  const last = writes.at(-1);
  assert.deepEqual(last, ["recordingOutbox.2"], "an event rewrites only the tail chunk");
});

test("a storage failure is shown to the panel", async () => {
  const hx = harness({ connected: false });
  hx.storage.data.__failSet = "QUOTA_BYTES quota exceeded";
  await hx.s.start({ tabId: 7 });
  await hx.s.saved();
  assert.match(hx.s.status().error, /could not save the recording buffer: QUOTA_BYTES/);
});

// ── restore (H1) ───────────────────────────────────────────────────────

test("the live recording is session state, never storage.local", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  await hx.s.saved();
  assert.ok(hx.sessionStorage.data[RS.STATE_KEY], "in storage.session");
  assert.equal(hx.storage.data[RS.STATE_KEY], undefined);
});

test("a restarted worker picks the recording and its unacked frames back up", async () => {
  const storage = fakeStorage();
  const sessionStorage = fakeStorage();
  const a = harness({ connected: false, storage, sessionStorage });
  await a.s.start({ tabId: 7 });
  a.s.event(ev("click", { at: 1 }), fromTab(7));
  await a.s.saved();

  const b = harness({ connected: false, storage, sessionStorage });
  const st = await b.s.restore();
  assert.equal(st.recording, true);
  assert.equal(st.queued, 2);
  assert.deepEqual(b.listening, [true], "navigation listeners are back");
  b.s.event(ev("click", { at: 2 }), fromTab(7));
  b.online();
  b.s.flush();
  assert.deepEqual(b.wire.map((f) => (f.event ? f.event.id : f.op)), ["start", "e1", "e2"], "numbering continues");
  assert.equal(new Set(b.wire.map((f) => f.id)).size, 3, "frame ids stay unique across the restart");
});

test("a restore whose tab is gone stops with error instead of recording another tab", async () => {
  const storage = fakeStorage();
  const sessionStorage = fakeStorage();
  const a = harness({ storage, sessionStorage });
  await a.s.start({ tabId: 7 });
  await a.s.saved();
  const b = harness({ storage, sessionStorage, tabAlive: false });
  const st = await b.s.restore();
  assert.equal(st.recording, false);
  assert.equal(st.stopReason, "error");
  assert.equal(b.s.outbox().at(-1).op, "stop");
});

test("a browser restart starts clean and closes the recording left in the outbox", async () => {
  const storage = fakeStorage();
  const a = harness({ connected: false, storage });
  await a.s.start({ tabId: 7 });
  a.s.event(ev("click", { at: 1 }), fromTab(7));
  await a.s.saved();
  const id = a.s.status().id;

  const b = harness({ connected: false, storage }); // a fresh session storage, as after a restart
  const st = await b.s.restore({ startup: true });
  assert.equal(st.recording, false);
  const stop = b.s.outbox().at(-1);
  assert.deepEqual([stop.op, stop.recordingId, stop.reason], ["stop", id, "error"]);
});

test("a stop acked as discarded (no events) is not analysable", async () => {
  const hx = harness();
  await hx.s.start({ tabId: 7 });
  await hx.s.stop("user");
  for (const f of hx.wire.splice(0)) {
    hx.s.handleFrame({ id: f.id, success: true, type: "recording", data: f.op === "stop" ? { recordingId: f.recordingId, discarded: "no events" } : undefined });
  }
  const st = hx.s.status();
  assert.equal(st.discarded, true);
  assert.equal(st.delivered, false);
  assert.equal(st.queued, 0);
});
