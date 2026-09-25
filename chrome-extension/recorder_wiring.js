/**
 * MonoAgent Bridge — the recorder's Chrome wiring, in the worker (§8.2, §8.6)
 *
 * recorder_session.js is the state machine; this hands it the real Chrome
 * APIs and routes what arrives: the page recorders' events, webNavigation,
 * tab lifecycle, and the side panel's requests. background.js calls
 * install() with the socket, as it does for recall_bridge.js, plus one line
 * each in onopen (flush) and onmessage (acks).
 *
 * The side panel holds a long-lived port named "monoagent-record". It is how
 * the panel hears state changes, and its disconnect is how "the panel was
 * closed" is noticed — the recording stops with reason "panel_closed" when
 * the port that started it goes away.
 *
 * Analyze / verify / save are extension→Go requests over ask.js
 * (record.analyze, record.verify, record.save); the Go side runs
 * `monoagentcli record … --json` and replies with its JSON.
 */

(function (root) {
  "use strict";

  const PORT_NAME = "monoagent-record";
  const RECORDER_FILES = ["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder.js"];
  // Analysis runs a model; verify replays the whole action in a browser.
  // Both report progress when the Go side supports it, which keeps the
  // idle timer from firing while they work.
  const LONG = { timeoutMs: 10 * 60 * 1000, idleTimeoutMs: 5 * 60 * 1000 };

  let session = null;
  let ready = Promise.resolve();
  let ownerPort = null;
  const ports = new Set();

  function broadcast(state) {
    for (const port of ports) {
      try {
        port.postMessage({ type: "record_state", state });
      } catch {
        ports.delete(port);
      }
    }
  }

  function badge(tabId, on) {
    const target = { tabId };
    chrome.action.setBadgeText(Object.assign({ text: on ? "REC" : "" }, target)).catch(() => {});
    if (on) chrome.action.setBadgeBackgroundColor(Object.assign({ color: "#d93025" }, target)).catch(() => {});
    chrome.action
      .setTitle(Object.assign({ title: on ? "MonoAgent — recording this tab" : "MonoAgent Bridge" }, target))
      .catch(() => {});
  }

  async function inject(tabId, frameId, pick) {
    const target = frameId == null ? { tabId, allFrames: true } : { tabId, frameIds: [frameId] };
    await chrome.scripting.executeScript({ target, files: RECORDER_FILES });
    if (pick) await setPickInTab(tabId, true, frameId);
  }

  async function uninject(tabId) {
    await chrome.scripting.executeScript({
      target: { tabId, allFrames: true },
      func: () => globalThis.MonoRecorder && globalThis.MonoRecorder.stopActive(),
    });
  }

  async function setPickInTab(tabId, on, frameId) {
    const target = frameId == null ? { tabId, allFrames: true } : { tabId, frameIds: [frameId] };
    await chrome.scripting
      .executeScript({ target, args: [!!on], func: (v) => globalThis.MonoRecorder && globalThis.MonoRecorder.setPick(v) })
      .catch(() => {});
  }

  async function tabInfo(tabId) {
    const tab = await chrome.tabs.get(tabId);
    return { url: tab.url || "", title: tab.title || "" };
  }

  async function profile() {
    try {
      const { captureProfile } = await chrome.storage.local.get("captureProfile");
      return typeof captureProfile === "string" ? captureProfile : "";
    } catch {
      return "";
    }
  }

  function install(d) {
    session = root.MonoRecorderSession.createSession({
      send: d.send,
      isConnected: d.isConnected,
      storage: d.storage || chrome.storage.local,
      inject,
      uninject,
      pick: (tabId, on) => setPickInTab(tabId, on),
      badge,
      notify: broadcast,
      tabInfo,
      profile,
    });
    // A worker woken BY a recorder event must not handle it before the
    // running recording has been read back from storage.
    ready = session.restore().then(() => session.flush(), () => {});
    registerChrome();
    registerMessages();
    registerPorts();
    return session;
  }

  /** later runs fn once the restored state is in place. */
  const later = (fn) => (...args) => {
    ready.then(() => fn(...args)).catch((err) => console.error("[monoagent] recorder:", err && err.message));
  };

  function registerChrome() {
    const nav = chrome.webNavigation;
    if (nav) {
      nav.onCommitted.addListener(later((details) => session.navigation(details, false)));
      nav.onHistoryStateUpdated.addListener(later((details) => session.navigation(details, true)));
      nav.onDOMContentLoaded.addListener(later((details) => session.documentReady(details)));
      nav.onCreatedNavigationTarget.addListener(later((details) => session.navigationTarget(details)));
    }
    chrome.tabs.onCreated.addListener(later((tab) => session.tabCreated(tab)));
    chrome.tabs.onRemoved.addListener(later((tabId) => session.tabRemoved(tabId)));
  }

  const request = (method, params, opts) => root.MonoAsk.request(method, params, opts);

  const handlers = {
    record_status: async () => ({ ok: true, state: session.status() }),
    record_start: async (msg) => {
      let tabId = msg.tabId;
      if (!tabId) {
        const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
        tabId = tab && tab.id;
      }
      return { ok: true, state: await session.start({ tabId, goal: msg.goal }) };
    },
    record_stop: async () => ({ ok: true, state: await session.stop("user") }),
    record_pick: async (msg) => ({ ok: true, state: await session.setPick(!!msg.on) }),
    record_mark: async (msg) => {
      session.mark(msg.mark);
      return { ok: true, state: session.status() };
    },
    record_clear: async () => ({ ok: true, state: session.clear() }),
    record_analyze: async (msg) => {
      const st = session.status();
      const recordingId = msg.recordingId || st.id;
      if (!recordingId) throw new Error("nothing has been recorded yet");
      if (st.id === recordingId && !st.delivered) {
        throw new Error("this recording is still waiting to reach the bridge — start it and try again");
      }
      const params = { recordingId };
      if (msg.automation) params.automation = msg.automation;
      // The recording landed in the inbox of the profile it started under.
      if (st.id === recordingId && st.profile) params.profile = st.profile;
      return { ok: true, result: await request("record.analyze", params, LONG) };
    },
    record_verify: async (msg) => ({
      ok: true,
      result: await request("record.verify", { draftDir: msg.draftDir }, LONG),
    }),
    record_save: async (msg) => {
      const params = { draftDir: msg.draftDir, saveAs: msg.saveAs || "action" };
      // An existing automation (`automation`) or a new one the analyzer named (`new`).
      if (msg.automation) params[msg.isNew ? "new" : "automation"] = msg.automation;
      if (msg.name) params.name = msg.name;
      return { ok: true, result: await request("record.save", params) };
    },
  };

  function registerMessages() {
    chrome.runtime.onMessage.addListener((msg, sender, respond) => {
      if (!msg || typeof msg.type !== "string") return false;
      if (msg.type === "recorder_event") {
        later(() => session.event(msg, sender))();
        return false;
      }
      const handler = handlers[msg.type];
      if (!handler) return false;
      ready
        .then(() => handler(msg, sender))
        .then((result) => respond(result))
        .catch((err) => respond({ ok: false, error: err.message || String(err), code: err.code }));
      return true;
    });
  }

  function registerPorts() {
    chrome.runtime.onConnect.addListener((port) => {
      if (port.name !== PORT_NAME) return;
      ports.add(port);
      port.onMessage.addListener((msg) => {
        // The panel that presses Record owns the recording: closing it stops it.
        if (msg && msg.type === "record_owner") ownerPort = port;
      });
      port.onDisconnect.addListener(() => {
        ports.delete(port);
        if (port === ownerPort || ports.size === 0) {
          ownerPort = null;
          later(() => session.panelClosed())();
        }
      });
      port.postMessage({ type: "record_state", state: session.status() });
    });
  }

  root.MonoRecorderWiring = {
    install,
    flush: () => session && session.flush(),
    handleFrame: (msg) => !!session && session.handleFrame(msg),
    PORT_NAME,
    RECORDER_FILES,
  };
})(globalThis);
