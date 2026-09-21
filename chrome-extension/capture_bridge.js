/**
 * MonoAgent Bridge — capture wiring (CLIP-01, CLIP-04, CLIP-08)
 *
 * Everything that has to touch both Chrome and the WebSocket: the Chrome
 * APIs capture.js needs handed to it, the three ways a user starts a capture
 * (popup button, keyboard shortcut, context menu), and what happens to a
 * capture taken while the bridge is down.
 *
 * background.js only imports this file and calls install() with the socket
 * and debugger helpers it owns — the dispatch it adds is four lines.
 */

(function (root) {
  "use strict";

  const MENU_PAGE = "monoagent-capture-page";
  const MENU_SELECTION = "monoagent-capture-selection";
  // A capture the user started carries an id nothing on the Go side is
  // waiting for, so it is routed by type instead — the same type as the
  // command it mirrors (see internal/extension/capture.go's isCaptureResponse).
  const PUSH_TYPE = "page_capture";
  let deps = null;

  /**
   * install hands over the pieces background.js owns: send(message) writes one
   * frame to the WebSocket, isConnected() reports whether that will land, and
   * attach/cdp/detach are its existing per-tab debugger helpers.
   *
   * Two of those are load-bearing in ways that are easy to leave out, and
   * both were:
   *
   *   maxMessageBytes — the per-frame budget capture.js splits artifacts
   *     against. Without it every capture is planned against a budget of
   *     NaN, which produces an envelope carrying no bytes at all.
   *   send() — returns false when the frame did not reach the socket. A
   *     send that quietly does nothing when the socket is closed is
   *     indistinguishable from a successful one, and the queue then deletes
   *     captures it never delivered (CLIP-08).
   */
  function install(d) {
    deps = d;
    registerMenus();
    registerCommands();
    registerPopupChannel();
  }

  function context() {
    return {
      resolveTabId: async (params) => params.tabId || (await activeTabId()),
      inject: (tabId, files) =>
        chrome.scripting.executeScript({ target: { tabId }, files }),
      callPage: async (tabId, fn, args) => {
        const results = await chrome.scripting.executeScript({
          target: { tabId },
          // The injected files above defined MonoCapturePage in this same
          // isolated world, so it is still here on the next call.
          func: (name, callArgs) => globalThis.MonoCapturePage[name](...callArgs),
          args: [fn, args],
        });
        if (!results || !results.length) throw new Error(`no result from ${fn}`);
        return results[0].result;
      },
      // RCL-04: the page's saved highlights, as a highlights.json artifact.
      // Absent when recall_bridge.js has not been installed, which is how
      // capture keeps working on its own.
      highlights: root.MonoRecall ? (url) => root.MonoRecall.highlightsArtifact(url) : null,
      attach: (tabId) => deps.attach(tabId),
      cdp: (tabId, method, params) => deps.cdp(tabId, method, params || {}),
      detach: async (tabId) => deps.detach(tabId),
    };
  }

  async function activeTabId() {
    const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    return tab && tab.id;
  }

  function sendEnvelope(id, result, type) {
    const messages = root.MonoCapture.planMessages(id, result.meta, result.artifacts, result.warnings, {
      type: type || null,
      maxMessageBytes: deps.maxMessageBytes,
    });
    for (const message of messages) {
      // A throw here is the only thing that tells the queue this capture is
      // still owed. Half an envelope on the wire is not a delivery: the
      // receiver completes on data.final, so a run that stops short leaves
      // nothing behind on the Go side either.
      if (deps.send(message) === false) throw new Error("the bridge disconnected mid-send");
    }
  }

  /** handleCommand answers a Go-initiated page_capture. */
  async function handleCommand(id, params) {
    const result = await root.MonoCapture.pageCapture(params, context());
    sendEnvelope(id, result, null);
    return result;
  }

  /**
   * captureActiveTab is the user-initiated path. The bridge may be down —
   * a capture is the user's work, so it is queued rather than lost (CLIP-08).
   */
  async function captureActiveTab(options) {
    const params = Object.assign({}, options || {});
    if (!params.tabId) params.tabId = await activeTabId();
    if (!params.tabId) throw new Error("no active tab");

    badge("...", "#888888");
    let result;
    try {
      result = await root.MonoCapture.pageCapture(params, context());
    } catch (err) {
      badge("!", "#c0392b");
      throw err;
    }

    // The zero-interaction path: a shortcut capture is filed into whichever
    // profile was last chosen, read from storage alone. Nothing here waits
    // on the bridge — the profile is a label on an envelope, and a capture
    // must never be held up by a question about filing.
    if (root.MonoCaptureProfile) {
      const profile =
        params.profile !== undefined
          ? params.profile
          : await root.MonoCaptureProfile.stickyOrAsk(
              root.MonoAsk,
              chrome.storage.local,
              deps.isConnected()
            );
      root.MonoCaptureProfile.applyToMeta(result.meta, profile);
    }

    const id = `ext-${(crypto.randomUUID && crypto.randomUUID()) || Date.now()}`;
    if (deps.isConnected()) {
      sendEnvelope(id, result, PUSH_TYPE);
      badge(result.warnings.length ? "!" : "ok", result.warnings.length ? "#c98a00" : "#2e8b57");
      return { ok: true, queued: false, title: result.meta.title, warnings: result.warnings };
    }

    const queued = await root.MonoCapture.queueCapture(chrome.storage.local, {
      id,
      meta: result.meta,
      artifacts: result.artifacts,
      warnings: result.warnings,
    });
    badge(queued.queued ? "q" : "!", queued.queued ? "#c98a00" : "#c0392b");
    return {
      ok: queued.queued,
      queued: queued.queued,
      title: result.meta.title,
      warnings: result.warnings.concat(queued.queued ? [] : [queued.reason]),
    };
  }

  /** flush resends everything captured while the bridge was down. */
  function flush() {
    if (!deps || !deps.isConnected()) return Promise.resolve({ flushed: 0 });
    return root.MonoCapture.flushQueue(chrome.storage.local, (envelope) =>
      sendEnvelope(envelope.id, envelope, PUSH_TYPE)
    );
  }

  function badge(text, color) {
    try {
      chrome.action.setBadgeText({ text });
      chrome.action.setBadgeBackgroundColor({ color });
      if (text !== "...") setTimeout(() => chrome.action.setBadgeText({ text: "" }).catch(() => {}), 4000);
    } catch {
      // No action badge (popup closed, API unavailable) — cosmetic only.
    }
  }

  function registerMenus() {
    if (!chrome.contextMenus) return;
    chrome.contextMenus.removeAll(() => {
      chrome.contextMenus.create({ id: MENU_PAGE, title: "Save page to monomind", contexts: ["page"] });
      chrome.contextMenus.create({
        id: MENU_SELECTION,
        title: "Save selection to monomind",
        contexts: ["selection"],
      });
      void chrome.runtime.lastError; // duplicate ids after a worker restart
    });
    chrome.contextMenus.onClicked.addListener((info, tab) => {
      if (info.menuItemId !== MENU_PAGE && info.menuItemId !== MENU_SELECTION) return;
      captureActiveTab({
        tabId: tab && tab.id,
        selection: info.menuItemId === MENU_SELECTION,
      }).catch((err) => console.error("[monoagent] capture failed:", err.message));
    });
  }

  function registerCommands() {
    if (!chrome.commands) return;
    chrome.commands.onCommand.addListener((command) => {
      if (command !== "capture-page" && command !== "capture-selection") return;
      captureActiveTab({ selection: command === "capture-selection" }).catch((err) =>
        console.error("[monoagent] capture failed:", err.message)
      );
    });
  }

  function registerPopupChannel() {
    chrome.runtime.onMessage.addListener((msg, sender, respond) => {
      if (!msg || msg.type !== "capture_active_tab") return false;
      captureActiveTab(msg.options || {})
        .then((result) => respond(result))
        .catch((err) => respond({ ok: false, error: err.message }));
      return true; // async response
    });
  }

  root.MonoCaptureBridge = { install, handleCommand, captureActiveTab, flush, context };
})(globalThis);
