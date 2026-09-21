/**
 * MonoAgent Bridge — Popup Script
 *
 * Connection status, the save form (CLIP-07), batch capture (CLIP-06) and
 * the pending-capture list (CLIP-08). Deliberately thin: a popup is torn
 * down the instant it loses focus, so it asks the service worker to do
 * everything and only draws the answers. Anything here that looked like
 * state would be lost mid-capture.
 *
 * Non-loopback servers are rejected unless the (unsafe, session-only)
 * override checkbox is enabled at save time.
 */

const $ = (id) => document.getElementById(id);

const dot = $("dot");
const statusText = $("status-text");
const wsUrlInput = $("ws-url");
const saveBtn = $("save-btn");
const savedMsg = $("saved-msg");
const errorMsg = $("error-msg");
const allowRemoteCheckbox = $("allow-remote");
const pairingTokenInput = $("pairing-token");
const pairBtn = $("pair-btn");
const pairSavedMsg = $("pair-saved-msg");
const captureBtn = $("capture-btn");
const captureMsg = $("capture-msg");
const noteInput = $("note");
const tagsInput = $("tags");
const collectionInput = $("collection");
const collectionList = $("collection-list");
const tagChips = $("tag-chips");
const batchWindowBtn = $("batch-window");
const batchGroupBtn = $("batch-group");
const batchCancelBtn = $("batch-cancel");
const batchProgress = $("batch-progress");
const batchLabel = $("batch-label");
const batchMsg = $("batch-msg");
const queueBox = $("queue");
const queueList = $("queue-list");
const queueCounts = $("queue-counts");
const queueClearBtn = $("queue-clear");

const STATUS_LABELS = {
  connected: "Connected",
  disconnected: "Disconnected",
  connecting: "Connecting...",
  unpaired: "Needs pairing",
};

function updateUI(status) {
  dot.className = `dot ${status}`;
  statusText.textContent = STATUS_LABELS[status] || status;
}

function showError(message) {
  savedMsg.style.display = "none";
  errorMsg.textContent = message;
  errorMsg.style.display = "block";
}

function show(el, kind, text) {
  el.className = `capture-msg ${kind}`;
  el.textContent = text;
  el.style.display = text ? "block" : "none";
}

const showCapture = (kind, text) => show(captureMsg, kind, text);
const showBatch = (kind, text) => show(batchMsg, kind, text);

/** ask sends one message to the worker and resolves with its answer. */
function ask(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (response) => {
      if (chrome.runtime.lastError) {
        resolve({ ok: false, error: chrome.runtime.lastError.message });
        return;
      }
      resolve(response || { ok: false, error: "no answer from the extension" });
    });
  });
}

const form = () => ({
  note: noteInput.value,
  tags: tagsInput.value,
  collection: collectionInput.value,
});

// --- CLIP-07: the snapshot starts while the note is still being typed -----

let began = false;

/**
 * beginEarly asks the worker to photograph the page now. The page is only
 * going to get further from what the person is looking at, and the note
 * field is about to hold their attention for a while.
 */
function beginEarly() {
  if (began) return;
  began = true;
  ask({ type: "capture_begin" });
}

for (const field of [noteInput, tagsInput, collectionInput]) {
  field.addEventListener("focus", beginEarly, { once: true });
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter") captureBtn.click();
  });
}

function drawTagSuggestions(recent) {
  tagChips.textContent = "";
  const chosen = tagsInput.value;
  const query = chosen.split(/[,\s]+/).pop() || "";
  const suggestions = MonoSuggest(recent, query, chosen);
  for (const tag of suggestions) {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "chip";
    chip.textContent = tag;
    chip.addEventListener("click", () => {
      const parts = tagsInput.value.split(/,\s*/).filter(Boolean);
      // Replace the fragment being typed, if that is what the chip matched.
      if (query && !tagsInput.value.endsWith(", ") && parts.length) parts.pop();
      parts.push(tag);
      tagsInput.value = `${parts.join(", ")}, `;
      tagsInput.focus();
      drawTagSuggestions(recent);
    });
    tagChips.appendChild(chip);
  }
}

/**
 * MonoSuggest is the popup's copy of the ranking rule. The worker owns the
 * canonical one (capture_form.js); a popup cannot importScripts, and this
 * is small enough that duplicating it beats a message round-trip per
 * keystroke.
 */
