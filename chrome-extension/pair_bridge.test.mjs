// Tests for pair_bridge.js, the content script the bridge's one-time pairing
// page runs. No dependencies: `node --test chrome-extension/`.
//
// The script is an IIFE that touches only location/fetch/window/chrome, so it
// runs under a handful of stubs in a vm context.

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const SRC = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "pair_bridge.js"), "utf8");

// runPairBridge evaluates the content script against a fake pairing page and
// returns what it wrote to chrome.storage.local and reported to the page.
async function runPairBridge({ origin = "http://127.0.0.1:9323", nonce = "abc123", exchange } = {}) {
  const stored = {};
  const messages = [];
  let settle;
  const finished = new Promise((r) => (settle = r));

  const sandbox = {
    location: { origin, search: nonce === null ? "" : `?n=${nonce}` },
    URLSearchParams,
    fetch:
      exchange ||
      (async () => ({ ok: true, json: async () => ({ token: "  tok-123  " }) })),
    window: {
      postMessage: (msg) => {
        messages.push(msg);
        settle();
      },
    },
    chrome: {
      storage: {
        local: {
          set: async (update) => Object.assign(stored, update),
        },
      },
    },
  };
  runInContext(SRC, createContext(sandbox));
  await finished;
  return { stored, report: messages[0] };
}

test("pairing records the port the bridge actually bound, not just the token", async () => {
  const { stored, report } = await runPairBridge({ origin: "http://127.0.0.1:9323" });

  assert.equal(report.ok, true);
  assert.equal(stored.pairingToken, "tok-123", "token is stored trimmed");
  // The whole point: the background worker tries 9222 first and only rotates
  // after PORT_SWITCH_AFTER_FAILURES, which outlasts the CLI's 30s wait. The
  // pairing page is served by the bridge itself, so its origin is the
  // authoritative port — record it.
  assert.equal(stored.pairedWsUrl, "ws://127.0.0.1:9323/monoagent");
});

test("a bridge on the default port is recorded the same way", async () => {
  const { stored } = await runPairBridge({ origin: "http://127.0.0.1:9222" });
  assert.equal(stored.pairedWsUrl, "ws://127.0.0.1:9222/monoagent");
});

test("a failed exchange stores nothing", async () => {
  const { stored, report } = await runPairBridge({
    exchange: async () => ({ ok: false, status: 403, json: async () => ({ error: "expired pairing code" }) }),
  });

  assert.equal(report.ok, false);
  assert.match(report.error, /expired pairing code/);
  assert.deepEqual(stored, {}, "no token and no port on failure");
});

test("a page without a nonce reports the problem and stores nothing", async () => {
  const { stored, report } = await runPairBridge({ nonce: null });

  assert.equal(report.ok, false);
  assert.match(report.error, /missing pairing code/);
  assert.deepEqual(stored, {});
});
