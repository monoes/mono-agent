/**
 * MonoAgent Bridge — Popup Script
 *
 * Displays connection status and allows configuring the WebSocket URL.
 * Non-loopback servers are rejected unless the (unsafe, session-only)
 * override checkbox is enabled at save time.
 */

const dot = document.getElementById("dot");
const statusText = document.getElementById("status-text");
const wsUrlInput = document.getElementById("ws-url");
const saveBtn = document.getElementById("save-btn");
const savedMsg = document.getElementById("saved-msg");
const errorMsg = document.getElementById("error-msg");
const allowRemoteCheckbox = document.getElementById("allow-remote");

const STATUS_LABELS = {
  connected: "Connected",
  disconnected: "Disconnected",
  connecting: "Connecting...",
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

// Load current status and saved URL on popup open
async function init() {
  // The non-loopback override is session-scoped and must be re-enabled
  // each time — reset it whenever the popup opens.
  try {
    await chrome.storage.session.set({ allowNonLoopback: false });
  } catch {
    // storage unavailable — background treats a missing flag as no override
  }

  // Get connection status from background
  chrome.runtime.sendMessage({ type: "get_status" }, (response) => {
    if (chrome.runtime.lastError || !response?.status) {
      updateUI("disconnected");
      return;
    }
    updateUI(response.status);
  });

  // Load saved URL
  const result = await chrome.storage.local.get("wsUrl");
  wsUrlInput.value = result.wsUrl || "ws://127.0.0.1:9222/monoagent";
}

// Listen for live status updates from background
chrome.runtime.onMessage.addListener((msg) => {
  if (msg.type === "status") {
    updateUI(msg.status);
  }
});

// Save button handler
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
