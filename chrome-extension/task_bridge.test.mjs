// The task bridge in the worker (task board spec 11.1 to 11.4): a task is
// queued, then sent one request at a time under the id it was queued with;
// it goes to the right profile with the page's address from the tab; nothing
// leaves the outbox unless MonoAgent took it or refused it for good.
// `CHROME_PATH=/nonexistent node --test chrome-extension/task_bridge.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { fakeAsk, refuse, setupTasks, send, PAGE, PANEL, until } from "./task_harness.mjs";

const waiting = (local) => (local.data.taskOutbox || []).length;
const entries = (n) =>
  Array.from({ length: n }, (_, i) => ({ client_id: `t-${i}`, text: `task ${i}`, url: "", title: "", kind: "note", profile: "p-work", at: "2026-10-06T10:00:00.000Z" }));

test("a selection from the page is queued, sent and reported, with the address from the tab", async () => {
  const ask = fakeAsk({ reply: () => ({ id: 12, created: true }) });
  const { listeners, record, local } = setupTasks({ ask });
  const out = await send(listeners, { type: "task_add", text: "Reply to Sam\nabout the invoice", url: "https://spoofed.example/", title: "Spoofed" }, PAGE);

  assert.equal(out.status, "added");
  assert.deepEqual(out.feedback, { level: "ok", text: "Added to Inbox in Work (#12)" });
  assert.equal(ask.calls.length, 1);
  assert.equal(ask.calls[0].method, "task.add");
  const p = ask.calls[0].params;
  assert.deepEqual([p.profile, p.kind, p.text, p.title], ["p-work", "selection", "Reply to Sam\nabout the invoice", "Inbox (3)"]);
  assert.equal(p.url, "https://mail.example/inbox?token=REDACTED&q=1", "the tab's address, without user-info, token or fragment");
  assert.match(p.client_id, /^t-[A-Za-z0-9-]{8,62}$/);
  assert.equal(waiting(local), 0, "a task MonoAgent took leaves the outbox");
  assert.deepEqual(record.toasts.at(-1), { tabId: 42, text: "Added to Inbox in Work (#12)", level: "ok" });
});

test("a task MonoAgent already had (a resend) leaves the outbox too", async () => {
  const ask = fakeAsk({ reply: () => ({ id: 9, created: false }) });
  const { listeners, local } = setupTasks({ ask });
  const out = await send(listeners, { type: "task_add", text: "once" }, PAGE);
  assert.equal(out.feedback.text, "Added to Inbox in Work (#9)");
  assert.equal(waiting(local), 0);
});

test("a note typed in the side panel goes to the profile the panel shows, about no page", async () => {
  const ask = fakeAsk();
  const { listeners, record } = setupTasks({ ask });
  await send(listeners, { type: "task_add", text: "Call the bank", profile: "p-home", url: "https://x.example/" }, PANEL);
  const p = ask.calls[0].params;
  assert.deepEqual([p.profile, p.kind, p.url, p.title, p.text], ["p-home", "note", "", "", "Call the bank"]);
  assert.equal(record.toasts.length, 0, "the panel shows its own answer; there is no page to toast in");
});

test("a page cannot pick the profile or touch the refusals; a side panel open in a tab is still the panel", async () => {
  const ask = fakeAsk();
  const { listeners, local } = setupTasks({ ask });
  await send(listeners, { type: "task_add", text: "x", profile: "p-home" }, PAGE);
  assert.equal(ask.calls[0].params.profile, "p-work", "a content script's profile is ignored");
  assert.equal((await send(listeners, { type: "task_add", text: "x", profile: "../etc" }, PANEL)).status, "no_profile");

  local.data.taskOutboxFailed = [{ client_id: "t-old", title: "Old", text: "Old", profile: "p-work", reason: "gone", at: "" }];
  assert.equal(await send(listeners, { type: "task_state" }, PAGE), undefined, "a page cannot read the refused tasks");
  assert.equal(await send(listeners, { type: "task_dismiss" }, PAGE), undefined, "nor clear them");
  assert.equal(local.data.taskOutboxFailed.length, 1);

  const inTab = { id: "ext-id", url: PANEL.url, tab: { id: 9, windowId: 7, url: PANEL.url, title: "MonoAgent" } };
  await send(listeners, { type: "task_add", text: "typed in a tab", profile: "p-home" }, inTab);
  const p = ask.calls.at(-1).params;
  assert.deepEqual([p.kind, p.profile, p.url, p.title], ["note", "p-home", "", ""]);
  assert.equal(await send(listeners, { type: "task_add", text: "x" }, { id: "another-extension", tab: PAGE.tab }), undefined);
  assert.equal(ask.calls.length, 2);
});

