/**
 * The recorder in a real browser.
 *
 * The node tests fake the DOM and fake the selector counts. This runs the
 * three page scripts (recorder_selectors.js, recorder_list.js, recorder.js)
 * in a headless Chrome on a local fixture page and drives it with real
 * input over CDP, so what is checked is what a person's clicks and typing
 * actually produce: every selector candidate is resolved back against the
 * live document, the password never leaves the page, and a picked list's
 * selectors really select the list.
 *
 * The worker is stubbed: chrome.runtime.sendMessage just collects messages.
 * Chrome is launched on a private DevTools port (browser_harness.mjs) —
 * never the user's bridge. WITHOUT CHROME the suite skips.
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
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
    why = `${CHROME} would not start: ${error.message.split("\n")[0]} (MONO_CHROME_NO_SANDBOX=1 may help)`;
  }
}
const skip = browser ? false : why;
let fixtures = null;

after(async () => {
  if (browser) await browser.close();
  if (fixtures) await rm(fixtures, { recursive: true, force: true }).catch(() => {});
});

const FIXTURE = `<!doctype html><html><head><meta charset="utf-8"><title>Recorder fixture</title>
<style>body{font:16px sans-serif;margin:20px} button,input{display:block;margin:8px 0;padding:6px}</style></head>
<body>
  <form id="login" onsubmit="event.preventDefault()">
    <label for="email">Email</label><input id="email" name="email" type="email">
    <label>Password <input type="password" name="pw" value="prefilled-secret"></label>
    <button type="submit" data-testid="go">Sign in</button>
  </form>
  <button class="dup">Delete</button><button class="dup">Delete</button>
  <ul id="list"><li class="item"><span class="t">One</span></li><li class="item"><span class="t">Two</span></li><li class="item"><span class="t">Three</span></li></ul>
  <div id=":r5:"><a href="#details">Details</a></div>
  <form id="pay"><textarea name="notes">card 4242 4242 4242 4242 thanks</textarea>
    <input name="card_cvv" value="123"><button type="button" id="pay-btn">Pay</button></form>
  <x-card id="host"></x-card>
  <script>
    const root = document.getElementById("host").attachShadow({ mode: "open" });
    root.innerHTML = '<label>Agree <input type="checkbox" id="agree"></label><button id="inner">Inner</button>';
  </script>
</body></html>`;

const STUB = `window.__sent = [];
window.chrome = { runtime: { sendMessage(m) { window.__sent.push(JSON.parse(JSON.stringify(m))); return Promise.resolve(); } } };`;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

describe("recorder.js in a browser", { skip, concurrency: 1 }, () => {
  async function open() {
    if (!fixtures) fixtures = await mkdtemp(join(tmpdir(), "mono-rec-fixture-"));
    const page = join(fixtures, "fixture.html");
    await writeFile(page, FIXTURE, "utf8");
    const src = await Promise.all(
      ["recorder_privacy.js", "recorder_selectors.js", "recorder_list.js", "recorder.js"].map((f) => readFile(join(HERE, f), "utf8"))
    );
    const id = await browser.onNewDocument(`${STUB}\n${src.join("\n")}`);
    await browser.navigate(fileUrl(page));
    await browser.removeNewDocumentScript(id);
  }

  const center = async (selector, index = 0) =>
    JSON.parse(
      await browser.evaluate(`(() => { const r = document.querySelectorAll(${JSON.stringify(selector)})[${index}].getBoundingClientRect();
        return JSON.stringify({ x: r.x + r.width / 2, y: r.y + r.height / 2 }); })()`)
    );

  async function click(selector, index = 0, modifiers = 0) {
    const { x, y } = await center(selector, index);
    const base = { x, y, button: "left", clickCount: 1, modifiers };
    await browser.session.send("Input.dispatchMouseEvent", { type: "mousePressed", buttons: 1, ...base });
    await browser.session.send("Input.dispatchMouseEvent", { type: "mouseReleased", buttons: 0, ...base });
    await sleep(60);
  }

  const typeText = (text) => browser.session.send("Input.insertText", { text });
  const press = async (key, code, vk) => {
    for (const type of ["rawKeyDown", "keyUp"]) {
      await browser.session.send("Input.dispatchKeyEvent", { type, key, code, windowsVirtualKeyCode: vk });
    }
    await sleep(60);
  };
  const sent = async () => JSON.parse(await browser.evaluate("JSON.stringify(window.__sent)"));

  /** resolves checks every candidate against the live DOM: count, and that it finds `selector`. */
  const resolves = (candidates, selector) =>
    browser.evaluate(`(() => {
      const want = document.querySelector(${JSON.stringify(selector)});
      return JSON.stringify(${JSON.stringify(candidates)}.map((c) => {
        let found = [];
        if (c.kind === "css") found = [...document.querySelectorAll(c.value)];
        if (c.kind === "xpath") {
          const r = document.evaluate(c.value, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
          for (let i = 0; i < r.snapshotLength; i++) found.push(r.snapshotItem(i));
        }
        if (c.kind === "aria" || c.kind === "text") return { c, ok: true };
        return { c, ok: found.length === c.count && found.includes(want) };
      }));
    })()`);

  it("records a sign-in with the final email, a masked password and Enter", async () => {
    await open();
    await click("#email");
    await typeText("ann@x.test");
    await click("input[name=pw]");
    await typeText("hunter2");
    await press("Enter", "Enter", 13);
    await sleep(900);
    const msgs = await sent();
    const events = msgs.map((m) => m.event);
    assert.deepEqual(events.map((e) => e.type), ["type", "type", "press_key"], JSON.stringify(events.map((e) => e.type)));
    assert.equal(events[0].value, "ann@x.test");
    assert.equal(events[0].target.label, "Email");
    assert.deepEqual([events[1].masked, events[1].secretAs, events[1].value], [true, "pw", undefined]);
    assert.equal(events[2].key, "Enter");
    const all = JSON.stringify(msgs);
    assert.ok(!all.includes("hunter2"), "the typed password never leaves the page");
    assert.ok(!all.includes("prefilled-secret"), "nor does the value attribute, in the DOM snippet");
    assert.match(msgs[1].snippet, /type="password"/, "the snippet still shows the field");

    const checked = JSON.parse(await resolves(events[0].target.candidates, "#email"));
    for (const { c, ok } of checked) assert.ok(ok, `candidate ${JSON.stringify(c)} resolves to #email`);
    assert.ok(events[0].target.candidates.some((c) => c.kind === "xpath" && c.value.includes("label")), "label XPath offered");
  });

  it("scores a test id first and resolves every candidate for the button", async () => {
    await open();
    await click("[data-testid=go]");
    const [msg] = await sent();
    const fp = msg.event.target;
    assert.equal(msg.event.type, "click");
    assert.deepEqual([fp.candidates[0].kind, fp.candidates[0].value, fp.candidates[0].unique], ["css", '[data-testid="go"]', true]);
    assert.equal(fp.ariaName, "Sign in");
    assert.ok(!fp.candidates.some((c) => c.kind === "aria"), "a unique test id makes counting aria unnecessary");
    for (const { c, ok } of JSON.parse(await resolves(fp.candidates, "[data-testid=go]"))) assert.ok(ok, JSON.stringify(c));
    assert.ok(fp.rect.W > 0 && fp.rect.H > 0);
  });

  it("marks a repeated name as not unique, and skips a generated id", async () => {
    await open();
    await click("button.dup", 1);
    await click("a[href='#details']");
    const [del, link] = (await sent()).map((m) => m.event.target);
    const aria = del.candidates.find((c) => c.kind === "aria");
    assert.deepEqual([aria.count, aria.unique], [2, false]);
    assert.equal(del.candidates[0].unique, true, "a unique selector ranks first");
    for (const { c, ok } of JSON.parse(await resolves(del.candidates, "button.dup:nth-of-type(2)"))) assert.ok(ok, JSON.stringify(c));
    assert.ok(!link.css.includes(":r5:"), "the React-style id is not an anchor for the path");
  });

  it("keeps card numbers and secret fields out of snippets and picks", async () => {
    await open();
    await click("#pay-btn");
    await click("input[name=card_cvv]", 0, 1 /* Alt: pick as data */);
    const msgs = await sent();
    const all = JSON.stringify(msgs);
    assert.match(msgs[0].snippet, /\[card\]/, "the textarea's card number is scrubbed");
    assert.ok(!all.includes("4242 4242"), "no card number anywhere");
    assert.ok(!all.includes('"123"') && !all.includes('value="123"'), "the CVV is neither picked nor in a snippet");
    assert.equal(msgs[1].event.masked, true);
  });

  it("records clicks and checkbox changes inside an open shadow root", async () => {
    await open();
    const inShadow = (sel) =>
      browser.evaluate(`(() => { const r = document.getElementById("host").shadowRoot.querySelector(${JSON.stringify(sel)}).getBoundingClientRect();
        return JSON.stringify({ x: r.x + r.width / 2, y: r.y + r.height / 2 }); })()`).then(JSON.parse);
    for (const sel of ["#inner", "#agree"]) {
      const { x, y } = await inShadow(sel);
      const base = { x, y, button: "left", clickCount: 1 };
      await browser.session.send("Input.dispatchMouseEvent", { type: "mousePressed", buttons: 1, ...base });
      await browser.session.send("Input.dispatchMouseEvent", { type: "mouseReleased", buttons: 0, ...base });
      await sleep(60);
    }
    const events = (await sent()).map((m) => m.event);
    assert.deepEqual(events.map((e) => e.type), ["click", "check"], JSON.stringify(events.map((e) => e.type)));
    assert.equal(events[0].target.text, "Inner");
    assert.equal(events[1].checked, true);
  });

  it("turns two Alt+clicks into a list whose selectors select the list", async () => {
    await open();
    await click("#list .t", 0, 1 /* Alt */);
    await click("#list .t", 2, 1);
    const events = (await sent()).map((m) => m.event);
    assert.deepEqual(events.map((e) => e.type), ["extract", "extract"]);
    const x = events[1].extract;
    assert.equal(x.list, true);
    assert.deepEqual(x.samples, ["One", "Two", "Three"]);
    const n = await browser.evaluate(
      `document.querySelectorAll(${JSON.stringify(`${x.containerSelector} > ${x.itemSelector} > ${x.fieldSelector}`)}).length`
    );
    assert.equal(n, 3);
  });
});
