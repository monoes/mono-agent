// The chat's pure core (chat_core.js): what page context may be sent, how a
// progress frame or a replayed event becomes a message, and what is kept
// across a closed panel.
// `CHROME_PATH=/nonexistent node --test chrome-extension/chat_core.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoChatCore: C } = loadExtensionScripts(["chat_core.js"]);

const ev = (seq, type, payload = {}) => ({ seq, type, payload });
const frame = (seq, stage, payload) => ({ stage, detail: JSON.stringify({ seq, payload }) });

test("page context is capped, trimmed and refused for non-web addresses", () => {
  const big = "x".repeat(50000);
  const c = C.buildContext({ url: "https://a.example/p", title: "  T  ", text: big, selection: big });
  assert.equal(c.title, "T");
  assert.equal(c.text.length, C.TEXT_CAP);
  assert.equal(c.selection.length, C.SELECTION_CAP);
  assert.equal(C.buildContext({ url: "chrome://settings", title: "x", text: "", selection: "" }), null);
  assert.equal(C.buildContext({ url: "javascript:alert(1)" }), null);
  assert.equal(C.buildContext(null), null);
  assert.deepEqual(C.buildContext({ url: "https://a.example/", title: 5, text: null }), { url: "https://a.example/", title: "", text: "", selection: "" });
});

test("a cap never splits an emoji", () => {
  const text = "a".repeat(C.TEXT_CAP - 1) + "😀😀";
  const out = C.buildContext({ url: "https://a.example/", text }).text;
  assert.equal(out.length, C.TEXT_CAP - 1);
  assert.doesNotMatch(out, /[\ud800-\udbff]$/);
});

test("a message is trimmed and an empty one is nothing", () => {
  assert.equal(C.checkMessage("  hi \n"), "hi");
  assert.equal(C.checkMessage("   "), "");
  assert.equal(C.checkMessage(undefined), "");
  assert.equal(C.checkMessage("y".repeat(C.MESSAGE_CAP + 10)).length, C.MESSAGE_CAP);
});

test("progress frames parse as stage + JSON detail, and bad ones are dropped", () => {
  assert.deepEqual(C.parseProgress(frame(3, "assistant.delta", { text: "hi" })), ev(3, "assistant.delta", { text: "hi" }));
  assert.equal(C.parseProgress({ stage: "notice", detail: "not json" }), null);
  assert.equal(C.parseProgress({ stage: "notice", detail: "{\"payload\":{}}" }), null, "no seq");
  assert.equal(C.parseProgress({ detail: "{}" }), null);
  assert.equal(C.parseProgress(null), null);
  assert.deepEqual(C.parseProgress({ stage: "usage.updated", detail: "{\"seq\":1}" }).payload, {});
});

test("replayed events are filtered and ordered", () => {
  const out = C.normalizeEvents([ev(5, "notice"), { type: "x" }, null, ev(2, "assistant.delta"), { seq: 1 }]);
  assert.deepEqual(out.map((e) => e.seq), [2, 5]);
  assert.deepEqual(C.normalizeEvents("nope"), []);
});

test("deltas stream into one assistant message after the user's", () => {
  const s = C.newState(null);
  C.startTurn(s, "hello", "A page");
  C.applyEvent(s, ev(1, "assistant.delta", { text: "Hel" }));
  C.applyEvent(s, ev(2, "assistant.delta", { delta: "lo " }));
  C.applyEvent(s, ev(3, "assistant.delta", { content: "there" }));
  assert.deepEqual(s.messages.map((m) => [m.role, m.text]), [["user", "hello"], ["assistant", "Hello there"]]);
  assert.equal(s.messages[0].page, "A page");
  assert.equal(s.messages[1].open, true);
  C.finishTurn(s, "Hello there");
  assert.equal(s.messages[1].open, false);
  assert.equal(s.messages.length, 2, "the final text does not double the streamed one");
});

test("the final text fills in a reply that streamed nothing", () => {
  const s = C.newState(null);
  C.startTurn(s, "q");
  C.finishTurn(s, "whole answer");
  assert.equal(s.messages.at(-1).text, "whole answer");
  assert.equal(s.messages.at(-1).role, "assistant");
});

test("an event already seen changes nothing: the stream and a replay may overlap", () => {
  const s = C.newState(null);
  C.startTurn(s, "q");
  assert.equal(C.applyEvent(s, ev(1, "assistant.delta", { text: "a" })), true);
  assert.equal(C.applyEvent(s, ev(1, "assistant.delta", { text: "a" })), false);
  assert.equal(C.applyEvent(s, ev(0, "assistant.delta", { text: "z" })), false);
  assert.equal(s.messages.at(-1).text, "a");
  assert.equal(s.lastSeq, 1);
});

test("tools show as running, then done or failed, matched by id then by name", () => {
  const s = C.newState(null);
  C.startTurn(s, "q");
  C.applyEvent(s, ev(1, "tool.started", { id: "t1", name: "read_page" }));
  C.applyEvent(s, ev(2, "tool.started", { id: "t2", name: "search" }));
  C.applyEvent(s, ev(3, "tool.completed", { id: "t2" }));
  C.applyEvent(s, ev(4, "tool.completed", { name: "read_page", error: "boom" }));
  assert.deepEqual(s.messages.at(-1).tools.map((t) => [t.name, t.status]), [["read_page", "failed"], ["search", "done"]]);
});

