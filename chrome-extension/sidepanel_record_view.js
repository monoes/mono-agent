/**
 * MonoAgent Bridge — what the Record section of the side panel says (§8.6)
 *
 * Pure, like sidepanel_view.js: a recording's steps in, the sentences the
 * panel shows out. sidepanel_record.js only paints them. Kept apart so the
 * wording — which is what a person reads to decide whether the recording
 * captured what they meant — is tested in node.
 *
 * Two kinds of recorded event are not steps of their own:
 *   - `param` marks an earlier typed value as a run-time input; it shows as
 *     the "input" toggle on that step (latest mark wins, note "unset" clears);
 *   - an `extract` whose note is "supersedes eN" replaces eN (a list built
 *     from two picks, or a renamed field), so eN is hidden.
 */

(function (root) {
  "use strict";

  const quote = (s) => `'${String(s).length > 60 ? `${String(s).slice(0, 57)}…` : s}'`;

  /** targetName is how a person would refer to the element. */
  function targetName(target) {
    const t = target || {};
    return t.label || t.ariaName || t.text || t.placeholder || t.name || (t.tag ? `the ${t.tag}` : "the page");
  }

  function hostPath(url) {
    const m = /^[a-z]+:\/\/(?:www\.)?([^/?#]+)([^?#]*)/i.exec(String(url || ""));
    if (!m) return String(url || "");
    const path = m[2] && m[2] !== "/" ? m[2] : "";
    return `${m[1]}${path}`;
  }

  /** sensitiveKind flags a value that looks personal, so it is not baked into an action. */
  function sensitiveKind(value) {
    const v = String(value || "").trim();
    if (!v) return "";
    if (/^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(v)) return "email";
    const digits = v.replace(/[\s().+-]/g, "");
    if (/^\d{13,19}$/.test(digits) && /^[\d\s-]+$/.test(v)) return "card";
    if (/^\+?[\d\s().-]{7,20}$/.test(v) && digits.length >= 7 && digits.length <= 15) return "phone";
    return "";
  }

  const NAV = {
    typed: (u) => `Go to ${hostPath(u)}`,
    reload: () => "Reload the page",
    back_forward: (u) => `Go back/forward to ${hostPath(u)}`,
  };

  /** describe is one step's sentence. */
  function describe(step) {
    const s = step || {};
    const name = quote(targetName(s.target));
    switch (s.type) {
      case "click":
        return s.note === "double" ? `Double-click ${name}` : s.note === "context" ? `Right-click ${name}` : `Click ${name}`;
      case "type":
        if (s.masked) return `Type into ${name} (hidden — becomes secret ${s.secretAs || "input"})`;
        return s.value ? `Type ${quote(s.value)} into ${name}` : `Clear ${name}`;
      case "select_option":
        return `Choose ${quote(s.value || "")} in ${name}`;
      case "check":
        return `${s.checked === false ? "Uncheck" : "Check"} ${name}`;
      case "submit":
        return "Submit the form";
      case "press_key":
        return `Press ${s.key || "a key"}`;
      case "navigate":
        return (NAV[s.navCause] || NAV.typed)(s.url);
      case "navigated":
        return `Page changed to ${hostPath(s.url)}`;
      case "upload":
        return `Upload ${quote(s.value || "a file")}`;
      case "scroll":
        return "Scroll";
      case "extract": {
        const x = s.extract || {};
        const field = quote(x.field || "data");
        if (x.list) {
          const n = (x.samples || []).length;
          return `Extract list ${field} (${n} sample${n === 1 ? "" : "s"})`;
        }
        return `Extract ${field}`;
      }
      case "param":
        return `Mark ${(s.param && s.param.refEvent) || "a step"} as an input`;
      default:
        return s.type || "Unknown step";
    }
  }

  /**
   * rows folds the raw step list into what the panel lists: params become
   * flags on the step they mark, superseded extracts disappear.
   */
  function rows(steps) {
    const list = steps || [];
    const hidden = new Set();
    const params = new Map(); // refEvent -> {on, name}
    for (const s of list) {
      const sup = /^supersedes (e\d+)$/.exec(s.note || "");
      if (s.type === "extract" && sup) hidden.add(sup[1]);
      if (s.type === "param" && s.param) {
        params.set(s.param.refEvent, { on: s.note !== "unset", name: s.param.name || "" });
      }
    }
    return list
      .filter((s) => s.type !== "param" && !hidden.has(s.id))
      .map((s) => {
        const p = params.get(s.id);
        const row = {
          id: s.id,
          type: s.type,
          text: describe(s),
          canParam: (s.type === "type" && !s.masked) || s.type === "select_option" || s.type === "upload",
          isParam: !!(p && p.on),
          paramName: (p && p.name) || "",
          sensitive: s.type === "type" && !s.masked ? sensitiveKind(s.value) : "",
          isExtract: s.type === "extract",
          field: (s.extract && s.extract.field) || "",
          samples: (s.extract && s.extract.samples) || [],
        };
        // A personal-looking value is suggested as an input until someone says otherwise.
        row.suggestInput = !!row.sensitive && !row.isParam;
        return row;
      });
  }

  const REASONS = {
    user: "",
    tab_closed: "the recorded tab was closed",
    new_tab: "a new tab was opened — recording follows one tab",
    panel_closed: "the side panel was closed",
    error: "the page could not be recorded",
  };

  /** hasErrors is true when a draft has error-level lint (saving needs "Save anyway"). */
  const hasErrors = (d) => !!d && (d.lint || []).some((l) => /^error$/i.test(l.level));

  /** summary is the line under the step list once recording has stopped. */
  function summary(state) {
    const st = state || {};
    if (st.discarded) return "Nothing was saved: the recording had no steps.";
    const n = rows(st.steps).length;
    const bits = [`${n} step${n === 1 ? "" : "s"}`];
    if (st.url) bits.push(`on ${hostPath(st.url)}`);
    let line = bits.join(" ");
    const why = REASONS[st.stopReason];
    if (why) line += ` — stopped because ${why}`;
    if (st.queued) line += `. ${st.queued} update${st.queued === 1 ? "" : "s"} waiting for the bridge`;
    return line;
  }

  function inputName(raw) {
    if (typeof raw === "string") return raw;
    return (raw && (raw.name || raw.key || raw.id)) || "";
  }

  /**
   * describeDraft reads `record analyze --json` ({draftDir, draft}) into
   * what the panel shows. Tolerant of where the action definition sits,
   * because the panel should show something useful from any draft.
   */
  function describeDraft(result) {
    const r = result || {};
    const draft = r.draft || {};
    const def =
      (draft.actionDef && typeof draft.actionDef === "object" && draft.actionDef) ||
      (draft.def && typeof draft.def === "object" && draft.def) ||
      (draft.action && typeof draft.action === "object" && draft.action) ||
      (r.action && typeof r.action === "object" && r.action) ||
      {};
    const names = draft.names || {};
    const recorded = draft.recordedInputs || {};
    return {
      draftDir: r.draftDir || "",
      action: names.action || (typeof draft.action === "string" ? draft.action : "") || def.actionType || "",
      automation: names.automation || draft.targetAutomation || "",
      isNew: !!draft.isNew,
      saveAs: draft.saveAs || "action",
      inputs: draftInputs(draft, def).map((i) => describeInput(i, recorded)),
      steps: (def.steps || []).map((s) => ({
        id: s.id || "",
        type: s.type || "",
        detail: s.description || s.configKey || s.selector || s.url || s.key || "",
        // A boolean in the action format: the step writes, sends or deletes.
        sideEffect: s.sideEffect === true || (typeof s.sideEffect === "string" && s.sideEffect !== "" && s.sideEffect !== "none"),
      })),
      lint: (draft.lint || []).map((i) => ({
        level: i.severity || i.level || "warning",
        message: i.message || i.code || String(i),
      })),
      // page_script sources, shown in full: they run with the person's session.
      scripts: Object.keys(draft.scripts || {})
        .sort()
        .map((name) => ({ name, source: String(draft.scripts[name]) })),
    };
  }

  /** draftInputs prefers the draft's flat input list; else the action's required/optional. */
  function draftInputs(draft, def) {
    if (Array.isArray(draft.inputs)) return draft.inputs.filter((i) => i && i.name);
    const inputs = def.inputs || {};
    return []
      .concat((inputs.required || []).map((x) => (typeof x === "object" ? Object.assign({}, x, { required: true }) : { name: inputName(x), required: true })))
      .concat((inputs.optional || []).map((x) => (typeof x === "object" ? Object.assign({}, x, { required: false }) : { name: inputName(x), required: false })))
      .filter((x) => x.name);
  }

  /**
   * describeInput says whether verify needs a value typed in: a secret
   * always does (its value was never recorded), and so does any input the
   * recording has no value for and that has no default.
   */
  function describeInput(i, recorded) {
    const secret =
      i.secret === true ||
      /^(secret|password)$/i.test(i.type || "") ||
      /^(secret|password)$/i.test(i.format || "") ||
      /^\{\{\s*secret:/.test(String(i.default || ""));
    const has = Object.prototype.hasOwnProperty.call(recorded, i.name);
    return {
      name: i.name,
      required: i.required !== false,
      secret,
      recorded: has && !secret ? String(recorded[i.name]) : "",
      needsValue: typeof i.needsValue === "boolean" ? i.needsValue || secret : secret || (!has && i.default == null),
    };
  }

  /** describeVerify turns `record verify --json` into one line per step. */
  function describeVerify(result) {
    const r = result || {};
    return {
      ok: !!r.ok,
      stoppedAt: r.stoppedAt || null,
      steps: (r.steps || []).map((s) => ({
        id: s.id || "",
        status: s.status || "",
        text: `${s.id || "?"} ${s.type || ""} — ${s.status || "?"}${s.message ? `: ${s.message}` : ""}`,
      })),
    };
  }

  root.MonoRecordView = { describe, rows, summary, hasErrors, sensitiveKind, targetName, hostPath, describeDraft, describeVerify };
})(globalThis);