test("a page's address is the frame that sent it, not where the tab has gone since", async () => {
  const ask = fakeAsk();
  const { listeners } = setupTasks({ ask });
  const sender = { id: "ext-id", frameId: 0, url: "https://mail.example/thread/1", tab: { id: 42, url: "https://mail.example/thread/2", title: "Thread 2" } };
  await send(listeners, { type: "task_add", text: "Reply to Sam" }, sender);
  assert.equal(ask.calls[0].params.url, "https://mail.example/thread/1");
});

test("with no profile, nothing is queued and the person is told to choose one", async () => {
  const ask = fakeAsk();
  const { listeners, local, record } = setupTasks({ ask, profile: null, connected: false });
  const fromPage = await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  const fromPanel = await send(listeners, { type: "task_add", text: "Call the bank", profile: "" }, PANEL);
  for (const out of [fromPage, fromPanel]) {
    assert.equal(out.status, "no_profile");
    assert.equal(out.feedback.text, 'Choose a profile first: pick one under "Saving into" in MonoAgent\'s side panel');
  }
  assert.equal(waiting(local), 0);
  assert.equal(ask.calls.length, 0);
  assert.equal(record.toasts.at(-1).text, fromPage.feedback.text);
});

test("empty text adds nothing, and text is cut to 64 KiB before it is queued", async () => {
  const ask = fakeAsk();
  const { listeners, local } = setupTasks({ ask });
  assert.equal((await send(listeners, { type: "task_add", text: "  \n " }, PAGE)).feedback.text, "Nothing to add: select some text first");
  assert.equal((await send(listeners, { type: "task_add", text: "", profile: "p-work" }, PANEL)).feedback.text, "Type a task first");
  assert.equal(waiting(local), 0);
  await send(listeners, { type: "task_add", text: "a".repeat(70000) }, PAGE);
  assert.equal(ask.calls[0].params.text.length, 64 * 1024);
});

test("offline, the task waits, says so, keeps no secret, and goes when the bridge connects", async () => {
  const ask = fakeAsk({ reply: () => ({ id: 3, created: true }) });
  const { listeners, local, record, net, B } = setupTasks({ ask, connected: false });
  const out = await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  assert.equal(out.status, "queued");
  assert.deepEqual(out.feedback, { level: "warn", text: "Saved: will sync when MonoAgent is running" });
  assert.equal(waiting(local), 1);
  const stored = JSON.stringify(local.data.taskOutbox);
  for (const secret of ["hunter2", "token=abc", "msg-3"]) assert.ok(!stored.includes(secret), `${secret} was stored in chrome.storage`);
  assert.deepEqual(record.alarms.get(B.ALARM), { periodInMinutes: 1 });

  net.up = true;
  await B.connected();
  assert.equal(ask.calls.length, 1);
  assert.equal(waiting(local), 0);
  assert.equal(record.alarms.has(B.ALARM), false, "no alarm while nothing waits");
});

test("a page with no title of its own: a title that is only its address is never queued or sent", async () => {
  const ask = fakeAsk({ reply: () => ({ id: 4, created: true }) });
  const { listeners, local, net, B } = setupTasks({ ask, connected: false });
  const bare = { id: "ext-id", tab: Object.assign({}, PAGE.tab, { title: "mail.example/inbox?token=abc&q=1#msg-3" }) };
  await send(listeners, { type: "task_add", text: "Reply to Sam" }, bare);
  assert.equal(waiting(local), 1);
  assert.ok(!JSON.stringify(local.data.taskOutbox).includes("token=abc"), "the token was stored in chrome.storage by way of the title");

  net.up = true;
  await B.connected();
  const p = ask.calls[0].params;
  assert.deepEqual([p.title, p.url], ["", "https://mail.example/inbox?token=REDACTED&q=1"]);
  assert.ok(!JSON.stringify(p).includes("token=abc"), "the token was sent by way of the title");
});

test("the minute alarm flushes what waits; another alarm does not", async () => {
  const ask = fakeAsk();
  const { listeners, local, net, B } = setupTasks({ ask, connected: false });
  await send(listeners, { type: "task_add", text: "later" }, PAGE);
  net.up = true;
  for (const fn of listeners.alarm) fn({ name: "monoagent-keepalive" });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(ask.calls.length, 0, "the keep-alive alarm is not ours");
  for (const fn of listeners.alarm) fn({ name: B.ALARM });
  await until(() => waiting(local) === 0, "the alarm's flush");
  assert.equal(ask.calls.length, 1);
});

test("an older MonoAgent without task.add keeps the task and says it needs updating", async () => {
  const ask = fakeAsk({ methods: ["ping", "doc.lookup"] });
  const { listeners, local } = setupTasks({ ask });
  const out = await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  assert.equal(out.feedback.text, "Saved: MonoAgent needs updating before it can take tasks; it syncs once updated");
  assert.equal(ask.calls.length, 0);
  assert.equal(waiting(local), 1);
});

