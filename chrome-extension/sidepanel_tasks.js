/**
 * MonoAgent Bridge - "Add a task" in the side panel (task board spec 11.1)
 *
 * A box, and a button that names where the task goes ("Add to Work"): the
 * profile the header's "Saving into" picker shows. A task always sits in a
 * profile, so with the shared inbox chosen the button says "Choose a
 * profile first" and adds nothing. The worker queues and sends the task
 * (task_bridge.js); this file only shows what the worker says happened, and
 * the tasks MonoAgent refused, with the latest one's text to copy.
 *
 * After sidepanel.js: it uses that file's `ask` helper and `profiles`, and
 * redraws on the `panel:profiles` event sidepanel.js sends when the header
 * changes. The add-task shortcut opens the panel on this box by writing
 * taskFocusAt to session storage (task_menu.js).
 */

(function () {
  "use strict";

  const el = (id) => document.getElementById(id);
  const View = globalThis.MonoPanelView;
  const FOCUS_KEY = "taskFocusAt";
  // A request to focus older than this was for some earlier opening.
  const FOCUS_FRESH_MS = 10000;

  const panel = el("task-panel");
  const box = el("task-text");
  const addBtn = el("task-add");
  const msg = el("task-msg");
  const failed = el("task-failed");
  const failedText = el("task-failed-text");
  const failedBody = el("task-failed-body");
  const dismissBtn = el("task-dismiss");

  let busy = false;

  /** The profile the header shows, as sidepanel.js last drew it. */
  function current() {
    return typeof profiles !== "undefined" && profiles ? profiles.current : null;
  }

  function drawButton() {
    const label = View.taskButton(current());
    addBtn.textContent = label.text;
    addBtn.disabled = busy || !label.enabled;
  }

  /** showResult draws the worker's one line: added #N, waiting to sync, or why not. */
  function showResult(feedback) {
    if (!feedback || !feedback.text) return;
    msg.dataset.kind = feedback.level === "error" ? "err" : feedback.level;
    msg.textContent = feedback.text;
  }

  async function drawFailures() {
    const state = await ask({ type: "task_state" });
    const failures = (state && state.failures) || [];
    failed.hidden = !failures.length;
    failedText.textContent = View.taskFailures(failures);
    failedBody.value = failures.length ? failures[failures.length - 1].text || "" : "";
  }

  async function submit() {
    const target = current();
    if (busy || !target || !target.id) return;
    const text = box.value.trim();
    if (!text) {
      showResult({ level: "error", text: "Type a task first" });
      box.focus();
      return;
    }
    busy = true;
    drawButton();
    try {
      const reply = await ask({ type: "task_add", text, profile: target.id });
      showResult(reply.feedback || { level: "error", text: `Not added: ${reply.error || "no answer from the extension"}` });
      if (reply.status === "added" || reply.status === "queued") box.value = "";
    } finally {
      busy = false;
      drawButton();
      drawFailures();
    }
  }

  addBtn.addEventListener("click", submit);
  box.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      submit();
    }
  });
  dismissBtn.addEventListener("click", async () => {
    await ask({ type: "task_dismiss" });
    drawFailures();
  });

  // The header's profile changed here, or in another window's panel.
  document.addEventListener("panel:profiles", drawButton);

  // A task added from a page or the menu while this panel is open.
  chrome.runtime.onMessage.addListener((message) => {
    if (message && message.type === "task_result") {
      showResult(message.feedback);
      drawFailures();
    }
    return false;
  });

  /** focusIfAsked opens this section on its box when the shortcut just asked for it. */
  function focusIfAsked(at) {
    if (typeof at !== "number" || Date.now() - at > FOCUS_FRESH_MS) return;
    panel.open = true;
    box.focus();
  }
  chrome.storage.onChanged.addListener((changes, area) => {
    if (area === "session" && changes[FOCUS_KEY]) focusIfAsked(changes[FOCUS_KEY].newValue);
  });
  if (chrome.storage.session) {
    chrome.storage.session.get(FOCUS_KEY).then((got) => focusIfAsked(got && got[FOCUS_KEY]), () => {});
  }

  drawButton();
  drawFailures();
})();
