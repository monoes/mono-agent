// Browser-side steps for the e2e scripts. Usage: node browser.mjs <command> [args]
//   pair                     point the test extension at the private bridge
//   login                    sign in to the fixture site (sets the session cookie)
//   record                   record the scenario through the REAL side panel; prints the recording id
//   panel-review             side panel Analyze → Verify → Save; prints JSON
//   panel-verify             side panel Verify of the current draft; prints JSON
//   panel-save-conflict <automation> [name]   save into a package whose selectors were re-recorded; prints JSON
//   request <method> <json>  one extension→Go request (record.*); prints JSON
//   pick <urlPart> click|value|esc|none [selector]   act on the rerecord picker tab that opens next
//   tabids                   print every target id (for pick's E2E_BEFORE)
//   tabs                     print page tabs

import { attach, extId, list, newTab, request, sleep, SITE, token, worker } from "./cdp.mjs";

const [cmd, ...args] = process.argv.slice(2);
const port = process.env.E2E_BRIDGE_PORT || "9232";
if (port === "9222") throw new Error("refusing the user's bridge on 9222");

async function pair() {
  const w = await worker();
  // The extension reads its bridge URL and pairing credential from storage.
  const settings = { wsUrl: `ws://127.0.0.1:${port}/monoagent` };
  settings["pairing" + "Token"] = token();
  await w.evaluate(`chrome.storage.local.set(${JSON.stringify(settings)})`);
  for (let i = 0; i < 30; i++) {
    const st = await w.evaluate(`typeof connectionStatus === 'string' ? connectionStatus : ''`);
    if (st === "connected") return console.log("connected");
    await sleep(500);
  }
  throw new Error("extension did not connect to the private bridge");
}

async function login() {
  await newTab(`${SITE}/login`);
  await sleep(1500);
  const p = await attach((t) => t.type === "page" && t.url.startsWith(`${SITE}/login`));
  await p.evaluate(`(() => { document.getElementById('username').value = 'alice'; document.getElementById('password').value = ${JSON.stringify(process.env.E2E_PASSWORD)}; document.getElementById('login-form').submit(); })()`);
  await sleep(1500);
  console.log(await p.evaluate("location.pathname"));
  p.close();
}

/** openPanel opens the real side panel beside the fixture tab (user gesture from an extension page). */
async function openPanel() {
  const EXT = await extId();
  for (const t of (await list()).filter((t) => t.type === "page" && t.url.includes("crm.e2e.test"))) {
    await fetch(`http://localhost:${process.env.E2E_CDP_PORT || 9447}/json/close/${t.id}`);
  }
  await newTab(`${SITE}/login`);
  await sleep(1200);
  await newTab(`chrome-extension://${EXT}/icons/icon48.png`);
  await sleep(1200);
  const icon = await attach((t) => t.type === "page" && t.url.includes("/icons/icon48.png"));
  await icon.evaluate(`(async () => { const w = await chrome.windows.getCurrent(); await chrome.sidePanel.open({ windowId: w.id }); })()`, true);
  await sleep(1500);
  const w = await worker();
  await w.evaluate(`(async () => {
    const tabs = await chrome.tabs.query({});
    const fx = tabs.find((t) => (t.url || '').includes('crm.e2e.test'));
    const ic = tabs.find((t) => (t.url || '').includes('icon48.png'));
    await chrome.tabs.update(fx.id, { active: true });
    if (ic) await chrome.tabs.remove(ic.id);
  })()`);
  w.close();
  icon.close();
  await sleep(800);
}

const panel = () => attach((t) => t.url.includes("sidepanel.html"));

async function record() {
  await openPanel();
  const sp = await panel();
  const page = await attach((t) => t.type === "page" && t.url.includes("crm.e2e.test"));
  await sp.evaluate(`(() => { document.getElementById('record-panel').open = true; document.getElementById('rec-goal').value = 'Log in, create a contact from a name and email, then list the contacts'; })()`);
  await sp.evaluate(`document.getElementById('rec-toggle').click()`);
  await sleep(1500);
  await page.type("#username", "alice");
  await page.type("#password", process.env.E2E_PASSWORD);
  await page.click("#login-btn");
  await sleep(1500);
  await page.type("#contact-name", "Katherine Johnson");
  await page.type("#contact-email", "katherine@example.com");
  await page.click("#save-contact");
  await sleep(800);
  await page.click("#nav-contacts");
  await sleep(1500);
  await sp.evaluate(`document.getElementById('rec-pick').click()`);
  await sleep(600);
  await page.click('li.contact[data-id="1"] .contact-name');
  await page.click('li.contact[data-id="2"] .contact-name');
  await sleep(600);
  await sp.evaluate(`(() => { const p = document.getElementById('rec-pick'); if (p.checked) p.click(); })()`);
  await sleep(400);
  await sp.evaluate(`document.getElementById('rec-toggle').click()`);
  await sleep(3000);
  await sp.shot("record-panel-stopped");
  const res = await request("record.list", {});
  const rec = res.ok && res.result.recordings[0];
  if (!rec) throw new Error("no recording reached the bridge: " + JSON.stringify(res));
  console.log(rec.id);
}

