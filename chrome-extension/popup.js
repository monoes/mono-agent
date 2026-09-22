/**
 * MonoAgent Bridge — Popup Script
 *
 * Connection status, the save form (CLIP-07), batch capture (CLIP-06) and
 * the pending-capture list (CLIP-08). Deliberately thin: a popup is torn
 * down the instant it loses focus, so it asks the service worker to do
 * everything and only draws the answers. Anything here that looked like
 * state would be lost mid-capture.
 *
 * The one piece of judgement it does keep is in popup_status.js, which
 * turns a status word into a sentence and an action. This file only draws
 * what that decides, so the wording and the state machine can be tested
 * without a DOM.
 *
 * Non-loopback servers are rejected unless the (unsafe, session-only)
 * override checkbox is enabled at save time.
 */

const $ = (id) => document.getElementById(id);

const Status = globalThis.MonoPopupStatus;

const statusPill = $("status-pill");
const statusLabel = $("status-label");
const notice = $("notice");
const noticeTitle = $("notice-title");
const noticeBody = $("notice-body");
const noticeCommandRow = $("notice-command-row");
const noticeCommand = $("notice-command");
const noticeCopy = $("notice-copy");
const noticeActions = $("notice-actions");
const noticeAction = $("notice-action");

const wsUrlInput = $("ws-url");
const saveBtn = $("save-btn");
const wsSavedMsg = $("ws-saved-msg");
const wsErrorMsg = $("ws-error-msg");
const allowRemoteCheckbox = $("allow-remote");
const pairingTokenInput = $("pairing-token");
const pairBtn = $("pair-btn");
const pairSavedMsg = $("pair-saved-msg");
const settingsPanel = $("settings-panel");

const captureBtn = $("capture-btn");
const captureMsg = $("capture-msg");
const hintQueues = $("hint-queues");
const hintShortcut = $("hint-shortcut");
const noteInput = $("note");
const tagsInput = $("tags");
const collectionInput = $("collection");
const collectionList = $("collection-list");
const profileField = $("profile-field");
const profileSelect = $("profile");
const profileNote = $("profile-note");
const tagChips = $("tag-chips");

const batchWindowBtn = $("batch-window");
const batchGroupBtn = $("batch-group");
const batchCancelBtn = $("batch-cancel");
const batchProgress = $("batch-progress");
const batchLabel = $("batch-label");
const batchMsg = $("batch-msg");

const queuePanel = $("queue-panel");
const queueList = $("queue-list");
const queueCounts = $("queue-counts");
const queueClearBtn = $("queue-clear");
const queueRetryAllBtn = $("queue-retry-all");

// --- connection status -----------------------------------------------------

/**
 * What the popup knows about the connection. `since` is stamped here rather
 * than taken on faith from the worker, because the worker does not send one
 * today — and the grace period that stops a service-worker respawn flashing
 * red needs to know how long this state has been true. A status that has
 * not changed keeps its original stamp.
 */
const connection = {
  status: "",
  reason: "",
  detail: "",
  since: Date.now(),
  everConnected: false,
  wsUrl: "",
};

/** The last thing describe() returned, so the action button knows its job. */
let shown = null;

/**
 * Whether the worker has pushed a status since the popup opened. A push is
 * a real event — the socket genuinely changed — so it outranks the health
 * probe's answer, which is only ever a snapshot taken at open. Without this
 * a slow probe could land after a successful connection and talk the UI
 * back down to "Bridge ready".
 */
let livePush = false;

/**
 * noteStatus records a status update, preserving `since` across repeats of
 * the same state so that a worker re-broadcasting "disconnected" every few
 * seconds cannot hold the UI in its grace period forever.
 */
function noteStatus(update) {
  const next = update || {};
  const status = String(next.status || "");
  const reason = String(next.reason || "");
  const changed = status !== connection.status || reason !== connection.reason;

  connection.status = status;
  connection.reason = reason;
  connection.detail = next.detail || "";
  if (typeof next.since === "number" && next.since > 0) {
    connection.since = next.since;
  } else if (changed) {
    connection.since = Date.now();
  }
  if (next.wsUrl) connection.wsUrl = next.wsUrl;
  if (status === "connected") connection.everConnected = true;

  drawStatus();
}

