/**
 * MonoAgent Bridge — the side panel's "AI for summaries" picker and its
 * Full page / Screenshot / Summary choice
 *
 * The picker is the desktop chat box's runtime → model choice: an
 * installed agent runtime, then one of its models (a list when the runtime
 * has one, free text when it does not, "Runtime default" either way). What
 * is picked is remembered by the worker at once, so the right-click
 * "Save page summary" / "Save video summary" use it too.
 *
 * Only drawing lives here. What the picker says, and when it can be used,
 * is MonoSummaryAI.describePicker (summary_ai.js, tested in node); the lists
 * and the stored choice are the worker's (capture_actions.js summary_ai_*).
 *
 * Loaded before sidepanel.js, which asks MonoPanelAI.captureMode() what the
 * Save button should save.
 */

(function () {
  "use strict";

  const AI = globalThis.MonoSummaryAI;
  const $ = (id) => document.getElementById(id);

  const box = $("ai");
  const btn = $("ai-btn");
  const nameEl = $("ai-name");
  const menu = $("ai-menu");
  const runtimeSel = $("ai-runtime");
  const modelSel = $("ai-model-select");
  const modelText = $("ai-model-text");
  const note = $("ai-note");
  const noteText = $("ai-note-text");
  const commandRow = $("ai-command-row");
  const command = $("ai-command");
  const copy = $("ai-copy");
  const modes = $("modes");
  const summaryLabel = $("mode-summary-label");

  /** The worker's last answer, which every redraw starts from. */
  let state = null;
  let pageUrl = "";
  // A later answer wins; an earlier one arriving late is dropped.
  let generation = 0;
  // "X is no longer installed" is said once by the worker (it then stores
  // the fallback), but stays on screen until the person picks something:
  // the panel reloads its lists more than once while it opens.
  let fallbackReason = "";

  function askWorker(message) {
    return new Promise((resolve) => {
      chrome.runtime.sendMessage(message, (response) => {
        const lastError = chrome.runtime.lastError;
        if (AI.unanswered(response, lastError)) {
          resolve({ ok: false, unanswered: true, error: lastError ? lastError.message : "" });
          return;
        }
        resolve(response);
      });
    });
  }

  function fillSelect(select, options) {
    select.textContent = "";
    for (const o of options) {
      const opt = document.createElement("option");
      opt.value = o.value;
      opt.textContent = o.label;
      if (o.selected) opt.selected = true;
      select.appendChild(opt);
    }
  }

  function draw(extra) {
    if (!state) return;
    const view = AI.describePicker(Object.assign({}, state, extra || {}));
    nameEl.textContent = view.name;
    btn.disabled = !view.interactive && !view.runtimeOptions.length;
    if (btn.disabled) close(false);

    fillSelect(runtimeSel, view.runtimeOptions);
    runtimeSel.disabled = !view.interactive;

    modelSel.hidden = view.modelMode === "text";
    modelText.hidden = view.modelMode !== "text";
    if (view.modelMode === "loading") {
      fillSelect(modelSel, [{ value: "", label: "Loading models…", selected: true }]);
      modelSel.disabled = true;
    } else if (view.modelMode === "select") {
      fillSelect(modelSel, view.modelOptions.map((o) => Object.assign({}, o, { selected: o.value === view.modelValue })));
      modelSel.disabled = !view.interactive;
    }
    if (document.activeElement !== modelText) modelText.value = view.modelValue;
    modelText.placeholder = view.modelPlaceholder;
    modelText.disabled = !view.interactive;

    const n = view.note;
    note.hidden = !n;
    note.dataset.tone = n ? n.tone : "";
    noteText.textContent = n ? n.text : "";
    commandRow.hidden = !(n && n.command);
    command.textContent = (n && n.command) || "";
    copy.setAttribute("aria-label", `Copy the command ${(n && n.command) || ""}`);
  }

  async function load(live) {
    const mine = ++generation;
    const answer = await askWorker({ type: "summary_ai_state", live });
    if (mine !== generation) return;
    // An unpacked extension keeps running the background worker it was
    // loaded with, while this document is read from disk every time the
    // panel opens. After an update on disk, the newer panel asks for
    // things the older worker has never heard of — so say the one thing
    // that fixes it instead of quietly dropping the picker.
    if (answer.unanswered) {
      box.hidden = true;
      document.dispatchEvent(new CustomEvent("panel:stale-worker"));
      return;
    }
    if (answer.ok === false) return;
    box.hidden = false;
    if (answer.changed && answer.reason) fallbackReason = answer.reason;
    state = fallbackReason ? Object.assign({}, answer, { changed: true, reason: fallbackReason }) : answer;
    draw();
  }

  async function choose(runtime, model) {
    const answer = await askWorker({ type: "summary_ai_set", runtime, model });
    if (answer && answer.ok && state) {
      fallbackReason = "";
      state = Object.assign({}, state, { choice: answer.choice, changed: false, reason: "" });
    }
    return answer;
  }

  runtimeSel.addEventListener("change", async () => {
    const runtime = runtimeSel.value;
    const mine = ++generation;
    // A new runtime starts on its own default model, as in the app.
    await choose(runtime, "");
    state = Object.assign({}, state, { models: null, modelsError: "" });
    draw({ modelsLoading: true });
    const answer = await askWorker({ type: "summary_ai_models", runtime });
    if (mine !== generation) return;
    state = Object.assign({}, state, {
      models: answer && answer.ok ? answer.models : [],
      modelsError: (answer && answer.modelsError) || "",
    });
    draw();
  });

  modelSel.addEventListener("change", async () => {
    await choose(state.choice.runtime || AI.effectiveRuntime(state.choice, state.catalog), modelSel.value);
    draw();
  });

  // Typed models are checked here as well as on the bridge, so a typo is
  // said at once rather than in a summary.json later.
  modelText.addEventListener("change", async () => {
    const value = modelText.value.trim();
    if (value && !AI.isValidModel(value)) {
      draw();
      note.hidden = false;
      note.dataset.tone = "warn";
      noteText.textContent = `“${value}” is not a model id: letters, digits and . _ - : / only, no spaces.`;
      return;
    }
    await choose(state.choice.runtime || AI.effectiveRuntime(state.choice, state.catalog), value);
    draw();
  });
  modelText.addEventListener("keydown", (event) => {
    if (event.key === "Enter") modelText.blur();
  });

  // --- opening and closing, as the profile switcher does ------------------

  function open() {
    if (btn.disabled) return;
    menu.hidden = false;
    btn.setAttribute("aria-expanded", "true");
    runtimeSel.focus();
  }

  function close(returnFocus) {
    if (menu.hidden) return;
    menu.hidden = true;
    btn.setAttribute("aria-expanded", "false");
    if (returnFocus) btn.focus();
  }

  btn.addEventListener("click", () => (menu.hidden ? open() : close(true)));
  box.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !menu.hidden) {
      event.stopPropagation();
      close(true);
    }
  });
  document.addEventListener("pointerdown", (event) => {
    if (!box.contains(event.target)) close(false);
  });

  copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(command.textContent);
      copy.textContent = "Copied";
      setTimeout(() => (copy.textContent = "Copy"), 1600);
    } catch {
      copy.textContent = "Press ⌘/Ctrl+C";
    }
  });

  // --- the mode beside Save ------------------------------------------------

  function checkedMode() {
    const input = modes.querySelector("input:checked");
    return input ? input.value : "full";
  }

  function drawModes() {
    summaryLabel.textContent = AI.panelMode(checkedMode(), pageUrl).label;
  }

  document.addEventListener("panel:page", (event) => {
    pageUrl = (event.detail && event.detail.url) || "";
    drawModes();
  });
  // The bridge came back: its lists may have changed while it was away.
  document.addEventListener("panel:bridge-up", () => load(true));

  globalThis.MonoPanelAI = {
    /** captureMode is what Save saves: { mode, label, summarizes }. */
    captureMode: () => AI.panelMode(checkedMode(), pageUrl),
    onModeChange: (fn) => modes.addEventListener("change", () => fn(AI.panelMode(checkedMode(), pageUrl))),
    /** summaryBy names the AI a summary saved now is written by. */
    summaryBy: () => (state ? AI.describePicker(state).name : ""),
  };

  // First paint from storage, then the bridge's answer when it has one.
  load(false).then(() => load(true));
})();
