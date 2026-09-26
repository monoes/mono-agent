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
  const RECORDER_FILES = ["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder_dom.js", "recorder.js"];
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

  async function tabExists(tabId) {
    try {
      return !!(await chrome.tabs.get(tabId));
    } catch {
      return false;
    }
  }

  function install(d) {
    session = root.MonoRecorderSession.createSession({
      send: d.send,
      isConnected: d.isConnected,
      storage: d.storage || chrome.storage.local,
      // The live recording is session state: gone with the browser session.
      sessionStorage: d.sessionStorage || (chrome.storage.session || chrome.storage.local),
      tabExists,
      listen,
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
    // A new browser session: whatever the last one was recording is over.
    if (chrome.runtime.onStartup) {
      chrome.runtime.onStartup.addListener(() => {
        ready = ready.then(() => session.restore({ startup: true })).then(() => session.flush(), () => {});
      });
    }
    registerMessages();
    registerPorts();
    return session;
  }

  /** later runs fn once the restored state is in place. */
  const later = (fn) => (...args) => {
    ready.then(() => fn(...args)).catch((err) => console.error("[monoagent] recorder:", err && err.message));
  };

  // Navigation and tab listeners exist only while something is recording,
  // so no other browsing ever runs through this code. The side panel pings
  // the worker while recording, which keeps it (and these listeners) alive.
  const nav = () => chrome.webNavigation || {};
  const LISTENERS = [
    [() => nav().onCommitted, (d) => session.navigation(d, false)],
    [() => nav().onHistoryStateUpdated, (d) => session.navigation(d, true)],
    // Hash-route SPAs change only the fragment.
    [() => nav().onReferenceFragmentUpdated, (d) => session.navigation(d, false)],
    [() => nav().onDOMContentLoaded, (d) => session.documentReady(d)],
    [() => nav().onCreatedNavigationTarget, (d) => session.navigationTarget(d)],
    [() => chrome.tabs.onCreated, (tab) => session.tabCreated(tab)],
    [() => chrome.tabs.onRemoved, (tabId) => session.tabRemoved(tabId)],
  ].map(([event, fn]) => ({ event, fn: later(fn) }));

  function listen(on) {
    for (const l of LISTENERS) {
      const ev = l.event();
      if (!ev || !ev.addListener) continue;
      const has = ev.hasListener ? ev.hasListener(l.fn) : false;
      if (on && !has) ev.addListener(l.fn);
      if (!on && ev.removeListener) ev.removeListener(l.fn);
    }
  }

  const request = (method, params, opts) => root.MonoAsk.request(method, params, opts);

  const handlers = {
    record_status: async () => ({ ok: true, state: session.status() }),
    // A page restored from the back/forward cache asks whether to keep recording.
    recorder_alive: async (msg, sender) => {
      const st = session.status();
      return { recording: st.recording && !!sender.tab && sender.tab.id === st.tabId };
    },
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
      if (st.id === recordingId && st.discarded) throw new Error("nothing was saved: the recording had no steps");
      if (st.id === recordingId && !st.delivered) {
        throw new Error("this recording is still waiting to reach the bridge — start it and try again");
      }
      const params = { recordingId };
      if (msg.automation) params.automation = msg.automation;
      // The recording landed in the inbox of the profile it started under.
      if (st.id === recordingId && st.profile) params.profile = st.profile;
      return { ok: true, result: await request("record.analyze", params, LONG) };
    },
    record_verify: async (msg) => {
      const params = { draftDir: msg.draftDir };
      // Values for inputs the recording did not capture (secrets included),
      // used for this replay only and never stored here.
      if (msg.inputs && typeof msg.inputs === "object" && Object.keys(msg.inputs).length) params.inputs = msg.inputs;
      const st = session.status();
      if (st.profile) params.profile = st.profile;
      // A failed replay settles normally with its report (ok:false); only
      // a command that never produced a report is an error.
      return { ok: true, result: await request("record.verify", params, LONG) };
    },
    record_save: async (msg) => {
      const params = { draftDir: msg.draftDir, saveAs: msg.saveAs || "action" };
      // An existing automation (`automation`) or a new one the analyzer named (`new`).
      if (msg.automation) params[msg.isNew ? "new" : "automation"] = msg.automation;
      if (msg.name) params.name = msg.name;
      // The person confirmed saving despite error-level lint.
      if (msg.force === true) params.force = true;
      return { ok: true, result: await request("record.save", params) };
    },
  };

  function registerMessages() {
    chrome.runtime.onMessage.addListener((msg, sender, respond) => {
      if (!msg || typeof msg.type !== "string") return false;
      if (!(msg.type in handlers) && msg.type !== "recorder_event") return false;
      // Only this extension talks to the recorder: the page recorders (from
      // a tab) send recorder_event / recorder_alive, and only the extension's
      // own pages (the side panel) may drive a recording.
      if (!sender || sender.id !== chrome.runtime.id) return false;
      const fromPage = msg.type === "recorder_event" || msg.type === "recorder_alive";
      if (fromPage ? !sender.tab : !isExtensionPage(sender)) return false;
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

  function isExtensionPage(sender) {
    const base = chrome.runtime.getURL ? chrome.runtime.getURL("") : "";
    return !!base && String(sender.url || "").startsWith(base);
  }

  function registerPorts() {
    chrome.runtime.onConnect.addListener((port) => {
      if (port.name !== PORT_NAME) return;
      if (!port.sender || port.sender.id !== chrome.runtime.id || !isExtensionPage(port.sender)) return;
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
      // After restore, so a worker woken by this panel does not report an
      // empty state for a recording it is still reading back.
      ready.then(() => {
        try {
          port.postMessage({ type: "record_state", state: session.status() });
        } catch {
          // the panel closed already
        }
      });
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
