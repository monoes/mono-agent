/**
 * MonoAgent Bridge - tasks from the browser (task board spec section 11)
 *
 * Every way a task is added in Chrome ends here: the task button beside a
 * selection (highlight_page.js), the side panel's "Add a task" box
 * (sidepanel_tasks.js), and the MonoAgent menu's two task items and the
 * add-task shortcut (task_menu.js). add() queues the task in the outbox
 * (task_outbox.js), then flushes the outbox over the request channel (ask.js,
 * method task.add: internal/extension/task_add.go).
 *
 * What this file guarantees:
 *
 *   - a task names a profile: the side panel's box uses the profile the
 *     panel shows, everything else the sticky "Saving into" choice, as the
 *     capture shortcut does. With none known nothing is queued and the
 *     person is told to choose one: a task never falls back to the shared
 *     inbox;
 *   - a page's address and title come from Chrome's description of the
 *     sender, never from the message, and the address loses its user-info,
 *     fragment and session tokens before it is stored; a title that is only
 *     that address (what Chrome is believed to give a page with no title) is
 *     dropped, for it would carry the tokens;
 *   - messages are checked as recorder_wiring.js checks them: only this
 *     extension's own pages may add a note or read or clear the refusals,
 *     and a tab's content script may only add a selection;
 *   - the outbox is sent one request at a time, oldest first, each task
 *     under the id it was queued with: the bridge answers a ninth concurrent
 *     request "busy", so a burst would never drain;
 *   - nothing leaves the outbox unless MonoAgent took it (made now, or by an
 *     earlier attempt) or refused it for good (invalid_input), and a refusal
 *     is reported: a failures list, the badge, an open side panel.
 *
 * Task text is never written to the console: it is text from a web page.
 */