function drawStatus() {
  const view = Status.describe({
    status: connection.status,
    reason: connection.reason,
    detail: connection.detail,
    since: connection.since,
    everConnected: connection.everConnected,
    wsUrl: connection.wsUrl || wsUrlInput.value,
  });
  shown = view;

  statusPill.dataset.tone = view.tone;
  statusPill.dataset.busy = String(view.busy);
  statusLabel.textContent = view.label;

  notice.dataset.open = String(!!view.title);
  notice.dataset.tone = view.tone === "busy" || view.tone === "ok" ? "idle" : view.tone;
  noticeTitle.textContent = view.title;
  noticeBody.textContent = view.body;
  noticeBody.hidden = !view.body;

  noticeCommand.textContent = view.command;
  noticeCommandRow.hidden = !view.command;
  noticeCopy.setAttribute("aria-label", `Copy the command ${view.command}`);

  // The button is hidden as well as its row: a button with no label is a
  // control our own accessibility checker is right to complain about, and
  // leaving it in the tree to be skipped by a wrapper is not an answer.
  noticeActions.hidden = !view.action;
  noticeAction.hidden = !view.action;
  if (view.action) {
    noticeAction.textContent = view.action.label;
    noticeAction.dataset.action = view.action.id;
  }

  // The button never lies about what it is about to do. A capture with no
  // bridge to send it to still succeeds — it is kept and sent later — and
  // saying so here is the difference between a queue and a surprise.
  hintQueues.hidden = !view.queues;
  hintShortcut.hidden = view.queues;
}

/**
 * The grace period expires on a timer as well as on the next event: with no
 * further status broadcast, a socket that never came back would otherwise
 * sit on "Reconnecting…" for as long as the popup stayed open.
 */
setInterval(() => {
  if (shown && shown.key === "reconnecting") drawStatus();
}, 1000);

noticeCopy.addEventListener("click", async () => {
  if (!shown || !shown.command) return;
  try {
    await navigator.clipboard.writeText(shown.command);
    noticeCopy.textContent = "Copied";
    setTimeout(() => {
      noticeCopy.textContent = "Copy";
    }, 1600);
  } catch {
    // Clipboard refused (no permission, no focus). Select it instead so the
    // keyboard can finish the job — never a dead button.
    const range = document.createRange();
    range.selectNodeContents(noticeCommand);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    noticeCopy.textContent = "Press ⌘/Ctrl+C";
  }
});

noticeAction.addEventListener("click", async () => {
  const which = noticeAction.dataset.action;
  if (which === Status.ACTIONS.PAIR) {
    settingsPanel.open = true;
    pairingTokenInput.focus();
    pairingTokenInput.scrollIntoView({ block: "nearest" });
    return;
  }
  if (which === Status.ACTIONS.SETTINGS) {
    settingsPanel.open = true;
    wsUrlInput.focus();
    wsUrlInput.scrollIntoView({ block: "nearest" });
    return;
  }
  if (which === Status.ACTIONS.RETRY) {
    // Re-saving the address we already have is how the popup asks the
    // worker to drop the socket and dial again, using only the message the
    // worker already answers.
    noteStatus({ status: "connecting" });
    await ask({ type: "set_ws_url", url: wsUrlInput.value.trim() || Status.DEFAULT_WS_URL });
  }
});

function showError(message) {
  hide(wsSavedMsg);
  wsErrorMsg.textContent = message;
  wsErrorMsg.dataset.kind = "err";
}

function show(el, kind, text) {
  if (!text) {
    hide(el);
    return;
  }
  el.textContent = text;
  el.dataset.kind = kind;
}

function hide(el) {
  delete el.dataset.kind;
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

// The form always carries `profile`, including the empty string for "no
// profile": the worker treats its presence as this save's choice and makes
// it the sticky one, so the picker means the same thing whether it was
// touched or left where it was.
const form = () => ({
  note: noteInput.value,
  tags: tagsInput.value,
  collection: collectionInput.value,
  profile: profileSelect.value || "",
});

/**
 * drawProfiles fills the "Save into" picker. It is hidden outright when
 * there is nothing to choose between — a single-profile install should not
 * grow a control that can only be set one way — and the choice is only ever
 * pre-selected, never forced: the capture saves whatever is showing.
 */
function drawProfiles(state) {
  const profiles = state.profiles || [];
  profileSelect.textContent = "";
  if (!profiles.length) {
    profileField.hidden = true;
    return;
  }
  profileField.hidden = false;

  for (const profile of profiles) {
    const option = document.createElement("option");
    option.value = profile.id;
    option.textContent = profile.default ? `${profile.name} (default)` : profile.name;
    profileSelect.appendChild(option);
  }
  const none = document.createElement("option");
  none.value = "";
  none.textContent = "No profile";
  none.title = "Save to the shared inbox, as captures were before profiles";
  profileSelect.appendChild(none);

  profileSelect.value = state.profile || "";

  // Two things are worth saying out loud, and nothing else is: the profile
  // last saved into has been deleted, and the bridge could not be asked so
  // this list may be stale.
  const message = state.profileChanged
    ? state.profileReason
    : state.profilesOffline
    ? "monoagent is not connected — this list is the last one it gave."
    : "";
  profileNote.textContent = message;
  profileNote.hidden = !message;
}

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

// The picker starts the snapshot like the other fields, but deliberately
// does not save on Enter: Enter is how a keyboard user commits a choice in
// a <select>, and it must not also commit the capture.
profileSelect.addEventListener("focus", beginEarly, { once: true });

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
    // Eight buttons that a screen reader reads as eight bare words are
    // eight guesses. The label says what pressing one does.
    chip.setAttribute("aria-label", `Add the tag ${tag}`);
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
    queuePanel.hidden = true;
    queuePanel.open = false;
    return;
  }
  queuePanel.hidden = false;

  const counts = state.counts || { queued: 0, failed: 0 };
  const summary = Status.describeQueue(counts);
  if (summary) {
    queueCounts.textContent = summary.text;
    queueCounts.dataset.tone = summary.tone;
    // Captures that could not be sent are the only thing in this popup that
    // will not resolve itself, so that is the one case that opens its own
    // drawer. A queue merely waiting on a reconnect does not.
    if (summary.failed) queuePanel.open = true;
  }
  queueClearBtn.hidden = !counts.failed;

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
    retry.type = "button";
    retry.className = "btn-secondary btn-tiny";
    retry.textContent = "Retry now";
    // Every row has a "Retry now" and a "Delete". Read aloud in a list they
    // are indistinguishable, so each one names the capture it acts on.
    retry.setAttribute("aria-label", `Retry sending ${item.title}`);
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
    del.type = "button";
    del.className = "btn-secondary btn-tiny";
    del.textContent = "Delete";
    del.title = "Discard this capture without sending it";
    del.setAttribute("aria-label", `Discard ${item.title} without sending it`);
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