test("a bridge that never answered the probe is offline, not old", async () => {
  const { listeners } = setupTasks({ ask: fakeAsk({ methods: null }) });
  const out = await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  assert.equal(out.feedback.text, "Saved: will sync when MonoAgent is running");
});

test("a refusal that would repeat drops the task and reports it, once, with its text", async () => {
  const ask = fakeAsk({
    reply: () => {
      throw refuse("invalid_input", 'invalid input: unknown profile "p-work"');
    },
  });
  const { listeners, local, record } = setupTasks({ ask });
  const out = await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  assert.equal(out.status, "refused");
  assert.deepEqual(out.feedback, { level: "error", text: 'Not added: invalid input: unknown profile "p-work"' });
  assert.equal(waiting(local), 0, "it is not retried");
  assert.deepEqual([local.data.taskOutboxFailed[0].title, local.data.taskOutboxFailed[0].text], ["Reply to Sam", "Reply to Sam"]);
  assert.equal(record.broadcasts.filter((m) => m.type === "task_result").length, 1, "an open side panel hears of it once");
});

test("busy and internal stop the flush, a full board's task waits while the flush goes on, and a resend keeps its id", async () => {
  let answer = () => {
    throw refuse("busy", "too many requests in flight (max 8)");
  };
  const ask = fakeAsk({ reply: (p) => answer(p) });
  const { listeners, local, net, B } = setupTasks({ ask, connected: false });
  await send(listeners, { type: "task_add", text: "first" }, PAGE);
  await send(listeners, { type: "task_add", text: "second" }, PAGE);
  net.up = true;
  await B.flush();
  assert.equal(ask.calls.length, 1, "nothing is sent after busy");
  answer = () => {
    throw refuse("internal", "MonoAgent could not add the task; it is kept and tried again");
  };
  await B.flush();
  assert.equal(ask.calls.length, 2, "nothing is sent after internal");
  assert.equal(waiting(local), 2);

  answer = (p) => {
    if (p.text === "first") throw refuse("limit", "this profile already has 2000 open tasks: archive some first");
    return { id: 5, created: true };
  };
  await B.flush();
  assert.deepEqual(local.data.taskOutbox.map((e) => e.text), ["first"], "the full board's task waits; the next one went");
  const ids = ask.calls.filter((c) => c.params.text === "first").map((c) => c.params.client_id);
  assert.equal(ids.length, 3);
  assert.equal(new Set(ids).size, 1, "every attempt at one task carries the id it was queued with");
});

test("a burst of 200 waiting tasks drains one request at a time, oldest first, each once", async () => {
  const ask = fakeAsk();
  const { local, B } = setupTasks({ ask });
  local.data.taskOutbox = entries(200);
  await B.flush();
  assert.deepEqual(ask.calls.map((c) => c.params.client_id), entries(200).map((e) => e.client_id));
  assert.equal(ask.most(), 1, "the bridge allows 8 in flight; a flush never uses more than one");
  assert.equal(waiting(local), 0);
});

test("two flushes at once send each task once", async () => {
  const ask = fakeAsk();
  const { local, B } = setupTasks({ ask });
  local.data.taskOutbox = entries(2);
  await Promise.all([B.flush(), B.flush(), B.connected()]);
  assert.deepEqual(ask.calls.map((c) => c.params.client_id), ["t-0", "t-1"]);
});

test("two adds a moment apart both reach MonoAgent, and both say so", async () => {
  const ask = fakeAsk();
  const { listeners, local } = setupTasks({ ask });
  const [a, b] = await Promise.all([
    send(listeners, { type: "task_add", text: "one" }, PAGE),
    send(listeners, { type: "task_add", text: "two" }, PAGE),
  ]);
  assert.deepEqual([a.status, b.status], ["added", "added"]);
  assert.deepEqual(ask.calls.map((c) => c.params.text).sort(), ["one", "two"]);
  assert.equal(waiting(local), 0);
});

test("a queued task keeps the profile it was queued under", async () => {
  const ask = fakeAsk();
  const { listeners, local, net, B } = setupTasks({ ask, connected: false });
  await send(listeners, { type: "task_add", text: "for work" }, PAGE);
  local.data.captureProfile = "p-home";
  net.up = true;
  await B.flush();
  assert.equal(ask.calls[0].params.profile, "p-work");
});

test("the side panel reads what waits and what was refused, and dismisses the refusals", async () => {
  const ask = fakeAsk({
    reply: () => {
      throw refuse("invalid_input", "gone");
    },
  });
  const { listeners, local } = setupTasks({ ask });
  await send(listeners, { type: "task_add", text: "Reply to Sam" }, PAGE);
  const state = await send(listeners, { type: "task_state" }, PANEL);
  assert.equal(state.waiting, 0);
  assert.equal(state.failures.length, 1);
  assert.deepEqual(await send(listeners, { type: "task_dismiss" }, PANEL), { ok: true });
  assert.deepEqual(local.data.taskOutboxFailed, []);
});

