/**
 * A very small CDP client, for the tests that need a real browser.
 *
 * highlight_page.js is DOM code: it measures selections, splits text nodes
 * and paints <mark> elements. None of that can be judged by a DOM shim, so
 * the tests that cover it drive a real headless Chrome over the DevTools
 * protocol. This file is the plumbing — launch, connect, evaluate, click —
 * and nothing about highlighting.
 *
 * It adds no dependency: Chrome is found on PATH (or skipped), and the
 * socket is Node's own global WebSocket.
 */

import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const CANDIDATES = [
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
];

/**
 * findChrome returns a usable browser binary, or null. Null is the signal
 * to skip: a machine without Chrome must not fail the suite.
 *
 * CHROME_PATH, when set, is the answer — a bad value skips rather than
 * quietly testing some other browser than the one that was asked for.
 */
export function findChrome() {
  if (process.env.CHROME_PATH) {
    return existsSync(process.env.CHROME_PATH) ? process.env.CHROME_PATH : null;
  }
  for (const path of CANDIDATES) {
    if (path && existsSync(path)) return path;
  }
  return null;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Kills the browser and everything it forked. */
function stop(child) {
  try {
    process.kill(-child.pid, "SIGKILL");
  } catch {
    try {
      child.kill("SIGKILL");
    } catch {
      // already gone
    }
  }
}

/** The port file appears once Chrome is listening; there is no other signal. */
async function devtoolsPort(dir, deadline) {
  const file = join(dir, "DevToolsActivePort");
  while (Date.now() < deadline) {
    try {
      const [port] = (await readFile(file, "utf8")).split("\n");
      if (port && Number(port) > 0) return Number(port);
    } catch {
      // not written yet
    }
    await sleep(50);
  }
  throw new Error("Chrome did not open a DevTools port in time");
}

class Session {
  constructor(socket) {
    this.socket = socket;
    this.nextId = 1;
    this.pending = new Map();
    this.handlers = new Map();
    socket.addEventListener("message", (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(`${msg.error.message} (${JSON.stringify(msg.error.data || "")})`));
        else resolve(msg.result);
        return;
      }
      if (msg.method) {
        for (const handler of this.handlers.get(msg.method) || []) handler(msg.params);
      }
    });
  }

  send(method, params = {}) {
    const id = this.nextId++;
    this.socket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      setTimeout(() => {
        if (this.pending.delete(id)) reject(new Error(`${method} timed out`));
      }, 15000);
    });
  }

  on(method, handler) {
    const list = this.handlers.get(method) || [];
    list.push(handler);
    this.handlers.set(method, list);
    return () => this.handlers.set(method, (this.handlers.get(method) || []).filter((h) => h !== handler));
  }

  /** once resolves on the next event of `method`, with a deadline. */
  once(method, timeout = 10000) {
    return new Promise((resolve, reject) => {
      const off = this.on(method, (params) => {
        off();
        resolve(params);
      });
      setTimeout(() => {
        off();
        reject(new Error(`waited for ${method}`));
      }, timeout);
    });
  }
}

/**
 * launch starts a headless browser and returns a handle on one blank page.
 * Everything the tests need goes through `evaluate`, `navigate` and `mouse`.
 */