queueRetryAllBtn.addEventListener("click", async () => {
  queueRetryAllBtn.disabled = true;
  const state = await ask({ type: "queue_state" });
  const items = (state.queued || []).concat(state.failed || []);
  for (const item of items) {
    await ask({ type: "queue_retry", key: item.key });
  }
  queueRetryAllBtn.disabled = false;
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
    showCapture("warn", `${title} — waiting for the bridge`);
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
  batchProgress.dataset.running = String(running);
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
    livePush = true;
    noteStatus(msg);
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
  drawProfiles(state);
  tagsInput.addEventListener("input", () => drawTagSuggestions(state.recentTags || []));
  collectionList.textContent = "";
  for (const name of state.collections || []) {
    const option = document.createElement("option");
    option.value = name;
    collectionList.appendChild(option);
  }
  drawQueue(state);
}

/** Set once the health probe has answered, either way. */
let probed = false;

/**
 * checkHealth asks the bridge directly whether it is running, and is the
 * authoritative answer to the only question this popup's status exists to
 * answer. It cannot fail: a probe that finds nothing IS the "no bridge"
 * result, which is the most common state and has to feel instant, so there
 * is no error path and nothing to wait on.
 */
async function checkHealth(stored) {
  const health = await MonoBridgeHealth.probe({ known: stored });
  probed = true;
  // A real socket event that has already arrived is newer than this
  // snapshot and wins; see `livePush`.
  if (livePush) return;
  noteStatus(MonoBridgeHealth.toStatus(health));
}

async function init() {
  // The non-loopback override is session-scoped and must be re-enabled
  // each time — reset it whenever the popup opens.
  try {
    await chrome.storage.session.set({ allowNonLoopback: false });
  } catch {
    // storage unavailable — background treats a missing flag as no override
  }

  drawStatus();

  chrome.runtime.sendMessage({ type: "get_status" }, (response) => {
    // Only a stand-in until the health probe answers, and never allowed to
    // overrule it: the worker can only report what its own socket did,
    // which is exactly the inference the probe exists to replace.
    if (probed) return;
    if (chrome.runtime.lastError || !response?.status) {
      // No answer at all means the worker is asleep or gone, which is not
      // the same as a bridge that refused us — and it is about to wake up
      // and tell us properly. Hold the calm state rather than guessing.
      noteStatus({ status: "connecting" });
      return;
    }
    noteStatus(response);
  });

  const stored = await chrome.storage.local.get(["wsUrl", "pairedWsUrl", "workingWsUrl"]);
  wsUrlInput.value = stored.wsUrl || Status.DEFAULT_WS_URL;
  connection.wsUrl = wsUrlInput.value;

  // Ask the bridge itself, rather than asking our own socket about it. This
  // is what makes "no bridge is running" a statement instead of a guess —
  // and it is deliberately not awaited before the form loads, so the popup
  // is never waiting on the network to become usable.
  checkHealth(stored);

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
  if (!value) {
    pairingTokenInput.focus();
    showError("Paste the token printed by: " + Status.PAIR_COMMAND);
    return;
  }

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
    hide(wsErrorMsg);
    pairingTokenInput.value = "";
    show(pairSavedMsg, "ok", "Saved — reconnecting…");
    noteStatus({ status: "connecting" });
    setTimeout(() => hide(pairSavedMsg), 2000);
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
    hide(wsErrorMsg);
    connection.wsUrl = url;
    show(wsSavedMsg, "ok", "Saved — reconnecting…");
    noteStatus({ status: "connecting" });
    setTimeout(() => hide(wsSavedMsg), 2000);
  });
});

init();
