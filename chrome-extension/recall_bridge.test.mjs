// RCL-04's join to the capture path, and RCL-02's join to the capture badge.
//
// The pieces are tested on their own elsewhere; what is pinned here is that
// they are actually connected — that highlighting a page and then saving it
// produces an envelope with `highlights.json` in it, and that saving a page
// stops the badge claiming it is unsaved.
//
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const PAGE_URL = "https://paper.test/lighthouse";

/** fakeStorage is chrome.storage.local's three methods, in a Map. */
function fakeStorage() {
  const data = {};
  return {
    data,
    get: async (key) => (key in data ? { [key]: data[key] } : {}),
    set: async (values) => Object.assign(data, values),
    remove: async (key) => delete data[key],
  };
}

/**
 * chromeStub is the slice of the extension APIs recall_bridge.js reaches
 * for at install time. It registers real listeners into arrays, so a test
 * can fire an event without a browser.
 */
function chromeStub() {
  const listeners = { message: [], updated: [], activated: [], removed: [], command: [] };
  const badges = [];
  return {
    listeners,
    badges,
    runtime: {
      onMessage: { addListener: (fn) => listeners.message.push(fn) },
      sendMessage: () => Promise.resolve(),
    },
    tabs: {
      onUpdated: { addListener: (fn) => listeners.updated.push(fn) },
      onActivated: { addListener: (fn) => listeners.activated.push(fn) },
      onRemoved: { addListener: (fn) => listeners.removed.push(fn) },
      query: async () => [{ id: 7, url: PAGE_URL }],
      get: async () => ({ id: 7, url: PAGE_URL }),
      sendMessage: async () => ({ ok: true }),
      create: async () => ({ id: 8 }),
    },
    action: {
      setBadgeText: (spec) => {
        badges.push(spec);
        return Promise.resolve();
      },
      setBadgeBackgroundColor: () => Promise.resolve(),
      setTitle: () => Promise.resolve(),
    },
    commands: { onCommand: { addListener: (fn) => listeners.command.push(fn) } },
    storage: { local: fakeStorage() },
  };
}

/** installed brings up the whole recall group against a fake socket. */
function installed(answers = {}) {
  const chrome = chromeStub();
  const sandbox = loadExtensionScripts(
    ["ask.js", "saved.js", "highlights.js", "recall_bridge.js"],
    { chrome }
  );
  const sent = [];
  let connected = true;

  sandbox.MonoRecall.install({
    send: (frame) => {
      sent.push(frame);
      // Answer synchronously from the script, the way the backend would.
      const answer = answers[frame.method];
      if (answer === undefined) return;
      queueMicrotask(() =>
        sandbox.MonoAsk.handleFrame({ kind: "reply", id: frame.id, ok: true, data: answer })
      );
    },
    isConnected: () => connected,
    storage: chrome.storage.local,
  });

  /** send delivers one runtime message to the handlers and awaits the reply. */
  const send = (msg, sender = {}) =>
    new Promise((resolve) => {
      for (const fn of chrome.listeners.message) {
        if (fn(msg, sender, resolve) === true) return;
      }
      resolve(null);
    });

  return { ...sandbox, chrome, sent, send, disconnect: () => (connected = false) };
}

test("the page's highlights become a highlights.json artifact on capture", async () => {
  const h = installed();
  const capture = loadExtensionScripts(["capture_meta.js", "capture.js"]);

  // Someone highlights two passages, one with a note.
  await h.send({ type: "highlight_add", url: PAGE_URL, highlight: { text: "the keeper kept a ledger" } });
  await h.send({
    type: "highlight_add",
    url: PAGE_URL,
    highlight: { text: "The Lighthouse at Dunmore", comment: "look this up" },
  });

  // …and then saves the page. The capture context is the seam: whatever
  // path started the capture, it goes through this.
  const ctx = captureCtx({ highlights: (url) => h.MonoRecall.highlightsArtifact(url) });
  const result = await capture.MonoCapture.pageCapture({ formats: ["readable"] }, ctx);

  const artifact = result.artifacts.find((a) => a.name === "highlights.json");
  assert.ok(artifact, `no highlights.json in ${result.artifacts.map((a) => a.name)}`);

  const doc = JSON.parse(Buffer.from(artifact.bytes, "base64").toString("utf8"));
  assert.equal(doc.url, PAGE_URL);
  assert.equal(doc.highlights.length, 2);
  assert.equal(doc.highlights[1].comment, "look this up");
  assert.ok(doc.highlights[0].anchor.quote, "each highlight carries its own anchor");
});

test("a page with no highlights captures exactly as it did before", async () => {
  const h = installed();
  const capture = loadExtensionScripts(["capture_meta.js", "capture.js"]);

  const ctx = captureCtx({ highlights: (url) => h.MonoRecall.highlightsArtifact(url) });
  const result = await capture.MonoCapture.pageCapture({ formats: ["readable"] }, ctx);

  assert.deepEqual(result.artifacts.map((a) => a.name), ["readable.md"]);
  assert.deepEqual(result.warnings, []);
});

test("highlights that cannot be read do not fail the capture", async () => {
  const capture = loadExtensionScripts(["capture_meta.js", "capture.js"]);
  const ctx = captureCtx({
    highlights: () => {
      throw new Error("storage is gone");
    },
  });

  const result = await capture.MonoCapture.pageCapture({ formats: ["readable"] }, ctx);
  assert.deepEqual(result.artifacts.map((a) => a.name), ["readable.md"]);
  assert.match(result.warnings.join(" "), /highlights skipped: storage is gone/);
});

