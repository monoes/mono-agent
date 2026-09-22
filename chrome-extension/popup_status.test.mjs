// Tests for what the popup says about the connection.
//
// The bug these exist to prevent is the one that started the redesign: a
// red "Disconnected" with no cause and no fix. So the assertions are mostly
// about wording and tone rather than internals — whether a state that is
// nobody's fault is coloured like a fault, whether a state with a one-line
// fix actually carries the line.
//
// `node --test 'chrome-extension/*.test.mjs'`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoPopupStatus: S } = loadExtensionScripts(["popup_status.js"]);

const at = (since, now) => ({ since, now });

test("a connection that is working says so and nothing else", () => {
  const view = S.describe({ status: "connected" });
  assert.equal(view.key, "connected");
  assert.equal(view.tone, "ok");
  assert.equal(view.label, "Connected");
  // Nothing to explain: no block is drawn at all.
  assert.equal(view.title, "");
  assert.equal(view.body, "");
  assert.equal(view.action, null);
  assert.equal(view.queues, false);
});

test("before the worker has answered, the popup is checking — not disconnected", () => {
  for (const status of ["", null, undefined, "checking"]) {
    const view = S.describe({ status });
    assert.equal(view.key, "checking", `for ${JSON.stringify(status)}`);
    assert.equal(view.tone, "busy");
    assert.equal(view.busy, true);
    assert.equal(view.title, "");
  }
});

test("no bridge running is not an error, and carries the command that fixes it", () => {
  const view = S.describe({ status: "disconnected", reason: "no_bridge" });
  assert.equal(view.key, "no_bridge");
  // The whole point: never red, never "error".
  assert.equal(view.tone, "idle");
  assert.notEqual(view.tone, "error");
  assert.equal(view.label, "Bridge not running");
  assert.match(view.title, /No bridge is running/);
  assert.match(view.body, /held on this machine/);
  assert.equal(view.command, S.START_COMMAND);
  assert.equal(view.queues, true);
});

test("a bridge that is up with nothing attached is readiness, not a fault", () => {
  // The health endpoint's "waiting". This is what a suspended MV3 service
  // worker looks like from outside, which is most of the time — so it gets
  // no block, no warning colour, and no claim that captures will be held.
  const view = S.describe({ status: "waiting" });
  assert.equal(view.key, "waiting");
  assert.equal(view.tone, "ok");
  assert.equal(view.label, "Bridge ready");
  assert.equal(view.title, "");
  assert.equal(view.body, "");
  assert.equal(view.action, null);
  assert.equal(view.queues, false);
});

test("waiting is never talked down into a disconnection", () => {
  // Age and history are what turn a drop into an alarm elsewhere; neither
  // may do that here, because nothing is wrong.
  for (const extra of [{}, { everConnected: true }, { since: 1, now: 10 ** 9 }]) {
    const view = S.describe(Object.assign({ status: "waiting" }, extra));
    assert.equal(view.key, "waiting", JSON.stringify(extra));
    assert.equal(view.tone, "ok");
  }
});

test("a dropped socket is reported as reconnecting while it is young", () => {
  const view = S.describe({ status: "disconnected", ...at(1000, 3000) });
  assert.equal(view.key, "reconnecting");
  assert.equal(view.tone, "busy");
  assert.equal(view.busy, true);
  // Transient states never open the explanation block — that is what makes
  // a service-worker respawn invisible instead of alarming.
  assert.equal(view.title, "");
});

test("a socket that stays down stops being called reconnecting", () => {
  const young = S.describe({ status: "disconnected", ...at(1000, 1000 + S.RECONNECT_GRACE_MS - 1) });
  const old = S.describe({ status: "disconnected", ...at(1000, 1000 + S.RECONNECT_GRACE_MS + 1) });
  assert.equal(young.key, "reconnecting");
  assert.equal(old.key, "offline");
  assert.equal(old.tone, "idle");
  assert.equal(old.command, S.START_COMMAND);
});

test("a drop after a connection this popup saw is always a blip", () => {
  // The MV3 case: the worker was killed while idle. Age is irrelevant —
  // having seen it work is enough to withhold the alarm.
  const view = S.describe({
    status: "disconnected",
    everConnected: true,
    ...at(1000, 1000 + S.RECONNECT_GRACE_MS * 10),
  });
  assert.equal(view.key, "reconnecting");
  assert.equal(view.title, "");
});

test("a settled cause is never softened by the grace period", () => {
  // no_bridge and bad_url are facts, not blips: they must not be swallowed
  // by "it only just happened", however fresh they are.
  const fresh = at(1000, 1001);
  assert.equal(S.describe({ status: "disconnected", reason: "no_bridge", ...fresh }).key, "no_bridge");
  assert.equal(S.describe({ status: "disconnected", reason: "bad_url", ...fresh }).key, "bad_url");
  assert.equal(
    S.describe({ status: "disconnected", reason: "no_bridge", everConnected: true, ...fresh }).key,
    "no_bridge"
  );
});

test("offline names where it looked, so the address is checkable", () => {
  const view = S.describe({
    status: "disconnected",
    wsUrl: "ws://127.0.0.1:9333/monoagent",
    ...at(1, 1 + S.RECONNECT_GRACE_MS + 1),
  });
  assert.match(view.title, /127\.0\.0\.1:9333/);
  assert.doesNotMatch(view.title, /monoagent$/, "the path is noise in a sentence");
  assert.equal(view.action.id, S.ACTIONS.RETRY);
});

