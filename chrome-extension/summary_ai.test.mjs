// The "AI for summaries" choice: what the picker shows (runtime, then a
// model list or free text, "Runtime default" either way), how a remembered
// choice survives the bridge being away or a runtime being uninstalled, and
// how the choice reaches a capture's meta.summarize from the side panel and
// from the right-click menu. `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoSummaryAI: AI } = loadExtensionScripts(["summary_ai.js"]);

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, data[k]])),
    set: async (values) => Object.assign(data, values),
  };
}

const CATALOG = {
  runtimes: [{ id: "claude", version: "2.1.3" }, { id: "codex" }, { id: "opencode" }],
  default: "claude",
  enabled: true,
};
const CLAUDE_MODELS = [
  { id: "claude-opus-5", label: "Opus 5" },
  { id: "claude-haiku-4-5-20251001", label: "Haiku 4.5" },
];

/** fakeAsk answers the two picker methods like the bridge, and records them. */
function fakeAsk({ offline = false, catalog = CATALOG, models = { claude: CLAUDE_MODELS }, fail = {} } = {}) {
  const calls = [];
  return {
    calls,
    supports: async () => !offline,
    request: async (method, params) => {
      calls.push({ method, params });
      if (offline) throw Object.assign(new Error("the monoagent bridge is not connected"), { code: "offline" });
      if (fail[method]) throw Object.assign(new Error(fail[method].message), { code: fail[method].code });
      if (method === "summary.runtimes") return catalog;
      if (method === "summary.models") return { runtime: params.runtime, models: models[params.runtime] || [] };
      throw new Error(`unexpected ${method}`);
    },
  };
}

// --- ids: the same rules as the Go side ------------------------------------

test("runtime and model ids are checked exactly as the bridge checks them", () => {
  for (const ok of ["claude", "codex", "open-code", "pi"]) assert.ok(AI.isValidRuntimeId(ok), ok);
  for (const bad of ["", "Claude", "../x", "a b", "/bin/sh", null, 7]) assert.ok(!AI.isValidRuntimeId(bad), String(bad));
  for (const ok of ["claude-haiku-4-5-20251001", "openai/gpt-5", "claude-opus-5[1m]", "llama3:8b"]) assert.ok(AI.isValidModel(ok), ok);
  for (const bad of ["", "-m", "--model=x", "a b", "$(x)"]) assert.ok(!AI.isValidModel(bad), bad);
  // A model without a runtime is not a choice anyone can make.
  assert.deepEqual(AI.normalizeChoice({ model: "claude-opus-5" }), { runtime: "", model: "" });
});

// --- choose ------------------------------------------------------------------

test("a remembered runtime is kept while installed, and kept as-is offline", () => {
  const stored = { runtime: "codex", model: "gpt-5.1-codex" };
  assert.deepEqual(AI.choose(stored, CATALOG, {}), { runtime: "codex", model: "gpt-5.1-codex", changed: false, reason: "" });
  assert.deepEqual(AI.choose(stored, { runtimes: [] }, { offline: true }).runtime, "codex");
});

test("a remembered runtime that was uninstalled falls back to the bridge default, out loud", () => {
  const got = AI.choose({ runtime: "grok", model: "grok-4" }, CATALOG, {});
  assert.equal(got.runtime, "");
  assert.equal(got.model, "");
  assert.equal(got.changed, true);
  assert.equal(got.reason, "grok is no longer installed — using the bridge's default, claude");
});

// --- describePicker ------------------------------------------------------------

test("a runtime with a model list: a dropdown, Runtime default first, as in the app", () => {
  const view = AI.describePicker({ choice: { runtime: "claude", model: "claude-haiku-4-5-20251001" }, catalog: CATALOG, models: CLAUDE_MODELS });
  assert.equal(view.name, "claude · Haiku 4.5");
  assert.equal(view.interactive, true);
  assert.deepEqual(view.runtimeOptions.map((o) => o.label), ["claude", "codex", "opencode"]);
  assert.equal(view.runtimeOptions.find((o) => o.selected).value, "claude");
  assert.equal(view.modelMode, "select");
  assert.deepEqual(view.modelOptions.map((o) => o.label), ["Runtime default", "Opus 5", "Haiku 4.5"]);
  assert.equal(view.modelValue, "claude-haiku-4-5-20251001");
  assert.equal(view.note, null);
});

