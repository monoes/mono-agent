/**
 * pick_element through the real command path: the unpacked extension's
 * worker handles {type: "pick_element", tabId, params} in handleCommand
 * (origin check included), recorder_picker.js shows its overlay in the
 * page, and a real CDP click picks. sendResponse -- normally the WS write
 * back to Go -- is swapped for a collector, so the test reads exactly what
 * Go would receive.
 *
 * Also: Esc cancels, the timeout fires, a page's own synthetic click does
 * not pick, a password field comes back `sensitive` with no value, and on
 * every way out the overlay, the listeners and the injected globals are gone.
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { findChrome } from "./browser_harness.mjs";
import { attach, extensionWorker, sleep, start } from "./extension_harness.mjs";

const FIXTURE = `<!doctype html><html><head><meta charset="utf-8"><title>pick</title>
<style>body{font:16px sans-serif;margin:80px 20px} button,input,a{display:block;margin:12px 0;padding:6px}</style></head>
<body>
  <button data-testid="save" onclick="window.__clicked=(window.__clicked||0)+1">Save</button>
  <label for="pw">Password</label><input id="pw" type="password" value="hunter2">
  <a id="link" href="/next?token=abc#part">Next</a>
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

describe("pick_element", { skip: browser ? false : why, concurrency: 1 }, () => {
  let sw;
  let page;
  let tabId;
  let n = 0;

  async function setup() {
    if (sw) return;
    const { cdp } = browser;
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const url = `http://127.0.0.1:${server.address().port}/app?session=s3cret`;
    const worker = await extensionWorker(cdp);
    page = await attach(cdp, { url });
    await sleep(600);
    sw = await attach(cdp, { targetId: worker.targetId });
    tabId = await cdp.evaluate(sw, `chrome.tabs.query({}).then((ts) => ts.find((t) => t.url.includes("/app")).id)`);
    // What Go would receive: collect responses instead of writing to the socket.
    await cdp.evaluate(sw, `(() => { self.__responses = {}; sendResponse = (id, success, data, error) => { self.__responses[id] = { success, data, error }; }; return true; })()`);
  }

  /** command sends pick_element through handleCommand; returns a waiter for its response. */
  async function command(params) {
    await setup();
    const id = `pick-${++n}`;
    await browser.cdp.evaluate(sw, `(handleCommand({ id: ${JSON.stringify(id)}, type: "pick_element", tabId: ${tabId}, params: ${JSON.stringify(params)} }), true)`);
    await sleep(300); // the overlay is up
    return async (limit = 8000) => {
      for (let t = 0; t < limit; t += 100) {
        const r = await browser.cdp.evaluate(sw, `JSON.stringify(self.__responses[${JSON.stringify(id)}] || null)`);
        if (r !== "null") return JSON.parse(r);
        await sleep(100);
      }
      throw new Error("no response");
    };
  }

  const inPage = (expr) => browser.cdp.evaluate(page, expr);
  async function clickAt(selector) {
    const { x, y } = JSON.parse(
      await inPage(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return JSON.stringify({x: r.x + r.width/2, y: r.y + r.height/2}); })()`)
    );
    for (const type of ["mousePressed", "mouseReleased"]) {
      await browser.cdp.send("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 }, page);
    }
  }

  /** leftovers lists what is still in the page after a pick: overlay nodes and isolated-world globals. */
  async function leftovers() {
    const overlay = await inPage(`document.querySelectorAll("[data-monoagent-picker]").length`);
    const globals = await browser.cdp.evaluate(
      sw,
      `chrome.scripting.executeScript({ target: { tabId: ${tabId} }, func: () => ["MonoRecorderPicker","MonoRecorderSelectors","MonoRecorderPrivacy"].filter((n) => globalThis[n] !== undefined) }).then((r) => JSON.stringify(r[0].result))`
    );
    return { overlay, globals: JSON.parse(globals) };
  }

  it("answers the clicked element's ranked fingerprint and the page URL; the page never sees the click", async () => {
    const wait = await command({ prompt: "Click: save_button", timeoutMs: 20000 });
    assert.equal(await inPage(`document.querySelectorAll("[data-monoagent-picker]").length`), 1, "overlay shown");
    await clickAt("[data-testid=save]");
    const res = await wait();
    assert.equal(res.success, true, res.error);
    const { fingerprint: fp, url } = res.data;
    assert.equal(fp.tag, "button");
    assert.deepEqual([fp.candidates[0].kind, fp.candidates[0].value, fp.candidates[0].unique], ["css", '[data-testid="save"]', true]);
    assert.ok(fp.candidates.every((c) => typeof c.score === "number" && typeof c.count === "number"));
    assert.match(url, /\/app\?session=REDACTED$/, "the URL is sanitised");
    assert.equal(await inPage(`window.__clicked || 0`), 0, "the pick click never reached the page");
    assert.deepEqual(await leftovers(), { overlay: 0, globals: [] });
    await clickAt("[data-testid=save]");
    assert.equal(await inPage(`window.__clicked || 0`), 1, "afterwards the page works normally");
  });

  it("a password field comes back sensitive and without its value", async () => {
    const wait = await command({ prompt: "Click: password", timeoutMs: 20000 });
    await clickAt("#pw");
    const res = await wait();
    assert.equal(res.success, true, res.error);
    assert.equal(res.data.fingerprint.sensitive, true);
    assert.ok(!JSON.stringify(res).includes("hunter2"));
  });

  it("link hrefs in the fingerprint are sanitised", async () => {
    const wait = await command({ prompt: "Click: next", timeoutMs: 20000 });
    await clickAt("#link");
    const res = await wait();
    assert.match(res.data.fingerprint.href, /\/next\?token=REDACTED$/);
  });

  it("Esc cancels and cleans up", async () => {
    const wait = await command({ prompt: "Click: anything", timeoutMs: 20000 });
    for (const type of ["rawKeyDown", "keyUp"]) {
      await browser.cdp.send("Input.dispatchKeyEvent", { type, key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, page);
    }
    const res = await wait();
    assert.deepEqual([res.success, res.error], [false, "cancelled"]);
    assert.deepEqual(await leftovers(), { overlay: 0, globals: [] });
  });

  it("a synthetic click from the page does not pick; the timeout ends it and cleans up", async () => {
    const wait = await command({ prompt: "Click: save", timeoutMs: 1500 });
    await inPage(`document.querySelector("[data-testid=save]").click(), true`);
    const res = await wait();
    assert.deepEqual([res.success, res.error], [false, "timeout"]);
    assert.deepEqual(await leftovers(), { overlay: 0, globals: [] });
  });

  it("leaves a running recorder's globals in place", async () => {
    await setup();
    await browser.cdp.evaluate(sw, `chrome.scripting.executeScript({ target: { tabId: ${tabId} }, files: ["recorder_privacy.js", "recorder_selectors.js"] }).then(() => true)`);
    const wait = await command({ prompt: "Click: save", timeoutMs: 20000 });
    await clickAt("[data-testid=save]");
    assert.equal((await wait()).success, true);
    assert.deepEqual(await leftovers(), { overlay: 0, globals: ["MonoRecorderSelectors", "MonoRecorderPrivacy"] });
  });
});
