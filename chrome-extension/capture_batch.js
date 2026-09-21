/**
 * MonoAgent Bridge — save everything open (CLIP-06)
 *
 * A window's worth of tabs, or one tab group, captured into a single named
 * collection.
 *
 * The shape of this is dictated by what an MV3 service worker can survive.
 * A capture attaches the debugger, scrolls the page, rasterizes it and
 * base64s tens of megabytes; forty of those at once would exhaust the
 * worker long before the fortieth finished. So: strictly one at a time,
 * each one fully finished — including its detach — before the next starts.
 *
 * The other rule is that a skipped tab is reported, never silently missing.
 * A person who saves twelve tabs and gets nine documents must be able to
 * see which three were left and why, or they will not trust the feature
 * again.
 */

(function (root) {
  "use strict";

  // Schemes no extension can script or attach a debugger to. Chrome refuses
  // these outright, so asking is a guaranteed error with a worse message
  // than the one here.
  const CLOSED_SCHEMES = [
    "chrome:", "chrome-untrusted:", "chrome-extension:", "moz-extension:",
    "devtools:", "about:", "edge:", "brave:", "vivaldi:", "opera:",
    "view-source:", "data:", "blob:",
  ];

  // The Web Store is special-cased by Chrome itself: extensions are blocked
  // from it regardless of host permissions.
  const WEB_STORE = /^(chromewebstore\.google\.com|chrome\.google\.com\/webstore)/;

  const titleOf = (tab) => (tab && (tab.title || tab.url)) || "Untitled tab";

  /**
   * skipReason says why a tab cannot be captured, or "" for one that can.
   * The reason is written for the person reading the popup, not for a log.
   */
  function skipReason(tab) {
    if (!tab || !tab.url) return "the tab has no address yet";
    let url;
    try {
      url = new URL(tab.url);
    } catch {
      return "the tab's address cannot be read";
    }
    const scheme = url.protocol;
    if (CLOSED_SCHEMES.includes(scheme)) return `${scheme}// pages are closed to extensions`;
    if (scheme === "file:") return "local files need the extension's file-access permission";
    if (scheme !== "http:" && scheme !== "https:") return `${scheme} pages cannot be captured`;
    if (WEB_STORE.test(url.host + url.pathname)) return "the Chrome Web Store is closed to extensions";
    // Chrome's built-in viewer renders a PDF into a plugin document with no
    // DOM to read; the file itself is already an archive worth saving, so
    // this is a limitation to name rather than to work around here.
    if (/\.pdf($|[?#])/i.test(tab.url)) return "PDFs open in Chrome's viewer, which has no page to read";
    if (tab.discarded) return "the tab is discarded — open it to capture it";
    if (tab.status === "unloaded") return "the tab is not loaded";
    return "";
  }

  /** defaultCollection names a batch after where it came from and when. */
  function defaultCollection(scope, now) {
    const date = (now || new Date()).toISOString().slice(0, 10);
    if (scope && scope.groupTitle) return `${scope.groupTitle} — ${date}`;
    if (scope && scope.kind === "group") return `Tab group — ${date}`;
    return `Window — ${date}`;
  }

  /**
   * planBatch splits the tabs into what will be captured and what will not,
   * before anything starts, so the popup can show the real total and the
   * skips at once rather than revealing them one at a time.
   */
  function planBatch(tabs, opts) {
    const o = Object.assign({ scope: { kind: "window" }, now: null }, opts || {});
    const items = [];
    const skipped = [];
    for (const tab of Array.isArray(tabs) ? tabs : []) {
      const reason = skipReason(tab);
      const entry = { tabId: tab && tab.id, title: titleOf(tab), url: (tab && tab.url) || "" };
      if (reason) skipped.push(Object.assign(entry, { reason }));
      else items.push(entry);
    }
    return {
      collection: root.MonoCaptureForm
        ? root.MonoCaptureForm.normalizeCollection(o.collection) || defaultCollection(o.scope, o.now)
        : o.collection || defaultCollection(o.scope, o.now),
      items,
      skipped,
      total: items.length,
    };
  }

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  /**
   * runBatch captures each planned tab in turn. deps:
   *
   *   capture({ tabId, collection })  one capture, resolved when it is
   *                                   fully delivered or queued
   *   onProgress(state)               called before and after each tab
   *   cancelled()                     checked between tabs
   *   pause(ms)                       optional; a breath between captures
   *
   * It resolves with a report rather than rejecting: one tab that threw
   * must not cost the person the other thirty-nine.
   */
  async function runBatch(plan, deps) {
    const d = deps || {};
    const pause = d.pause || sleep;
    const report = {
      collection: plan.collection,
      total: plan.items.length,
      done: [],
      failed: [],
      skipped: plan.skipped.slice(),
      cancelled: false,
    };

    const progress = (extra) => {
      if (!d.onProgress) return;
      d.onProgress(
        Object.assign(
          {
            collection: report.collection,
            total: report.total,
            completed: report.done.length + report.failed.length,
            done: report.done.length,
            failed: report.failed.length,
            skipped: report.skipped.length,
          },
          extra || {}
        )
      );
    };

    progress({ phase: "start" });

    for (let i = 0; i < plan.items.length; i++) {
      const item = plan.items[i];
      if (d.cancelled && d.cancelled()) {
        report.cancelled = true;
        for (const rest of plan.items.slice(i)) {
          report.skipped.push(Object.assign({}, rest, { reason: "cancelled before this tab was captured" }));
        }
        break;
      }

      progress({ phase: "capturing", index: i, title: item.title });
      try {
        const result = await d.capture({ tabId: item.tabId, collection: report.collection });
        if (result && result.ok === false) {
          report.failed.push(Object.assign({}, item, { reason: result.error || "capture failed" }));
        } else {
          report.done.push(
            Object.assign({}, item, {
              queued: !!(result && result.queued),
              warnings: (result && result.warnings) || [],
            })
          );
        }
      } catch (err) {
        report.failed.push(Object.assign({}, item, { reason: err.message }));
      }
      progress({ phase: "captured", index: i, title: item.title });

      // Let the worker's event loop drain between captures — the debugger
      // detach from the tab just finished lands here rather than racing the
      // attach for the next one.
      if (i < plan.items.length - 1) await pause(0);
    }

    progress({ phase: "done" });
    return report;
  }

  /** summarize is the one line the popup shows when a batch finishes. */
  function summarize(report) {
    const parts = [`${report.done.length} of ${report.total} saved to “${report.collection}”`];
    if (report.failed.length) parts.push(`${report.failed.length} failed`);
    if (report.skipped.length) parts.push(`${report.skipped.length} skipped`);
    if (report.cancelled) parts.push("cancelled");
    return parts.join(" · ");
  }

  root.MonoCaptureBatch = { planBatch, runBatch, skipReason, defaultCollection, summarize };
})(globalThis);
