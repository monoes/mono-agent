/**
 * MonoAgent Bridge -- the activity recorder, in the page (section 8.2)
 *
 * Injected into every frame of ONE tab by recorder_session.js, and only
 * while a recording is running; it is never a declared content script, so
 * a page nobody is recording has none of this in it. It turns what the
 * person does into partial Events (internal/recording/types.go) and posts
 * them to the worker, which numbers them and puts them on the wire.
 *
 * What it records, and what it deliberately does not:
 *
 *   click / dblclick / contextmenu  -> click on the nearest actionable ancestor
 *   input / change on a text field  -> type, debounced: the FINAL value only,
 *                                     never the keystrokes that built it
 *   <select> change                 -> select_option
 *   checkbox / radio change         -> check (the click itself is dropped)
 *   form submit                     -> submit, unless the click or Enter that
 *                                     caused it was just recorded
 *   Enter / Escape / Tab / shortcuts -> press_key; printable keys, AltGr and
 *                                     auto-repeat are dropped
 *   file input change               -> upload, file NAMES only
 *   Alt+click, or "Pick data" on    -> extract (the click never reaches the page)
 *   scroll                          -> not recorded in v1
 *
 * PRIVACY (recorder_privacy.js), before anything leaves the page: secret
 * fields and card numbers are sent as {masked: true, secretAs} with no
 * value -- typed OR picked as data. A field's mask is latched at the first
 * keystroke, so a "show password" toggle that turns it into a text field
 * does not unmask it. DOM snippets lose secret value attributes and any
 * card number in their text.
 *
 * Open shadow roots: targets come from composedPath(), and the events that
 * do not cross a shadow boundary (change, submit) are also listened for on
 * each shadow root the person touches.
 *
 * Any event flushes a pending debounced value first, so "type then click
 * Save" is recorded in that order even when the click beats the debounce;
 * pagehide flushes too, so the last value before a navigation is kept.
 */

