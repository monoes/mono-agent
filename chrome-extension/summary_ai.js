/**
 * MonoAgent Bridge — which AI writes a capture's summary
 *
 * "Save page summary" and "Save video summary" (the right-click menu, and
 * the side panel's Summary mode) end with the bridge asking a local agent
 * runtime for summary.md. This file is the choice of runtime and model,
 * made the way the desktop app's chat box makes it: pick an installed
 * runtime, then one of its models — a list when the runtime has one, free
 * text when it does not — or leave the model to the runtime's own default.
 *
 * The rules mirror capture_profile.js, for the same reasons:
 *
 *   STICKY. The choice lives in chrome.storage.local, so a right-click
 *   summary uses it with no questions, the same as a panel save.
 *
 *   NEVER BLOCK A CAPTURE. The lists come from the bridge (summary.runtimes,
 *   summary.models) and the bridge is often not there. The last list seen
 *   is cached for an offline panel, and a capture only ever reads storage.
 *
 *   THE BRIDGE DECIDES. A remembered runtime is only a request: the bridge
 *   checks it against its own scan before running anything, and records a
 *   refusal in summary.json. Nothing here needs to be trusted.
 *
 * The choice is { runtime, model }. An empty runtime means "the bridge's
 * default" and is never sent; an empty model means "the runtime's default".
 *
 * Pure apart from the storage and request calls it is handed, so the
 * judgement (choose, describePicker) is tested in node. The panel loads
 * this file for describePicker; the worker for everything else.
 */

