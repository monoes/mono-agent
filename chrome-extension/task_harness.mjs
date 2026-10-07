// The task bridge against a fake Chrome and a fake request channel, shared by
// task_bridge.test.mjs and task_menu.test.mjs. Nothing here starts a browser.

import { loadExtensionScripts } from "./test_helpers.mjs";

const SCRIPTS = [
  "capture_profile.js", "capture_modes.js", "capture_queue.js", "recorder_privacy.js",
  "task_outbox.js", "task_bridge.js", "task_menu.js",
];

const clone = (v) => (v === undefined ? v : JSON.parse(JSON.stringify(v)));

/** fakeStorage has chrome.storage.local's shape over a plain object. */
export function fakeStorage(seed = {}) {
  const data = clone(seed);
  return {
    data,
    get: async (keys) => Object.fromEntries([].concat(keys).filter((k) => k in data).map((k) => [k, clone(data[k])])),
    set: async (values) => {
      Object.assign(data, clone(values));
    },
  };
}

/**
 * fakeAsk stands in for ask.js. `methods` is what ping advertised, or null
 * for a bridge that never answered; `reply(params, method)` answers a
 * request, returning its data or throwing refuse(code, message).
 */
export function fakeAsk({ methods = ["ping", "profile.list", "task.add"], reply = () => ({ id: 1, created: true }) } = {}) {
  const calls = [];
  let inFlight = 0;
  let most = 0;
  return {
    calls,
    most: () => most,
    probe: async () => methods || [],
    known: () => methods,
    supports: async (m) => !!methods && methods.includes(m),
    async request(method, params) {
      calls.push({ method, params: clone(params) });
      inFlight += 1;
      most = Math.max(most, inFlight);
      try {
        await new Promise((r) => setTimeout(r, 1));
        return await reply(params, method);
      } finally {
        inFlight -= 1;
      }
    },
  };
}

/** refuse is what ask.js rejects with when the bridge answers ok:false. */
export function refuse(code, message) {
  const err = new Error(message || code);
  err.code = code;
  return err;
}

/**
 * setupTasks installs the task bridge (and the task menu, once it exists)
 * over fakes. `profile` is the stored "Saving into" choice (null: none, and
 * no cached list either); `selection` is what a page's selection reads as;
 * `scriptFails` makes the page refuse scripts.
 */
export function setupTasks({ connected = true, ask = fakeAsk(), profile = "p-work", selection = "", scriptFails = false } = {}) {
  const net = { up: connected };
  const listeners = { message: [], menu: [], command: [], alarm: [] };
  const record = { toasts: [], badges: [], titles: [], broadcasts: [], opened: [], scripts: [], alarms: new Map() };
  const local = fakeStorage(
    profile
      ? { captureProfile: profile, captureProfilesCache: [{ id: "p-work", name: "Work" }, { id: "p-home", name: "Home" }] }
      : {}
  );
  const session = fakeStorage();
  const on = (list) => ({ addListener: (fn) => list.push(fn) });
  const chrome = {
    runtime: {
      id: "ext-id",
      lastError: null,
      getURL: (path) => `chrome-extension://ext-id/${path}`,
      onMessage: on(listeners.message),
      sendMessage: (m) => {
        record.broadcasts.push(clone(m));
        return Promise.resolve();
      },
    },
    contextMenus: { onClicked: on(listeners.menu) },
    commands: { onCommand: on(listeners.command) },
    alarms: {
      onAlarm: on(listeners.alarm),
      create: async (name, info) => {
        record.alarms.set(name, info);
      },
      get: async (name) => record.alarms.get(name),
      clear: async (name) => record.alarms.delete(name),
    },
    action: {
      setBadgeText: async (o) => {
        record.badges.push(o.text);
      },
      setBadgeBackgroundColor: async () => {},
      setTitle: async (o) => {
        record.titles.push(o.title);
      },
    },
    scripting: {
      executeScript: async ({ func, target }) => {
        record.scripts.push({ name: func && func.name, target: clone(target) });
        if (scriptFails) throw new Error("Cannot access contents of the page");
        return [{ result: selection }];
      },
    },
    sidePanel: {
      open: (o) => {
        record.opened.push(clone(o));
        return Promise.resolve();
      },
    },
    storage: { local, session },
  };
  const env = loadExtensionScripts(SCRIPTS, { chrome });
  env.MonoAsk = ask;
  env.MonoCaptureBridge = { toast: (tabId, text, level) => record.toasts.push({ tabId, text, level }) };
  env.MonoTaskBridge.install({ isConnected: () => net.up, storage: local });
  if (env.MonoTaskMenu) env.MonoTaskMenu.install();
  return { env, chrome, listeners, record, local, session, ask, net, B: env.MonoTaskBridge, M: env.MonoTaskMenu };
}

/** send delivers one runtime message the way Chrome does and resolves the answer (undefined when nobody answers). */
export function send(listeners, msg, sender) {
  return new Promise((resolve) => {
    for (const fn of listeners.message) {
      if (fn(msg, sender, resolve) === true) return;
    }
    resolve(undefined);
  });
}

/** A content script's sender: a tab, as Chrome describes it. */
export const PAGE = {
  id: "ext-id",
  tab: { id: 42, windowId: 7, url: "https://sam:hunter2@mail.example/inbox?token=abc&q=1#msg-3", title: "Inbox (3)" },
};

/** The side panel's sender: this extension's own page, no tab. */
export const PANEL = { id: "ext-id", url: "chrome-extension://ext-id/sidepanel.html" };

/** until waits for something the bridge reaches on its own, with a deadline. */
export async function until(check, what) {
  const deadline = Date.now() + 5000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 2));
  }
}
