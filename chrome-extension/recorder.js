/**
 * MonoAgent Bridge — the activity recorder, in the page (§8.2)
 *
 * Injected into every frame of ONE tab by recorder_session.js, and only
 * while a recording is running; it is never a declared content script, so
 * a page nobody is recording has none of this in it. It turns what the
 * person does into partial Events (internal/recording/types.go) and posts
 * them to the worker, which numbers them and puts them on the wire.
 *
 * What it records, and what it deliberately does not:
 *
 *   click / dblclick / contextmenu  → click on the nearest actionable ancestor
 *   input / change on a text field  → type, debounced: the FINAL value only,
 *                                     never the keystrokes that built it
 *   <select> change                 → select_option
 *   checkbox / radio change         → check (the click itself is dropped)
 *   form submit                     → submit, unless the click or Enter that
 *                                     caused it was just recorded
 *   Enter / Escape / Tab / shortcuts → press_key; printable keys are dropped
 *   file input change               → upload, file NAMES only
 *   Alt+click, or "Pick data" on    → extract (the click never reaches the page)
 *   scroll                          → not recorded in v1
 *
 * PRIVACY, before anything leaves the page: password fields, autocomplete
 * cc-* / one-time-code fields, hidden inputs and anything that passes a Luhn
 * check are sent as {masked: true, secretAs: "<name>"} with no value. The
 * DOM snippet sent with each event has those fields' value attributes
 * removed too.
 *
 * Any event flushes a pending debounced value first, so "type then click
 * Save" is recorded in that order even when the click beats the debounce.
 */

