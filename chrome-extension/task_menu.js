/**
 * MonoAgent Bridge - the task board's menu items and shortcut (spec 11.1)
 *
 * "Add selection as task" and "Add page as task" in the MonoAgent
 * right-click menu (capture_modes.js lists them, capture_bridge.js's one
 * registrar creates them), and the add-task shortcut. Each ends in
 * MonoTaskBridge.add (task_bridge.js), which queues, sends and reports.
 *
 * A selection is read in the page as the reader sees it: getSelection()
 * leaves out text the page hides with display:none and keeps line breaks,
 * which the menu's own selectionText flattens.
 */

(function (root) {
  "use strict";

  const Bridge = () => root.MonoTaskBridge;

  function install() {
    if (chrome.contextMenus && chrome.contextMenus.onClicked) {
      chrome.contextMenus.onClicked.addListener((info, tab) => {
        handleMenuClick(info, tab).catch((err) => console.warn("[monoagent] task from the menu:", err.message));
      });
    }
    if (chrome.commands && chrome.commands.onCommand) {
      chrome.commands.onCommand.addListener((command, tab) => {
        if (command !== "add-task") return;
        handleCommand(tab).catch((err) => console.warn("[monoagent] add-task shortcut:", err.message));
      });
    }
  }

  /**
   * handleMenuClick answers the menu's two task items. Resolves the outcome,
   * or null for an item that is not a task item (or a click with no tab).
   */
  async function handleMenuClick(info, tab) {
    const ids = root.MonoCaptureModes && root.MonoCaptureModes.TASK_IDS;
    const id = info && info.menuItemId;
    if (!ids || !tab || (id !== ids.selection && id !== ids.page)) return null;
    const B = Bridge();
    const page = id === ids.page;
    const text = page ? "" : (await readSelection(tab.id, info.frameId)) || info.selectionText || "";
    const out = await B.add({ kind: page ? "page" : "selection", text, url: tab.url, title: tab.title, profile: await B.stickyProfile() });
    B.announce(tab.id, out.feedback);
    return out;
  }

  /**
   * readSelection is the selected text as the reader sees it, read in the
   * frame given. "" when the page refuses scripts (the web store, a browser
   * page).
   */
  async function readSelection(tabId, frameId) {
    try {
      const results = await chrome.scripting.executeScript({
        target: { tabId, frameIds: [frameId || 0] },
        func: selectedText,
      });
      const value = results && results[0] && results[0].result;
      return typeof value === "string" ? value : "";
    } catch {
      return "";
    }
  }

  /** selectedText runs in the page (serialized): it must close over nothing. */
  function selectedText() {
    const selection = window.getSelection();
    return selection ? selection.toString() : "";
  }

  /**
   * handleCommand is the add-task shortcut. Chrome opens a side panel only
   * from inside the shortcut's own handler, before anything is awaited, and
   * whether text is selected can only be learned with an await, so the panel
   * opens first, on its task box, on every press (the lead's ruling); then a
   * selection in the top frame, if there is one, is added and the panel
   * shows how that went. Chrome may pass no tab (the docs call it optional):
   * then there is no window to open the panel in, and nothing happens.
   */
  function handleCommand(tab) {
    if (!tab || !tab.id) return Promise.resolve(null);
    const opened = openPanel(tab);
    return (async () => {
      await opened;
      const text = await readSelection(tab.id, 0);
      if (!text.trim()) return null;
      const B = Bridge();
      const out = await B.add({ kind: "selection", text, url: tab.url, title: tab.title, profile: await B.stickyProfile() });
      B.announce(tab.id, out.feedback);
      return out;
    })();
  }

  /** openPanel opens the side panel on its task box; a browser that will not gets a toast. */
  function openPanel(tab) {
    // Both start now, inside the shortcut's handler: an await first would
    // spend the gesture open() needs.
    if (chrome.storage && chrome.storage.session) {
      chrome.storage.session.set({ [Bridge().FOCUS_KEY]: Date.now() }).catch(() => {});
    }
    let opening;
    try {
      opening = Promise.resolve(chrome.sidePanel.open({ windowId: tab.windowId }));
    } catch (err) {
      opening = Promise.reject(err);
    }
    return opening.catch(() => Bridge().toast(tab.id, "To type a task, open MonoAgent's side panel (its toolbar button)", "warn"));
  }

  root.MonoTaskMenu = { install, handleMenuClick, handleCommand };
})(globalThis);
