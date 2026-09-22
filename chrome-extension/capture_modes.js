/**
 * MonoAgent Bridge — capture modes and the right-click menu
 *
 * One page, four ways to save it. A mode is only ever a set of capture
 * params, so every entry point (the right-click menu, the side panel, a Go
 * page_capture carrying `mode`) produces the same envelope for the same
 * choice:
 *
 *   full        screenshot + page data: MHTML, readable text, tables
 *   screenshot  screenshot.png and meta.json, nothing else
 *   summary     full, plus an AI summary the bridge writes afterwards
 *   video       a YouTube video: its record (meta.video), transcript.md,
 *               readable text and screenshot, plus an AI summary
 *
 * The summary is not written here. The capture carries `meta.summarize`
 * and the Go bridge, once the envelope is on disk, runs the configured
 * agent runtime and adds summary.md beside it (internal/capturesummary).
 *
 * Pure: the menu is described as data (menuItems) and a click is routed by
 * menuRoute, so both are tested in node without a chrome.contextMenus.
 */

(function (root) {
  "use strict";

  const FULL_FORMATS = ["mhtml", "readable", "screenshot", "tables"];

  const MODES = {
    full: { label: "Save full page (screenshot + data)", formats: FULL_FORMATS },
    screenshot: { label: "Save screenshot only", formats: ["screenshot"], screenshotOnly: true },
    summary: { label: "Save page summary", formats: FULL_FORMATS, summarize: "page" },
    // No MHTML for a video: a watch page's archive is tens of megabytes of
    // player script and says less about the video than its transcript does.
    video: { label: "Save video summary", formats: ["readable", "screenshot", "video"], summarize: "video" },
  };

  const ROOT_ID = "monoagent-root";
  const SELECTION_ID = "monoagent-capture-selection";
  const idFor = (mode) => `monoagent-capture-${mode}`;

  /**
   * menuItems is every contextMenus.create call, in order. The parent shows
   * on a page and on a selection; Chrome gives a right-click on selected text
   * the "selection" context and not "page", so the page modes and the
   * selection item never appear together.
   */
  function menuItems(videoPatterns) {
    return [
      { id: ROOT_ID, title: "MonoAgent", contexts: ["page", "selection", "video"] },
      { id: idFor("full"), parentId: ROOT_ID, title: MODES.full.label, contexts: ["page"] },
      { id: idFor("screenshot"), parentId: ROOT_ID, title: MODES.screenshot.label, contexts: ["page"] },
      { id: idFor("summary"), parentId: ROOT_ID, title: MODES.summary.label, contexts: ["page"] },
      {
        id: idFor("video"),
        parentId: ROOT_ID,
        title: MODES.video.label,
        // "video" too: a right-click on the <video> element itself is not
        // a "page" click.
        contexts: ["page", "video"],
        documentUrlPatterns: videoPatterns,
      },
      { id: SELECTION_ID, parentId: ROOT_ID, title: "Save selection to monomind", contexts: ["selection"] },
    ];
  }

  /** menuRoute turns a click into capture options, or null for someone else's item. */
  function menuRoute(info, tab) {
    const id = info && info.menuItemId;
    const tabId = tab && tab.id;
    if (id === SELECTION_ID) return { tabId, selection: true, mode: "full" };
    for (const mode of Object.keys(MODES)) {
      if (id === idFor(mode)) return { tabId, selection: false, mode };
    }
    return null;
  }

  /**
   * paramsFor expands a mode into page_capture params. Anything the caller
   * set explicitly (formats, a note) wins over the mode's defaults, so a mode
   * never silently discards what was asked for.
   */
  function paramsFor(options) {
    const o = Object.assign({}, options || {});
    const mode = MODES[o.mode] ? o.mode : "full";
    const spec = MODES[mode];
    const params = Object.assign({}, o, { mode });
    if (!Array.isArray(o.formats) || !o.formats.length) params.formats = spec.formats.slice();
    if (spec.screenshotOnly) params.screenshotOnly = true;
    if (spec.summarize && o.summarize !== false) params.summarize = spec.summarize;
    return params;
  }

  /** feedback is the one line a person sees after a menu capture. */
  function feedback(mode, result) {
    const what = { full: "Page", screenshot: "Screenshot", summary: "Page", video: "Video" }[mode] || "Page";
    if (!result || result.ok === false) {
      const why = (result && (result.error || (result.warnings || []).join("; "))) || "unknown error";
      return { level: "error", text: `MonoAgent: save failed — ${why}` };
    }
    const title = result.title ? `: ${result.title}` : "";
    const summary = MODES[mode] && MODES[mode].summarize ? " — the summary is being written" : "";
    if (result.queued) return { level: "warn", text: `${what} saved offline${title} — it goes to MonoAgent when the bridge is back${summary}` };
    const warn = (result.warnings || []).length;
    return {
      level: warn ? "warn" : "ok",
      text: `${what} saved${title}${summary}${warn ? ` (${result.warnings.join("; ")})` : ""}`,
    };
  }

  root.MonoCaptureModes = { MODES, menuItems, menuRoute, paramsFor, feedback, ROOT_ID, SELECTION_ID, idFor };
})(globalThis);
