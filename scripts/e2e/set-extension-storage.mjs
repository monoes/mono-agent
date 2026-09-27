// Writes keys into the MonoAgent Bridge extension's chrome.storage.local in
// a browser started with --remote-debugging-port, through the extension's
// service worker. Usage: node set-extension-storage.mjs <debugPort> '<json>'
const [port, json] = process.argv.slice(2);
const values = JSON.parse(json);

async function workerTarget() {
  for (let i = 0; i < 100; i++) {
    let list = [];
    try {
      list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    } catch {
      // The browser is still starting and not listening yet.
    }
    const sw = list.find((t) => t.type === "service_worker" && /^chrome-extension:\/\/.+\/background\.js$/.test(t.url));
    if (sw) return sw;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`no extension service worker on port ${port}`);
}

const target = await workerTarget();
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
const reply = new Promise((resolve) => {
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id === 1) resolve(msg);
  };
});
ws.send(JSON.stringify({
  id: 1,
  method: "Runtime.evaluate",
  params: { expression: `chrome.storage.local.set(${JSON.stringify(values)}).then(() => "ok")`, awaitPromise: true },
}));
const msg = await reply;
ws.close();
if (msg.result?.result?.value !== "ok") {
  console.error(JSON.stringify(msg));
  process.exit(1);
}