test("nothing chosen: the bridge default runtime, on its own default model", () => {
  const view = AI.describePicker({ choice: {}, catalog: CATALOG, models: CLAUDE_MODELS });
  assert.equal(view.runtime, "claude");
  assert.equal(view.name, "claude · runtime default");
  assert.equal(view.modelValue, "");
});

test("a runtime with no model list: free text, and a line saying why", () => {
  const view = AI.describePicker({ choice: { runtime: "opencode" }, catalog: CATALOG, models: [] });
  assert.equal(view.modelMode, "text");
  assert.equal(view.modelPlaceholder, "Runtime default");
  assert.match(view.note.text, /opencode reports no model list/);
  const typed = AI.describePicker({ choice: { runtime: "opencode", model: "anthropic/claude-sonnet-5" }, catalog: CATALOG, models: [] });
  assert.equal(typed.name, "opencode · anthropic/claude-sonnet-5");
});

test("while a runtime's models load, the model control says so", () => {
  const view = AI.describePicker({ choice: { runtime: "codex" }, catalog: CATALOG, models: null, modelsLoading: true });
  assert.equal(view.modelMode, "loading");
});

test("a model list that failed to load falls back to free text with the reason", () => {
  const view = AI.describePicker({ choice: { runtime: "codex" }, catalog: CATALOG, models: [], modelsError: "codex exited 1" });
  assert.equal(view.modelMode, "text");
  assert.match(view.note.text, /Couldn't list codex's models/);
});

