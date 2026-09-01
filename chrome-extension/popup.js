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
const pairingTokenInput = document.getElementById("pairing-token");
const pairBtn = document.getElementById("pair-btn");
const pairSavedMsg = document.getElementById("pair-saved-msg");
const siteOriginInput = document.getElementById("site-origin");
const grantBtn = document.getElementById("grant-btn");
const grantErrorMsg = document.getElementById("grant-error-msg");
const authorizedList = document.getElementById("authorized-list");

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

  await renderAuthorizedSites();
}

// Renders the currently-granted host-permission origins with a revoke
// button each. chrome.permissions is directly available to the popup —
// no need to relay through the background service worker for reads.
async function renderAuthorizedSites() {
  authorizedList.innerHTML = "";
  const granted = await chrome.permissions.getAll();
  const origins = granted.origins || [];
  if (origins.length === 0) {
    const hint = document.createElement("li");
    hint.className = "empty-hint";
    hint.textContent = "No sites authorized yet.";
    authorizedList.appendChild(hint);
    return;
  }
  for (const origin of origins) {
    const row = document.createElement("li");
    row.className = "site-row";
    const label = document.createElement("span");
    label.textContent = origin;
    label.title = origin;
    const revokeBtn = document.createElement("button");
    revokeBtn.textContent = "Revoke";
    revokeBtn.addEventListener("click", async () => {
      await chrome.permissions.remove({ origins: [origin] });
      await renderAuthorizedSites();
    });
    row.appendChild(label);
    row.appendChild(revokeBtn);
    authorizedList.appendChild(row);
  }
}

// Listen for live status updates from background
chrome.runtime.onMessage.addListener((msg) => {
  if (msg.type === "status") {
    updateUI(msg.status);
  }
});

// Grant button handler — calls chrome.permissions.request directly in this
// click handler (not relayed to the background service worker), since the
// API requires transient user activation from the calling context.
grantBtn.addEventListener("click", async () => {
  const value = siteOriginInput.value.trim();
  grantErrorMsg.style.display = "none";
  if (!value) return;

  let granted;
  try {
    granted = await chrome.permissions.request({ origins: [value] });
  } catch (err) {
    grantErrorMsg.textContent = err.message;
    grantErrorMsg.style.display = "block";
    return;
  }
  if (!granted) {
    grantErrorMsg.textContent = "Permission request was denied or the pattern is invalid.";
    grantErrorMsg.style.display = "block";
    return;
  }
  siteOriginInput.value = "";
  await renderAuthorizedSites();
});

// Pair button handler — sends the token typed into the field, never reads
// it back out of storage, so the popup never displays a previously-saved
// secret.
pairBtn.addEventListener("click", async () => {
  const value = pairingTokenInput.value.trim();
  if (!value) return;

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
