// Shared set-up for the recording session tests (recorder_session.test.mjs
// and recorder_session_delivery.test.mjs): the session loaded with its
// outbox and privacy modules, fake storage, and a harness that fakes every
// Chrome-facing dependency.

import { loadExtensionScripts } from "./test_helpers.mjs";
import { goJsonTags } from "./recorder_go_types.mjs";
export const { MonoRecorderSession: RS } = loadExtensionScripts(["recorder_privacy.js", "recorder_outbox.js", "recorder_session.js"]);
export const TAGS = goJsonTags();

export function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const out = {};
      for (const k of [].concat(keys)) if (k in data) out[k] = JSON.parse(JSON.stringify(data[k]));
      return out;
    },
    set: async (values) => {
      if (data.__failSet) throw new Error(data.__failSet);
      Object.assign(data, JSON.parse(JSON.stringify(values)));
    },
    remove: async (keys) => {
      for (const k of [].concat(keys)) delete data[k];
    },
  };
}

export function harness(opts = {}) {
  let connected = opts.connected !== false;
  let clock = 1_000_000;
  const wire = [];
  const injected = [];
  const badges = [];
  const notes = [];
  const storage = opts.storage || fakeStorage();
  const sessionStorage = opts.sessionStorage || fakeStorage();
  const listening = [];
  const picks = [];
  let tabAlive = opts.tabAlive !== false;
  const deps = {
    send: (f) => {
      if (!connected) return false;
      wire.push(JSON.parse(JSON.stringify(f)));
      return true;
    },
    isConnected: () => connected,
    storage,
    sessionStorage,
    maxFrames: opts.maxFrames,
    maxBytes: opts.maxBytes,
    tabExists: async () => tabAlive,
    listen: (on) => listening.push(on),
    now: () => clock,
    settleMs: 0,
    inject: async (tabId, frameId) => {
      if (opts.injectFails) throw new Error("Cannot access a chrome:// URL");
      injected.push([tabId, frameId]);
    },
    uninject: async () => {},
    pick: async (tabId, on) => picks.push(on),
    badge: (tabId, on) => badges.push([tabId, on]),
    notify: (st) => notes.push(st),
    tabInfo: async (tabId) => ({ url: `https://app.test/t${tabId}`, title: "App" }),
    profile: async () => opts.profile || "",
  };
  const s = RS.createSession(deps);
  return {
    s, wire, injected, badges, notes, storage, sessionStorage, deps, listening, picks,
    closeTab: () => (tabAlive = false),
    /** ackAll answers every frame on the wire the way Go does. */
    ackAll: (opts2 = {}) => {
      const frames = wire.splice(0);
      for (const f of frames) {
        s.handleFrame({ id: f.id, success: true, type: "recording", data: f.op === "stop" ? { id: opts2.envelope || "env-1" } : undefined });
      }
      return frames;
    },
    tick: (ms) => (clock += ms),
    now: () => clock,
    online: () => (connected = true),
    offline: () => (connected = false),
  };
}

export const fromTab = (id) => ({ tab: { id } });
export const ev = (type, extra = {}) => ({ type: "recorder_event", event: Object.assign({ type, url: "https://app.test/t7" }, extra) });
