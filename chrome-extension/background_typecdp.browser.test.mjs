/**
 * type_cdp with an elementId, through the real command path: the unpacked
 * extension's service worker resolves the element with the content
 * script's `element` command (sendToContent), then typeCDP types into it.
 *
 * The bug this guards: typeCDP ignored elementId and typed into the first
 * large contenteditable, so on a plain form nothing was focused, the text
 * went nowhere and it still answered {typed: true} -- every recorded form
 * was replayed empty. Now the text must be READ BACK from the element.
 *
 * Runs only the worker's own functions; no bridge (see extension_harness).
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { findChrome } from "./browser_harness.mjs";
import { attach, extensionWorker, sleep, start } from "./extension_harness.mjs";

const FIXTURE = `<!doctype html><html><head><meta charset="utf-8"><title>type_cdp</title>
<style>body{font:16px sans-serif;margin:20px} .big{min-height:120px;width:600px;border:1px solid #999}</style></head>
<body>
  <div class="big" contenteditable="true" id="decoy"></div>
  <form onsubmit="event.preventDefault()">
    <input id="name" name="name">
    <textarea id="bio"></textarea>
    <div id="note" contenteditable="true" style="min-height:40px;border:1px solid #ccc"></div>
    <input id="off" disabled>
  </form>
  <div style="height:2000px"></div>
  <input id="far" name="far">
</body></html>`;

const CHROME = findChrome();
let browser = null;
let why = "no Chrome found — set CHROME_PATH to run these";
if (CHROME && typeof WebSocket === "function") {
  try {
    browser = await start(CHROME);
  } catch (e) {
    why = `${CHROME} would not start: ${e.message.split("\n")[0]}`;
  }
}
const server = createServer((req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(FIXTURE);
});

after(async () => {
  if (browser) await browser.close();
  server.close();
});

describe("type_cdp types into the element it is given", { skip: browser ? false : why, concurrency: 1 }, () => {
  let sw;
  let page;
  let tabId;

  async function setup() {
    if (sw) return;
    const { cdp } = browser;
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const url = `http://127.0.0.1:${server.address().port}/form`;
    const worker = await extensionWorker(cdp);
    page = await attach(cdp, { url });
    await sleep(700); // content.js is declared at document_idle
    sw = await attach(cdp, { targetId: worker.targetId });
    tabId = await cdp.evaluate(sw, `chrome.tabs.query({}).then((ts) => ts.find((t) => t.url === ${JSON.stringify(url)}).id)`);
  }

  /** typeInto runs the worker's command path: element lookup, then typeCDP. */
  async function typeInto(selector, text) {
    await setup();
    return JSON.parse(
      await browser.cdp.evaluate(
        sw,
        `(async () => {
          try {
            const found = await sendToContent(${tabId}, { type: "element", params: { selector: ${JSON.stringify(selector)}, timeout: 2000 } });
            const r = await typeCDP({ tabId: ${tabId}, elementId: found.elementId, text: ${JSON.stringify(text)} });
            return JSON.stringify({ ok: true, r });
          } catch (e) {
            return JSON.stringify({ ok: false, error: e.message });
          }
        })()`
      )
    );
  }

  const valueOf = (expr) => browser.cdp.evaluate(page, expr);

  it("types into an <input> and reads it back", async () => {
    const res = await typeInto("#name", "Ann Example");
    assert.equal(res.ok, true, res.error);
    assert.equal(res.r.typed, true);
    assert.equal(res.r.value, "Ann Example");
    assert.equal(await valueOf(`document.getElementById("name").value`), "Ann Example");
    assert.equal(await valueOf(`document.getElementById("decoy").textContent`), "", "the big contenteditable was not the target");
  });

  it("types into a <textarea>", async () => {
    const res = await typeInto("#bio", "line one");
    assert.equal(res.ok, true, res.error);
    assert.equal(await valueOf(`document.getElementById("bio").value`), "line one");
  });

  it("types into a small contenteditable, not the big decoy", async () => {
    const res = await typeInto("#note", "a note");
    assert.equal(res.ok, true, res.error);
    assert.equal(await valueOf(`document.getElementById("note").innerText.trim()`), "a note");
    assert.equal(await valueOf(`document.getElementById("decoy").textContent`), "");
  });

  it("scrolls a far element into view first", async () => {
    const res = await typeInto("#far", "down here");
    assert.equal(res.ok, true, res.error);
    assert.equal(await valueOf(`document.getElementById("far").value`), "down here");
  });

  it("fails, rather than claiming success, when the element cannot take the text", async () => {
    const res = await typeInto("#off", "nope");
    assert.equal(res.ok, false);
    assert.match(res.error, /could not be focused|did not land/);
    assert.equal(await valueOf(`document.getElementById("off").value`), "");
  });
});
