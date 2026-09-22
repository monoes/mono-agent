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
      // The video capture reads YouTube's own globals and fetches from the
      // page, so both run in the MAIN world: the isolated world cannot see
      // window.ytInitialPlayerResponse, and a fetch from the page carries
      // exactly the origin and cookies the player's own requests do.
      youtube: root.MonoYouTubeVideo
        ? {
            readPage: (tabId) => mainWorld(tabId, root.MonoYouTubeVideo.readPageState, []),
            fetchText: (tabId, url, init) => mainWorld(tabId, pageFetchText, [url, init || null]),
          }
        : null,
      attach: (tabId) => deps.attach(tabId),
      cdp: (tabId, method, params) => deps.cdp(tabId, method, params || {}),
      detach: async (tabId) => deps.detach(tabId),
    };
  }

  async function mainWorld(tabId, func, args) {
    const results = await chrome.scripting.executeScript({ target: { tabId }, world: "MAIN", func, args });
    if (!results || !results.length) throw new Error("the page did not answer");
    return results[0].result;
  }

  /** pageFetchText runs in the page (serialized): it must close over nothing. */
  async function pageFetchText(url, init) {
    const res = await fetch(url, Object.assign({ credentials: "include" }, init || {}));
    return { status: res.status, text: await res.text() };
  }

  /** withMode expands a named capture mode into its params (capture_modes.js). */
  function withMode(params) {
    if (!params || !params.mode || !root.MonoCaptureModes) return params;
    return root.MonoCaptureModes.paramsFor(params);
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
    const result = await root.MonoCapture.pageCapture(withMode(params), context());
    sendEnvelope(id, result, null);
    return result;
  }

  /**
   * captureActiveTab is the user-initiated path. The bridge may be down —
   * a capture is the user's work, so it is queued rather than lost (CLIP-08).
   */
  async function captureActiveTab(options) {
    const params = withMode(Object.assign({}, options || {}));
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
    // Likewise the AI that writes a summary: the panel's sticky choice,
    // from storage alone, unless the caller named one.
    if (root.MonoSummaryAI && result.meta.summarize) {
      const choice =
        params.summaryRuntime !== undefined
          ? { runtime: params.summaryRuntime, model: params.summaryModel }
          : await root.MonoSummaryAI.sticky(chrome.storage.local);
      root.MonoSummaryAI.applyToMeta(result.meta, choice);
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
    const modes = root.MonoCaptureModes;
    const items = modes
      ? modes.menuItems((root.MonoYouTubeVideo && root.MonoYouTubeVideo.MENU_PATTERNS) || ["*://*.youtube.com/watch*"])
      : [
          { id: MENU_PAGE, title: "Save page to monomind", contexts: ["page"] },
          { id: MENU_SELECTION, title: "Save selection to monomind", contexts: ["selection"] },
        ];
    chrome.contextMenus.removeAll(() => {
      for (const item of items) chrome.contextMenus.create(item, () => void chrome.runtime.lastError);
      void chrome.runtime.lastError; // duplicate ids after a worker restart
    });
    chrome.contextMenus.onClicked.addListener((info, tab) => {
      handleMenuClick(info, tab);
    });
  }

  /**
   * handleMenuClick is the right-click menu's whole behaviour: route the
   * item to a capture mode, capture, and tell the person how it went — on
   * the badge, in the page, and in the side panel if it is open. Resolves
   * to { route, result, feedback }, or null for an item that is not ours.
   * Never rejects: a failure is reported, not thrown into the void.
   */
  async function handleMenuClick(info, tab) {
    const modes = root.MonoCaptureModes;
    let route = modes ? modes.menuRoute(info, tab) : null;
    if (!route && !modes && (info.menuItemId === MENU_PAGE || info.menuItemId === MENU_SELECTION)) {
      route = { tabId: tab && tab.id, selection: info.menuItemId === MENU_SELECTION };
    }
    if (!route) return null;
    let result;
    try {
      result = await captureActiveTab(route);
    } catch (err) {
      console.error("[monoagent] capture failed:", err.message);
      result = { ok: false, error: err.message };
    }
    const fb = modes
      ? modes.feedback(route.mode, result)
      : { level: result.ok === false ? "error" : "ok", text: result.error || "Saved" };
    announce(route.tabId, fb, route.mode, result);
    return { route, result, feedback: fb };
  }

  /** announce shows a menu capture's outcome everywhere it can. */
  function announce(tabId, fb, mode, result) {
    if (fb.level === "error") badge("!", "#c0392b");
    try {
      chrome.action.setTitle({ title: fb.text.slice(0, 250) });
      // Unref where the host has it (node, in tests): a cosmetic reset must
      // not hold a process open. A service worker has no such thing.
      const reset = setTimeout(() => chrome.action.setTitle({ title: "Open MonoAgent" }).catch(() => {}), 15000);
      if (reset && reset.unref) reset.unref();
    } catch {
      // No action title — cosmetic.
    }
    // The side panel, if open, shows it in its own status line.
    try {
      const sent = chrome.runtime.sendMessage({ type: "capture_menu_result", mode, feedback: fb, result: { ok: result.ok !== false, title: result.title || null, queued: !!result.queued } });
      if (sent && sent.catch) sent.catch(() => {});
    } catch {
      // No listener — the panel is closed.
    }
    // And in the page, where the right-click was. A restricted page
    // (the web store, a browser page) refuses the script; the badge and
    // the action title above still carry it.
    if (tabId && chrome.scripting) {
      chrome.scripting
        .executeScript({ target: { tabId }, func: pageToast, args: [fb.text, fb.level] })
        .catch(() => {});
    }
  }

  /** pageToast runs in the page (serialized): it must close over nothing. */
  function pageToast(text, level) {
    const id = "monoagent-capture-toast";
    const old = document.getElementById(id);
    if (old) old.remove();
    const el = document.createElement("div");
    el.id = id;
    el.setAttribute("role", level === "error" ? "alert" : "status");
    el.textContent = text;
    const colors = { ok: "#1f7a4d", warn: "#9a6b00", error: "#b3261e" };
    el.style.cssText =
      "position:fixed;z-index:2147483647;right:16px;bottom:16px;max-width:420px;padding:10px 14px;" +
      "border-radius:8px;font:13px/1.4 system-ui,sans-serif;color:#fff;box-shadow:0 6px 24px rgba(0,0,0,.3);" +
      `background:${colors[level] || colors.ok};`;
    (document.body || document.documentElement).appendChild(el);
    setTimeout(() => el.remove(), level === "error" ? 9000 : 5000);
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

  root.MonoCaptureBridge = { install, handleCommand, handleMenuClick, captureActiveTab, flush, context };
})(globalThis);