export async function launch(binary, { noSandbox = process.env.MONO_CHROME_NO_SANDBOX === "1" } = {}) {
  if (typeof WebSocket !== "function") throw new Error("this Node has no global WebSocket");
  const dir = await mkdtemp(join(tmpdir(), "mono-hl-chrome-"));
  const child = spawn(
    binary,
    [
      // A container without user namespaces cannot start the sandbox. That
      // is opt-in rather than automatic: skipping is better than quietly
      // turning it off.
      ...(noSandbox ? ["--no-sandbox"] : []),
      "--headless=new",
      "--remote-debugging-port=0",
      `--user-data-dir=${dir}`,
      "--window-size=1280,2000",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-gpu",
      "--disable-dev-shm-usage",
      "--disable-extensions",
      "--hide-scrollbars",
      "about:blank",
    ],
    // Its own process group: Chrome forks a zygote and a renderer, and
    // killing only the parent leaves them writing to the profile directory
    // while it is being deleted.
    { stdio: ["ignore", "ignore", "pipe"], detached: true }
  );
  let stderr = "";
  child.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });

  let port;
  try {
    port = await devtoolsPort(dir, Date.now() + 20000);
  } catch (error) {
    stop(child);
    throw new Error(`${error.message}\n${stderr.slice(-2000)}`);
  }

  // /json/list is the only way to the page target's socket.
  let targets = [];
  for (let attempt = 0; attempt < 40 && !targets.some((t) => t.type === "page"); attempt++) {
    const response = await fetch(`http://127.0.0.1:${port}/json/list`);
    targets = await response.json();
    if (!targets.some((t) => t.type === "page")) await sleep(50);
  }
  const page = targets.find((t) => t.type === "page");
  if (!page) throw new Error("no page target");

  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("devtools socket failed")), { once: true });
  });

  const session = new Session(socket);
  await session.send("Page.enable");
  await session.send("Runtime.enable");

  const browser = {
    session,
    dir,

    /** evaluate runs an expression in the page and returns its value.
     *  Promises are awaited; a thrown error is re-thrown here. */
    async evaluate(expression, { awaitPromise = true } = {}) {
      const result = await session.send("Runtime.evaluate", {
        expression,
        awaitPromise,
        returnByValue: true,
      });
      if (result.exceptionDetails) {
        const ex = result.exceptionDetails;
        throw new Error(`page threw: ${ex.exception?.description || ex.text}`);
      }
      return result.result.value;
    },

    /** Runs before any of the page's own script, on every document. */
    async onNewDocument(source) {
      const { identifier } = await session.send("Page.addScriptToEvaluateOnNewDocument", { source });
      return identifier;
    },

    async removeNewDocumentScript(identifier) {
      await session.send("Page.removeScriptToEvaluateOnNewDocument", { identifier });
    },

    async navigate(url) {
      const loaded = session.once("Page.loadEventFired");
      await session.send("Page.navigate", { url });
      await loaded;
      await sleep(30); // let the load listeners run
    },

    async reload() {
      const loaded = session.once("Page.loadEventFired");
      await session.send("Page.reload");
      await loaded;
      await sleep(30);
    },

    /** A real press-drag-release, which is how a selection is actually made. */
    async drag(x1, y1, x2, y2) {
      const base = { button: "left", clickCount: 1, buttons: 1 };
      await session.send("Input.dispatchMouseEvent", { type: "mousePressed", x: x1, y: y1, ...base });
      const steps = 8;
      for (let i = 1; i <= steps; i++) {
        await session.send("Input.dispatchMouseEvent", {
          type: "mouseMoved",
          x: x1 + ((x2 - x1) * i) / steps,
          y: y1 + ((y2 - y1) * i) / steps,
          ...base,
        });
      }
      await session.send("Input.dispatchMouseEvent", { type: "mouseReleased", x: x2, y: y2, ...base });
      await sleep(50);
    },

    async click(x, y) {
      const base = { button: "left", clickCount: 1 };
      await session.send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, buttons: 1, ...base });
      await session.send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, buttons: 0, ...base });
      await sleep(50);
    },

    /** answerPrompt handles the next window.prompt; headless dismisses
     *  dialogs otherwise, so a note would never be typed. */
    answerPrompt(text) {
      return new Promise((resolve) => {
        const off = session.on("Page.javascriptDialogOpening", async (params) => {
          off();
          await session.send("Page.handleJavaScriptDialog", { accept: true, promptText: text });
          resolve(params);
        });
      });
    },

    async screenshot(path) {
      const { data } = await session.send("Page.captureScreenshot", { format: "png" });
      await writeFile(path, Buffer.from(data, "base64"));
      return path;
    },

    async close() {
      try {
        socket.close();
      } catch {
        // already gone
      }
      stop(child);
      await Promise.race([
        new Promise((resolve) => child.once("exit", resolve)),
        sleep(5000),
      ]);
      // Best effort, and never fatal: a profile directory left in the
      // system temp dir is not worth failing a test run over.
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          await rm(dir, { recursive: true, force: true });
          return;
        } catch {
          await sleep(200);
        }
      }
    },
  };

  return browser;
}

export const fileUrl = (path) => pathToFileURL(path).href;