(function (root) {
  "use strict";

  const DEBOUNCE_MS = 700;
  const SNIPPET_MAX = 2048;
  const SNIPPET_WALK = 6; // ancestor levels considered for the snippet
  const SNIPPET_NODES = 80; // an ancestor with more descendants is never serialised
  const FOLD_MS = 800; // a submit this soon after its own click/Enter is folded into it
  const OVERLAY_ID = "monoagent-recorder-pick";

  const Sel = () => root.MonoRecorderSelectors;
  const List = () => root.MonoRecorderList;
  const Priv = () => root.MonoRecorderPrivacy;
  const tagOf = (el) => String((el && el.tagName) || "").toLowerCase();
  const attr = (el, name) => (el && el.getAttribute ? el.getAttribute(name) : null);
  const typeOf = (el) => String(attr(el, "type") || "text").toLowerCase();

  const ACTIONABLE =
    'a,button,input,select,textarea,summary,label,[role="button"],[role="link"],[role="menuitem"],' +
    '[role="tab"],[role="checkbox"],[role="option"],[onclick],[contenteditable=""],[contenteditable="true"]';

  /** actionable is the nearest ancestor a person would say they clicked. */
  function actionable(el) {
    if (!el) return null;
    if (el.closest) return el.closest(ACTIONABLE) || el;
    return el;
  }

  const isTextField = (el) => {
    const tag = tagOf(el);
    if (tag === "textarea") return true;
    if (tag !== "input") return !!(el && el.isContentEditable);
    return !/^(checkbox|radio|file|submit|button|reset|image|range|color|hidden)$/.test(typeOf(el));
  };
  const isToggle = (el) => tagOf(el) === "input" && /^(checkbox|radio)$/.test(typeOf(el));

  /** targetOf is the real target, inside an open shadow root when there is one. */
  const targetOf = (e) => (e.composedPath && e.composedPath()[0]) || e.target;

  /**
   * keyCombo names a keydown worth recording ("Enter", "Control+k",
   * "Shift+Tab"), or "" for one that is covered by the typed value or is
   * just a modifier going down.
   */
  function keyCombo(e, inField) {
    const k = e.key;
    if (!k || /^(Shift|Control|Alt|AltGraph|Meta|CapsLock|Dead|Unidentified|Process)$/.test(k)) return "";
    const printable = k.length === 1;
    // AltGr (and Option on a Mac) types characters; those are text, not shortcuts.
    if (printable && e.getModifierState && e.getModifierState("AltGraph")) return "";
    if (printable && inField && e.altKey && !e.ctrlKey && !e.metaKey) return "";
    const mods = [];
    if (e.ctrlKey) mods.push("Control");
    if (e.altKey) mods.push("Alt");
    if (e.metaKey) mods.push("Meta");
    const name = k === " " ? "Space" : printable ? k.toLowerCase() : k;
    if (!mods.length) {
      if (printable) return "";
      if (/^(Enter|Escape|Tab|F\d{1,2})$/.test(k)) return (e.shiftKey ? "Shift+" : "") + name;
      if (/^(Arrow\w+|PageUp|PageDown|Home|End|Delete|Backspace)$/.test(k)) return inField ? "" : name;
      return "";
    }
    // Editing shortcuts inside a field change its value, which `type` records.
    if (inField && (e.ctrlKey || e.metaKey) && /^[acvxyz]$|^Backspace$/.test(name)) return "";
    if (e.shiftKey) mods.push("Shift");
    return `${mods.join("+")}+${name}`;
  }

  /** fits is true when an element is small enough to serialise for a snippet. */
  function fits(el) {
    if (el.getElementsByTagName && el.getElementsByTagName("*").length > SNIPPET_NODES) return false;
    return el.outerHTML.length <= SNIPPET_MAX;
  }

  /**
   * snippetOf is ~2KB of outerHTML around the element: the largest nearby
   * ancestor that still fits, with scripts dropped and secrets removed. The
   * walk is capped, and a large ancestor is judged by its node count before
   * anything is serialised, so a click deep in a big page stays cheap.
   */
  function snippetOf(el, label) {
    if (!el || !el.cloneNode || el.outerHTML == null) return "";
    let node = el;
    for (let i = 0; i < SNIPPET_WALK; i++) {
      const up = node.parentElement;
      if (!up || tagOf(up) === "body" || tagOf(up) === "html" || !fits(up)) break;
      node = up;
    }
    const copy = node.cloneNode(true);
    if (copy.querySelectorAll) {
      for (const s of copy.querySelectorAll("script,style,noscript,template")) s.remove();
      const all = [copy].concat(Array.from(copy.querySelectorAll("input,textarea")));
      for (const f of all) {
        if (!/^(input|textarea)$/.test(tagOf(f))) continue;
        const withheld = Priv().maskReason(f, attr(f, "value") || f.textContent, f === el ? label : "");
        if (withheld) {
          f.removeAttribute("value");
          if (tagOf(f) === "textarea") f.textContent = "";
        }
      }
    }
    const html = Priv().scrubCards(copy.outerHTML || "");
    return html.length > SNIPPET_MAX ? html.slice(0, SNIPPET_MAX) : html;
  }

  /** framePath is this frame's index path from the top window; [] at the top. */
  function framePath(win) {
    const path = [];
    try {
      let w = win;
      while (w && w.parent && w !== w.parent) {
        const p = w.parent;
        let at = -1;
        for (let i = 0; i < p.frames.length; i++) {
          if (p.frames[i] === w) {
            at = i;
            break;
          }
        }
        path.unshift(at);
        w = p;
      }
    } catch {
      // an unreachable parent: keep what was found
    }
    return path;
  }

  /**
   * createRecorder wires one document. opts: doc, win, send(msg), now(),
   * env (selector counting), setTimer/clearTimer, debounceMs.
   */
  function createRecorder(opts) {
    const doc = opts.doc;
    const win = opts.win || null;
    const send = opts.send;
    const now = opts.now || (() => Date.now());
    const setTimer = opts.setTimer || ((fn, ms) => setTimeout(fn, ms));
    const clearTimer = opts.clearTimer || ((t) => clearTimeout(t));
    const debounceMs = opts.debounceMs == null ? DEBOUNCE_MS : opts.debounceMs;
    const env = opts.env || Sel().browserEnv(doc);
    const frame = opts.frame || [];
    const location = () => (opts.url ? opts.url() : (win && win.location && win.location.href) || "");
    const labelOf = (el) => (env.labelOf ? String(env.labelOf(el) || "") : "");

    const pending = new Map(); // field -> {at, reason}: reason latched at the first input
    let timer = null;
    let pick = false;
    let stopped = false;
    let lastPick = null;
    let last = null; // {type, form, at}
    let overlay = null;
    const bound = []; // [target, type, fn]
    const roots = new Set(); // shadow roots already listened on

    function emit(partial, target, extra) {
      const event = Object.assign({ type: partial.type, url: location(), at: now() }, partial);
      if (frame.length) event.frame = frame.slice();
      if (target) event.target = Sel().fingerprint(target, env);
      const msg = Object.assign({ type: "recorder_event", event }, extra || {});
      if (target && event.type !== "extract") msg.snippet = snippetOf(target, labelOf(target));
      last = { type: event.type, form: target && target.closest ? target.closest("form") : null, key: event.key, at: event.at };
      try {
        send(msg);
      } catch {
        // the worker went away; the session notices the missing tab itself
      }
      return event;
    }

    function typedValue(el) {
      if (el.isContentEditable && tagOf(el) !== "input" && tagOf(el) !== "textarea") {
        return String(el.innerText != null ? el.innerText : el.textContent || "").trim();
      }
      return String(el.value == null ? "" : el.value);
    }

    function emitType(el, latched) {
      const value = typedValue(el);
      const reason = latched || Priv().maskReason(el, value, labelOf(el));
      if (reason) {
        emit({ type: "type", masked: true, secretAs: Priv().secretName(el, reason, labelOf(el), Sel().looksGenerated) }, el);
      } else {
        emit({ type: "type", value }, el);
      }
    }

    /** flush emits every debounced value now, in the order they were first typed. */
    function flush(only) {
      if (timer) {
        clearTimer(timer);
        timer = null;
      }
      const fields = Array.from(pending.keys()).filter((el) => !only || el === only);
      for (const el of fields) {
        const p = pending.get(el);
        pending.delete(el);
        emitType(el, p.reason);
      }
      if (pending.size) timer = setTimer(() => flush(), debounceMs);
    }

    /** watchRoot listens for the non-composed events inside an open shadow root. */
    function watchRoot(el) {
      const r = el && el.getRootNode ? el.getRootNode() : null;
      if (!r || r === doc || !r.host || roots.has(r) || !r.addEventListener) return;
      roots.add(r);
      listen(r, "change", onChange);
      listen(r, "submit", onSubmit);
    }

    function onInput(e) {
      if (stopped) return;
      const el = targetOf(e);
      if (!isTextField(el)) return;
      watchRoot(el);
      const reason = Priv().maskReason(el, typedValue(el), labelOf(el));
      const p = pending.get(el);
      if (!p) pending.set(el, { at: now(), reason });
      else if (reason && !p.reason) p.reason = reason; // once masked, always masked
      if (timer) clearTimer(timer);
      timer = setTimer(() => flush(), debounceMs);
    }

    function onChange(e) {
      if (stopped) return;
      const el = targetOf(e);
      const tag = tagOf(el);
      if (tag === "select") {
        flush();
        const opt = el.options && el.options[el.selectedIndex];
        emit({ type: "select_option", value: String(el.value || (opt && opt.text) || "") }, el);
      } else if (isToggle(el)) {
        flush();
        emit({ type: "check", checked: !!el.checked }, el);
      } else if (tag === "input" && typeOf(el) === "file") {
        flush();
        const names = Array.from(el.files || []).map((f) => f.name).join(", ");
        emit({ type: "upload", value: names }, el);
      } else if (isTextField(el)) {
        if (pending.has(el)) flush(el);
      }
    }

    function onFocusIn(e) {
      if (!stopped) watchRoot(targetOf(e));
    }

    function onFocusOut(e) {
      if (stopped) return;
      const el = targetOf(e);
      if (pending.has(el)) flush(el);
    }

    function onPick(e) {
      const el = targetOf(e);
      if (!el || (overlay && el === overlay)) return;
      e.preventDefault();
      if (e.stopImmediatePropagation) e.stopImmediatePropagation();
      flush();
      // A masked field picked as data yields no value at all: masked, no samples.
      const withheld = !!Priv().maskReason(el, typedValue(el), labelOf(el));
      const extra = withheld ? { masked: true } : {};
      const list = lastPick && lastPick !== el ? List().proposeList(lastPick, el, env) : null;
      if (list) {
        lastPick = null;
        emit(Object.assign({ type: "extract", extract: list }, extra), el, { replacesLastExtract: true });
      } else {
        lastPick = el;
        emit(Object.assign({ type: "extract", extract: List().single(el) }, extra), el);
      }
    }

    function onClick(e) {
      if (stopped) return;
      if (pick || e.altKey) {
        if (e.type === "click") onPick(e);
        else e.preventDefault();
        return;
      }
      if (e.isTrusted === false) return;
      const raw = targetOf(e);
      watchRoot(raw);
      const el = actionable(raw);
      if (!el || el === overlay) return;
      if (isToggle(el) || isTextField(el) || tagOf(el) === "select") return;
      if (tagOf(el) === "label" && el.control && (isToggle(el.control) || isTextField(el.control))) return;
      if (tagOf(el) === "input" && typeOf(el) === "file") return;
      flush();
      const note = e.type === "dblclick" ? "double" : e.type === "contextmenu" ? "context" : undefined;
      emit(note ? { type: "click", note } : { type: "click" }, el);
    }

    function onSubmit(e) {
      if (stopped) return;
      const form = targetOf(e);
      flush();
      const folded =
        last &&
        now() - last.at <= FOLD_MS &&
        last.form === form &&
        (last.type === "click" || (last.type === "press_key" && last.key === "Enter"));
      if (folded) return;
      emit({ type: "submit" }, form);
    }

    function onKeyDown(e) {
      if (stopped || e.isComposing || e.repeat) return;
      const el = targetOf(e);
      const key = keyCombo(e, isTextField(el));
      if (!key) return;
      flush();
      emit({ type: "press_key", key }, el && el.tagName ? el : null);
    }

    function onHover(e) {
      if (!pick || !overlay) return;
      const el = targetOf(e);
      if (!el || !el.getBoundingClientRect) return;
      const r = el.getBoundingClientRect();
      Object.assign(overlay.style, {
        display: "block",
        left: `${r.left}px`,
        top: `${r.top}px`,
        width: `${r.width}px`,
        height: `${r.height}px`,
      });
    }

    function onPageHide() {
      if (!stopped) flush();
    }

    function listen(target, type, fn) {
      target.addEventListener(type, fn, true);
      bound.push([target, type, fn]);
    }

    function start() {
      listen(doc, "click", onClick);
      listen(doc, "dblclick", onClick);
      listen(doc, "contextmenu", onClick);
      listen(doc, "input", onInput);
      listen(doc, "change", onChange);
      listen(doc, "focusin", onFocusIn);
      listen(doc, "focusout", onFocusOut);
      listen(doc, "submit", onSubmit);
      listen(doc, "keydown", onKeyDown);
      listen(doc, "mouseover", onHover);
      if (win && win.addEventListener) listen(win, "pagehide", onPageHide);
    }

    function setPick(on) {
      pick = !!on && !stopped;
      if (!pick) lastPick = null;
      if (pick && !overlay && doc.createElement && doc.documentElement) {
        overlay = doc.createElement("div");
        overlay.id = OVERLAY_ID;
        overlay.setAttribute("aria-hidden", "true");
        Object.assign(overlay.style, {
          position: "fixed", zIndex: "2147483647", pointerEvents: "none", display: "none",
          outline: "2px solid #d93025", background: "rgba(217,48,37,.08)",
        });
        doc.documentElement.appendChild(overlay);
      } else if (!pick && overlay) {
        overlay.remove();
        overlay = null;
      }
    }

    /** stop flushes, then makes every handler inert even if one is still attached. */
    function stop() {
      if (stopped) return;
      flush();
      stopped = true;
      for (const [target, type, fn] of bound) target.removeEventListener(type, fn, true);
      bound.length = 0;
      roots.clear();
      setPick(false);
    }

    return {
      start, stop, flush, setPick,
      isStopped: () => stopped,
      handlers: { onClick, onInput, onChange, onFocusIn, onFocusOut, onSubmit, onKeyDown, onPageHide },
    };
  }

  root.MonoRecorder = {
    createRecorder, keyCombo, snippetOf, framePath, actionable, targetOf,
    maskReason: (el, value, label) => Priv().maskReason(el, value, label),
    secretName: (el, reason, label) => Priv().secretName(el, reason, label, Sel().looksGenerated),
    luhn: (v) => Priv().luhn(v),
    DEBOUNCE_MS, SNIPPET_MAX,
  };

  root.MonoRecorder.setPick = (on) => {
    root.__monoRecorderPick = !!on;
    if (root.__monoRecorderActive) root.__monoRecorderActive.setPick(on);
  };
  root.MonoRecorder.stopActive = () => {
    root.__monoRecorderPick = false;
    if (root.__monoRecorderActive) root.__monoRecorderActive.stop();
    root.__monoRecorderActive = null;
  };

  // In the page: start once per document. The session calls
  // MonoRecorder.setPick / stopActive through chrome.scripting.
  if (typeof document !== "undefined" && typeof chrome !== "undefined" && chrome.runtime && !root.__monoRecorderActive) {
    const active = createRecorder({
      doc: document,
      win: window,
      frame: framePath(window),
      send: (msg) => chrome.runtime.sendMessage(msg).catch(() => {}),
    });
    root.__monoRecorderActive = active;
    active.start();
    if (root.__monoRecorderPick) active.setPick(true);
    // A page restored from the back/forward cache kept this recorder even if
    // the recording stopped while it was cached: ask, and go inert if so.
    window.addEventListener("pageshow", (e) => {
      if (!e.persisted || active.isStopped()) return;
      chrome.runtime
        .sendMessage({ type: "recorder_alive" })
        .then((res) => {
          if (!res || !res.recording) root.MonoRecorder.stopActive();
        })
        .catch(() => root.MonoRecorder.stopActive());
    });
  }
})(globalThis);