test("offline: the last known list, not operable, with a note", () => {
  const view = AI.describePicker({ choice: { runtime: "codex" }, catalog: CATALOG, models: null, offline: true, known: true });
  assert.equal(view.interactive, false);
  assert.deepEqual(view.runtimeOptions.map((o) => o.value), ["claude", "codex", "opencode"]);
  assert.match(view.note.text, /isn't connected, so this is the last list/);

  const never = AI.describePicker({ choice: {}, catalog: {}, offline: true, known: false });
  assert.equal(never.name, "Not known yet");
  assert.match(never.note.text, /Start the bridge/);
});

test("offline with a remembered runtime the cached list lacks: still shown as chosen", () => {
  const view = AI.describePicker({ choice: { runtime: "grok" }, catalog: CATALOG, offline: true, known: true });
  assert.deepEqual(view.runtimeOptions[0], { value: "grok", label: "grok", selected: true });
});

test("summaries turned off on the bridge, no runtimes, a scan that failed, a runtime gone", () => {
  const off = AI.describePicker({ choice: {}, catalog: { runtimes: CATALOG.runtimes, default: "", enabled: false } });
  assert.equal(off.name, "Turned off on this bridge");
  assert.equal(off.interactive, false);
  assert.match(off.note.text, /turned off on this bridge/);

  const none = AI.describePicker({ choice: {}, catalog: { runtimes: [], default: "claude", enabled: true } });
  assert.equal(none.interactive, false);
  assert.match(none.note.text, /No agent runtimes are installed/);

  const noMonomind = AI.describePicker({ choice: {}, catalog: {}, offline: true, scanError: "monomind not found on PATH" });
  assert.equal(noMonomind.note.command, "npm install -g @monoes/monomindcli");

  const gone = AI.describePicker({ choice: {}, catalog: CATALOG, changed: true, reason: "grok is no longer installed — using the bridge's default, claude" });
  assert.equal(gone.note.tone, "warn");
  assert.equal(gone.note.text, "Grok is no longer installed — using the bridge's default, claude.");
});

// --- the stamp on a capture ---------------------------------------------------

test("applyToMeta names the AI only on a capture that asked for a summary", () => {
  const full = { title: "x" };
  assert.deepEqual(AI.applyToMeta(full, { runtime: "claude", model: "claude-opus-5" }), { title: "x" });

  const page = { summarize: { kind: "page" } };
  AI.applyToMeta(page, { runtime: "claude", model: "claude-haiku-4-5-20251001" });
  assert.deepEqual(page.summarize, { kind: "page", runtime: "claude", model: "claude-haiku-4-5-20251001" });

  // No choice: {kind} alone, which the bridge reads as its default.
  const video = { summarize: { kind: "video", runtime: "stale" } };
  AI.applyToMeta(video, { runtime: "", model: "" });
  assert.deepEqual(video.summarize, { kind: "video" });

  const hostile = { summarize: { kind: "page" } };
  AI.applyToMeta(hostile, { runtime: "/bin/sh", model: "--yolo" });
  assert.deepEqual(hostile.summarize, { kind: "page" });
});

test("panelMode: Summary on a YouTube video is the video summary", () => {
  assert.deepEqual(AI.panelMode("full", "https://paper.test/"), { mode: "full", label: "Summary", summarizes: false });
  assert.deepEqual(AI.panelMode("screenshot", "https://paper.test/").mode, "screenshot");
  assert.deepEqual(AI.panelMode("summary", "https://paper.test/"), { mode: "summary", label: "Summary", summarizes: true });
  assert.deepEqual(AI.panelMode("summary", "https://www.youtube.com/watch?v=jNQXAC9IVRw"), { mode: "video", label: "Video summary", summarizes: true });
  assert.equal(AI.panelMode("summary", "https://youtu.be/jNQXAC9IVRw").mode, "video");
  assert.equal(AI.panelMode("summary", "https://www.youtube.com/@jawed").mode, "summary");
});

// --- state: storage + the bridge ---------------------------------------------

test("state asks the bridge, caches both lists, and draws offline from the cache", async () => {
  const storage = fakeStorage();
  await AI.remember(storage, { runtime: "claude", model: "claude-haiku-4-5-20251001" });
  const ask = fakeAsk();
  const live = await AI.state(ask, storage, true);
  assert.equal(live.offline, false);
  assert.deepEqual(live.models.map((m) => m.id), CLAUDE_MODELS.map((m) => m.id));
  assert.deepEqual(ask.calls.map((c) => c.method), ["summary.runtimes", "summary.models"]);
  assert.deepEqual(ask.calls[1].params, { runtime: "claude" });

  const down = await AI.state(fakeAsk({ offline: true }), storage, true);
  assert.equal(down.offline, true);
  assert.equal(down.known, true);
  assert.deepEqual(down.catalog.runtimes.map((r) => r.id), ["claude", "codex", "opencode"]);
  assert.deepEqual(down.models.map((m) => m.label), ["Opus 5", "Haiku 4.5"]);
  assert.deepEqual(down.choice, { runtime: "claude", model: "claude-haiku-4-5-20251001" });

  // A first paint asks nobody.
  const quiet = fakeAsk();
  await AI.state(quiet, storage, false);
  assert.equal(quiet.calls.length, 0);
});

test("state moves a runtime that is no longer installed back to the default, and remembers that", async () => {
  const storage = fakeStorage({ summaryAI: { runtime: "grok", model: "grok-4" } });
  const got = await AI.state(fakeAsk(), storage, true);
  assert.equal(got.changed, true);
  assert.deepEqual(got.choice, { runtime: "", model: "" });
  assert.deepEqual(storage.data.summaryAI, { runtime: "", model: "" });
  // The models asked for are the default runtime's.
  assert.deepEqual(got.models.map((m) => m.id), CLAUDE_MODELS.map((m) => m.id));
});

test("a bridge that cannot scan says why and keeps the last list", async () => {
  const storage = fakeStorage({ summaryAICache: Object.assign({ models: {} }, CATALOG) });
  const got = await AI.state(fakeAsk({ fail: { "summary.runtimes": { code: "unavailable", message: "cannot list agent runtimes: monomind not found" } } }), storage, true);
  assert.equal(got.offline, true);
  assert.match(got.scanError, /monomind not found/);
  assert.equal(got.catalog.runtimes.length, 3);
});

test("modelsFor: a list, an empty list, and a lister that failed", async () => {
  const storage = fakeStorage({ summaryAICache: Object.assign({ models: {} }, CATALOG) });
  const ask = fakeAsk({ fail: { "summary.models": null } });
  assert.deepEqual((await AI.modelsFor(ask, storage, "claude")).models.map((m) => m.id), CLAUDE_MODELS.map((m) => m.id));
  assert.deepEqual(storage.data.summaryAICache.models.claude.length, 2, "cached for an offline panel");
  assert.deepEqual((await AI.modelsFor(ask, storage, "opencode")).models, []);
  const broken = fakeAsk({ fail: { "summary.models": { code: "unavailable", message: "codex exited 1" } } });
  const got = await AI.modelsFor(broken, storage, "codex");
  assert.deepEqual(got.models, []);
  assert.match(got.modelsError, /codex exited 1/);
});

// --- plumbing: the side panel's Save -----------------------------------------

function worker() {
  const store = {};
  const storage = {
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in store).map((k) => [k, store[k]])),
    set: async (u) => Object.assign(store, u),
  };
  const chrome = {
    storage: { local: storage },
    tabs: { query: async () => [{ id: 1, url: "https://paper.test/a", active: true }] },
    action: { setBadgeText: async () => {}, setBadgeBackgroundColor: async () => {}, setTitle: async () => {} },
    runtime: { onMessage: { addListener: () => {} }, sendMessage: () => Promise.resolve() },
  };
  const env = loadExtensionScripts(
    ["capture_meta.js", "capture.js", "capture_modes.js", "capture_form.js", "capture_profile.js", "summary_ai.js",
      "capture_batch.js", "capture_queue.js", "capture_actions.js"],
    { chrome }
  );
  const captures = [];
  const frames = [];
  env.MonoCaptureActions.install({
    storage,
    isConnected: () => true,
    send: (f) => { frames.push(f); return true; },
    capture: async (params) => {
      captures.push(params);
      const meta = { url: "https://paper.test/a", title: "A", tags: [], contentHash: "sha256:abc", captureMode: params.mode };
      if (params.summarize) meta.summarize = { kind: params.summarize };
      return { meta, artifacts: [], warnings: [] };
    },
  });
  // A sticky profile, so no capture stops to ask for one.
  store.captureProfile = "";
  store.captureProfilesCache = [];
  return { env, store, captures, frames, ask: (m) => env.MonoCaptureActions.handle(m) };
}

