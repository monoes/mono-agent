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
 * context of a page served by the same host:port it fetches from), hands
 * the token to the background service worker exactly the way the popup's
 * manual "Pair & Reconnect" button does, and reports the outcome back to
 * the page so it can show a status message and close itself.
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

  chrome.runtime.sendMessage({ type: "auto_pair", token }, (response) => {
    if (chrome.runtime.lastError) {
      report(false, chrome.runtime.lastError.message);
      return;
    }
    if (!response || response.ok === false) {
      report(false, response?.error || "background script rejected the token");
      return;
    }
    report(true);
  });
})();