function MonoSuggest(recent, query, chosen) {
  const taken = new Set(
    String(chosen || "")
      .split(/[,\s]+/)
      .map((t) => t.replace(/^#+/, "").toLowerCase())
      .filter(Boolean)
  );
  const q = String(query || "").replace(/^#+/, "").trim().toLowerCase();
  const prefix = [];
  const contains = [];
  for (const tag of recent || []) {
    const key = String(tag).toLowerCase();
    if (!key || taken.has(key)) continue;
    if (!q || key.startsWith(q)) prefix.push(tag);
    else if (key.includes(q)) contains.push(tag);
  }
  return prefix.concat(contains).slice(0, 8);
}

// --- CLIP-08: the pending list --------------------------------------------

function drawQueue(state) {
  const items = (state.queued || []).concat(state.failed || []);
  queueList.textContent = "";
  if (!items.length) {
    queueBox.style.display = "none";
    return;
  }
  queueBox.style.display = "block";

  const counts = state.counts || { queued: 0, failed: 0 };
  const parts = [];
  if (counts.queued) parts.push(`${counts.queued} waiting`);
  if (counts.failed) parts.push(`${counts.failed} failed`);
  queueCounts.textContent = parts.join(" · ");
  queueClearBtn.style.display = counts.failed ? "inline-block" : "none";

  for (const item of items) {
    const li = document.createElement("li");
    li.className = `queue-item ${item.status}`;

    const title = document.createElement("div");
    title.className = "title";
    title.textContent = item.title;
    title.title = item.url || "";
    li.appendChild(title);

    const why = document.createElement("div");
    why.className = "why";
    why.textContent =
      item.status === "failed"
        ? `Failed: ${item.reason || "no reason recorded"}`
        : item.reason
        ? `Waiting — last attempt: ${item.reason}`
        : "Waiting for the bridge to reconnect";
    li.appendChild(why);

    const actions = document.createElement("div");
    actions.className = "actions";
    const retry = document.createElement("button");
    retry.className = "secondary tiny";
    retry.textContent = "Retry now";
    retry.addEventListener("click", async () => {
      retry.disabled = true;
      const result = await ask({ type: "queue_retry", key: item.key });
      if (!result.ok) {
        why.textContent = `Could not send: ${result.reason || result.error}`;
        retry.disabled = false;
      }
      await refreshQueue();
    });
    const del = document.createElement("button");
    del.className = "secondary tiny";
    del.textContent = "Delete";
    del.title = "Discard this capture without sending it";
    del.addEventListener("click", async () => {
      await ask({ type: "queue_delete", key: item.key });
      await refreshQueue();
    });
    actions.appendChild(retry);
    actions.appendChild(del);
    li.appendChild(actions);

    queueList.appendChild(li);
  }
}

async function refreshQueue() {
  drawQueue(await ask({ type: "queue_state" }));
}

queueClearBtn.addEventListener("click", async () => {
  await ask({ type: "queue_clear_failures" });
  await refreshQueue();
});

// --- saving ---------------------------------------------------------------

captureBtn.addEventListener("click", async () => {
  captureBtn.disabled = true;
  showCapture("ok", "Capturing...");

  const result = await ask({ type: "capture_commit", form: form() });
  captureBtn.disabled = false;
  began = false;

  if (!result || result.ok === false) {
    showCapture("err", result?.error || result?.warnings?.join("; ") || "Capture failed");
    await refreshQueue();
    return;
  }

  const title = result.title ? `Saved: ${result.title}` : "Saved";
  if (result.queued) {
    showCapture("warn", `${title} — queued until the bridge reconnects`);
  } else if (result.warnings?.length) {
    showCapture("warn", `${title} — ${result.warnings.join("; ")}`);
  } else {
    showCapture("ok", title);
  }
  noteInput.value = "";
  tagsInput.value = "";
  await Promise.all([refreshQueue(), loadFormState()]);
});

// --- CLIP-06: batch capture -----------------------------------------------

function batchRunning(running) {
  batchWindowBtn.disabled = running;
  batchGroupBtn.disabled = running;
  batchProgress.style.display = running ? "flex" : "none";
  batchCancelBtn.disabled = false;
}

async function startBatch(scope) {
  batchRunning(true);
  batchLabel.textContent = "Starting...";
  showBatch("ok", "");

  const result = await ask({ type: "capture_batch", scope, form: form() });
  batchRunning(false);

  if (!result || result.ok === false) {
    showBatch("err", result?.error || "Batch capture failed");
  } else {
    const report = result.report || {};
    showBatch(report.failed?.length || report.skipped?.length ? "warn" : "ok", result.summary);
    drawSkips(report);
  }
  await Promise.all([refreshQueue(), loadFormState()]);
}

/** drawSkips names every tab that was not captured — a skip is never silent. */
function drawSkips(report) {
  const skipped = (report.skipped || []).concat(report.failed || []);
  if (!skipped.length) return;
  const details = document.createElement("details");
  const summary = document.createElement("summary");
  summary.textContent = `${skipped.length} not saved`;
  details.appendChild(summary);
  for (const item of skipped) {
    const line = document.createElement("div");
    line.textContent = `${item.title} — ${item.reason}`;
    details.appendChild(line);
  }
  batchMsg.appendChild(details);
}

batchWindowBtn.addEventListener("click", () => startBatch("window"));
batchGroupBtn.addEventListener("click", () => startBatch("group"));
batchCancelBtn.addEventListener("click", async () => {
  batchCancelBtn.disabled = true;
  batchLabel.textContent = "Cancelling after this tab...";
  await ask({ type: "capture_batch_cancel" });
});

// --- live messages from the worker ----------------------------------------

chrome.runtime.onMessage.addListener((msg) => {
  if (msg.type === "status") {
    updateUI(msg.status);
    return;
  }
  if (msg.type === "capture_batch_progress") {
    const s = msg.state || {};
    batchRunning(s.phase !== "done");
    batchLabel.textContent =
      s.phase === "done"
        ? `${s.done} of ${s.total} saved`
        : `${s.completed} of ${s.total}${s.title ? ` — ${s.title}` : ""}`;
  }
});

// --- startup --------------------------------------------------------------

async function loadFormState() {
  const state = await ask({ type: "capture_form_state" });
  if (!state || state.ok === false) return;
  drawTagSuggestions(state.recentTags || []);
  tagsInput.addEventListener("input", () => drawTagSuggestions(state.recentTags || []));
  collectionList.textContent = "";
  for (const name of state.collections || []) {
    const option = document.createElement("option");
    option.value = name;
    collectionList.appendChild(option);
  }
  drawQueue(state);
}

async function init() {
  // The non-loopback override is session-scoped and must be re-enabled
  // each time — reset it whenever the popup opens.
  try {
    await chrome.storage.session.set({ allowNonLoopback: false });
  } catch {
    // storage unavailable — background treats a missing flag as no override
  }

  chrome.runtime.sendMessage({ type: "get_status" }, (response) => {
    if (chrome.runtime.lastError || !response?.status) {
      updateUI("disconnected");
      return;
    }
    updateUI(response.status);
  });

  const result = await chrome.storage.local.get("wsUrl");
  wsUrlInput.value = result.wsUrl || "ws://127.0.0.1:9222/monoagent";

  await loadFormState();
}

// A capture begun for a note that was never saved is dropped rather than
// left pinned in the worker's memory. Nothing was promised: only a
// committed capture is a capture.
window.addEventListener("pagehide", () => {
  if (began) chrome.runtime.sendMessage({ type: "capture_discard" });
});

// --- connection settings ---------------------------------------------------

pairBtn.addEventListener("click", async () => {
  const value = pairingTokenInput.value.trim();
  if (!value) return;

  // Sends the token typed into the field, never reads it back out of
  // storage, so the popup never displays a previously-saved secret.
  chrome.runtime.sendMessage({ type: "set_pairing_token", value }, (response) => {
    if (chrome.runtime.lastError) {
      showError(chrome.runtime.lastError.message);
      return;
    }
    if (!response || response.ok === false) {
      showError(response?.error || "Failed to save pairing token");
      return;
    }
    errorMsg.style.display = "none";
    pairingTokenInput.value = "";
    pairSavedMsg.style.display = "block";
    updateUI("connecting");
    setTimeout(() => {
      pairSavedMsg.style.display = "none";
    }, 2000);
  });
});

saveBtn.addEventListener("click", async () => {
  const url = wsUrlInput.value.trim();
  if (!url) return;

  // Session-only override flag — mirrors the checkbox, never persisted to
  // local storage, and cleared again when the popup reopens.
  try {
    await chrome.storage.session.set({ allowNonLoopback: allowRemoteCheckbox.checked });
  } catch {
    // ignore — background will treat as no override
  }

  chrome.runtime.sendMessage({ type: "set_ws_url", url }, (response) => {
    if (chrome.runtime.lastError) {
      showError(chrome.runtime.lastError.message);
      return;
    }
    if (!response || response.ok === false) {
      showError(response?.error || "Failed to save URL");
      return;
    }
    errorMsg.style.display = "none";
    savedMsg.style.display = "block";
    updateUI("connecting");
    setTimeout(() => {
      savedMsg.style.display = "none";
    }, 2000);
  });
});

init();