test("never paired and refused are told apart", () => {
  const never = S.describe({ status: "unpaired" });
  const refused = S.describe({ status: "unpaired", reason: "auth_rejected" });

  assert.equal(never.key, "unpaired");
  assert.equal(refused.key, "rejected");
  assert.notEqual(never.title, refused.title);
  // Same fix, and both say it out loud with a button that goes there.
  assert.equal(never.command, S.PAIR_COMMAND);
  assert.equal(refused.command, S.PAIR_COMMAND);
  assert.equal(never.action.id, S.ACTIONS.PAIR);
  assert.equal(refused.action.id, S.ACTIONS.PAIR);
  assert.equal(never.tone, "warn");
});

test("a rejected token is a pairing problem whatever status carries it", () => {
  const view = S.describe({ status: "disconnected", reason: "auth_rejected" });
  assert.equal(view.key, "rejected");
  assert.equal(view.action.id, S.ACTIONS.PAIR);
});

test("connecting distinguishes a first attempt from a retry", () => {
  assert.equal(S.describe({ status: "connecting" }).label, "Connecting…");
  assert.equal(S.describe({ status: "connecting", everConnected: true }).label, "Reconnecting…");
  assert.equal(S.describe({ status: "connecting", everConnected: true }).key, "reconnecting");
});

test("every state that stops captures reaching the bridge says they are kept", () => {
  const blocked = [
    { status: "connecting" },
    { status: "unpaired" },
    { status: "disconnected", reason: "no_bridge" },
    { status: "disconnected", reason: "bad_url" },
    { status: "disconnected", ...at(1, 1 + S.RECONNECT_GRACE_MS + 1) },
  ];
  for (const input of blocked) {
    assert.equal(S.describe(input).queues, true, JSON.stringify(input));
  }
  assert.equal(S.describe({ status: "connected" }).queues, false);
});

test("a worker's own sentence outranks ours", () => {
  const view = S.describe({
    status: "disconnected",
    reason: "no_bridge",
    detail: "The bridge stopped when the workflow finished.",
  });
  assert.equal(view.body, "The bridge stopped when the workflow finished.");
  // Still carries the fix — a better sentence does not cost the command.
  assert.equal(view.command, S.START_COMMAND);
});

test("an unknown status from a newer worker is reported, not invented", () => {
  const view = S.describe({ status: "degraded" });
  assert.equal(view.key, "unknown");
  assert.equal(view.label, "degraded");
  assert.equal(view.tone, "idle", "we do not know that anything is wrong");
});

test("every state has a pill label and a usable tone", () => {
  const tones = new Set(["ok", "busy", "idle", "warn", "error"]);
  const inputs = [
    {},
    { status: "connected" },
    { status: "waiting" },
    { status: "connecting" },
    { status: "unpaired" },
    { status: "disconnected" },
    { status: "disconnected", reason: "no_bridge" },
    { status: "disconnected", reason: "bad_url" },
    { status: "disconnected", reason: "auth_rejected" },
    { status: "weird" },
  ];
  for (const input of inputs) {
    const view = S.describe(input);
    assert.ok(view.label, `label for ${JSON.stringify(input)}`);
    assert.ok(tones.has(view.tone), `tone for ${JSON.stringify(input)}`);
    // A block that opens must say something; a title with no body is a
    // headline with no answer, which is the thing being fixed.
    if (view.title) assert.ok(view.body, `body for ${JSON.stringify(input)}`);
  }
});

test("a state that asks you to run something always shows the command", () => {
  for (const input of [
    { status: "unpaired" },
    { status: "disconnected", reason: "no_bridge" },
    { status: "disconnected", ...at(1, 1 + S.RECONNECT_GRACE_MS + 1) },
  ]) {
    const view = S.describe(input);
    assert.ok(view.command, `command for ${JSON.stringify(input)}`);
    assert.match(view.command, /^monoagentcli /);
  }
});

// ── the address in a sentence ────────────────────────────────────────

test("hostOf keeps the part worth reading", () => {
  assert.equal(S.hostOf("ws://127.0.0.1:9222/monoagent"), "127.0.0.1:9222");
  assert.equal(S.hostOf("wss://localhost:1234/x?y=1"), "localhost:1234");
  assert.equal(S.hostOf(""), "127.0.0.1:9222");
  assert.equal(S.hostOf(null), "127.0.0.1:9222");
  assert.equal(S.hostOf("not a url"), "not a url");
});

// ── the queue ────────────────────────────────────────────────────────

test("an empty queue is drawn as nothing at all", () => {
  assert.equal(S.describeQueue({ queued: 0, failed: 0 }), null);
  assert.equal(S.describeQueue({}), null);
  assert.equal(S.describeQueue(null), null);
});

test("the queue counts what is waiting, in a sentence, and counts one as one", () => {
  assert.equal(S.describeQueue({ queued: 1, failed: 0 }).text, "1 capture is waiting to send");
  assert.equal(S.describeQueue({ queued: 3, failed: 0 }).text, "3 captures are waiting to send");
  assert.equal(S.describeQueue({ queued: 0, failed: 1 }).text, "1 capture couldn't be sent");
  assert.equal(S.describeQueue({ queued: 0, failed: 2 }).text, "2 captures couldn't be sent");
  assert.equal(S.describeQueue({ queued: 2, failed: 1 }).text, "2 captures waiting, 1 failed");
});

test("waiting is a warning, failed is an error", () => {
  assert.equal(S.describeQueue({ queued: 4, failed: 0 }).tone, "warn");
  assert.equal(S.describeQueue({ queued: 0, failed: 1 }).tone, "error");
  assert.equal(S.describeQueue({ queued: 1, failed: 1 }).tone, "error");
});

test("nonsense counts do not produce nonsense sentences", () => {
  assert.equal(S.describeQueue({ queued: -3, failed: 0 }), null);
  assert.equal(S.describeQueue({ queued: "2", failed: null }).text, "2 captures are waiting to send");
});