test("a capture with no recall group installed still works", async () => {
  const capture = loadExtensionScripts(["capture_meta.js", "capture.js"]);
  // ctx.highlights absent: the hook is opt-in, so capture stands alone.
  const result = await capture.MonoCapture.pageCapture({ formats: ["readable"] }, captureCtx({}));
  assert.deepEqual(result.artifacts.map((a) => a.name), ["readable.md"]);
});

test("highlights survive the page being revisited", async () => {
  const h = installed();

  await h.send({ type: "highlight_add", url: PAGE_URL, highlight: { text: "the keeper kept a ledger" } });

  // A fresh content script on a later visit asks for what is stored. The
  // sender's own URL wins over whatever the message claims.
  const reply = await h.send({ type: "highlight_list" }, { tab: { id: 7, url: `${PAGE_URL}#chapter-2` } });
  assert.equal(reply.ok, true);
  assert.equal(reply.records.length, 1);
  assert.equal(reply.records[0].text, "the keeper kept a ledger");
});

test("a highlight can be commented on and removed through the worker", async () => {
  const h = installed();

  const added = await h.send({ type: "highlight_add", url: PAGE_URL, highlight: { text: "a passage" } });
  const id = added.record.id;

  const updated = await h.send({ type: "highlight_update", url: PAGE_URL, id, patch: { comment: "why" } });
  assert.equal(updated.record.comment, "why");

  const removed = await h.send({ type: "highlight_remove", url: PAGE_URL, id });
  assert.equal(removed.ok, true);
  assert.equal(removed.count, 0);
});

test("the popup's saved lookup goes over the request channel", async () => {
  const h = installed({ "doc.lookup": { saved: true, url: PAGE_URL, versions: 2, title: "Dunmore" } });

  const reply = await h.send({ type: "saved_get", tabId: 7 });
  assert.equal(reply.ok, true);
  assert.equal(reply.record.saved, true);
  assert.equal(reply.record.versions, 2);

  const request = h.sent.find((f) => f.method === "doc.lookup");
  assert.ok(request, "no doc.lookup went out");
  assert.equal(request.kind, "request");
  assert.equal(request.params.url, PAGE_URL);

  assert.equal(h.chrome.badges.pop().text, "✓", "and the tab is badged");
});

test("asking the brain relays the backend's progress to the popup", async () => {
  const chrome = chromeStub();
  const relayed = [];
  chrome.runtime.sendMessage = (msg) => {
    relayed.push(msg);
    return Promise.resolve();
  };
  const sandbox = loadExtensionScripts(
    ["ask.js", "saved.js", "highlights.js", "recall_bridge.js"],
    { chrome }
  );
  const sent = [];
  sandbox.MonoRecall.install({
    send: (frame) => sent.push(frame),
    isConnected: () => true,
    storage: chrome.storage.local,
  });

  const answering = new Promise((resolve) => {
    for (const fn of chrome.listeners.message) {
      if (fn({ type: "ask_brain", q: "the ledger" }, {}, resolve) === true) return;
    }
  });

  const id = sent.find((f) => f.method === "doc.ask").id;
  sandbox.MonoAsk.handleFrame({ kind: "reply", id, progress: { stage: "searching", detail: "12 documents" } });
  sandbox.MonoAsk.handleFrame({
    kind: "reply",
    id,
    ok: true,
    data: { query: "the ledger", answers: [{ quote: "a ledger" }] },
  });

  const reply = await answering;
  assert.equal(reply.ok, true);
  assert.equal(reply.answer.answers.length, 1);
  assert.deepEqual(
    relayed.filter((m) => m.type === "ask_progress").map((m) => m.progress.stage),
    ["searching"],
    "a slow ask must say what it is doing rather than look frozen"
  );
});

test("asking while the bridge is down answers, rather than hanging the panel", async () => {
  const h = installed();
  h.disconnect();

  const reply = await h.send({ type: "ask_brain", q: "anything" });
  assert.equal(reply.ok, false);
  assert.equal(reply.code, "offline");
  assert.match(reply.error, /not connected/);
});

test("the socket closing settles a question the popup is waiting on", async () => {
  const h = installed(); // no scripted answer: nothing ever comes back

  const answering = h.send({ type: "ask_brain", q: "the ledger" });
  h.MonoRecall.disconnected("the bridge disconnected");

  const reply = await answering;
  assert.equal(reply.ok, false);
  assert.equal(reply.code, "offline");
});

test("closing a tab forgets it", async () => {
  const h = installed({ "doc.lookup": { saved: true, url: PAGE_URL } });

  await h.send({ type: "saved_get", tabId: 7 });
  for (const fn of h.chrome.listeners.removed) fn(7);
  // No throw, and the tab is no longer tracked — the next saved_get has to
  // go and ask again rather than serve a record for a tab that is gone.
  assert.equal(h.MonoSaved.get(7), null);
});

/** captureCtx is capture.test.mjs's fake Chrome, trimmed to the readable
 *  path — these tests are about the highlights hook, not CDP. */
function captureCtx(extra) {
  return Object.assign(
    {
      resolveTabId: async () => 7,
      inject: async () => {},
      callPage: async (tabId, fn) =>
        ({
          prepare: { scrollPasses: 1 },
          extract: {
            meta: {
              url: PAGE_URL,
              canonicalUrl: PAGE_URL,
              title: "The Lighthouse at Dunmore",
              capturedAt: "2026-09-21T10:00:00.000Z",
              tags: [],
              source: "extension",
            },
            markdown: "# The Lighthouse at Dunmore\n\nThe keeper kept a ledger.",
            text: "The Lighthouse at Dunmore The keeper kept a ledger.",
            selectionRequested: false,
            selectionFound: false,
          },
          restore: { restored: true },
        })[fn],
      attach: async () => {},
      cdp: async () => ({}),
      detach: async () => {},
    },
    extra
  );
}
