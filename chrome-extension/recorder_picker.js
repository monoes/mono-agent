/**
 * MonoAgent Bridge -- pick one element, for re-recording a selector
 * (contracts section 9)
 *
 * The `pick_element` command injects this (after recorder_privacy.js and
 * recorder_selectors.js) into the top frame of a tab and calls
 * MonoRecorderPicker.pick({prompt, timeoutMs, keep}). The page shows a
 * banner with the prompt and outlines whatever is under the pointer; the
 * person clicks once, and the answer is the same Fingerprint a recording
 * would have made for that element -- ranked candidates, uniqueness
 * checked in the live page -- plus the page URL. Never a value: the
 * fingerprint has none, secret fields are flagged `sensitive`, and URLs
 * go through sanitizeUrl.
 *
 * While picking, the page sees none of the person's pointer presses: they
 * are stopped in the capture phase at the window, before the page (or a
 * running recorder) could act on them. Only a TRUSTED click picks.
 *
 * Esc cancels ("cancelled"); the timeout ends it ("timeout"). Every way out
 * runs the same cleanup: listeners removed, overlay removed, and the
 * globals this injection added deleted -- `keep` names the ones that were
 * already there (a recording in the same tab still needs them).
 *
 * Page script: plain ASCII (recorder_encoding.test.mjs).
 */

(function (root) {
  "use strict";

  const HOST_ATTR = "data-monoagent-picker";
  const OURS = ["MonoRecorderPicker", "MonoRecorderSelectors", "MonoRecorderPrivacy"];
  const MIN_MS = 1000;
  const MAX_MS = 10 * 60 * 1000;
  const DEFAULT_MS = 120000;

  let active = null; // the running pick's finish(err, value)

  function overlay(doc, prompt) {
    const host = doc.createElement("div");
    host.setAttribute(HOST_ATTR, "");
    host.style.cssText = "position:fixed;inset:0;z-index:2147483647;pointer-events:none;";
    const shadow = host.attachShadow({ mode: "closed" });
    const banner = doc.createElement("div");
    banner.style.cssText =
      "position:fixed;top:12px;left:50%;transform:translateX(-50%);max-width:80vw;padding:8px 14px;" +
      "font:600 14px/1.4 system-ui,sans-serif;color:#fff;background:#1d4ed8;border-radius:8px;" +
      "box-shadow:0 4px 16px rgba(0,0,0,.25);pointer-events:none;";
    banner.textContent = `${prompt}  (Esc to cancel)`;
    const box = doc.createElement("div");
    box.style.cssText =
      "position:fixed;display:none;outline:2px solid #1d4ed8;background:rgba(29,78,216,.10);" +
      "border-radius:2px;pointer-events:none;";
    shadow.appendChild(banner);
    shadow.appendChild(box);
    (doc.body || doc.documentElement).appendChild(host);
    return { host, box };
  }

  /** cleanupGlobals deletes what this injection added, keeping what was there before. */
  function cleanupGlobals(keep) {
    for (const name of OURS) {
      if (keep.indexOf(name) === -1) {
        try {
          delete root[name];
        } catch {
          root[name] = undefined;
        }
      }
    }
  }

  function pick(opts) {
    const o = opts || {};
    const keep = Array.isArray(o.keep) ? o.keep : [];
    if (active) return Promise.reject(new Error("a pick is already running"));
    const doc = root.document;
    const prompt = String(o.prompt || "Click the element").slice(0, 200);
    const timeoutMs = Math.min(MAX_MS, Math.max(MIN_MS, Number(o.timeoutMs) || DEFAULT_MS));
    const Sel = root.MonoRecorderSelectors;
    const Priv = root.MonoRecorderPrivacy;

    return new Promise((resolve, reject) => {
      const { host, box } = overlay(doc, prompt);
      const env = Sel.browserEnv(doc);
      const bound = [];
      let timer = null;

      const inside = (el) => el === host || (el && host.contains && host.contains(el));
      const targetOf = (e) => (e.composedPath && e.composedPath()[0]) || e.target;

      function finish(err, value) {
        if (active !== finish) return;
        active = null;
        clearTimeout(timer);
        for (const [type, fn] of bound) root.removeEventListener(type, fn, true);
        bound.length = 0;
        host.remove();
        cleanupGlobals(keep);
        if (err) reject(err);
        else resolve(value);
      }

      function block(e) {
        if (!e.isTrusted) return;
        e.preventDefault();
        e.stopImmediatePropagation();
      }

      function onMove(e) {
        const el = targetOf(e);
        if (!el || inside(el) || !el.getBoundingClientRect) return;
        const r = el.getBoundingClientRect();
        box.style.display = "block";
        box.style.left = `${r.left}px`;
        box.style.top = `${r.top}px`;
        box.style.width = `${r.width}px`;
        box.style.height = `${r.height}px`;
      }

      function onClick(e) {
        if (!e.isTrusted) return; // the page cannot pick for the person
        block(e);
        const el = targetOf(e);
        if (!el || inside(el) || el.nodeType !== 1) return;
        try {
          const fp = Sel.fingerprint(el, env);
          if (fp.href) fp.href = Priv.sanitizeUrl(fp.href);
          const label = env.labelOf ? String(env.labelOf(el) || "") : "";
          if (Priv.sensitiveField(el, label)) fp.sensitive = true;
          finish(null, { fingerprint: fp, url: Priv.sanitizeUrl(root.location.href) });
        } catch (err) {
          finish(new Error(`could not describe that element: ${err.message}`));
        }
      }

      function onKey(e) {
        if (!e.isTrusted) return;
        if (e.key === "Escape") {
          block(e);
          finish(new Error("cancelled"));
        }
      }

      function listen(type, fn) {
        root.addEventListener(type, fn, true);
        bound.push([type, fn]);
      }

      active = finish;
      listen("mousemove", onMove);
      for (const t of ["pointerdown", "mousedown", "pointerup", "mouseup", "dblclick", "contextmenu", "auxclick"]) listen(t, block);
      listen("click", onClick);
      listen("keydown", onKey);
      // A page that goes away ends the pick; nothing is left to clean up then.
      listen("pagehide", () => finish(new Error("the page navigated away")));
      timer = setTimeout(() => finish(new Error("timeout")), timeoutMs);
    });
  }

  /** cancel ends a running pick from outside (the worker's own deadline). */
  function cancel(reason) {
    if (active) active(new Error(reason || "cancelled"));
  }

  root.MonoRecorderPicker = { pick, cancel, HOST_ATTR, OURS };
})(globalThis);
