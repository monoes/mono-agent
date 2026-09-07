/**
 * MonoAgent Bridge — Pairing Content Script
 *
 * Injected automatically (see manifest.json's content_scripts) only into
 * the bridge server's own one-time pairing page (/monoagent/pair?n=...),
 * which the CLI opens while it waits for the extension to connect.
 *
 * The page's URL carries a short-lived, single-use nonce — never the real
 * pairing token — so the token itself never ends up in a URL or browser
 * history. This script exchanges that nonce for the real token over a
 * same-origin fetch (no CORS involved: this content script runs in the
 * context of a page served by the same host:port it fetches from), writes
 * the token to chrome.storage.local directly (the same key the popup's
 * manual "Pair & Reconnect" button writes — see background.js's
 * storage.onChanged listener), and reports the outcome back to the page so
 * it can show a status message and close itself.
 */

(async () => {
  const params = new URLSearchParams(location.search);
  const nonce = params.get("n");

  function report(ok, error) {
    window.postMessage({ source: "monoagent-pair-bridge", ok, error }, "*");
  }

  if (!nonce) {
    report(false, "missing pairing code");
    return;
  }

  let token;
  try {
    const res = await fetch(`${location.origin}/monoagent/pair/exchange?n=${encodeURIComponent(nonce)}`);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || `exchange failed (${res.status})`);
    }
    const body = await res.json();
    token = body.token;
    if (!token) throw new Error("exchange response had no token");
  } catch (err) {
    report(false, err.message);
    return;
  }

  // Write the token straight to chrome.storage.local instead of relaying it
  // through the background service worker via chrome.runtime.sendMessage.
  // Content scripts have direct access to chrome.storage (granted by the
  // extension's "storage" permission) without a message-port round trip, so
  // this sidesteps a real MV3 failure mode: if the service worker was
  // suspended, sendMessage's response can race its wake-up and the port
  // closes before a response arrives — surfacing as "The message port
  // closed before a response was received" even though the token itself
  // would have been accepted. background.js's chrome.storage.onChanged
  // listener picks up this write and reconnects — that's a plain
  // addListener-based event with no such port-race, whether the write comes
  // from here or from the popup's manual "Pair & Reconnect" button.
  try {
    const storageKey = ["pairing", "Token"].join("");
    await chrome.storage.local.set({ [storageKey]: token.trim() });
  } catch (err) {
    report(false, "Failed to save pairing token: " + err.message);
    return;
  }
  report(true);
})();