(function (root) {
  "use strict";

  const CHOICE_KEY = "summaryAI";
  const CACHE_KEY = "summaryAICache";
  const METHOD_RUNTIMES = "summary.runtimes";
  const METHOD_MODELS = "summary.models";
  // A scan probes every agent CLI on the machine and can take seconds the
  // first time; the bridge caches it for five minutes after that.
  const RUNTIMES_TIMEOUT_MS = 30000;
  const MODELS_TIMEOUT_MS = 25000;
  const DEFAULT_MODEL_LABEL = "Runtime default";

  // Identical to capturesummary.ValidRuntimeID / ValidModel (Go).
  const RUNTIME_ID = /^[a-z0-9][a-z0-9._-]{0,63}$/;
  const MODEL_ID = /^[A-Za-z0-9][A-Za-z0-9._:/@[\]+-]{0,127}$/;

  const str = (v) => (typeof v === "string" ? v.trim() : "");
  const isValidRuntimeId = (id) => RUNTIME_ID.test(str(id));
  const isValidModel = (m) => MODEL_ID.test(str(m));

  function normalizeRuntimes(list) {
    const out = [];
    const seen = new Set();
    for (const raw of Array.isArray(list) ? list : []) {
      const id = str(raw && raw.id);
      if (!isValidRuntimeId(id) || seen.has(id)) continue;
      seen.add(id);
      out.push(Object.assign({ id }, str(raw.version) ? { version: str(raw.version) } : {}));
    }
    return out;
  }

  function normalizeModels(list) {
    const out = [];
    const seen = new Set();
    for (const raw of Array.isArray(list) ? list : []) {
      const id = str(raw && raw.id);
      if (!isValidModel(id) || seen.has(id)) continue;
      seen.add(id);
      out.push({ id, label: str(raw.label) || id });
    }
    return out;
  }

  /** normalizeChoice keeps only what could be sent; a model needs a runtime. */
  function normalizeChoice(raw) {
    const runtime = isValidRuntimeId(raw && raw.runtime) ? str(raw.runtime) : "";
    const model = runtime && isValidModel(raw && raw.model) ? str(raw.model) : "";
    return { runtime, model };
  }

  function normalizeCatalog(raw) {
    const c = raw || {};
    return {
      runtimes: normalizeRuntimes(c.runtimes),
      default: isValidRuntimeId(c.default) ? str(c.default) : "",
      enabled: c.enabled !== false,
    };
  }

  /**
   * choose settles the choice against a runtime list:
   *
   *   - offline, or nothing listed yet: the remembered choice as it is;
   *   - remembered runtime still installed: kept;
   *   - remembered runtime GONE from a live list: back to the bridge's
   *     default, with `changed` and a reason the panel says out loud —
   *     summarizing with a different AI than the one picked is worth a line.
   */
  function choose(stored, catalog, opts) {
    const choice = normalizeChoice(stored);
    const cat = normalizeCatalog(catalog);
    const offline = !!(opts && opts.offline);
    if (offline || !choice.runtime) return Object.assign(choice, { changed: false, reason: "" });
    if (cat.runtimes.some((r) => r.id === choice.runtime)) return Object.assign(choice, { changed: false, reason: "" });
    const fallback = cat.default ? `the bridge's default, ${cat.default}` : "the bridge's default";
    return {
      runtime: "",
      model: "",
      changed: true,
      reason: `${choice.runtime} is no longer installed — using ${fallback}`,
    };
  }

  /** effectiveRuntime is the runtime a summary would use right now. */
  function effectiveRuntime(choice, catalog) {
    return normalizeChoice(choice).runtime || normalizeCatalog(catalog).default;
  }

  /**
   * describePicker turns the worker's state into what the panel draws.
   *
   * state: { choice, catalog, offline, known (a list was ever seen),
   *          models (array, or null while unknown), modelsLoading,
   *          modelsError, changed, reason }
   */
  function describePicker(state) {
    const s = state || {};
    const cat = normalizeCatalog(s.catalog);
    const choice = normalizeChoice(s.choice);
    const runtime = effectiveRuntime(choice, cat);
    const models = Array.isArray(s.models) ? normalizeModels(s.models) : null;
    const loading = !!s.modelsLoading;

    const runtimeOptions = cat.runtimes.map((r) => ({ value: r.id, label: r.id, selected: r.id === runtime }));
    // A remembered runtime the offline list does not have is still shown,
    // so the picker never claims a choice nobody made.
    if (runtime && !runtimeOptions.some((o) => o.selected)) runtimeOptions.unshift({ value: runtime, label: runtime, selected: true });

    let modelMode = "text";
    if (loading) modelMode = "loading";
    else if (models && models.length) modelMode = "select";
    const modelOptions = [{ value: "", label: DEFAULT_MODEL_LABEL }].concat(
      (models || []).map((m) => ({ value: m.id, label: m.label }))
    );
    // A typed model on a runtime that now has a list is kept visible too.
    if (modelMode === "select" && choice.model && !modelOptions.some((o) => o.value === choice.model)) {
      modelOptions.push({ value: choice.model, label: choice.model });
    }

    const interactive = !s.offline && cat.enabled && cat.runtimes.length > 0;
    const modelName = choice.model
      ? ((models || []).find((m) => m.id === choice.model) || { label: choice.model }).label
      : DEFAULT_MODEL_LABEL.toLowerCase();

    let note = null;
    if (!cat.enabled && !s.offline) {
      note = { tone: "warn", text: "Summaries are turned off on this bridge, so this choice is not used. Restart it without --summary-runtime off to turn them on." };
    } else if (s.changed && s.reason) {
      note = { tone: "warn", text: `${capitalize(s.reason)}.` };
    } else if (s.scanError) {
      note = { tone: "warn", text: `The bridge couldn't list agent runtimes: ${s.scanError}` };
      // The app's chat box gives the same fix for the same failure.
      if (/monomind/i.test(s.scanError) && /not (found|installed)/i.test(s.scanError)) note.command = "npm install -g @monoes/monomindcli";
    } else if (s.offline) {
      note = s.known
        ? { tone: "idle", text: "The bridge isn't connected, so this is the last list it gave. Summaries saved now still use this choice." }
        : { tone: "idle", text: "Start the bridge to choose which AI writes summaries." };
    } else if (!cat.runtimes.length) {
      note = { tone: "warn", text: "No agent runtimes are installed, so summaries cannot be written.", command: "monoagentcli agent scan --installed" };
    } else if (s.modelsError) {
      note = { tone: "idle", text: `Couldn't list ${runtime}'s models. Type one, or leave it empty for the runtime's default.` };
    } else if (modelMode === "text" && runtime) {
      note = { tone: "idle", text: `${runtime} reports no model list. Type a model id, or leave it empty for its default.` };
    }

    return {
      runtime,
      model: choice.model,
      name: displayName(runtime, modelName, s, cat),
      interactive,
      runtimeOptions,
      modelMode,
      modelOptions,
      modelValue: choice.model,
      modelPlaceholder: DEFAULT_MODEL_LABEL,
      note,
    };
  }

  // The pages the right-click menu offers "Save video summary" on
  // (MonoYouTubeVideo.MENU_PATTERNS), as one test for the panel.
  const VIDEO_PAGE = /^https?:\/\/(([^/]+\.)?youtube\.com\/(watch|shorts\/|live\/)|youtu\.be\/)/i;

  /**
   * panelMode is the capture mode the panel's Save button uses for a mode
   * choice on a page. "Summary" on a video is the right-click menu's video
   * summary — the transcript is what is worth summarizing there. `label`
   * is what the Summary choice is called on this page.
   */
  function panelMode(choice, url) {
    const video = VIDEO_PAGE.test(String(url || ""));
    const label = video ? "Video summary" : "Summary";
    if (choice === "screenshot") return { mode: "screenshot", label, summarizes: false };
    if (choice !== "summary") return { mode: "full", label, summarizes: false };
    return { mode: video ? "video" : "summary", label, summarizes: true };
  }

  function displayName(runtime, modelName, s, cat) {
    if (!cat.enabled && !s.offline) return "Turned off on this bridge";
    if (runtime) return `${runtime} · ${modelName}`;
    return s.offline && !s.known ? "Not known yet" : "None available";
  }

  function capitalize(text) {
    return text ? text[0].toUpperCase() + text.slice(1) : text;
  }

  /**
   * applyToMeta stamps the choice onto a capture that asked for a summary.
   * A capture that did not ask is left exactly as it was, and an empty
   * choice leaves meta.summarize as {kind}, which the bridge reads as "use
   * the default".
   */
  function applyToMeta(meta, choice) {
    if (!meta || !meta.summarize || typeof meta.summarize !== "object") return meta;
    const c = normalizeChoice(choice);
    const next = { kind: meta.summarize.kind };
    if (c.runtime) next.runtime = c.runtime;
    if (c.model) next.model = c.model;
    meta.summarize = next;
    return meta;
  }

  // --- storage ------------------------------------------------------------

  async function load(storage) {
    let stored = {};
    try {
      stored = (await storage.get([CHOICE_KEY, CACHE_KEY])) || {};
    } catch {
      // No storage: summaries use the bridge's default.
    }
    const cache = stored[CACHE_KEY] || null;
    return {
      choice: normalizeChoice(stored[CHOICE_KEY]),
      catalog: normalizeCatalog(cache),
      models: (cache && cache.models) || {},
      known: !!cache,
    };
  }

  async function remember(storage, choice) {
    const value = normalizeChoice(choice);
    try {
      await storage.set({ [CHOICE_KEY]: value });
    } catch {
      // A convenience; failing to store it must not fail anything.
    }
    return value;
  }

  async function cacheCatalog(storage, catalog, models) {
    const value = Object.assign(normalizeCatalog(catalog), { models: models || {} });
    try {
      await storage.set({ [CACHE_KEY]: value });
    } catch {
      // See remember().
    }
    return value;
  }

  /** sticky is what a right-click or panel capture uses: storage only. */
  async function sticky(storage) {
    return (await load(storage)).choice;
  }

  // --- asking the bridge ---------------------------------------------------

  async function fetchRuntimes(ask) {
    if (!ask || typeof ask.request !== "function") return { offline: true };
    try {
      if (typeof ask.supports === "function" && !(await ask.supports(METHOD_RUNTIMES))) return { offline: true };
      const data = await ask.request(METHOD_RUNTIMES, {}, { timeoutMs: RUNTIMES_TIMEOUT_MS, idleTimeoutMs: RUNTIMES_TIMEOUT_MS });
      return { offline: false, catalog: normalizeCatalog(data) };
    } catch (err) {
      // "unavailable" is a bridge that is there but could not scan
      // (monomind missing): worth saying, unlike a bridge that is not there.
      const scanError = err && err.code === "unavailable" ? err.message || "the scan failed" : "";
      return { offline: true, scanError };
    }
  }

  async function fetchModels(ask, runtime) {
    if (!isValidRuntimeId(runtime)) return { models: [], error: "no runtime" };
    if (!ask || typeof ask.request !== "function") return { offline: true, models: null };
    try {
      const data = await ask.request(METHOD_MODELS, { runtime: str(runtime) }, { timeoutMs: MODELS_TIMEOUT_MS, idleTimeoutMs: MODELS_TIMEOUT_MS });
      return { models: normalizeModels(data && data.models) };
    } catch (err) {
      if (err && err.code === "offline") return { offline: true, models: null };
      return { models: [], error: (err && err.message) || "failed" };
    }
  }

  /**
   * state is what the panel draws. With `live` false it answers from
   * storage alone (instant, for the first paint); with `live` true it asks
   * the bridge for the runtimes and the effective runtime's models, caches
   * both, and moves a remembered runtime that is gone back to the default.
   */
  async function state(ask, storage, live) {
    const saved = await load(storage);
    let catalog = saved.catalog;
    let offline = true;
    let known = saved.known;
    let models = saved.models;
    let scanError = "";
    if (live) {
      const got = await fetchRuntimes(ask);
      scanError = got.scanError || "";
      if (!got.offline) {
        offline = false;
        known = true;
        // A model list from an older scan is only kept for runtimes still there.
        const kept = {};
        for (const r of got.catalog.runtimes) if (models[r.id]) kept[r.id] = models[r.id];
        catalog = got.catalog;
        models = kept;
      }
    }
    const chosen = choose(saved.choice, catalog, { offline });
    if (chosen.changed) await remember(storage, chosen);
    const runtime = effectiveRuntime(chosen, catalog);

    let list = runtime && models[runtime] ? models[runtime] : null;
    let modelsError = "";
    if (live && !offline && runtime && catalog.enabled) {
      const got = await fetchModels(ask, runtime);
      if (!got.offline) {
        list = got.models;
        modelsError = got.error || "";
        if (!modelsError) models = Object.assign({}, models, { [runtime]: list });
      }
    }
    if (!offline) await cacheCatalog(storage, catalog, models);
    return {
      choice: { runtime: chosen.runtime, model: chosen.model },
      catalog,
      offline,
      known,
      scanError,
      models: list,
      modelsError,
      changed: chosen.changed,
      reason: chosen.reason,
    };
  }

  /** modelsFor answers the panel's "this runtime was just picked". */
  async function modelsFor(ask, storage, runtime) {
    const saved = await load(storage);
    const got = await fetchModels(ask, runtime);
    if (got.offline) return { models: saved.models[runtime] || null, offline: true, modelsError: "" };
    if (!got.error && saved.known) {
      await cacheCatalog(storage, saved.catalog, Object.assign({}, saved.models, { [runtime]: got.models }));
    }
    return { models: got.models, offline: false, modelsError: got.error || "" };
  }

  root.MonoSummaryAI = {
    isValidRuntimeId, isValidModel, normalizeRuntimes, normalizeModels, normalizeChoice, normalizeCatalog,
    choose, effectiveRuntime, describePicker, applyToMeta, panelMode,
    load, remember, cacheCatalog, sticky, fetchRuntimes, fetchModels, state, modelsFor,
    CHOICE_KEY, CACHE_KEY, METHOD_RUNTIMES, METHOD_MODELS, DEFAULT_MODEL_LABEL,
  };
})(globalThis);
