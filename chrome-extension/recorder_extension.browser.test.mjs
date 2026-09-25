/**
 * The recorder as production runs it: the unpacked extension loaded into a
 * headless Chromium, recording started from the side panel page through
 * chrome.runtime messages, recorder_*.js injected by chrome.scripting into
 * a real http page, and real CDP input on that page.
 *
 * recorder.browser.test.mjs evaluates the page scripts directly, which is
 * why it passed while Chrome refused to load recorder_selectors.js (a raw
 * U+FFFF made the file "not UTF-8"). This test goes through the loader.
 *
 * Isolation: its own profile dir under TMPDIR and a random DevTools port.
 * The extension has no pairing token there, so it never dials a bridge
 * (doConnect stops at "unpaired" before probing any port) — the user's
 * bridge on 9222 is never touched. Frames just queue in the outbox.
 *
 * WITHOUT CHROME the suite skips.
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { findChrome } from "./browser_harness.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const FIXTURE = `<!doctype html><html><head><meta charset="utf-8"><title>Rec e2e</title>
<style>body{font:16px sans-serif;margin:20px} input,button{display:block;margin:10px 0;padding:6px}</style></head>
<body><form onsubmit="event.preventDefault()">
<label for="email">Email</label><input id="email" name="email">
<label>Password <input type="password" name="pw"></label>
<button type="button" data-testid="save">Save</button></form></body></html>`;

/** A flat-session CDP client over the browser socket. */
class Cdp {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    ws.addEventListener("message", (e) => {
      const m = JSON.parse(e.data);
      const p = m.id && this.pending.get(m.id);
      if (!p) return;
      this.pending.delete(m.id);
      if (m.error) p.reject(new Error(m.error.message));
      else p.resolve(m.result);
    });
  }
  send(method, params = {}, sessionId) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params, sessionId }));
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      setTimeout(() => this.pending.delete(id) && reject(new Error(`${method} timed out`)), 15000);
    });
  }
  async evaluate(sessionId, expression) {
    const r = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, sessionId);
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  }
}

