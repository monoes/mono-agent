// Tests for the worker-side actions the popup drives: annotate-while-
// capturing (CLIP-07), batch capture (CLIP-06) and the queue commands
// (CLIP-08). `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** world builds a worker with a fake Chrome, a fake socket and a scripted capture. */
function world(options = {}) {
  const store = {};
  const badge = {};
  const tabs = options.tabs || [
    { id: 1, url: "https://paper.test/a", title: "A", active: true, groupId: 7 },
    { id: 2, url: "https://paper.test/b", title: "B", groupId: 7 },
    { id: 3, url: "chrome://settings", title: "Settings", groupId: -1 },
  ];
  const broadcasts = [];

  const chrome = {
    storage: {
      local: {
        get: async (key) => {
          const wanted = Array.isArray(key) ? key : [key];
          const out = {};
          for (const k of wanted) if (k in store) out[k] = store[k];
          return out;
        },
        set: async (values) => Object.assign(store, values),
      },
    },
    tabs: {
      query: async (q) => {
        // A tab with no windowId is in window 1, so the older fixtures
        // need not say where they are.
        const inWindow = (t) => q.windowId === undefined || (t.windowId || 1) === q.windowId;
        if (q.active) return tabs.filter((t) => t.active && inWindow(t));
        if (q.groupId !== undefined) return tabs.filter((t) => t.groupId === q.groupId);
        return tabs.filter(inWindow);
      },
      get: async (id) => {
        const tab = tabs.find((t) => t.id === id);
        if (!tab) throw new Error(`No tab with id: ${id}`);
        return tab;
      },
    },
    tabGroups: { get: async () => ({ title: "Reading" }) },
    action: {
      setBadgeText: async ({ text }) => {
        badge.text = text;
      },
      setBadgeBackgroundColor: async ({ color }) => {
        badge.color = color;
      },
      setTitle: async ({ title }) => {
        badge.title = title;
      },
    },
    runtime: {
      onMessage: { addListener: () => {} },
      sendMessage: (msg) => {
        broadcasts.push(msg);
        return Promise.resolve();
      },
    },
  };

  const sandbox = loadExtensionScripts(
    [
      "capture_meta.js",
      "capture.js",
      "capture_form.js",
      "capture_profile.js",
      "capture_batch.js",
      "capture_queue.js",
      "capture_actions.js",
    ],
    { chrome }
  );

  const captures = [];
  let connected = options.connected !== false;
  const frames = [];

  sandbox.MonoCaptureActions.install({
    storage: chrome.storage.local,
    isConnected: () => connected,
    // background.js's real send returns false rather than throwing when the
    // socket is not OPEN, which is the case `sendSilentlyFails` reproduces.
    send: (frame) => {
      if (options.sendThrows) throw new Error(options.sendThrows);
      if (options.sendSilentlyFails) return false;
      frames.push(frame);
      return true;
    },
    capture: async (params) => {
      captures.push(params);
      if (options.captureThrows) throw new Error(options.captureThrows);
      await new Promise((r) => setTimeout(r, options.captureMs || 1));
      const tab = tabs.find((t) => t.id === params.tabId) || tabs[0];
      return {
        meta: {
          url: tab.url,
          canonicalUrl: tab.url,
          title: tab.title,
          tags: [],
          note: null,
          collection: null,
          contentHash: "sha256:abc",
        },
        artifacts: [{ name: "readable.md", encoding: "base64", bytes: "eA==", rawBytes: 1 }],
        warnings: [],
      };
    },
  });

  return {
    ...sandbox,
    // The worker's own global, for a test that has to install something
    // into it (MonoAsk) — the spread above is a copy, not the sandbox.
    sandbox,
    store,
    badge,
    frames,
    captures,
    broadcasts,
    tabs,
    disconnect: () => (connected = false),
    sendSilentlyFails: () => (options.sendSilentlyFails = true),
    connect: () => (connected = true),
    ask: (message) => sandbox.MonoCaptureActions.handle(message),
  };
}

