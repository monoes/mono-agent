/**
 * MonoAgent Bridge — the Record section of the side panel (§8.2, §8.6)
 *
 * Paints what the worker's recorder session says (recorder_wiring.js) and
 * sends it the person's decisions. It holds no recording state of its own:
 * the worker pushes the whole state over the "monoagent-record" port on
 * every change, and this redraws from it. That port is also how the worker
 * knows the panel closed — which stops a recording, by design.
 *
 * The wording lives in sidepanel_record_view.js (node-tested). `ask` and
 * MonoPanelPage come from sidepanel.js, loaded first.
 */

(function () {
  "use strict";

  const View = globalThis.MonoRecordView;
  const el = (id) => document.getElementById(id);

  const panel = el("record-panel");
  const dot = el("rec-dot");
  const goal = el("rec-goal");
  const toggle = el("rec-toggle");
  const pick = el("rec-pick");
  const list = el("rec-steps");
  const summaryLine = el("rec-summary");
  const after = el("rec-after");
  const analyzeBtn = el("rec-analyze");
  const clearBtn = el("rec-clear");
  const msg = el("rec-msg");
  const draftBox = el("rec-draft");
  const draftName = el("rec-draft-name");
  const draftWhere = el("rec-draft-where");
  const draftInputs = el("rec-draft-inputs");
  const draftSteps = el("rec-draft-steps");
  const draftLint = el("rec-draft-lint");
  const saveAs = el("rec-save-as");
  const saveName = el("rec-save-name");
  const saveAutomation = el("rec-save-automation");
  const verifyBtn = el("rec-verify");
  const saveBtn = el("rec-save");
  const verifySteps = el("rec-verify-steps");
  const draftMsg = el("rec-draft-msg");

  let port = null;
  let state = { recording: false, steps: [] };
  let draft = null;
  let pendingDraw = false;

  function say(target, kind, text) {
    target.textContent = text || "";
    if (text) target.dataset.kind = kind;
    else delete target.dataset.kind;
  }

  function node(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  async function send(message) {
    const res = await ask(message); // sidepanel.js
    if (!res || res.ok === false) throw new Error((res && res.error) || "the extension did not answer");
    return res;
  }

  // ── the step list ────────────────────────────────────────────────

  function stepRow(row) {
    const li = node("li", "rec-step");
    li.dataset.type = row.type;
    li.appendChild(node("span", "rec-step-text", row.text));

    if (row.canParam) {
      const label = node("label", "rec-input");
      const box = document.createElement("input");
      box.type = "checkbox";
      box.checked = row.isParam;
      box.addEventListener("change", () => mark({ kind: "param", refEvent: row.id, on: box.checked }));
      label.appendChild(box);
      label.appendChild(document.createTextNode(" input"));
      label.title = "This value changes each run";
      li.appendChild(label);
    }
    if (row.suggestInput) li.appendChild(node("span", "chip rec-suggest", `looks like ${row.sensitive} — suggested input`));

    if (row.isExtract) {
      const field = document.createElement("input");
      field.type = "text";
      field.className = "rec-field";
      field.value = row.field;
      field.setAttribute("aria-label", `Field name for ${row.id}`);
      field.addEventListener("change", () => {
        const name = field.value.trim();
        if (name && name !== row.field) mark({ kind: "extract", refEvent: row.id, field: name });
      });
      li.appendChild(field);
      if (row.samples.length) li.appendChild(node("span", "rec-samples", row.samples.slice(0, 3).join(" · ")));
    }
    return li;
  }

  function drawSteps() {
    // Never redraw under someone typing a field name: it would eat the edit.
    if (list.contains(document.activeElement)) {
      pendingDraw = true;
      return;
    }
    pendingDraw = false;
    list.textContent = "";
    for (const row of View.rows(state.steps)) list.appendChild(stepRow(row));
  }

  list.addEventListener("focusout", () => {
    if (pendingDraw) setTimeout(drawSteps, 0);
  });

  function draw(next) {
    state = next || { recording: false, steps: [] };
    const rec = !!state.recording;
    dot.hidden = !rec;
    panel.classList.toggle("recording", rec);
    if (rec) panel.open = true;
    toggle.textContent = rec ? "Stop" : "Record";
    toggle.dataset.recording = rec ? "true" : "false";
    goal.disabled = rec;
    if (rec && state.goal && !goal.value) goal.value = state.goal;
    pick.disabled = !rec;
    pick.checked = rec && !!state.pick;
    drawSteps();
    const done = !rec && !!state.id;
    summaryLine.hidden = !done;
    after.hidden = !done;
    if (done) {
      summaryLine.textContent = View.summary(state);
      analyzeBtn.disabled = !state.delivered;
      analyzeBtn.title = state.delivered ? "" : "Waiting for the bridge to receive the whole recording";
    }
    if (!state.id) {
      draftBox.hidden = true;
      draft = null;
    }
  }

  async function mark(m) {
    try {
      await send({ type: "record_mark", mark: m });
    } catch (err) {
      say(msg, "err", err.message);
    }
  }

  // ── controls ─────────────────────────────────────────────────────

  toggle.addEventListener("click", async () => {
    say(msg, "", "");
    toggle.disabled = true;
    try {
      if (state.recording) {
        await send({ type: "record_stop" });
      } else {
        draftBox.hidden = true;
        draft = null;
        const page = globalThis.MonoPanelPage && globalThis.MonoPanelPage.current();
        if (port) port.postMessage({ type: "record_owner" });
        await send({ type: "record_start", goal: goal.value.trim(), tabId: page && page.tabId });
      }
    } catch (err) {
      say(msg, "err", err.message);
    } finally {
      toggle.disabled = false;
    }
  });

  pick.addEventListener("change", async () => {
    try {
      await send({ type: "record_pick", on: pick.checked });
    } catch (err) {
      pick.checked = !pick.checked;
      say(msg, "err", err.message);
    }
  });

  clearBtn.addEventListener("click", async () => {
    try {
      await send({ type: "record_clear" });
      goal.value = "";
      say(msg, "", "");
    } catch (err) {
      say(msg, "err", err.message);
    }
  });

  // ── analyze → verify → save ──────────────────────────────────────

  function drawDraft(d) {
    draft = d;
    draftBox.hidden = false;
    draftName.textContent = d.action || "Untitled action";
    draftWhere.textContent = d.automation ? `${d.isNew ? "New automation" : "In"} ${d.automation}` : "";
    draftInputs.textContent = "";
    for (const i of d.inputs) draftInputs.appendChild(node("span", "chip", i.required ? i.name : `${i.name}?`));
    if (!d.inputs.length) draftInputs.appendChild(node("span", "note-line", "No inputs"));
    draftSteps.textContent = "";
    for (const s of d.steps) {
      const li = node("li", "", `${s.type}${s.detail ? ` — ${s.detail}` : ""}`);
      if (s.sideEffect) li.appendChild(node("span", "chip rec-suggest", s.sideEffect));
      draftSteps.appendChild(li);
    }
    draftLint.textContent = "";
    for (const l of d.lint) draftLint.appendChild(node("li", `rec-lint-${l.level}`, l.message));
    saveAs.value = d.saveAs || "action";
    saveName.value = d.action || "";
    saveAutomation.value = d.automation || "";
    verifySteps.hidden = true;
    say(draftMsg, "", "");
  }

  analyzeBtn.addEventListener("click", async () => {
    analyzeBtn.disabled = true;
    say(msg, "ok", "Analyzing the recording — this runs a model and can take a minute…");
    try {
      const res = await send({ type: "record_analyze", recordingId: state.id });
      drawDraft(View.describeDraft(res.result));
      say(msg, "", "");
    } catch (err) {
      say(msg, "err", `Analysis failed: ${err.message}`);
    } finally {
      analyzeBtn.disabled = !state.delivered;
    }
  });

  verifyBtn.addEventListener("click", async () => {
    if (!draft) return;
    verifyBtn.disabled = true;
    say(draftMsg, "ok", "Replaying up to the first step that changes anything…");
    try {
      const res = await send({ type: "record_verify", draftDir: draft.draftDir });
      const v = View.describeVerify(res.result);
      verifySteps.textContent = "";
      for (const s of v.steps) {
        const li = node("li", "", s.text);
        li.dataset.status = s.status;
        verifySteps.appendChild(li);
      }
      verifySteps.hidden = !v.steps.length;
      const stopped = v.stoppedAt ? " Stopped before a step with side effects." : "";
      say(draftMsg, v.ok ? "ok" : "warn", (v.ok ? "Verified." : "Some steps failed.") + stopped);
    } catch (err) {
      say(draftMsg, "err", `Verify failed: ${err.message}`);
    } finally {
      verifyBtn.disabled = false;
    }
  });

  saveBtn.addEventListener("click", async () => {
    if (!draft) return;
    saveBtn.disabled = true;
    try {
      const res = await send({
        type: "record_save",
        draftDir: draft.draftDir,
        saveAs: saveAs.value,
        name: saveName.value.trim(),
        automation: saveAutomation.value.trim(),
      });
      const r = res.result || {};
      const what = r.nodeType || [r.automation, r.action].filter(Boolean).join(".");
      say(draftMsg, "ok", `Saved${what ? ` as ${what}` : ""}${r.version ? ` (v${r.version})` : ""}.`);
    } catch (err) {
      say(draftMsg, "err", `Save failed: ${err.message}`);
    } finally {
      saveBtn.disabled = false;
    }
  });

  // ── the port to the worker ───────────────────────────────────────

  function connectPort() {
    try {
      port = chrome.runtime.connect({ name: "monoagent-record" });
    } catch {
      port = null;
      return;
    }
    port.onMessage.addListener((m) => {
      if (m && m.type === "record_state") draw(m.state);
    });
    port.onDisconnect.addListener(() => {
      // The worker was restarted. Reconnect; it re-sends its state on connect.
      port = null;
      setTimeout(connectPort, 500);
    });
  }

  connectPort();
})();