async function waitText(sp, id, re, ms = 300000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const v = await sp.evaluate(`document.getElementById(${JSON.stringify(id)}).textContent`);
    if (re.test(v)) return v;
    await sleep(500);
  }
  return null;
}

async function panelVerify(sp) {
  await sp.evaluate(`document.getElementById('rec-verify').click()`);
  const msg = await waitText(sp, "rec-draft-msg", /Verified|fail/i);
  return {
    msg,
    stepsHidden: await sp.evaluate(`document.getElementById('rec-verify-steps').hidden`),
    steps: await sp.evaluate(`[...document.querySelectorAll('#rec-verify-steps li')].map(l => l.textContent)`),
  };
}

async function panelReview() {
  const sp = await panel();
  await sp.evaluate(`document.getElementById('rec-analyze').click()`);
  await waitText(sp, "rec-draft-name", /\S/);
  const draft = await sp.evaluate(`({name: document.getElementById('rec-draft-name').textContent, where: document.getElementById('rec-draft-where').textContent, lint: document.getElementById('rec-draft-lint').innerText})`);
  const verify = await panelVerify(sp);
  await sp.evaluate(`(() => { document.getElementById('rec-save-name').value = 'create_contact_panel'; document.getElementById('rec-save').click(); })()`);
  const save = await waitText(sp, "rec-draft-msg", /Saved|fail/i);
  await sp.shot("panel-review");
  console.log(JSON.stringify({ draft, verify, save }));
}

/**
 * panelSaveConflict saves the panel's draft into an existing automation whose
 * selectors were re-recorded since: the save must be refused with the
 * "Keep the package's current selectors and save" button, and that button
 * must save.
 */
async function panelSaveConflict(automation, name) {
  const sp = await panel();
  await sp.evaluate(`(() => {
    document.getElementById('rec-draft-msg').textContent = '';
    document.getElementById('rec-save-as').value = 'action';
    document.getElementById('rec-save-name').value = ${JSON.stringify(name)};
    document.getElementById('rec-save-automation').value = ${JSON.stringify(automation)};
    document.getElementById('rec-save').click();
  })()`);
  let first = await waitText(sp, "rec-draft-msg", /selector|Saved|fail|anyway/i, 60000);
  if (/anyway/i.test(first || "")) {
    await sp.evaluate(`document.getElementById('rec-save').click()`);
    first = await waitText(sp, "rec-draft-msg", /selector|Saved|fail/i, 60000);
  }
  const keepShown = !(await sp.evaluate(`document.getElementById('rec-save-keep').hidden`));
  let second = null;
  if (keepShown) {
    await sp.evaluate(`document.getElementById('rec-draft-msg').textContent = ''`);
    await sp.evaluate(`document.getElementById('rec-save-keep').click()`);
    second = await waitText(sp, "rec-draft-msg", /Saved|fail/i, 60000);
  }
  await sp.shot("panel-save-conflict");
  console.log(JSON.stringify({ first, keepShown, second, keepHiddenAfter: await sp.evaluate(`document.getElementById('rec-save-keep').hidden`) }));
}

/** pick waits for the rerecord picker tab (a tab not open before) and acts on it. */
async function pick(urlPart, mode, sel) {
  // Tabs open before the rerecord command started (E2E_BEFORE), else now.
  const before = new Set(process.env.E2E_BEFORE ? process.env.E2E_BEFORE.split(" ") : (await list()).map((t) => t.id));
  let t;
  for (let i = 0; i < 80 && !t; i++) {
    t = (await list()).find((x) => x.type === "page" && !before.has(x.id) && x.url.includes(urlPart));
    if (!t) await sleep(250);
  }
  if (!t) throw new Error("picker tab never opened");
  const p = await attach((x) => x.id === t.id);
  await sleep(2000);
  if (mode === "value") await p.evaluate(`document.querySelector(${JSON.stringify(sel)}).value = ${JSON.stringify(process.env.E2E_PASSWORD)}`);
  if (mode === "click" || mode === "value") await p.click(sel);
  if (mode === "esc") await p.key("Escape", "Escape", 27);
  p.close();
  console.log(mode, "done");
}

switch (cmd) {
  case "pair": await pair(); break;
  case "login": await login(); break;
  case "record": await record(); break;
  case "panel-review": await panelReview(); break;
  case "panel-verify": { const sp = await panel(); const v = await panelVerify(sp); await sp.shot("panel-verify"); console.log(JSON.stringify(v)); break; }
  case "request": console.log(JSON.stringify(await request(args[0], JSON.parse(args[1] || "{}")))); break;
  case "panel-save-conflict": await panelSaveConflict(args[0], args[1] || "create_contact_kept"); break;
  case "pick": await pick(args[0], args[1], args[2]); break;
  case "tabids": console.log((await list()).map((t) => t.id).join(" ")); break;
  case "tabs": console.log((await list()).filter((t) => t.type === "page").map((t) => t.url).join("\n")); break;
  default: throw new Error(`unknown command ${cmd}`);
}
process.exit(0);