test("a note typed after the snapshot still lands on that snapshot", async () => {
  const w = world({ captureMs: 5 });

  const started = await w.ask({ type: "capture_begin" });
  assert.equal(started.ok, true);
  // The page is being photographed while the sentence is being typed.
  const result = await w.ask({
    type: "capture_commit",
    form: { note: "the bit about ledgers", tags: "#ml, papers", collection: "Reading" },
  });

  assert.equal(w.captures.length, 1, "committing must not re-capture the page");
  assert.equal(result.ok, true);
  assert.equal(result.note, "the bit about ledgers");
  assert.deepEqual(result.tags, ["ml", "papers"]);
  assert.equal(result.collection, "Reading");

  const envelope = w.frames[0].data;
  assert.equal(envelope.meta.note, "the bit about ledgers");
  assert.deepEqual(envelope.meta.tags, ["ml", "papers"]);
  assert.equal(envelope.meta.collection, "Reading");
  assert.equal(envelope.meta.contentHash, "sha256:abc", "the capture itself is unchanged");
});

test("committing with no pending capture takes one now", async () => {
  const w = world();
  const result = await w.ask({ type: "capture_commit", form: {} });
  assert.equal(w.captures.length, 1);
  assert.equal(result.ok, true);
  assert.equal(result.title, "A");
});

test("the fast path sends exactly what an empty form sends", async () => {
  const w = world();
  await w.ask({ type: "capture_commit", form: {} });
  const meta = w.frames[0].data.meta;
  assert.equal(meta.note, null);
  assert.deepEqual(meta.tags, []);
  assert.equal(meta.collection, null);
});

test("a discarded pending capture is not sent", async () => {
  const w = world();
  await w.ask({ type: "capture_begin" });
  await w.ask({ type: "capture_discard" });
  assert.equal(w.frames.length, 0);

  await w.ask({ type: "capture_commit", form: {} });
  assert.equal(w.captures.length, 2, "after a discard, committing captures afresh");
});

test("tags and collections are remembered for next time", async () => {
  const w = world();
  await w.ask({ type: "capture_commit", form: { tags: "ml, papers", collection: "Reading" } });

  const state = await w.ask({ type: "capture_form_state" });
  assert.deepEqual(state.recentTags, ["ml", "papers"]);
  assert.deepEqual(state.collections, ["Reading"]);
});

test("a capture taken offline is queued, listed and retried", async () => {
  const w = world();
  w.disconnect();

  const saved = await w.ask({ type: "capture_commit", form: { note: "for later" } });
  assert.equal(saved.queued, true);
  assert.equal(w.frames.length, 0);

  const queue = await w.ask({ type: "queue_state" });
  assert.equal(queue.counts.queued, 1);
  assert.equal(queue.queued[0].title, "A");
  assert.equal(w.badge.text, "1");
  assert.equal(w.badge.color, "#c98a00");

  w.connect();
  const retried = await w.ask({ type: "queue_retry", key: queue.queued[0].key });
  assert.equal(retried.ok, true);
  assert.equal(w.frames.length, 1);
  assert.equal(w.frames[0].data.meta.note, "for later");
  assert.equal(w.badge.text, "");
});

test("a retry that cannot connect leaves the capture where it was", async () => {
  const w = world();
  w.disconnect();
  await w.ask({ type: "capture_commit", form: {} });
  const { queued } = await w.ask({ type: "queue_state" });

  const retried = await w.ask({ type: "queue_retry", key: queued[0].key });
  assert.equal(retried.ok, false);
  assert.match(retried.reason, /still disconnected/);
  assert.equal((await w.ask({ type: "queue_state" })).counts.queued, 1);
});

test("deleting a queued capture is explicit and final", async () => {
  const w = world();
  w.disconnect();
  await w.ask({ type: "capture_commit", form: {} });
  const { queued } = await w.ask({ type: "queue_state" });

  assert.deepEqual(await w.ask({ type: "queue_delete", key: queued[0].key }), { ok: true });
  assert.equal((await w.ask({ type: "queue_state" })).counts.queued, 0);
  assert.equal(w.badge.text, "");
});

test("a whole window becomes one collection, skips accounted for", async () => {
  const w = world();
  const { ok, report, summary } = await w.ask({ type: "capture_batch", scope: "window" });

  assert.equal(ok, true);
  assert.equal(report.total, 2);
  assert.deepEqual(report.done.map((d) => d.tabId), [1, 2]);
  assert.equal(report.skipped.length, 1);
  assert.match(report.skipped[0].reason, /closed to extensions/);
  assert.match(summary, /2 of 2 saved/);

  const collections = w.frames.map((f) => f.data.meta.collection);
  assert.equal(new Set(collections).size, 1, "every tab lands in the same collection");
  assert.match(collections[0], /^Window — \d{4}-\d{2}-\d{2}$/);
});

test("a tab group is captured under the group's name", async () => {
  const w = world();
  const { report } = await w.ask({ type: "capture_batch", scope: "group" });
  assert.match(report.collection, /^Reading — /);
  assert.deepEqual(report.done.map((d) => d.tabId), [1, 2]);
});

