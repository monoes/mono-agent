// The right-click menu and its capture modes: which items exist where, how a
// click is routed, what each mode puts in the envelope, and how the outcome
// is reported. The menu handler is driven the way contextMenus.onClicked
// drives it, against the same fake Chrome the other capture tests use.

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { loadExtensionScripts } from "./test_helpers.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => readFileSync(join(HERE, "fixtures", "youtube", name), "utf8");
const b64 = (s) => Buffer.from(s, "utf8").toString("base64");

const SCRIPTS = [
  "capture_meta.js", "capture.js", "capture_profile.js", "capture_modes.js",
  "youtube_video.js", "youtube_transcript.js", "capture_bridge.js",
];

/**
 * setup installs the capture bridge against a fake Chrome. `page` is what the
 * page's extract answers; `mainWorld` answers MAIN-world executeScript calls
 * (the video capture's reads and fetches) by function name.
 */
function setup({ connected = true, url = "https://paper.test/lighthouse", mainWorld = {}, failExtract = null } = {}) {
  const listeners = {};
  const record = { menus: [], badges: [], toasts: [], messages: [], main: [] };
  const on = (name) => ({ addListener: (fn) => (listeners[name] = fn) });
  const store = {};
  const chrome = {
    runtime: { onMessage: on("message"), lastError: null, sendMessage: (m) => { record.messages.push(m); return Promise.resolve(); } },
    commands: { onCommand: on("command") },
    contextMenus: { removeAll: (cb) => cb(), create: (item) => record.menus.push(item), onClicked: on("menu") },
    tabs: { query: async () => [{ id: 42, url }] },
    action: {
      setBadgeText: (o) => { record.badges.push(o.text); return Promise.resolve(); },
      setBadgeBackgroundColor: () => Promise.resolve(),
      setTitle: () => Promise.resolve(),
    },
    storage: { local: {
      get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in store).map((k) => [k, store[k]])),
      set: async (u) => Object.assign(store, u),
    } },
    scripting: {
      executeScript: async ({ files, func, args, world }) => {
        if (files) return [];
        if (world === "MAIN") {
          record.main.push(func.name);
          const answer = mainWorld[func.name];
          if (!answer) throw new Error(`no MAIN-world answer for ${func.name}`);
          return [{ result: await answer(...args) }];
        }
        if (func && func.name === "pageToast") {
          record.toasts.push({ text: args[0], level: args[1] });
          return [{ result: undefined }];
        }
        const [fn] = args;
        if (fn === "prepare") return [{ result: {} }];
        if (fn === "restore") return [{ result: {} }];
        if (failExtract) throw new Error(failExtract);
        return [{ result: {
          meta: { url, title: "The Lighthouse at Dunmore", capturedAt: "2026-09-22T10:00:00.000Z", tags: [] },
          markdown: "# The Lighthouse at Dunmore", text: "The Lighthouse at Dunmore",
          artifacts: [{ name: "items.json", text: "[]" }], selectionFound: !!args[1][0].selection,
        } }];
      },
    },
  };
  const sent = [];
  const env = loadExtensionScripts(SCRIPTS, { chrome });
  // A sticky profile is set, so the capture never stops to ask.
  store.captureProfile = "p-work";
  env.MonoCaptureBridge.install({
    send: (m) => { sent.push(m); return true; },
    isConnected: () => connected,
    maxMessageBytes: 8 * 1024 * 1024,
    attach: async () => {},
    cdp: async (tabId, method) =>
      method === "Page.getLayoutMetrics" ? { cssContentSize: { width: 1000, height: 2000 } } : { data: b64("BYTES") },
    detach: async () => {},
  });
  return { env, chrome, listeners, record, sent, store };
}

const envelope = (sent) => sent[sent.length - 1].data;
const artifactNames = (sent) => envelope(sent).artifacts.map((a) => a.name).sort();

test("one MonoAgent parent, four page modes, the video one only on videos, and the selection item", () => {
  const { record } = setup();
  const byId = Object.fromEntries(record.menus.map((m) => [m.id, m]));
  assert.equal(byId["monoagent-root"].title, "MonoAgent");
  const children = record.menus.filter((m) => m.parentId === "monoagent-root");
  assert.deepEqual(children.map((m) => m.title), [
    "Save full page (screenshot + data)",
    "Save screenshot only",
    "Save page summary",
    "Save video summary",
    "Save selection to monomind",
  ]);
  const video = byId["monoagent-capture-video"];
  assert.ok(video.documentUrlPatterns.some((p) => p.includes("youtube.com/watch")));
  assert.ok(video.documentUrlPatterns.some((p) => p.includes("youtube.com/shorts")));
  for (const id of ["monoagent-capture-full", "monoagent-capture-screenshot", "monoagent-capture-summary"]) {
    assert.equal(byId[id].documentUrlPatterns, undefined, `${id} shows on every page`);
  }
  assert.deepEqual(byId["monoagent-capture-selection"].contexts, ["selection"]);
});

test("menuRoute maps every item, and ignores items that are not ours", () => {
  const { env } = setup();
  const M = env.MonoCaptureModes;
  assert.deepEqual(M.menuRoute({ menuItemId: "monoagent-capture-screenshot" }, { id: 3 }), { tabId: 3, selection: false, mode: "screenshot" });
  assert.deepEqual(M.menuRoute({ menuItemId: "monoagent-capture-selection" }, { id: 3 }), { tabId: 3, selection: true, mode: "full" });
  assert.equal(M.menuRoute({ menuItemId: "monoagent-root" }, { id: 3 }), null);
  assert.equal(M.menuRoute({ menuItemId: "someone-else" }, { id: 3 }), null);
});