test("a tool left running when the turn closes is not shown running forever", () => {
  const s = C.newState(null);
  C.startTurn(s, "q");
  C.applyEvent(s, ev(1, "tool.started", { name: "x" }));
  C.finishTurn(s, "done");
  assert.equal(s.messages.at(-1).tools[0].status, "done");
});

test("notices become transcript lines; session.bound names the conversation; usage is ignored", () => {
  const s = C.newState(null);
  C.applyEvent(s, ev(1, "notice", { message: "Rate limited, retrying" }));
  C.applyEvent(s, ev(2, "session.bound", { conversation: "c-9" }));
  C.applyEvent(s, ev(3, "usage.updated", { tokens: 10 }));
  assert.deepEqual(s.messages.map((m) => [m.role, m.text]), [["notice", "Rate limited, retrying"]]);
  assert.equal(s.conversation, "c-9");
  C.applyEvent(s, ev(4, "session.bound", { conversation: "other" }));
  assert.equal(s.conversation, "c-9", "a bound conversation is not swapped mid-chat");
  assert.equal(s.lastSeq, 4);
});

test("a replayed user message is added once, and is not doubled by the one typed locally", () => {
  const s = C.newState(null);
  C.startTurn(s, "hi");
  C.applyEvent(s, ev(1, "user.message", { text: "hi" }));
  assert.equal(s.messages.filter((m) => m.role === "user").length, 1);
  C.applyEvent(s, ev(2, "assistant.delta", { text: "yo" }));
  C.applyEvent(s, ev(3, "user.message", { text: "again" }));
  C.applyEvent(s, ev(4, "assistant.delta", { text: "yo2" }));
  assert.deepEqual(s.messages.map((m) => m.text), ["hi", "yo", "again", "yo2"]);
});

test("a failed turn says so in the transcript and closes the reply", () => {
  const s = C.newState(null);
  C.startTurn(s, "q");
  C.applyEvent(s, ev(1, "assistant.delta", { text: "part" }));
  C.failTurn(s, "went offline");
  assert.equal(s.messages.at(-1).role, "notice");
  assert.equal(s.messages.at(-1).error, true);
  assert.equal(s.messages.find((m) => m.role === "assistant").open, false);
});

function fakeStorage(seed = {}) {
  const data = { ...seed };
  return {
    data,
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, data[k]])),
    set: async (o) => Object.assign(data, o),
  };
}

test("state survives a closed panel, per profile", async () => {
  const st = fakeStorage();
  const s = C.newState(null);
  C.startTurn(s, "q");
  C.applyEvent(s, ev(4, "assistant.delta", { text: "a" }));
  s.conversation = "c-1";
  await C.saveState(st, "p-work", s);

  const back = await C.loadState(st, "p-work");
  assert.equal(back.conversation, "c-1");
  assert.equal(back.lastSeq, 4);
  assert.deepEqual(back.messages.map((m) => m.text), ["q", "a"]);
  assert.equal(back.messages[1].open, true, "a reply cut off by a closed panel carries on from the replay");

  const other = await C.loadState(st, "p-home");
  assert.deepEqual([other.conversation, other.messages.length], ["", 0]);
  assert.ok(C.convKey("p-work") !== C.convKey("p-home"));
});

test("a conversation the worker learned while the panel was closed is picked up and replayed from the start", async () => {
  const st = fakeStorage();
  const s = C.newState(null);
  C.startTurn(s, "first question"); // saved before any reply: no conversation yet
  await C.saveState(st, "", s);
  await C.rememberConversation(st, "", "c-7");
  const back = await C.loadState(st, "");
  assert.equal(back.conversation, "c-7");
  assert.equal(back.lastSeq, 0);
  assert.equal(back.messages[0].text, "first question", "the question is kept");
});

test("New chat elsewhere makes a stored transcript someone else's", async () => {
  const st = fakeStorage();
  const s = C.newState(null);
  s.conversation = "old";
  C.startTurn(s, "q");
  await C.saveState(st, "p", s);
  await C.rememberConversation(st, "p", "new");
  const back = await C.loadState(st, "p");
  assert.deepEqual([back.conversation, back.messages.length, back.lastSeq], ["new", 0, 0]);
});

test("the stored transcript is bounded", async () => {
  const st = fakeStorage();
  const s = C.newState(null);
  for (let i = 0; i < 200; i++) C.startTurn(s, "m" + i);
  s.messages.at(-1).text = "z".repeat(100000);
  await C.saveState(st, "p", s);
  const stored = st.data[C.logKey("p")];
  assert.equal(stored.messages.length, 60);
  assert.ok(stored.messages.at(-1).text.length <= 40000);
});

test("storage that throws leaves a working, empty chat", async () => {
  const broken = { get: async () => { throw new Error("gone"); }, set: async () => { throw new Error("full"); } };
  assert.deepEqual((await C.loadState(broken, "p")).messages, []);
  await C.saveState(broken, "p", C.newState(null));
  await C.rememberConversation(broken, "p", "c");
});

test("a profile id with a path in it is not a key of its own", () => {
  assert.equal(C.convKey("../x"), C.convKey(""));
  assert.equal(C.validProfile("p-work"), true);
  assert.equal(C.validProfile("a/b"), false);
});