test("feedbackFor says every outcome in one line", () => {
  const { B } = setupTasks();
  assert.deepEqual(B.feedbackFor("queued", { code: "busy", reason: "too many requests in flight (max 8)" }), {
    level: "warn",
    text: "Saved: will sync when MonoAgent can take it (too many requests in flight (max 8))",
  });
  assert.deepEqual(B.feedbackFor("queued", { code: "limit", reason: "this profile already has 2000 open tasks: archive some first" }), {
    level: "error",
    text: "Not added yet: this profile already has 2000 open tasks: archive some first. It stays queued and is tried again every minute",
  });
  assert.equal(B.feedbackFor("queued", { code: "internal", reason: "x" }).level, "error");
  assert.equal(B.feedbackFor("full", { reason: "200 tasks are already waiting to sync" }).text, "Not added: 200 tasks are already waiting to sync; start MonoAgent to send them");
  assert.equal(B.feedbackFor("empty", { kind: "page" }).text, "This page has no title or address to add");
});

test("pageUrl keeps an http or https address within 2048 bytes, and fails closed", () => {
  const { B, env } = setupTasks();
  for (const raw of ["chrome-extension://abc/x.html", "file:///etc/hosts", "javascript:alert(1)", "not a url", ""]) {
    assert.equal(B.pageUrl(raw), "", raw);
  }
  const base = "https://x.example/";
  assert.equal(B.pageUrl(base + "a".repeat(2048 - base.length)).length, 2048, "exactly the limit is kept");
  assert.equal(B.pageUrl(base + "a".repeat(2049 - base.length)), "", "one byte over is dropped");
  delete env.MonoRecorderPrivacy;
  assert.equal(B.pageUrl("https://x.example/a"), "", "no sanitizer, no address");
});

test("pageTitle drops a title that is only the page's address, and keeps any other", () => {
  const { B } = setupTasks();
  const url = "https://mail.example/inbox?token=abc&q=1#msg-3";
  // What Chrome is believed to give a page with no <title>: its address, query string and all.
  for (const [title, at] of [
    ["mail.example/inbox?token=abc&q=1", url],
    ["  mail.example/inbox  ", url],
    ["https://mail.example/inbox?token=abc&q=1#msg-3", url],
    ["HTTPS://Mail.Example/Inbox?Token=abc", url],
    ["ftp://files.example/x?token=abc", ""],
    ["mail.example", "https://mail.example/"],
    ["www.example.com/a?x=1", "https://example.com/a?x=1"],
    ["example.com/a?x=1", "https://www.example.com/a?x=1"],
    ["localhost:3000/app?token=abc", "http://localhost:3000/app?token=abc"],
    ["mail.example/older?token=abc", url],
  ]) {
    assert.equal(B.pageTitle({ title, url: at }), "", title);
  }

  // Any other title stays, even one that has the host in it.
  for (const title of ["Inbox (3)", "Why mail.example is slow today", "mail.example is down", "mail.example - Inbox (3)", "mail.example: the inbox"]) {
    assert.equal(B.pageTitle({ title, url }), title, title);
  }
  assert.equal(B.pageTitle({ title: "  Inbox (3) ", url }), "  Inbox (3) ", "a title is returned as it came");

  // With no http or https address to compare with, only a scheme gives a title away.
  for (const at of ["", "not a url", "chrome://newtab/", "file:///home/sam/mail.example/inbox"]) {
    assert.equal(B.pageTitle({ title: "mail.example/inbox?token=abc", url: at }), "mail.example/inbox?token=abc", at);
  }
  assert.equal(B.pageTitle({ title: "/home/sam/notes.txt", url: "file:///home/sam/notes.txt" }), "/home/sam/notes.txt", "no host, nothing to match");
  for (const page of [undefined, null, {}, { title: null }, { title: 7 }, { title: "  " }]) assert.equal(B.pageTitle(page), "");
});

test("the badge counts a waiting task, then the same task refused", async () => {
  const ask = fakeAsk({
    reply: () => {
      throw refuse("invalid_input", "gone");
    },
  });
  const { listeners, record, net, B } = setupTasks({ ask, connected: false });
  await send(listeners, { type: "task_add", text: "later" }, PAGE);
  assert.equal(record.badges.at(-1), "1");
  assert.equal(record.titles.at(-1), "1 task waiting for the bridge");
  net.up = true;
  await B.flush();
  assert.equal(record.badges.at(-1), "1");
  assert.match(record.titles.at(-1), /^1 task was not added/);
});