(function (root) {
  "use strict";

  const METHOD = "task.add";
  const ALARM = "monoagent-task-outbox";
  // The side panel opens its task box when this is fresh (sidepanel_tasks.js).
  const FOCUS_KEY = "taskFocusAt";
  const MAX_TITLE_CHARS = 1000;
  const MAX_URL_BYTES = 2048;
  // Answers that keep the task and stop the flush: the rest of the outbox
  // would get the same answer (and a locked database would cost its timeout
  // once per task). `limit` keeps the task and the flush goes on, since
  // another profile may have room; `invalid_input` drops it.
  const STOP = new Set(["offline", "busy", "timeout", "unavailable", "unknown_method", "internal"]);

  let deps = null;
  let box = null;
  let flushing = null; // the flush running now
  let again = null; // one more, queued behind it
  // What a flush did with a task an add() is waiting on, by client id: the
  // flush that sends it may be one that started before that add() asked.
  const awaited = new Set();
  const outcomes = new Map();

  const storage = () => deps.storage;
  const Outbox = () => root.MonoTaskOutbox;
  const Profiles = () => root.MonoCaptureProfile;
  const Ask = () => root.MonoAsk;

  /**
   * install takes isConnected() and storage from background.js. The request
   * channel is ask.js's, installed by MonoRecall.install; installing it again
   * here would forget what the bridge said it can answer.
   */
  function install(d) {
    deps = d;
    box = Outbox().create(d.storage);
    registerMessages();
    registerAlarm();
    afterChange();
  }

  // --- adding ----------------------------------------------------------------

  /**
   * add queues one task, then tries to send it. `what` is {kind, text, url,
   * title, profile}; kind is selection, page or note; a title that is only
   * the url's address is not kept (pageTitle). Resolves {ok, status, id,
   * feedback}: status is added, queued, refused, full, no_profile or empty,
   * and feedback is the one line the person sees, {level, text}.
   */
  async function add(what) {
    const T = Outbox();
    const kind = what.kind;
    const text = kind === "page" ? "" : T.capBytes(String(what.text || "").trim(), T.MAX_TEXT_BYTES);
    const url = kind === "note" ? "" : pageUrl(what.url);
    const title = kind === "note" ? "" : pageTitle(what).trim().slice(0, MAX_TITLE_CHARS);
    if (!what.profile) return outcome("no_profile", { kind });
    if (kind === "page" ? !url && !title : !text) return outcome("empty", { kind });

    const entry = { client_id: newClientId(), text, url, title, kind, profile: what.profile, at: new Date().toISOString() };
    // Awaited before it is queued: a flush another add() started may send it
    // before this one gets to ask, and what it did must not be lost.
    awaited.add(entry.client_id);
    try {
      const queued = await box.enqueue(entry);
      if (!queued.queued) return outcome("full", { reason: queued.reason });
      await afterChange();
      const { stopped } = await flush();
      const sent = outcomes.get(entry.client_id) || { state: "kept", code: stopped || "offline" };
      if (sent.state === "added") return outcome("added", { id: sent.id, name: await profileName(what.profile) });
      if (sent.state === "refused") return outcome("refused", { reason: sent.reason });
      return outcome("queued", { code: sent.code, reason: sent.reason });
    } finally {
      awaited.delete(entry.client_id);
      outcomes.delete(entry.client_id);
    }
  }

  function outcome(status, info) {
    return {
      ok: status === "added" || status === "queued",
      status,
      id: (info && info.id) || null,
      feedback: feedbackFor(status, info),
    };
  }

  /** feedbackFor is the one line a person sees about an add. */
  function feedbackFor(status, info) {
    const i = info || {};
    switch (status) {
      case "added":
        return { level: "ok", text: `Added to Inbox in ${i.name} (#${i.id})` };
      case "queued":
        if (i.code === "offline") return { level: "warn", text: "Saved: will sync when MonoAgent is running" };
        if (i.code === "outdated") {
          return { level: "warn", text: "Saved: MonoAgent needs updating before it can take tasks; it syncs once updated" };
        }
        if (i.code === "limit" || i.code === "internal") {
          return { level: "error", text: `Not added yet: ${i.reason || i.code}. It stays queued and is tried again every minute` };
        }
        return { level: "warn", text: `Saved: will sync when MonoAgent can take it (${i.reason || i.code})` };
      case "refused":
        return { level: "error", text: `Not added: ${i.reason}` };
      case "full":
        return { level: "error", text: `Not added: ${i.reason}; start MonoAgent to send them` };
      case "no_profile":
        return { level: "error", text: 'Choose a profile first: pick one under "Saving into" in MonoAgent\'s side panel' };
      case "empty":
        if (i.kind === "note") return { level: "error", text: "Type a task first" };
        if (i.kind === "page") return { level: "error", text: "This page has no title or address to add" };
        return { level: "error", text: "Nothing to add: select some text first" };
    }
    return { level: "error", text: "Not added" };
  }

  /**
   * pageUrl is the address a task keeps of its page: http or https only,
   * without user-info, through the recorder's sanitizer (no fragment,
   * session tokens redacted); "" when nothing is worth keeping, and "" when
   * the sanitizer is missing (fail closed). Go checks it again.
   */
  function pageUrl(raw) {
    const privacy = root.MonoRecorderPrivacy;
    if (!privacy) return "";
    let u;
    try {
      u = new URL(String(raw || ""));
    } catch {
      return "";
    }
    if (u.protocol !== "http:" && u.protocol !== "https:") return "";
    u.username = "";
    u.password = "";
    const out = privacy.sanitizeUrl(u.toString());
    return new TextEncoder().encode(out).length > MAX_URL_BYTES ? "" : out;
  }

  // A scheme and "://": a title that starts with one is an address.
  const ADDRESS_SCHEME = /^[a-z][a-z0-9+.-]*:\/\//;
  const WWW = /^www\./;

  /**
   * pageTitle is the title a task keeps of a page: page.title (a tab, or what
   * add() is given: anything with a title and a url), unless that is only the
   * page's address, and then "" (the sink names the page by its sanitized
   * address). Chrome is believed to give a page with no <title> its address
   * as the title, query string and all, and only the url passes pageUrl's
   * sanitizer: sent as it is, a session token would reach the task, and
   * chrome.storage, by way of the title. In lower case and without a leading "www.", a title is
   * only the address when it starts with a scheme ("https://..."), or is the
   * url's host (with its port) alone or followed by "/", "?" or "#" and the
   * rest of the address. A title that merely mentions the host ("mail.example
   * is down") or starts with it and goes on in words ("mail.example - Inbox")
   * is a title and stays. A host that is not plain ASCII is not recognised:
   * the url has it in punycode, Chrome shows it in Unicode. "" when there is
   * no title.
   */
  function pageTitle(page) {
    const raw = page && typeof page.title === "string" ? page.title : "";
    const title = raw.trim().toLowerCase().replace(WWW, "");
    if (!title) return "";
    if (ADDRESS_SCHEME.test(title)) return "";
    let u;
    try {
      u = new URL(String((page && page.url) || ""));
    } catch {
      return raw;
    }
    if (u.protocol !== "http:" && u.protocol !== "https:") return raw;
    const host = u.host.replace(WWW, "");
    return title === host || (title.startsWith(host) && "/?#".includes(title[host.length])) ? "" : raw;
  }

  /** newClientId names a task for good: a resend under it adds nothing twice. */
  function newClientId() {
    const uuid = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : "";
    return `t-${uuid || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`}`;
  }

  // --- which profile -----------------------------------------------------------

  /** stickyProfile is the "Saving into" choice the page, the menu and the shortcut use, or "". */
  async function stickyProfile() {
    const P = Profiles();
    const id = await P.stickyOrAsk(Ask(), storage(), deps.isConnected());
    return P.isValidProfileId(id) ? id.trim() : "";
  }

  /** panelProfile is the profile the side panel says it shows; "" is the shared inbox, or nonsense. */
  function panelProfile(msg) {
    const id = typeof msg.profile === "string" ? msg.profile.trim() : "";
    return Profiles().isValidProfileId(id) ? id : "";
  }

  async function profileName(id) {
    const { profiles } = await Profiles().load(storage());
    const match = profiles.find((p) => p.id === id);
    return match ? match.name : "your profile";
  }

  // --- what the page and the side panel may ask ------------------------------

  /** isExtensionPage: one of this extension's own pages, wherever it is open (recorder_wiring.js). */
  function isExtensionPage(sender) {
    const base = chrome.runtime.getURL ? chrome.runtime.getURL("") : "";
    return !!base && String(sender.url || "").startsWith(base);
  }

  function registerMessages() {
    chrome.runtime.onMessage.addListener((msg, sender, respond) => {
      if (!msg || typeof msg.type !== "string" || !HANDLERS[msg.type]) return false;
      // Only this extension: its own pages for every message, and a tab's
      // content script for task_add alone.
      if (!sender || sender.id !== chrome.runtime.id) return false;
      const panel = isExtensionPage(sender);
      if (!panel && !(msg.type === "task_add" && sender.tab)) return false;
      HANDLERS[msg.type](msg, sender, panel).then(respond, (err) =>
        respond({ ok: false, status: "error", feedback: { level: "error", text: `Not added: ${(err && err.message) || err}` } })
      );
      return true; // async response
    });
  }

  const HANDLERS = {
    // The page's task button sends a selection; the side panel sends a note
    // with the profile it shows. Nothing else in a message is trusted: the
    // page's address is the sending frame's (else the tab's), its title the
    // tab's, and a page cannot pick the profile.
    task_add: async (msg, sender, panel) => {
      if (panel) return add({ kind: "note", text: msg.text, profile: panelProfile(msg) });
      const tab = sender.tab;
      const address = sender.frameId === 0 && sender.url ? sender.url : tab.url;
      const out = await add({ kind: "selection", text: msg.text, url: address, title: tab.title, profile: await stickyProfile() });
      announce(tab.id, out.feedback);
      return out;
    },
    task_state: async () => ({ ok: true, waiting: (await box.list()).length, failures: await box.failures() }),
    task_dismiss: async () => {
      await box.dismiss();
      await afterChange();
      return { ok: true };
    },
  };

  // --- sending -----------------------------------------------------------------

  /**
   * flush sends what waits. One runs at a time; a call made while one runs
   * gets the flush after it, which sees everything queued before the call.
   * Resolves {stopped}: the code it stopped on ("offline", "outdated",
   * "busy", ...), or null when it went through the whole outbox.
   */
  function flush() {
    if (!flushing) {
      flushing = flushOnce().finally(() => {
        flushing = null;
      });
      return flushing;
    }
    if (!again) {
      again = flushing.then(() => {
        again = null;
        return flush();
      });
    }
    return again;
  }

  async function flushOnce() {
    try {
      const waiting = await box.list();
      if (!waiting.length) return { stopped: null };
      if (!deps.isConnected()) return { stopped: "offline" };
      const methods = await Ask().probe();
      // probe() answers [] both for a bridge that has no task.add and for one
      // that never heard the ping; only the first needs updating.
      if (Ask().known() === null) return { stopped: "offline" };
      if (methods.indexOf(METHOD) === -1) return { stopped: "outdated" };
      for (const entry of waiting) {
        const sent = await sendOne(entry);
        if (awaited.has(entry.client_id)) outcomes.set(entry.client_id, sent);
        if (sent.state === "kept" && STOP.has(sent.code)) return { stopped: sent.code };
      }
      return { stopped: null };
    } catch {
      // Storage could not be read or written: the tasks stay where they are.
      return { stopped: "offline" };
    } finally {
      await afterChange();
    }
  }

  /** sendOne sends one task, under the id it was queued with, and settles its place in the outbox. */
  async function sendOne(entry) {
    let data;
    try {
      data = await Ask().request(METHOD, {
        client_id: entry.client_id,
        text: entry.text,
        url: entry.url,
        title: entry.title,
        kind: entry.kind,
        profile: entry.profile,
      });
    } catch (err) {
      const code = (err && err.code) || "internal";
      const reason = (err && err.message) || code;
      if (code !== "invalid_input") return { state: "kept", code, reason };
      await box.fail(entry, reason);
      // An add() waiting on this task reports it itself; a background flush
      // tells an open side panel.
      if (!awaited.has(entry.client_id)) notify(feedbackFor("refused", { reason }));
      return { state: "refused", reason };
    }
    await box.remove(entry.client_id);
    return { state: "added", id: data && data.id, created: !!(data && data.created) };
  }

  /** afterChange keeps the alarm and the badge in step with the outbox. Never throws. */
  async function afterChange() {
    try {
      await settleAlarm();
      if (root.MonoCaptureQueue) await root.MonoCaptureQueue.paintBadge(storage());
    } catch {
      // Cosmetic and self-healing: the next change paints again.
    }
  }

  /** settleAlarm keeps the minute alarm armed while a task waits, and only then. */
  async function settleAlarm() {
    if (!chrome.alarms) return;
    if (!(await Outbox().count(storage()))) {
      await chrome.alarms.clear(ALARM);
      return;
    }
    if (!(await chrome.alarms.get(ALARM))) await chrome.alarms.create(ALARM, { periodInMinutes: 1 });
  }

  function registerAlarm() {
    if (!chrome.alarms || !chrome.alarms.onAlarm) return;
    chrome.alarms.onAlarm.addListener((alarm) => {
      if (alarm && alarm.name === ALARM) flush();
    });
  }

  /** connected is called from ws.onopen: whatever waited goes now. */
  function connected() {
    return flush();
  }

  // --- telling the person --------------------------------------------------------

  /** announce shows an outcome in the page it came from and in an open side panel. */
  function announce(tabId, feedback) {
    toast(tabId, feedback.text, feedback.level);
    notify(feedback);
  }

  function toast(tabId, text, level) {
    const bridge = root.MonoCaptureBridge;
    if (tabId && bridge && bridge.toast) bridge.toast(tabId, text, level);
  }

  /** notify tells an open side panel; with none open nobody listens, which is fine. */
  function notify(feedback) {
    try {
      const sent = chrome.runtime.sendMessage({ type: "task_result", feedback });
      if (sent && sent.catch) sent.catch(() => {});
    } catch {
      // No listener: the panel is closed.
    }
  }

  root.MonoTaskBridge = {
    install, add, flush, connected, stickyProfile, announce, toast, feedbackFor, pageUrl, pageTitle, METHOD, ALARM, FOCUS_KEY,
  };
})(globalThis);