async function start(binary) {
  const dir = await mkdtemp(join(tmpdir(), "mono-rec-ext-"));
  const child = spawn(
    binary,
    [
      ...(process.env.MONO_CHROME_NO_SANDBOX === "1" ? ["--no-sandbox"] : []),
      "--headless=new",
      "--remote-debugging-port=0",
      `--user-data-dir=${dir}`,
      `--load-extension=${HERE}`,
      `--disable-extensions-except=${HERE}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-gpu",
      "--window-size=1200,900",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"], detached: true }
  );
  let stderr = "";
  child.stderr.on("data", (c) => (stderr += String(c)));
  let port = 0;
  let path = "";
  for (let i = 0; i < 400 && !port; i++) {
    try {
      [port, path] = (await readFile(join(dir, "DevToolsActivePort"), "utf8")).split("\n");
      port = Number(port);
    } catch {
      await sleep(50);
    }
  }
  if (!port) throw new Error(`no DevTools port\n${stderr.slice(-1000)}`);
  const ws = new WebSocket(`ws://127.0.0.1:${port}${path.trim()}`);
  await new Promise((res, rej) => {
    ws.addEventListener("open", res, { once: true });
    ws.addEventListener("error", () => rej(new Error("devtools socket failed")), { once: true });
  });
  const close = async () => {
    try {
      ws.close();
    } catch {}
    try {
      process.kill(-child.pid, "SIGKILL");
    } catch {}
    await sleep(300);
    await rm(dir, { recursive: true, force: true }).catch(() => {});
  };
  return { cdp: new Cdp(ws), close };
}

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

describe("the unpacked extension records a real page", { skip: browser ? false : why, concurrency: 1 }, () => {
  it("injects the recorder through chrome.scripting and records click and typing", async () => {
    const { cdp } = browser;
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const pageUrl = `http://127.0.0.1:${server.address().port}/fixture`;

    // The extension's worker tells us its id.
    let extId = "";
    for (let i = 0; i < 100 && !extId; i++) {
      const { targetInfos } = await cdp.send("Target.getTargets");
      const sw = targetInfos.find((t) => t.type === "service_worker" && t.url.endsWith("/background.js"));
      if (sw) extId = new URL(sw.url).host;
      else await sleep(100);
    }
    assert.ok(extId, "the extension's service worker started");

    const attach = async (url) => {
      const { targetId } = await cdp.send("Target.createTarget", { url });
      const { sessionId } = await cdp.send("Target.attachToTarget", { targetId, flatten: true });
      await cdp.send("Runtime.enable", {}, sessionId);
      return sessionId;
    };
    const page = await attach(pageUrl);
    await sleep(500);
    const panel = await attach(`chrome-extension://${extId}/sidepanel.html`);
    await sleep(500);

    const ask = (msg) =>
      cdp.evaluate(panel, `new Promise((r) => chrome.runtime.sendMessage(${JSON.stringify(msg)}, (res) => r(JSON.stringify(res || null))))`).then(JSON.parse);
    const tabId = await cdp.evaluate(panel, `chrome.tabs.query({}).then((ts) => ts.find((t) => t.url === ${JSON.stringify(pageUrl)}).id)`);

    const started = await ask({ type: "record_start", tabId, goal: "e2e" });
    assert.equal(started.ok, true, `start failed: ${started.error}`);
    assert.equal(started.state.recording, true);

    const center = async (sel) =>
      JSON.parse(await cdp.evaluate(page, `(() => { const r = document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect(); return JSON.stringify({x: r.x + r.width/2, y: r.y + r.height/2}); })()`));
    const click = async (sel) => {
      const { x, y } = await center(sel);
      for (const type of ["mousePressed", "mouseReleased"]) {
        await cdp.send("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 }, page);
      }
      await sleep(80);
    };
    await cdp.send("Page.bringToFront", {}, page);
    await click("#email");
    await cdp.send("Input.insertText", { text: "ann@x.test" }, page);
    await click("input[name=pw]");
    await cdp.send("Input.insertText", { text: "hunter2" }, page);
    await click("[data-testid=save]");
    await sleep(400);

    const stopped = await ask({ type: "record_stop" });
    assert.equal(stopped.ok, true, stopped.error);
    const st = stopped.state;
    assert.equal(st.stopReason, "user", "not stopped by an injection error");
    const steps = st.steps.map((s) => `${s.type}:${s.value || s.target?.text || ""}${s.masked ? ":masked" : ""}`);
    assert.deepEqual(steps, ["type:ann@x.test", "type::masked", "click:Save"]);
    assert.ok(st.queued >= 5, "frames wait in the outbox (no bridge in this profile)");
    const local = await cdp.evaluate(panel, `chrome.storage.local.get(null).then((o) => JSON.stringify(o))`);
    assert.ok(local.includes("ann@x.test"), "unacked frames are buffered in storage.local");
    assert.ok(!local.includes("hunter2"), "the password never reached the worker");
    assert.ok(!local.includes('"recordingState"'), "the live recording is not in storage.local");
    const sess = await cdp.evaluate(panel, `chrome.storage.session.get("recordingState").then((o) => JSON.stringify(o))`);
    assert.match(sess, /"recordingState"/, "it is session state");

    // H4: pick mode on, stop, record again -- a click is a click, not an extract.
    await ask({ type: "record_clear" });
    assert.equal((await ask({ type: "record_start", tabId })).ok, true);
    assert.equal((await ask({ type: "record_pick", on: true })).state.pick, true);
    await ask({ type: "record_stop" });
    await ask({ type: "record_clear" });
    assert.equal((await ask({ type: "record_start", tabId })).ok, true);
    await click("[data-testid=save]");
    await sleep(200);
    const again = await ask({ type: "record_stop" });
    assert.deepEqual(again.state.steps.map((x) => x.type), ["click"], "pick mode did not carry over");
  });
});
