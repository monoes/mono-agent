// What the bridge says about the monoes.me account, read from its frames.
//
// The case that matters: a locked state that only LOOKS locked (before the
// enforcement date the bridge still answers everything, and `ping` says "locked"
// all the same) must not make the side panel tell anyone to sign in. These pin
// what counts as "refusing", and that a refusal is read from the frame the
// bridge really sends: a failed reply has no "ok" key (Reply.OK is omitempty in
// internal/extension/request.go), so `ok === false` never appears on the wire.
//
// `node --test 'chrome-extension/**/*.test.mjs'`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoAccount: A } = loadExtensionScripts(["account_state.js"]);

const reply = (extra) => Object.assign({ kind: "reply", id: "req-1" }, extra);
const ping = (account) => reply({ ok: true, data: { pong: true, methods: ["ping"], account } });

test("a refused request proves the account is locked and enforced", () => {
  const account = A.fromReply(reply({ code: "account_locked", error: "Log in to monoes.me first: monoagentcli account login" }));
  assert.deepEqual(account, { state: "locked", reason: "", enforced: true });
  assert.equal(A.refusing(account), true);
});

test("a refusal has no ok key on the wire, and is read from its code alone", () => {
  // Exactly what internal/extension/request.go marshals for a failed reply.
  const wire = JSON.parse('{"kind":"reply","id":"req-1","error":"Log in to monoes.me first: monoagentcli account login","code":"account_locked"}');
  assert.equal("ok" in wire, false);
  assert.deepEqual(A.fromReply(wire), { state: "locked", reason: "", enforced: true });
  assert.deepEqual(A.fromReply({ ...wire, ok: false }), { state: "locked", reason: "", enforced: true }, "ask.js documents ok:false: it reads the same");
});

test("ping carries the state, with the reason", () => {
  assert.deepEqual(A.fromReply(ping({ state: "locked", reason: "refused", enforced: true })), { state: "locked", reason: "refused", enforced: true });
  assert.deepEqual(A.fromReply(ping({ state: "ok" })), { state: "ok", reason: "", enforced: true });
  assert.deepEqual(A.fromReply(ping({ state: "grace", reason: "unreachable" })), { state: "grace", reason: "unreachable", enforced: true });
});

test("locked before the enforcement date is not a refusal", () => {
  const account = A.fromReply(ping({ state: "locked", reason: "not_logged_in", enforced: false }));
  assert.equal(account.enforced, false);
  assert.equal(A.refusing(account), false);
  assert.equal(A.refusing({ state: "locked", reason: "not_logged_in" }), false, "an account that does not say it is enforced is no refusal");
});

test("a locked state with no word on enforcement proves nothing: it is null, so it neither invents a refusal nor clears one", () => {
  // A bridge that sends only {state, reason, valid_until} cannot be told from the warn period.
  assert.equal(A.fromReply(ping({ state: "locked", reason: "not_logged_in" })), null);
  const t = A.tracker();
  const take = (frame) => {
    const said = A.fromReply(frame);
    if (said) t.set(said);
  };
  take(ping({ state: "locked" }));
  assert.equal(t.refusing(), false, "nothing was proven");
  take(reply({ code: "account_locked" }));
  assert.equal(t.refusing(), true);
  take(ping({ state: "locked" }));
  assert.equal(t.refusing(), true, "an ambiguous ping does not clear a proven refusal");
  take(ping({ state: "ok" }));
  assert.equal(t.refusing(), false, "a signed-in ping does");
});

test("a successful answer to anything but ping proves the bridge is not refusing", () => {
  assert.deepEqual(A.fromReply(reply({ ok: true, data: { saved: false } })), { state: "ok", reason: "", enforced: true });
  assert.deepEqual(A.fromReply(reply({ ok: true })), { state: "ok", reason: "", enforced: true });
  assert.equal(A.fromReply(ping(undefined)), null, "a ping that says nothing about the account proves nothing");
});

test("signed-in and in-grace bridges are not refusing", () => {
  for (const state of ["ok", "grace"]) assert.equal(A.refusing({ state, reason: "", enforced: true }), false, state);
});

test("a frame that says nothing about the account says nothing", () => {
  const silent = [
    null,
    undefined,
    "reply",
    {},
    reply({ ok: true, data: { pong: true, methods: [] } }), // a bridge that predates the gate, or a ping that does not say
    reply({ ok: true, data: { account: "locked" } }),
    reply({ ok: true, data: { account: { reason: "refused" } } }),
    reply({ code: "unavailable", error: "monomind is not installed" }), // a failed reply: no ok key
    reply({ code: "timeout" }),
    reply({ ok: false, code: "unavailable" }),
    reply({ progress: { stage: "searching" } }),
    { kind: "request", id: "x", method: "ping" },
    { id: "1", success: true, data: { account: { state: "locked" } } }, // a command response, not a reply
  ];
  for (const frame of silent) assert.equal(A.fromReply(frame), null, JSON.stringify(frame));
  assert.equal(A.refusing(null), false);
  assert.equal(A.refusing(undefined), false);
});

test("an unknown account is not refusing: a bridge that predates the gate never says", () => {
  const t = A.tracker();
  assert.equal(t.get(), null);
  assert.equal(t.refusing(), false);
});

test("the tracker tells its listeners when the account changes, and whether it was refusing", () => {
  const t = A.tracker();
  const seen = [];
  t.onChange((account, wasRefusing) => seen.push([account && account.state, wasRefusing]));

  t.set({ state: "locked", reason: "not_logged_in", enforced: true });
  assert.equal(t.refusing(), true);
  t.set({ state: "locked", reason: "not_logged_in", enforced: true }); // the same again: nothing new
  t.set({ state: "ok", reason: "", enforced: true });
  assert.equal(t.refusing(), false);
  t.set(null); // the socket closed
  assert.deepEqual(seen, [["locked", false], ["ok", true], [null, false]]);
});

test("a listener that throws cannot stop the next one", () => {
  const t = A.tracker();
  const seen = [];
  t.onChange(() => {
    throw new Error("decoration");
  });
  t.onChange((account) => seen.push(account.state));
  t.set({ state: "ok", reason: "", enforced: true });
  assert.deepEqual(seen, ["ok"]);
});