test("batch progress is broadcast for the popup to count", async () => {
  const w = world();
  await w.ask({ type: "capture_batch", scope: "window" });
  const phases = w.broadcasts.map((b) => b.state.phase);
  assert.deepEqual(phases, ["start", "capturing", "captured", "capturing", "captured", "done"]);
  assert.equal(w.broadcasts[w.broadcasts.length - 1].state.done, 2);
});

test("a batch can be cancelled mid-flight", async () => {
  const w = world({ captureMs: 20 });
  const running = w.ask({ type: "capture_batch", scope: "window" });
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(await w.ask({ type: "capture_batch_cancel" }), { ok: true, cancelling: true });

  const { report } = await running;
  assert.equal(report.cancelled, true);
  assert.equal(report.done.length, 1);
  assert.match(report.skipped.find((s) => s.tabId === 2).reason, /cancelled/);
});

test("two batches cannot run at once", async () => {
  const w = world({ captureMs: 20 });
  const first = w.ask({ type: "capture_batch", scope: "window" });
  const second = await w.ask({ type: "capture_batch", scope: "window" });
  assert.equal(second.ok, false);
  assert.match(second.error, /already running/);
  await first;
});

test("a capture that throws is reported, not swallowed", async () => {
  const w = world({ captureThrows: "debugger refused to attach" });
  const result = await w.ask({ type: "capture_commit", form: {} });
  assert.equal(result.ok, false);
  assert.equal(result.error, "debugger refused to attach");
});

test("an unknown message is not this module's to answer", async () => {
  const w = world();
  assert.equal(await w.ask({ type: "get_status" }), null);
});

// --- the profile a capture is filed into ---------------------------------

test("the picked profile rides the envelope and becomes the sticky choice", async () => {
  const w = world();
  await w.ask({ type: "capture_commit", form: { profile: "p-work" } });

  assert.equal(w.frames[0].data.meta.profile, "p-work");
  assert.equal(w.store[w.MonoCaptureProfile.PROFILE_KEY], "p-work");

  // The next capture, with no picker involved at all, inherits it.
  await w.ask({ type: "capture_commit", form: {} });
  assert.equal(w.frames[1].data.meta.profile, "p-work");
});

test("picking 'no profile' is a choice, and is remembered as one", async () => {
  const w = world();
  await w.ask({ type: "capture_commit", form: { profile: "p-work" } });
  await w.ask({ type: "capture_commit", form: { profile: "" } });

  assert.equal(w.frames[1].data.meta.profile, undefined);
  assert.equal(w.store[w.MonoCaptureProfile.PROFILE_KEY], "");
});

test("a capture with no profile anywhere is exactly what it always was", async () => {
  const w = world();
  const result = await w.ask({ type: "capture_commit", form: {} });
  const meta = w.frames[0].data.meta;
  assert.equal("profile" in meta, false, "an unprofiled capture carries no profile key");
  assert.equal(result.profile, null);
});

test("a hostile profile id never reaches the envelope", async () => {
  const w = world();
  await w.ask({ type: "capture_commit", form: { profile: "../../etc/passwd" } });
  assert.equal("profile" in w.frames[0].data.meta, false);
  assert.equal(w.store[w.MonoCaptureProfile.PROFILE_KEY], "");
});

test("the form state carries the picker, and works with no bridge to ask", async () => {
  const w = world();
  const state = await w.ask({ type: "capture_form_state" });
  assert.equal(state.ok, true);
  assert.deepEqual(state.profiles, []);
  assert.equal(state.profile, "");
  assert.equal(state.profilesOffline, true);
});

test("the form state asks the backend for the real profiles", async () => {
  const w = world();
  w.sandbox.MonoAsk = {
    supports: async () => true,
    request: async () => ({
      profiles: [
        { id: "p-home", name: "Personal" },
        { id: "p-work", name: "Work", default: true },
      ],
    }),
  };
  const state = await w.ask({ type: "capture_form_state" });
  assert.deepEqual(state.profiles.map((p) => p.id), ["p-home", "p-work"]);
  assert.equal(state.profile, "p-work", "the default is pre-selected");
  assert.equal(state.profilesOffline, false);

  // A profile that has since been deleted is reported, not silently swapped.
  w.store[w.MonoCaptureProfile.PROFILE_KEY] = "p-gone";
  const again = await w.ask({ type: "capture_form_state" });
  assert.equal(again.profile, "p-work");
  assert.equal(again.profileChanged, true);
  assert.match(again.profileReason, /Work/);
});

