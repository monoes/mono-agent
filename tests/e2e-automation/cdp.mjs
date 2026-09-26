// Minimal DevTools-protocol helpers for the e2e browser (no dependencies:
// Node's global fetch and WebSocket). Ports and paths come from env.sh.

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";

export const CDP = `http://localhost:${process.env.E2E_CDP_PORT || 9447}`;
export const WORK = process.env.E2E_WORK || `${process.env.HOME}/scratch/automation-e2e-run`;
export const SITE = process.env.E2E_SITE || "http://crm.e2e.test:18765";
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
export const list = async () => (await fetch(`${CDP}/json/list`)).json();
export const newTab = (url) => fetch(`${CDP}/json/new?${url}`, { method: "PUT" });

export async function extId() {
  const t = (await list()).find((x) => x.type === "service_worker" && x.url.endsWith("/background.js"));
  return t && new URL(t.url).host;
}

/** attach connects to the first target that matches (retrying briefly). */
export async function attach(match, tries = 20) {
  let t;
  for (let i = 0; i < tries && !t; i++) {
    t = (await list()).find(match);
    if (!t) await sleep(300);
  }
  if (!t) throw new Error("no matching target");
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  let id = 0;
  const pending = new Map();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg);
      pending.delete(msg.id);
    }
  };
  await new Promise((r, j) => ((ws.onopen = r), (ws.onerror = j)));
  const send = (method, params = {}) =>
    new Promise((r) => {
      const i = ++id;
      pending.set(i, r);
      ws.send(JSON.stringify({ id: i, method, params }));
    });
  const evaluate = async (expression, userGesture = false) => {
    const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true, userGesture });
    if (r.result?.exceptionDetails) throw new Error(r.result.exceptionDetails.exception?.description || r.result.exceptionDetails.text);
    return r.result?.result?.value;
  };
  const shot = async (name) => {
    const r = await send("Page.captureScreenshot", { format: "png" });
    if (!r.result?.data) return null;
    mkdirSync(`${WORK}/shots`, { recursive: true });
    writeFileSync(`${WORK}/shots/${name}.png`, Buffer.from(r.result.data, "base64"));
    return `${WORK}/shots/${name}.png`;
  };
  /** click dispatches a real (trusted) mouse click at the element's centre. */
  const click = async (sel) => {
    let r;
    for (let i = 0; i < 30 && !r; i++) {
      r = await evaluate(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; e.scrollIntoView({block:'center'}); const b = e.getBoundingClientRect(); return {x: b.x + b.width/2, y: b.y + b.height/2}; })()`);
      if (!r) await sleep(200);
    }
    if (!r) throw new Error(`no element ${sel}`);
    for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) {
      await send("Input.dispatchMouseEvent", { type, x: r.x, y: r.y, button: "left", clickCount: 1 });
    }
    await sleep(400);
  };
  const type = async (sel, text) => {
    await click(sel);
    await send("Input.insertText", { text });
    await sleep(300);
  };
  // A key can close the page (Esc on the picker), so don't wait forever for the reply.
  const key = async (k, code = k, vk = 0) => {
    for (const t of ["keyDown", "keyUp"]) {
      await Promise.race([send("Input.dispatchKeyEvent", { type: t, key: k, code, windowsVirtualKeyCode: vk }), sleep(2000)]);
    }
  };
  return { t, send, evaluate, shot, click, type, key, close: () => ws.close() };
}

export const worker = () => attach((t) => t.type === "service_worker" && t.url.endsWith("/background.js"));

/** request sends an extension→Go request (MonoAsk) from the worker. */
export async function request(method, params, ms = 600000) {
  const w = await worker();
  try {
    return await w.evaluate(`MonoAsk.request(${JSON.stringify(method)}, ${JSON.stringify(params)}, {timeoutMs: ${ms}, idleTimeoutMs: ${ms}}).then(r => ({ok: true, result: r}), e => ({ok: false, error: e.message, code: e.code}))`);
  } finally {
    w.close();
  }
}

export const token = () => readFileSync(`${process.env.E2E_HOME}/.monoagent/extension.token`, "utf8").trim();
