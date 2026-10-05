// Drives the real side panel in a browser started by capture-ask.sh: save the
// page in front into the profile the panel is "Saving into", then ask about
// it. Usage: node capture-ask.mjs <debugPort> <pageUrlPart> <save|ask|profile> [arg]
//   save            click Save and wait for the panel to say it saved
//   ask <question>  ask the brain; prints {status, answers:[{quote,title,url}]}
//   profile <id>    pick a profile in the panel's "Saving into" menu
const [port, pagePart, action, arg] = process.argv.slice(2);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const list = async () => (await fetch(`http://127.0.0.1:${port}/json/list`)).json();

async function attach(match) {
  let t;
  for (let i = 0; i < 100 && !t; i++) {
    t = (await list()).find(match);
    if (!t) await sleep(200);
  }
  if (!t) throw new Error("no target: " + JSON.stringify((await list()).map((x) => [x.type, x.url])));
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
  await new Promise((r) => (ws.onopen = r));
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
  return { t, evaluate, close: () => ws.close() };
}

/** panel opens the real side panel beside the page (a user gesture from an
 *  extension page), or reuses one that is already open. */
async function panel() {
  const open = (await list()).find((t) => t.url.endsWith("/sidepanel.html"));
  if (open) return attach((t) => t.id === open.id);
  const sw = await attach((t) => t.type === "service_worker" && t.url.endsWith("/background.js"));
  const extId = new URL(sw.t.url).host;
  sw.close();
  // Focus the page first so the panel follows it.
  const page = (await list()).find((t) => t.type === "page" && t.url.includes(pagePart));
  await fetch(`http://127.0.0.1:${port}/json/activate/${page.id}`);
  await fetch(`http://127.0.0.1:${port}/json/new?chrome-extension://${extId}/icons/icon48.png`, { method: "PUT" });
  const ext = await attach((t) => t.type === "page" && t.url.includes("/icons/icon48.png"));
  await ext.evaluate(
    `(async () => { const w = await chrome.windows.getCurrent(); await chrome.sidePanel.open({ windowId: w.id }); })()`,
    true
  );
  const p = await attach((t) => t.url.endsWith("/sidepanel.html"));
  // The helper tab goes, so the page is the tab in front again.
  await fetch(`http://127.0.0.1:${port}/json/close/${ext.t.id}`);
  ext.close();
  await fetch(`http://127.0.0.1:${port}/json/activate/${page.id}`);
  await sleep(1500);
  return p;
}

const p = await panel();
const waitFor = async (expr, ms = 60000) => {
  const deadline = Date.now() + ms;
  for (;;) {
    const v = await p.evaluate(expr);
    if (v) return v;
    if (Date.now() > deadline) throw new Error("timed out waiting for " + expr);
    await sleep(250);
  }
};

if (action === "save") {
  await waitFor(`!document.getElementById("capture-btn").disabled`);
  const dest = await p.evaluate(`document.getElementById("profile-name").textContent`);
  await p.evaluate(`document.getElementById("capture-btn").click()`, true);
  await waitFor(`document.getElementById("capture-btn").dataset.done || ""`);
  console.log(JSON.stringify({ saved: true, savingInto: dest }));
} else if (action === "ask") {
  await p.evaluate(`document.getElementById("ask-panel").open = true`);
  await p.evaluate(
    `(() => { const q = document.getElementById("ask-q"); q.value = ${JSON.stringify(arg)}; document.getElementById("ask-answers").textContent = ""; document.getElementById("ask-status").textContent = ""; document.getElementById("ask-btn").click(); })()`,
    true
  );
  await waitFor(`(() => { const s = document.getElementById("ask-status").textContent; return s && !/asking|searching|citing/.test(s) ? s : ""; })()`, 90000);
  const out = await p.evaluate(`({
    savingInto: document.getElementById("profile-name").textContent,
    status: document.getElementById("ask-status").textContent,
    tone: document.getElementById("ask-status").dataset.tone || "",
    answers: [...document.querySelectorAll("#ask-answers .answer")].map((a) => ({
      quote: a.querySelector(".quote").textContent,
      title: a.querySelector(".src a").textContent,
      url: a.querySelector(".src a").href,
    })),
  })`);
  console.log(JSON.stringify(out));
} else if (action === "profile") {
  await waitFor(`!document.getElementById("profile-btn").disabled`);
  await p.evaluate(`document.getElementById("profile-btn").click()`, true);
  const ok = await p.evaluate(`(() => {
    const r = [...document.querySelectorAll('#profile-options input[type=radio]')].find((i) => i.value === ${JSON.stringify(arg)});
    if (!r) return false;
    r.checked = true;
    r.dispatchEvent(new Event("change", { bubbles: true }));
    return true;
  })()`);
  if (!ok) throw new Error("no such profile in the panel: " + arg);
  await sleep(500);
  console.log(JSON.stringify({ savingInto: await p.evaluate(`document.getElementById("profile-name").textContent`) }));
}
p.close();
process.exit(0);