test("a socket that swallows the frame instead of throwing still queues the capture", async () => {
  // isConnected() says yes and the send says nothing at all — the shape of
  // an MV3 socket that closed between the check and the write. Reported as
  // sent, this capture would be gone: the queue is the only copy.
  const w = world({ sendSilentlyFails: true });
  const result = await w.ask({ type: "capture_commit", form: { note: "worth keeping" } });

  assert.equal(result.queued, true, "not delivered, so it is queued");
  assert.equal(w.frames.length, 0, "nothing reached the wire");
  assert.equal(w.store.captureQueue.length, 1);
  assert.equal(w.store.captureQueue[0].envelope.meta.note, "worth keeping");
});

test("a retry against a swallowing socket leaves the capture exactly where it was", async () => {
  const w = world({ connected: false });
  await w.ask({ type: "capture_commit", form: {} });
  assert.equal(w.store.captureQueue.length, 1);
  const key = w.store.captureQueue[0].envelope.id;

  // The bridge comes back, the retry goes out, and the frame lands nowhere.
  // A retry that loses the capture is the one thing CLIP-08 cannot allow.
  w.connect();
  w.sendSilentlyFails();
  const result = await w.ask({ type: "queue_retry", key });

  assert.equal(result.ok, false);
  assert.equal(w.store.captureQueue.length, 1, "still queued, not deleted on a send that did nothing");
});

// ── the side panel's own window ─────────────────────────────────────
//
// A side panel outlives the tab and the focus that opened it. "The last
// focused window" is whichever window the person touched last, which is
// not necessarily the one whose panel the button was pressed in — so the
// panel says which window and which tab it means.

const twoWindows = () => [
  { id: 1, url: "https://paper.test/a", title: "A", active: true, groupId: -1, windowId: 1 },
  { id: 2, url: "https://paper.test/b", title: "B", groupId: -1, windowId: 1 },
  { id: 10, url: "https://other.test/x", title: "X", active: true, groupId: 4, windowId: 2 },
  { id: 11, url: "https://other.test/y", title: "Y", groupId: 4, windowId: 2 },
  { id: 12, url: "https://other.test/z", title: "Z", groupId: -1, windowId: 2 },
];

test("a batch from a panel covers that panel's window, not the last focused one", async () => {
  const w = world({ tabs: twoWindows() });
  const { report } = await w.ask({ type: "capture_batch", scope: "window", windowId: 2 });
  assert.deepEqual(report.done.map((d) => d.tabId), [10, 11, 12]);
});

test("a group batch uses the tab the panel is showing", async () => {
  const w = world({ tabs: twoWindows() });
  const { report } = await w.ask({ type: "capture_batch", scope: "group", windowId: 2, tabId: 10 });
  assert.deepEqual(report.done.map((d) => d.tabId), [10, 11]);
});

test("a group batch on a tab outside any group says so", async () => {
  const w = world({ tabs: twoWindows() });
  const result = await w.ask({ type: "capture_batch", scope: "group", windowId: 1, tabId: 1 });
  assert.equal(result.ok, false);
  assert.match(result.error, /not in a tab group/);
});

test("the panel's commit captures the tab it names, even with another pending", async () => {
  const w = world({ tabs: twoWindows() });
  await w.ask({ type: "capture_begin", options: { tabId: 1 } });
  // The person switched tabs after touching the note field.
  await w.ask({ type: "capture_commit", form: {}, options: { tabId: 10 } });
  assert.equal(w.frames[0].data.meta.url, "https://other.test/x");
});

test("choosing a profile in the panel is remembered before any save", async () => {
  const w = world();
  const result = await w.ask({ type: "capture_profile_set", profile: "p-work" });
  assert.deepEqual(result, { ok: true, profile: "p-work" });
  assert.equal(w.store[w.MonoCaptureProfile.PROFILE_KEY], "p-work");

  // The shortcut's path — no form, no picker — now files into it.
  await w.ask({ type: "capture_commit", form: {} });
  assert.equal(w.frames[0].data.meta.profile, "p-work");
});

test("choosing a hostile profile id in the panel remembers nothing usable", async () => {
  const w = world();
  const result = await w.ask({ type: "capture_profile_set", profile: "../escape" });
  assert.equal(result.profile, "");
  assert.equal(w.store[w.MonoCaptureProfile.PROFILE_KEY], "");
});