test("the panel's Summary save carries the picked runtime and model", async () => {
  const w = worker();
  const set = await w.ask({ type: "summary_ai_set", runtime: "claude", model: "claude-haiku-4-5-20251001" });
  assert.deepEqual(set.choice, { runtime: "claude", model: "claude-haiku-4-5-20251001" });

  const result = await w.ask({ type: "capture_commit", form: {}, options: { tabId: 1, mode: "summary" } });
  assert.equal(result.ok, true);
  assert.equal(w.captures[0].summarize, "page", "the mode was expanded like the menu's");
  assert.deepEqual(w.frames.at(-1).data.meta.summarize, { kind: "page", runtime: "claude", model: "claude-haiku-4-5-20251001" });
});

test("a Full page save is not touched by the AI choice", async () => {
  const w = worker();
  await w.ask({ type: "summary_ai_set", runtime: "claude", model: "claude-opus-5" });
  await w.ask({ type: "capture_commit", form: {}, options: { tabId: 1, mode: "full" } });
  assert.equal(w.frames.at(-1).data.meta.summarize, undefined);
});

test("the choice is read at commit, and a snapshot begun in another mode is retaken", async () => {
  const w = worker();
  await w.ask({ type: "capture_begin", options: { tabId: 1, mode: "full" } });
  await w.ask({ type: "summary_ai_set", runtime: "codex", model: "gpt-5.1-codex" });
  await w.ask({ type: "capture_commit", form: {}, options: { tabId: 1, mode: "summary" } });
  assert.equal(w.captures.length, 2, "the Full page snapshot was not a summary capture");
  assert.deepEqual(w.frames.at(-1).data.meta.summarize, { kind: "page", runtime: "codex", model: "gpt-5.1-codex" });

  // Same mode: the early snapshot is used, and the choice made since still counts.
  await w.ask({ type: "capture_begin", options: { tabId: 1, mode: "summary" } });
  await w.ask({ type: "summary_ai_set", runtime: "claude", model: "" });
  await w.ask({ type: "capture_commit", form: {}, options: { tabId: 1, mode: "summary" } });
  assert.equal(w.captures.length, 3);
  assert.deepEqual(w.frames.at(-1).data.meta.summarize, { kind: "page", runtime: "claude" });
});

