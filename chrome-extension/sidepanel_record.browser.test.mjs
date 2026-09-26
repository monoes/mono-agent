/**
 * The Record section's Verify results, in a real browser (R6-1).
 *
 * The bug: after a failed Verify the panel still showed the PREVIOUS run's
 * step list next to the new error. So: run verify three times against a
 * stubbed worker -- a pass, a plain error, a failing report -- and check
 * what is on screen after each. The worker is replaced by a stub
 * chrome.runtime that answers from a queue; the panel's own scripts are
 * the real ones (sidepanel.html as shipped).
 *
 * WITHOUT CHROME the suite skips.
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fileUrl, findChrome, launch } from "./browser_harness.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const CHROME = findChrome();
let browser = null;
let why = "no Chrome found — set CHROME_PATH to run these";
if (CHROME) {
  try {
    browser = await launch(CHROME);
  } catch (error) {
    why = `${CHROME} would not start: ${error.message.split("\n")[0]}`;
  }
}

after(async () => {
  if (browser) await browser.close();
});

const STATE = { recording: false, id: "rec-1", tabId: 7, url: "https://app.test/", stopReason: "user", delivered: true, queued: 0, steps: [{ id: "e1", type: "click", target: { tag: "button", text: "Save" } }] };
const DRAFT = {
  ok: true,
  result: {
    draftDir: "/d",
    draft: {
      action: "create_contact",
      names: { action: "create_contact", automation: "crm" },
      lint: [],
      inputs: [],
      actionDef: { steps: [{ id: "open", type: "navigate", url: "https://app.test/" }, { id: "name", type: "type" }] },
    },
  },
};

// The stub worker: record_verify answers come from window.__verify, one per call.
const STUB = `
  window.__verify = [];
  const ev = () => ({ addListener() {} });
  window.chrome = {
    runtime: {
      lastError: undefined,
      getURL: (p) => p,
      onMessage: ev(),
      sendMessage(m, cb) {
        let r = { ok: true };
        if (m.type === "record_analyze") r = ${JSON.stringify(DRAFT)};
        if (m.type === "record_verify") r = window.__verify.shift();
        if (cb) setTimeout(() => cb(r), 0);
        return Promise.resolve(r);
      },
      connect() {
        return {
          onMessage: { addListener(fn) { setTimeout(() => fn({ type: "record_state", state: ${JSON.stringify(STATE)} }), 20); } },
          onDisconnect: ev(),
          postMessage() {},
        };
      },
    },
    tabs: { query: async () => [], onActivated: ev(), onUpdated: ev(), onAttached: ev(), onDetached: ev(), get: async () => ({}) },
    windows: { getCurrent: async () => ({ id: 1 }) },
    storage: { local: { get: async () => ({}), set: async () => {} }, session: { get: async () => ({}), set: async () => {} }, onChanged: ev() },
    commands: { getAll: async () => [] },
  };`;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

describe("the Record panel's Verify results", { skip: browser ? false : why, concurrency: 1 }, () => {
  async function screen() {
    return JSON.parse(
      await browser.evaluate(`JSON.stringify({
        steps: [...document.querySelectorAll("#rec-verify-steps li")].map((li) => ({ text: li.textContent, failed: li.dataset.failed === "true" })),
        listHidden: document.getElementById("rec-verify-steps").hidden,
        error: document.getElementById("rec-verify-error").hidden ? "" : document.getElementById("rec-verify-error").textContent,
        msg: document.getElementById("rec-draft-msg").textContent,
      })`)
    );
  }

  async function verify(answer) {
    await browser.evaluate(`window.__verify.push(${JSON.stringify(answer)}); document.getElementById("rec-verify").click(); true`);
    await sleep(150);
    return screen();
  }

  it("each verify replaces the last one's results", async () => {
    const id = await browser.onNewDocument(STUB);
    await browser.navigate(fileUrl(join(HERE, "sidepanel.html")));
    await browser.removeNewDocumentScript(id);
    await sleep(200);
    await browser.evaluate(`document.getElementById("rec-analyze").click(); true`);
    await sleep(150);

    const pass = await verify({ ok: true, result: { ok: true, steps: [{ id: "open", type: "navigate", status: "pass" }, { id: "name", type: "type", status: "pass" }] } });
    assert.equal(pass.steps.length, 2);
    assert.equal(pass.error, "");
    assert.match(pass.msg, /Verified/);

    // A plain error: the passing run's steps must not stay next to it.
    const broken = await verify({ ok: false, error: "the monoagent bridge is not connected" });
    assert.deepEqual(broken.steps, [], "no stale steps");
    assert.equal(broken.listHidden, true);
    assert.match(broken.msg, /Verify failed: the monoagent bridge is not connected/);

    // A failing report: its steps, the failing one marked, the error above.
    const failed = await verify({
      ok: true,
      result: {
        ok: false,
        steps: [
          { id: "open", type: "navigate", status: "pass" },
          { id: "name", type: "type", status: "fail", message: "element not found: name_field" },
          { id: "save", type: "click", status: "skipped" },
        ],
      },
    });
    assert.deepEqual(failed.steps.map((s) => s.failed), [false, true, false]);
    assert.match(failed.steps[1].text, /name type — fail: element not found/);
    assert.equal(failed.error, "name: element not found: name_field");
    const order = await browser.evaluate(
      `document.getElementById("rec-verify-error").compareDocumentPosition(document.getElementById("rec-verify-steps")) & Node.DOCUMENT_POSITION_FOLLOWING ? "above" : "below"`
    );
    assert.equal(order, "above", "the error sits above the step list");
    assert.match(failed.msg, /Verify failed/);
  });
});
