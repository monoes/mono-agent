// The unpacked extension in a headless Chromium, for browser tests that
// must go through Chrome's own loader (chrome.scripting, the service
// worker) rather than evaluating the scripts. Its own profile directory
// and a random DevTools port; no pairing token, so the extension never
// dials a bridge -- the user's bridge on 9222 is never touched.

import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** A flat-session CDP client over the browser socket. */
export class Cdp {
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

export async function start(binary) {
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


/** extensionId waits for the extension's service worker and returns its id and target. */
export async function extensionWorker(cdp) {
  for (let i = 0; i < 100; i++) {
    const { targetInfos } = await cdp.send("Target.getTargets");
    const sw = targetInfos.find((t) => t.type === "service_worker" && t.url.endsWith("/background.js"));
    if (sw) return { id: new URL(sw.url).host, targetId: sw.targetId };
    await sleep(100);
  }
  throw new Error("the extension's service worker did not start");
}

/** attach opens a flat session on a target (created from `url` when given). */
export async function attach(cdp, { url, targetId }) {
  const id = targetId || (await cdp.send("Target.createTarget", { url })).targetId;
  const { sessionId } = await cdp.send("Target.attachToTarget", { targetId: id, flatten: true });
  await cdp.send("Runtime.enable", {}, sessionId);
  return sessionId;
}