// --- plumbing: the right-click menu -------------------------------------------

function menuWorld(seed) {
  const store = Object.assign({ captureProfile: "" }, seed);
  const sent = [];
  const chrome = {
    runtime: { onMessage: { addListener: () => {} }, lastError: null, sendMessage: () => Promise.resolve() },
    commands: { onCommand: { addListener: () => {} } },
    contextMenus: { removeAll: (cb) => cb(), create: () => {}, onClicked: { addListener: () => {} } },
    tabs: { query: async () => [{ id: 42, url: "https://paper.test/lighthouse" }] },
    action: { setBadgeText: () => Promise.resolve(), setBadgeBackgroundColor: () => Promise.resolve(), setTitle: () => Promise.resolve() },
    storage: { local: {
      get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in store).map((k) => [k, store[k]])),
      set: async (u) => Object.assign(store, u),
    } },
    scripting: {
      executeScript: async ({ files, func, args }) => {
        if (files || (func && func.name === "pageToast")) return [{ result: undefined }];
        const [fn] = args;
        if (fn !== "extract") return [{ result: {} }];
        return [{ result: {
          meta: { url: "https://paper.test/lighthouse", title: "The Lighthouse", capturedAt: "2026-09-22T10:00:00.000Z", tags: [] },
          markdown: "# The Lighthouse", text: "The Lighthouse", artifacts: [], warnings: [],
        } }];
      },
    },
  };
  const env = loadExtensionScripts(
    ["capture_meta.js", "capture.js", "capture_profile.js", "summary_ai.js", "capture_modes.js", "youtube_video.js", "capture_bridge.js"],
    { chrome }
  );
  env.MonoCaptureBridge.install({
    send: (m) => { sent.push(m); return true; },
    isConnected: () => true,
    maxMessageBytes: 8 * 1024 * 1024,
    attach: async () => {},
    cdp: async (tabId, method) => (method === "Page.getLayoutMetrics" ? { cssContentSize: { width: 800, height: 600 } } : { data: "QllURVM=" }),
    detach: async () => {},
  });
  return { env, sent };
}

test("right-click Save page summary uses the sticky choice", async () => {
  const { env, sent } = menuWorld({ summaryAI: { runtime: "claude", model: "claude-haiku-4-5-20251001" } });
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-summary" }, { id: 42 });
  assert.deepEqual(sent.at(-1).data.meta.summarize, { kind: "page", runtime: "claude", model: "claude-haiku-4-5-20251001" });
});

test("right-click with no choice made leaves the bridge default", async () => {
  const { env, sent } = menuWorld({});
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-summary" }, { id: 42 });
  assert.deepEqual(sent.at(-1).data.meta.summarize, { kind: "page" });
});

test("a Go-initiated capture's own choice wins over the sticky one", async () => {
  const { env, sent } = menuWorld({ summaryAI: { runtime: "claude", model: "claude-opus-5" } });
  await env.MonoCaptureBridge.handleCommand("cmd-1", { tabId: 42, mode: "summary", summaryRuntime: "codex", summaryModel: "gpt-5.1-codex" });
  assert.deepEqual(sent.at(-1).data.meta.summarize, { kind: "page", runtime: "codex", model: "gpt-5.1-codex" });
});
