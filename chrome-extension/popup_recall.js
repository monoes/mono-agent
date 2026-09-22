/**
 * MonoAgent Bridge — the recall panels in the popup (RCL-02, RCL-05)
 *
 * Its own file because popup.js is already the length it should be, and
 * because these two panels share nothing with the save form beyond the
 * `ask` helper and the document they both draw into. Both are plain
 * scripts, so that helper is simply in scope.
 *
 * As thin as the rest of the popup: a popup is torn down the moment it
 * loses focus, so nothing here holds state the worker does not also hold.
 * The ask panel in particular has to survive being closed mid-question —
 * the request keeps running in the worker, and reopening asks again rather
 * than trying to reattach to something that may already be gone.
 */

(function () {
  "use strict";

  const el = (id) => document.getElementById(id);

  const savedBox = el("saved");
  const savedHead = el("saved-head");
  const savedTitle = el("saved-title");
  const savedMeta = el("saved-meta");
  const savedNote = el("saved-note");
  const savedChips = el("saved-chips");
  const savedOpen = el("saved-open");
  const savedRecheck = el("saved-recheck");
  const savedHighlights = el("saved-highlights");

  const askInput = el("ask-q");
  const askBtn = el("ask-btn");
  const askStatus = el("ask-status");
  const askAnswers = el("ask-answers");

  let current = null; // the record the panel is currently showing

  // ── RCL-02: already saved ────────────────────────────────────────

  function when(iso) {
    const t = Date.parse(iso || "");
    if (Number.isNaN(t)) return "";
    const days = Math.floor((Date.now() - t) / 86400000);
    if (days <= 0) return "today";
    if (days === 1) return "yesterday";
    if (days < 30) return `${days} days ago`;
    return new Date(t).toISOString().slice(0, 10);
  }

  function chip(text) {
    const span = document.createElement("span");
    span.className = "chip";
    span.textContent = text;
    return span;
  }

  function drawSaved(record) {
    current = record;
    savedChips.textContent = "";
    savedHighlights.textContent = "";

    // Nothing known and nothing wrong: the panel is not there at all. An
    // empty box saying "not saved" is noise on every page you visit.
    if (!record || (!record.saved && !record.unavailable)) {
      savedBox.hidden = true;
      return;
    }

    savedBox.hidden = false;
    savedBox.classList.toggle("unavailable", !record.saved);

    if (!record.saved) {
      // The lookup could not be answered. Said out loud here, because the
      // absent badge on the toolbar cannot distinguish this from "no".
      savedHead.textContent = "Can't check your brain right now";
      savedTitle.textContent = "";
      savedMeta.textContent = record.reason || "";
      savedNote.textContent = "";
      savedOpen.hidden = true;
      return;
    }

    savedHead.textContent = record.versions > 1 ? `Saved ${record.versions} times` : "Already saved";
    savedTitle.textContent = record.title || record.url || "";
    savedTitle.title = record.url || "";

    const bits = [];
    if (record.capturedAt) bits.push(`captured ${when(record.capturedAt)}`);
    if (record.site) bits.push(record.site);
    if (record.collection) bits.push(`in ${record.collection}`);
    if (record.source && record.source !== "extension") bits.push(`via ${record.source}`);
    savedMeta.textContent = bits.join(" · ");

    savedNote.textContent = record.note || "";
    savedNote.hidden = !record.note;

    for (const tag of record.tags || []) savedChips.appendChild(chip(tag));

    savedOpen.hidden = !record.envelope;
  }

  async function loadSaved(force) {
    const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    if (!tab || !tab.id) return;
    if (force) savedMeta.textContent = "checking…";
    const reply = await ask({ type: "saved_get", tabId: tab.id, force: !!force });
    drawSaved(reply && reply.ok ? reply.record : null);
    drawHighlightCount();
  }

  async function drawHighlightCount() {
    const reply = await ask({ type: "highlight_list" });
    const count = reply && reply.ok ? (reply.records || []).length : 0;
    if (!count) return;
    savedBox.hidden = false;
    savedHighlights.textContent =
      count === 1 ? "1 highlight on this page" : `${count} highlights on this page`;
  }

  savedRecheck.addEventListener("click", () => loadSaved(true));

  savedOpen.addEventListener("click", async () => {
    if (!current || !current.envelope) return;
    const reply = await ask({ type: "saved_open", path: current.envelope });
    if (reply && reply.ok === false) savedMeta.textContent = reply.error || "could not open it";
  });

  // ── RCL-05: ask your brain ───────────────────────────────────────

  function drawAnswer(answer) {
    const box = document.createElement("div");
    box.className = "answer";

    const quote = document.createElement("div");
    quote.className = "quote";
    quote.textContent = `“${answer.quote}”`;
    box.appendChild(quote);

    const src = document.createElement("div");
    src.className = "src";
    const link = document.createElement("a");
    // The cite URL carries a text fragment, so the link lands on the
    // passage itself rather than the top of the page.
    link.href = answer.citeUrl || answer.url || "#";
    link.textContent = answer.title || answer.site || answer.url || "source";
    link.title = answer.citeUrl || answer.url || "";
    link.target = "_blank";
    src.appendChild(link);

    const bits = [];
    if (answer.site) bits.push(answer.site);
    if (answer.capturedAt) bits.push(when(answer.capturedAt));
    if (bits.length) src.appendChild(document.createTextNode(` — ${bits.join(" · ")}`));

    if (answer.stale) {
      // Never hidden: the page has been re-captured since these offsets
      // were measured, so the passage may have moved.
      const warn = document.createElement("span");
      warn.className = "stale";
      warn.textContent = " · older version";
      src.appendChild(warn);
    }
    box.appendChild(src);
    return box;
  }

  async function runAsk() {
    const q = askInput.value.trim();
    if (!q) return;
    askAnswers.textContent = "";
    askStatus.textContent = "asking…";
    askBtn.disabled = true;

    const reply = await ask({ type: "ask_brain", q, limit: 4 });
    askBtn.disabled = false;

    if (!reply || reply.ok === false) {
      // "unavailable" is an absence, not a failure: monomind is not
      // installed, or the bridge is down. Said plainly either way.
      askStatus.textContent = reply && reply.error ? reply.error : "no answer";
      return;
    }
    const answers = (reply.answer && reply.answer.answers) || [];
    if (!answers.length) {
      askStatus.textContent = "Nothing in your captures matches that yet.";
      return;
    }
    askStatus.textContent = answers.length === 1 ? "1 passage" : `${answers.length} passages`;
    for (const answer of answers) askAnswers.appendChild(drawAnswer(answer));
    for (const warning of (reply.answer && reply.answer.warnings) || []) {
      const note = document.createElement("div");
      note.className = "ask-status";
      note.textContent = warning;
      askAnswers.appendChild(note);
    }
  }

  askBtn.addEventListener("click", runAsk);
  askInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") runAsk();
  });

  // The worker relays the backend's progress frames here, so a slow ask
  // says what it is doing instead of looking frozen.
  chrome.runtime.onMessage.addListener((msg) => {
    if (!msg || msg.type !== "ask_progress" || !msg.progress) return;
    const { stage, detail } = msg.progress;
    askStatus.textContent = detail ? `${stage} — ${detail}` : `${stage}…`;
  });

  loadSaved(false);
})();