(function (root) {
  "use strict";

  const DEBOUNCE_MS = 700;
  const SNIPPET_MAX = 2048;
  const FOLD_MS = 800; // a submit this soon after its own click/Enter is folded into it
  const OVERLAY_ID = "monoagent-recorder-pick";

  const Sel = () => root.MonoRecorderSelectors;
  const List = () => root.MonoRecorderList;
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

  function luhn(value) {
    const digits = String(value || "").replace(/[\s-]/g, "");
    if (!/^\d{13,19}$/.test(digits)) return false;
    let sum = 0;
    for (let i = 0; i < digits.length; i++) {
      let d = Number(digits[digits.length - 1 - i]);
      if (i % 2 === 1) {
        d *= 2;
        if (d > 9) d -= 9;
      }
      sum += d;
    }
    return sum % 10 === 0;
  }

  /** maskReason says why a field's value must not be recorded, or "". */
  function maskReason(el, value) {
    const type = typeOf(el);
    const auto = String(attr(el, "autocomplete") || "").toLowerCase();
    if (tagOf(el) === "input" && type === "password") return "password";
    if (/(^|\s)(current-password|new-password)$/.test(auto)) return "password";
    if (/(^|\s)cc-/.test(auto)) return "card";
    if (/(^|\s)one-time-code$/.test(auto)) return "otp";
    if (tagOf(el) === "input" && type === "hidden") return "hidden";
    if (luhn(value)) return "card";
    return "";
  }

  const slug = (s) =>
    String(s || "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 40);

  /** secretName suggests the secret input a masked value becomes. */
  function secretName(el, reason, env) {
    const label = env && env.labelOf ? env.labelOf(el) : "";
    const auto = String(attr(el, "autocomplete") || "").split(" ").pop();
    const id = attr(el, "id");
    const stableId = id && !Sel().looksGenerated(id) ? id : "";
    const named = slug(attr(el, "name") || stableId || label || (auto !== "on" && auto !== "off" ? auto : ""));
    if (named) return named;
    return { password: "password", card: "card_number", otp: "one_time_code", hidden: "hidden_value" }[reason] || "secret";
  }

  /**
   * keyCombo names a keydown worth recording ("Enter", "Control+k",
   * "Shift+Tab"), or "" for one that is covered by the typed value or is
   * just a modifier going down.
   */
  function keyCombo(e, inField) {
    const k = e.key;
    if (!k || /^(Shift|Control|Alt|Meta|CapsLock|Dead|Unidentified|Process)$/.test(k)) return "";
    const printable = k.length === 1;
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

  /**
   * snippetOf is ≈2KB of outerHTML around the element: the largest ancestor
   * that still fits, with scripts dropped and secret values removed.
   */
  function snippetOf(el) {
    if (!el || !el.cloneNode || el.outerHTML == null) return "";
    let node = el;
    while (node.parentElement && tagOf(node.parentElement) !== "body" && node.parentElement.outerHTML.length <= SNIPPET_MAX) {
      node = node.parentElement;
    }
    const copy = node.cloneNode(true);
    if (copy.querySelectorAll) {
      for (const s of copy.querySelectorAll("script,style,noscript,template")) s.remove();
      const all = [copy].concat(Array.from(copy.querySelectorAll("input,textarea")));
      for (const f of all) {
        if (!/^(input|textarea)$/.test(tagOf(f))) continue;
        if (maskReason(f, attr(f, "value"))) f.removeAttribute("value");
      }
    }
    const html = copy.outerHTML || "";
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
    const send = opts.send;
    const now = opts.now || (() => Date.now());
    const setTimer = opts.setTimer || ((fn, ms) => setTimeout(fn, ms));
    const clearTimer = opts.clearTimer || ((t) => clearTimeout(t));
    const debounceMs = opts.debounceMs == null ? DEBOUNCE_MS : opts.debounceMs;
    const env = opts.env || Sel().browserEnv(doc);
    const frame = opts.frame || [];
    const location = () => (opts.url ? opts.url() : (opts.win && opts.win.location && opts.win.location.href) || "");

    const pending = new Map(); // field element -> first input time
    let timer = null;
    let pick = false;
    let lastPick = null;
    let last = null; // {type, form, at}
    let overlay = null;
    const bound = [];

    function emit(partial, target, extra) {
      const event = Object.assign({ type: partial.type, url: location(), at: now() }, partial);
      if (frame.length) event.frame = frame.slice();
      if (target) event.target = Sel().fingerprint(target, env);
      const msg = Object.assign({ type: "recorder_event", event }, extra || {});
      if (target && event.type !== "extract") msg.snippet = snippetOf(target);
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

    function emitType(el) {
      const value = typedValue(el);
      const reason = maskReason(el, value);
      if (reason) emit({ type: "type", masked: true, secretAs: secretName(el, reason, env) }, el);
      else emit({ type: "type", value }, el);
    }

    /** flush emits every debounced value now, in the order they were first typed. */
    function flush(only) {
      if (timer) {
        clearTimer(timer);
        timer = null;
      }
      const fields = Array.from(pending.keys()).filter((el) => !only || el === only);
      for (const el of fields) {
        pending.delete(el);
        emitType(el);
      }
      if (pending.size) timer = setTimer(() => flush(), debounceMs);
    }

    function onInput(e) {
      const el = e.target;
      if (!isTextField(el)) return;
      if (!pending.has(el)) pending.set(el, now());
      if (timer) clearTimer(timer);
      timer = setTimer(() => flush(), debounceMs);
    }

    function onChange(e) {
      const el = e.target;
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

    function onFocusOut(e) {
      if (pending.has(e.target)) flush(e.target);
    }

    function onPick(e) {
      const el = e.target;
      if (!el || (overlay && el === overlay)) return;
      e.preventDefault();
      if (e.stopImmediatePropagation) e.stopImmediatePropagation();
      flush();
      const list = lastPick && lastPick !== el ? List().proposeList(lastPick, el, env) : null;
      if (list) {
        lastPick = null;
        emit({ type: "extract", extract: list }, el, { replacesLastExtract: true });
      } else {
        lastPick = el;
        emit({ type: "extract", extract: List().single(el) }, el);
      }
    }

    function onClick(e) {
      if (pick || e.altKey) {
        if (e.type === "click") onPick(e);
        else e.preventDefault();
        return;
      }
      if (e.isTrusted === false) return;
      const el = actionable(e.target);
      if (!el || el === overlay) return;
      if (isToggle(el) || isTextField(el) || tagOf(el) === "select") return;
      if (tagOf(el) === "label" && el.control && (isToggle(el.control) || isTextField(el.control))) return;
      if (tagOf(el) === "input" && typeOf(el) === "file") return;
      flush();
      const note = e.type === "dblclick" ? "double" : e.type === "contextmenu" ? "context" : undefined;
      emit(note ? { type: "click", note } : { type: "click" }, el);
    }

    function onSubmit(e) {
      const form = e.target;
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
      if (e.isComposing) return;
      const inField = isTextField(e.target);
      const key = keyCombo(e, inField);
      if (!key) return;
      flush();
      emit({ type: "press_key", key }, e.target && e.target.tagName ? e.target : null);
    }

    function onHover(e) {
      if (!pick || !overlay || !e.target || !e.target.getBoundingClientRect) return;
      const r = e.target.getBoundingClientRect();
      Object.assign(overlay.style, {
        display: "block",
        left: `${r.left}px`,
        top: `${r.top}px`,
        width: `${r.width}px`,
        height: `${r.height}px`,
      });
    }

    function listen(type, fn) {
      doc.addEventListener(type, fn, true);
      bound.push([type, fn]);
    }

    function start() {
      listen("click", onClick);
      listen("dblclick", onClick);
      listen("contextmenu", onClick);
      listen("input", onInput);
      listen("change", onChange);
      listen("focusout", onFocusOut);
      listen("submit", onSubmit);
      listen("keydown", onKeyDown);
      listen("mouseover", onHover);
    }

    function setPick(on) {
      pick = !!on;
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

    function stop() {
      flush();
      for (const [type, fn] of bound) doc.removeEventListener(type, fn, true);
      bound.length = 0;
      setPick(false);
    }

    return { start, stop, flush, setPick, handlers: { onClick, onInput, onChange, onFocusOut, onSubmit, onKeyDown } };
  }

  root.MonoRecorder = {
    createRecorder, keyCombo, maskReason, secretName, luhn, snippetOf, framePath, actionable,
    DEBOUNCE_MS, SNIPPET_MAX,
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
  }
  root.MonoRecorder.setPick = (on) => {
    root.__monoRecorderPick = !!on;
    if (root.__monoRecorderActive) root.__monoRecorderActive.setPick(on);
  };
  root.MonoRecorder.stopActive = () => {
    if (root.__monoRecorderActive) root.__monoRecorderActive.stop();
    root.__monoRecorderActive = null;
  };
})(globalThis);
