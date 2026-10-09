/**
 * MonoAgent Bridge — the chat's worker half (issue #451, slice 2)
 *
 * The side panel is a document that can close at any moment, so the long
 * request (`chat.send`, minutes) is made here, over ask.js, and the panel is
 * only told about it: progress frames are relayed as `chat_progress`
 * messages that nobody has to be listening to. Whatever a closed panel
 * misses it gets back from `chat.events` when it reopens (chat_core.js).
 *
 * Messages the panel may send (all answered {ok, ...}):
 *   chat_ready         {state: "ready" | "offline" | "update"}
 *   chat_page_context  {context}  url, title, readable text, selection of the
 *                      panel's active tab, capped. The page is untrusted; it is
 *                      read as data and nothing in it is ever run.
 *   chat_send          {data}     {conversation, turn, text} when the turn ends
 *   chat_stop, chat_events
 *
 * Runtime and model are the side panel's "AI for summaries" choice
 * (summary_ai.js), read from storage here so the panel and the right-click
 * menus keep one picker.
 *
 * After ask.js, chat_core.js and summary_ai.js; recall_bridge.js installs
 * ask.js itself, so install() here never does.
 */

(function (root) {
  "use strict";

  const Core = () => root.MonoChatCore;
  const CONTEXT_TIMEOUT_MS = 8000;
  const PAGE_FILES = ["markdown.js", "readable.js"];

  let deps = null;
  // One turn at a time per profile: a second send while one is running would
  // interleave two replies in one conversation.
  const sending = new Set();

  const storage = () => (deps && deps.storage) || chrome.storage.local;
  const profileOf = (msg) => (Core().validProfile(msg && msg.profile) ? msg.profile.trim() : "");
  const withProfile = (params, profile) => (profile ? Object.assign({}, params, { profile }) : params);

  function install(d) {
    deps = d || {};
    chrome.runtime.onMessage.addListener((msg, sender, respond) => {
      if (!msg || typeof msg.type !== "string") return false;
      const handler = handlers[msg.type];
      if (!handler) return false;
      handler(msg)
        .then((result) => respond(result))
        .catch((err) => respond({ ok: false, error: err.message || String(err), code: err.code }));
      return true;
    });
  }

  /** The page, read inside the tab. Serialized by executeScript: closes over nothing. */
  function readPage() {
    const R = globalThis.MonoReadable;
    let text = "";
    try {
      const tree = R.snapshot(document.documentElement, globalThis);
      text = R.extract(tree, { baseUrl: document.baseURI || location.href }).text || "";
    } catch {
      text = "";
    }
    let selection = "";
    try {
      selection = String(globalThis.getSelection ? globalThis.getSelection() : "");
    } catch {
      selection = "";
    }
    return { url: location.href, title: document.title, text, selection };
  }

  async function targetTab(msg) {
    const query = Number.isInteger(msg.windowId)
      ? { active: true, windowId: msg.windowId }
      : { active: true, lastFocusedWindow: true };
    const [tab] = await chrome.tabs.query(query);
    return tab || null;
  }

  async function pageContext(msg) {
    const tab = await targetTab(msg);
    if (!tab || !tab.id) return { ok: true, context: null };
    // What the tab itself says is the fallback: a page we cannot read (the
    // web store, a PDF viewer, chrome://) still has an address and a title.
    const fallback = Core().buildContext({ url: tab.url, title: tab.title, text: "", selection: "" });
    let timer = null;
    try {
      await chrome.scripting.executeScript({ target: { tabId: tab.id }, files: PAGE_FILES });
      const run = chrome.scripting.executeScript({ target: { tabId: tab.id }, func: readPage });
      const timeout = new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error("the page did not answer")), CONTEXT_TIMEOUT_MS);
      });
      const [res] = await Promise.race([run, timeout]);
      return { ok: true, context: Core().buildContext(res && res.result) || fallback };
    } catch {
      return { ok: true, context: fallback };
    } finally {
      clearTimeout(timer);
    }
  }

  async function ready() {
    if (!deps || !deps.isConnected || !deps.isConnected()) return { ok: true, state: "offline" };
    const methods = await root.MonoAsk.probe();
    if (root.MonoAsk.known() === null) return { ok: true, state: "offline" };
    return { ok: true, state: methods.indexOf(Core().METHOD_SEND) !== -1 ? "ready" : "update" };
  }

  async function choice() {
    try {
      return (await root.MonoSummaryAI.sticky(storage())) || {};
    } catch {
      return {};
    }
  }

  async function send(msg) {
    const message = Core().checkMessage(msg.message);
    if (!message) throw new Error("there is nothing to send");
    const profile = profileOf(msg);
    const key = profile || "_";
    if (sending.has(key)) throw new Error("a reply is still being written");

    const params = { message };
    if (typeof msg.conversation === "string" && msg.conversation) params.conversation = msg.conversation;
    const ai = await choice();
    if (ai.runtime) params.runtime = ai.runtime;
    if (ai.runtime && ai.model) params.model = ai.model;
    // Re-capped here whatever the panel sent: the cap is the worker's rule.
    const context = Core().buildContext(msg.context);
    if (context) params.context = context;

    sending.add(key);
    let remembered = false;
    try {
      const data = await root.MonoAsk.request("chat.send", withProfile(params, profile), {
        timeoutMs: 600000,
        idleTimeoutMs: 60000,
        onProgress: (progress) => {
          // The conversation may be named before the turn ends (the progress
          // wrapper's `conversation`); keep it even if nobody is listening
          // (a closed panel), once.
          if (!remembered) {
            const ev = Core().parseProgress(progress);
            const c = ev && (ev.conversation || ev.payload.conversation || ev.payload.conversation_id);
            if (typeof c === "string" && c) {
              remembered = true;
              Core().rememberConversation(storage(), profile, c);
            }
          }
          chrome.runtime.sendMessage({ type: "chat_progress", tag: msg.tag || "", progress }).catch(() => {});
        },
      });
      // Persisted here, not by the panel: it may have been closed on a
      // first turn, and the next one must continue the same conversation.
      await Core().rememberConversation(storage(), profile, data && data.conversation);
      return { ok: true, data: data || {} };
    } finally {
      sending.delete(key);
    }
  }

  const handlers = {
    chat_ready: ready,
    chat_page_context: pageContext,
    chat_send: send,
    chat_stop: async (msg) => {
      const params = { conversation: String(msg.conversation || "") };
      if (!params.conversation) throw new Error("there is no conversation to stop");
      await root.MonoAsk.request("chat.stop", withProfile(params, profileOf(msg)), { timeoutMs: 15000, idleTimeoutMs: 15000 });
      return { ok: true };
    },
    chat_events: async (msg) => {
      const conversation = String(msg.conversation || "");
      if (!conversation) throw new Error("there is no conversation to read");
      const params = { conversation };
      // seq is per turn, so after_seq only means something with its turn.
      if (msg.turn !== undefined && msg.turn !== null && msg.turn !== "") params.turn = msg.turn;
      if (Number.isFinite(msg.after_seq) && msg.after_seq > 0 && params.turn !== undefined) params.after_seq = msg.after_seq;
      const data = await root.MonoAsk.request("chat.events", withProfile(params, profileOf(msg)), { timeoutMs: 20000, idleTimeoutMs: 20000 });
      const turn = data && data.turn !== undefined && data.turn !== null ? String(data.turn) : "";
      return { ok: true, turn, events: Core().normalizeEvents(data && data.events, turn), turn_active: !!(data && data.turn_active) };
    },
  };

  root.MonoChat = { install, readPage, handlers };
})(globalThis);