test("full page: screenshot + data, filed into the sticky profile", async () => {
  const { env, sent, record } = setup();
  const out = await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-full" }, { id: 42 });
  assert.equal(out.result.ok, true);
  assert.deepEqual(artifactNames(sent), ["items.json", "page.mhtml", "readable.md", "screenshot.png"]);
  const meta = envelope(sent).meta;
  assert.equal(meta.captureMode, "full");
  assert.equal(meta.summarize, undefined, "no summary was asked for");
  assert.equal(meta.profile, "p-work");
  assert.equal(record.toasts[0].level, "ok");
  assert.match(record.toasts[0].text, /Page saved: The Lighthouse at Dunmore/);
  assert.equal(record.messages[0].type, "capture_menu_result");
});

test("screenshot only: screenshot.png and nothing else", async () => {
  const { env, sent } = setup();
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-screenshot" }, { id: 42 });
  assert.deepEqual(artifactNames(sent), ["screenshot.png"]);
  assert.equal(envelope(sent).meta.captureMode, "screenshot");
});

test("page summary: the full capture, asking the bridge for a summary", async () => {
  const { env, sent, record } = setup();
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-summary" }, { id: 42 });
  assert.ok(artifactNames(sent).includes("readable.md"));
  assert.deepEqual(envelope(sent).meta.summarize, { kind: "page" });
  assert.match(record.toasts[0].text, /summary is being written/);
});

test("video summary: the record in meta.video and transcript.md, read from the page", async () => {
  const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw";
  const { env, sent, record } = setup({
    url,
    mainWorld: {
      readPageState: () => JSON.parse(fixture("zoo.state.json")),
      pageFetchText: (u) => {
        if (u.includes("/youtubei/v1/player")) return { status: 200, text: fixture("zoo.android.json") };
        if (u.includes("fmt=json3") && !u.includes("exp=xpe")) return { status: 200, text: fixture("zoo.json3") };
        return { status: 200, text: "" };
      },
    },
  });
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-video" }, { id: 42 });
  assert.deepEqual(artifactNames(sent), ["items.json", "readable.md", "screenshot.png", "transcript.md"]);
  const meta = envelope(sent).meta;
  assert.deepEqual(meta.summarize, { kind: "video" });
  assert.equal(meta.video.title, "Me at the zoo");
  assert.equal(meta.video.channel, "jawed");
  assert.equal(meta.video.duration, "0:19");
  assert.equal(meta.video.transcript.available, true);
  assert.equal(meta.video.transcript.source, "player-api");
  const transcript = envelope(sent).artifacts.find((a) => a.name === "transcript.md");
  assert.match(Buffer.from(transcript.bytes, "base64").toString("utf8"), /\[0:01\]\(https:\/\/www\.youtube\.com\/watch\?v=jNQXAC9IVRw&t=1s\)/);
  assert.ok(record.main.includes("readPageState"));
  assert.ok(!artifactNames(sent).includes("page.mhtml"), "no multi-megabyte player archive");
});

test("a video with no captions still saves, with the reason in meta and the warnings", async () => {
  const state = JSON.parse(fixture("zoo.state.json"));
  state.players[0].captions = null;
  const { env, sent, record } = setup({
    url: "https://www.youtube.com/watch?v=jNQXAC9IVRw",
    mainWorld: { readPageState: () => state, pageFetchText: () => ({ status: 200, text: "" }) },
  });
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-video" }, { id: 42 });
  const data = envelope(sent);
  assert.ok(!data.artifacts.some((a) => a.name === "transcript.md"));
  assert.equal(data.meta.video.transcript.available, false);
  assert.match(data.warnings.join("\n"), /no captions/);
  assert.equal(record.toasts[0].level, "warn");
});

test("a failed capture is reported on the badge and in the page, never swallowed", async () => {
  const { env, record, sent } = setup({ failExtract: "the page went away" });
  const out = await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-full" }, { id: 42 });
  assert.equal(out.result.ok, false);
  assert.equal(sent.length, 0);
  assert.equal(out.feedback.level, "error");
  assert.match(record.toasts[0].text, /save failed — readable extraction failed: the page went away/);
  assert.ok(record.badges.includes("!"));
  assert.equal(record.messages[0].feedback.level, "error");
});

test("with the bridge down a menu capture is queued, summary request and all", async () => {
  const { env, store, record } = setup({ connected: false });
  await env.MonoCaptureBridge.handleMenuClick({ menuItemId: "monoagent-capture-summary" }, { id: 42 });
  assert.deepEqual(store.captureQueue[0].envelope.meta.summarize, { kind: "page" });
  assert.match(record.toasts[0].text, /saved offline/);
});

test("the onClicked listener is the handler", async () => {
  const { listeners, sent } = setup();
  listeners.menu({ menuItemId: "monoagent-capture-screenshot" }, { id: 42 });
  const deadline = Date.now() + 5000;
  while (!sent.length && Date.now() < deadline) await new Promise((r) => setTimeout(r, 2));
  assert.deepEqual(artifactNames(sent), ["screenshot.png"]);
});

test("paramsFor keeps what the caller asked for over the mode's defaults", () => {
  const { env } = setup();
  const M = env.MonoCaptureModes;
  assert.deepEqual(M.paramsFor({ mode: "summary", formats: ["readable"] }).formats, ["readable"]);
  assert.ok(!M.paramsFor({ mode: "summary", summarize: false }).summarize);
  assert.equal(M.paramsFor({ mode: "nonsense" }).mode, "full");
});
