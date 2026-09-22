/**
 * MonoAgent Bridge — the recall parts of the side panel (RCL-02, RCL-05)
 *
 * Its own file because sidepanel.js is already the length it should be,
 * and because these two parts share nothing with the save form beyond the
 * `ask` helper and the document they both draw into. Both are plain
 * scripts, so that helper is simply in scope.
 *
 * As thin as the rest of the panel: nothing here holds state the worker
 * does not also hold. The ask section has to survive being closed
 * mid-question — the request keeps running in the worker, and reopening
 * asks again rather than trying to reattach to something that may already
 * be gone.
 *
 * "Already saved" is about the page in front of the person, and in a panel
 * that page changes underneath it. sidepanel.js announces each new page
 * (`panel:page`); every lookup is stamped with the page it was for, and an
 * answer that comes back after the person has moved on is dropped rather
 * than drawn over the page they are actually looking at.
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
  let asking = ""; // the page key the latest lookup was for

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
    // The card already names the page just above. The saved title is only
    // worth repeating when it differs — a page retitled since it was saved.
    const page = pageNow();
    const title = record.title || record.url || "";
    savedTitle.textContent = page && page.title === title ? "" : title;
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

  const pageNow = () => (globalThis.MonoPanelPage ? globalThis.MonoPanelPage.current() : null);

  async function loadSaved(force) {
    const page = pageNow();
    const key = page ? page.key : "";
    asking = key;

    // A page that could never have been saved (a new tab, settings) has
    // nothing to look up. Whatever was drawn belonged to the last page.
    if (!page || !page.tabId || !page.capturable) {
      drawSaved(null);
      return;
    }
    if (force) savedMeta.textContent = "checking…";
    else drawSaved(null);

    const [reply, highlights] = await Promise.all([
      ask({ type: "saved_get", tabId: page.tabId, force: !!force }),
      // The page's URL is sent because the panel is not a tab: the worker
      // cannot read it off the sender, as it does for a content script.
      ask({ type: "highlight_list", url: page.url }),
    ]);
    if (asking !== key) return; // the person has moved on; this is stale

    drawSaved(reply && reply.ok ? reply.record : null);
    drawHighlightCount(highlights);
  }

  function drawHighlightCount(reply) {
    const count = reply && reply.ok ? (reply.records || []).length : 0;
    if (!count) return;
    if (!current || (!current.saved && !current.unavailable)) {
      // Highlights with no capture behind them. The band is shown for the
      // highlights, so its heading must not claim the page is saved.
      savedBox.classList.add("unavailable");
      savedHead.textContent = "Not saved yet";
      savedTitle.textContent = "";
      savedMeta.textContent = "";
      savedNote.textContent = "";
      savedOpen.hidden = true;
    }
    savedBox.hidden = false;
    savedHighlights.textContent =
      count === 1 ? "1 highlight on this page" : `${count} highlights on this page`;
  }

  savedRecheck.addEventListener("click", () => loadSaved(true));
  document.addEventListener("panel:page", () => loadSaved(false));
  document.addEventListener("panel:recheck", () => loadSaved(true));

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

})();
