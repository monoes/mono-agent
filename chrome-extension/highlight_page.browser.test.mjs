/**
 * highlight_page.js, in a real browser.
 *
 * Everything else in this directory is tested against plain objects, which
 * is enough for the modules that only shuffle data. It is not enough here:
 * this file's whole job is to measure a live selection, split text nodes and
 * put <mark> elements around them without damaging the page. A DOM shim
 * would agree with whatever the code did.
 *
 * So these tests drive a headless Chrome over CDP: a real press-drag-release
 * to make the selection, a real click on the panel it offers, a real reload
 * to check the highlight comes back. The service worker is the one thing
 * stubbed — `chrome.runtime.sendMessage` is answered in-page by the real
 * MonoHighlights store over an in-memory bag, replying with the same shapes
 * recall_bridge.js does over chrome.storage.local.
 *
 * WITHOUT CHROME the whole suite skips, reporting as skipped rather than
 * failed, so a machine (or CI image) with no browser stays green.
 */

import { after, describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fileUrl, findChrome, launch } from "./browser_harness.mjs";
import { AWKWARD, FIXTURE, HELPERS } from "./highlight_page.fixtures.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const CHROME = findChrome();

// The browser starts before the suite is declared, so that a machine which
// HAS Chrome but cannot run it (a container with no user namespaces, say)
// skips with the reason printed rather than failing every test in turn.
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

// A root hook, so the browser is closed even when the suite never runs.
after(async () => {
  if (browser) await browser.close();
  if (fixtures) await rm(fixtures, { recursive: true, force: true }).catch(() => {});
});

const collapse = (s) => String(s || "").replace(/\s+/g, " ").trim();

describe("highlight_page.js in a browser", { skip, concurrency: 1 }, () => {
  let page;
  let url;
  let bootstrapSource;
  let injected = null;

  // Not a `before` hook: it has to run inside the first test so a skipped
  // suite never touches the disk.
  async function prepare() {
    if (bootstrapSource) return;
    fixtures = await mkdtemp(join(tmpdir(), "mono-hl-fixture-"));
    page = join(fixtures, "fixture.html");
    url = fileUrl(page);
    const highlights = await readFile(join(HERE, "highlights.js"), "utf8");
    const highlighter = await readFile(join(HERE, "highlight_page.js"), "utf8");
    // The worker, replaced by the real store over an in-memory bag. SEED is
    // substituted per navigation so a reload keeps what was saved.
    const worker = `
      (function () {
        const mem = new Map(JSON.parse(__SEED__));
        const storage = {
          get: (k) => Promise.resolve(mem.has(k) ? { [k]: mem.get(k) } : {}),
          set: (obj) => { for (const k of Object.keys(obj)) mem.set(k, obj[k]); return Promise.resolve(); },
          remove: (k) => { mem.delete(k); return Promise.resolve(); },
        };
        window.__dump = () => JSON.stringify([...mem]);
        const H = globalThis.MonoHighlights;
        // The replies are the shapes recall_bridge.js really sends back.
        async function handle(msg) {
          if (msg.type === "highlight_list") {
            return { ok: true, records: await H.load(storage, msg.url) };
          }
          if (msg.type === "highlight_add") {
            const { record, records } = await H.add(storage, msg.url, msg.highlight);
            return { ok: !!record, record, count: records.length };
          }
          if (msg.type === "highlight_update") {
            const { record } = await H.update(storage, msg.url, msg.id, msg.patch);
            return { ok: !!record, record };
          }
          if (msg.type === "highlight_remove") {
            const { removed, records } = await H.remove(storage, msg.url, msg.id);
            return { ok: removed, count: records.length };
          }
          return null;
        }
        window.chrome = {
          runtime: {
            lastError: undefined,
            sendMessage(msg, cb) { handle(msg).then((r) => cb(r), () => cb(null)); },
            onMessage: { addListener(fn) { window.__onMessage = fn; } },
          },
        };
      })();
    `;
    bootstrapSource = `${highlights}\n${worker}\n${highlighter}`;
  }

  /** open (re)writes the fixture, seeds the stub store, and loads the page. */
  async function open({ html = FIXTURE, seed = "[]", reload = false } = {}) {
    await prepare();
    await writeFile(page, html, "utf8");
    if (injected) await browser.removeNewDocumentScript(injected);
    injected = await browser.onNewDocument(bootstrapSource.replace("__SEED__", JSON.stringify(seed)));
    if (reload) await browser.reload();
    else await browser.navigate(url);
    await browser.evaluate("document.fonts.ready.then(() => true)");
    await browser.evaluate(HELPERS);
  }

  /** select drags across the given character offsets and returns what the
   *  browser actually selected — the tests assert against that, not against
   *  what the drag was aiming at. */
  async function select(s1, o1, s2, o2) {
    // A press that starts inside an existing selection is a drag-and-drop,
    // not a new selection — so the last one has to go first.
    await browser.evaluate("window.getSelection().removeAllRanges()");
    const box = await browser.evaluate(`JSON.stringify(window.__box(${JSON.stringify(s1)},${o1},${JSON.stringify(s2)},${o2}))`);
    const { x1, y1, x2, y2 } = JSON.parse(box);
    await browser.drag(x1, y1, x2, y2);
    return browser.evaluate("window.getSelection().toString()");
  }

  /** highlight clicks the panel button whose title matches. */
  async function clickButton(match) {
    const buttons = JSON.parse(await browser.evaluate("JSON.stringify(window.__buttons())"));
    const target = buttons.find((b) => b.title.includes(match));
    assert.ok(target, `no panel button matching ${match}; saw ${JSON.stringify(buttons)}`);
    await browser.click(target.x, target.y);
    await browser.evaluate("new Promise((r) => setTimeout(r, 80))");
    return buttons;
  }

  async function marks() {
    return JSON.parse(await browser.evaluate("JSON.stringify(window.__marks())"));
  }

  it("wraps exactly the selected text in a mark", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('one').textContent");
    const selected = await select("#one", 20, "#one", 70);
    assert.ok(selected.length > 20, `selection too short: ${JSON.stringify(selected)}`);

    await clickButton("Highlight (yellow)");
    const found = await marks();
    assert.ok(found.length >= 1, "no mark was painted");
    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
    assert.ok(await browser.evaluate("window.__gapless()"), "the mark skipped part of the selection");
    assert.equal(await browser.evaluate("document.getElementById('one').textContent"), before);
  });

  it("carries a selection across inline markup without breaking the link", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('two').textContent");
    // from the middle of the second paragraph, through <em>, the <a> and <code>
    const selected = await select("#two", 5, "#two", 90);
    assert.match(selected, /emphasis/, `selection missed the <em>: ${JSON.stringify(selected)}`);
    await clickButton("Highlight (green)");

    // a mark inside each kind of inline markup the selection ran through
    for (const host of ["em", "a", "code"]) {
      const inside = await browser.evaluate(`document.querySelectorAll("${host} mark.monoagent-highlight").length`);
      assert.ok(inside >= 1, `nothing was marked inside <${host}>`);
    }
    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
    assert.ok(await browser.evaluate("window.__gapless()"), "the mark skipped part of the selection");
    assert.equal(await browser.evaluate("document.getElementById('two').textContent"), before);
  });

  it("carries a selection from one block into the next", async () => {
    await open();
    const beforeTwo = await browser.evaluate("document.getElementById('two').textContent");
    const beforeThree = await browser.evaluate("document.getElementById('three').textContent");
    const selected = await select("#two", 5, "#three", 40);
    assert.ok(collapse(selected).length > 40, `selection too short: ${JSON.stringify(selected)}`);

    await clickButton("Highlight (blue)");
    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
    assert.ok(await browser.evaluate("window.__gapless()"), "the mark skipped part of the selection");
    assert.equal(await browser.evaluate("document.getElementById('two').textContent"), beforeTwo);
    assert.equal(await browser.evaluate("document.getElementById('three').textContent"), beforeThree);
  });

  it("leaves the link and its handler intact under a highlight", async () => {
    await open();
    const href = await browser.evaluate("document.getElementById('link').href");
    await select("#two", 20, "#two", 60);
    await clickButton("Highlight (yellow)");

    assert.equal(await browser.evaluate("document.getElementById('link').href"), href);
    const at = JSON.parse(await browser.evaluate("JSON.stringify(window.__center('#link'))"));
    await browser.click(at.x, at.y);
    assert.equal(await browser.evaluate("window.__linkClicks"), 1, "the link's own handler stopped firing");
  });

  it("puts the page back exactly as it was when a highlight is removed", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('one').innerHTML");
    await select("#one", 15, "#one", 60);
    await clickButton("Highlight (pink)");
    assert.ok((await marks()).length >= 1, "nothing was painted");

    const mark = JSON.parse(await browser.evaluate("JSON.stringify(window.__center('mark.monoagent-highlight'))"));
    await browser.click(mark.x, mark.y);
    await clickButton("Remove this highlight");

    assert.deepEqual(await marks(), []);
    assert.equal(await browser.evaluate("document.getElementById('one').innerHTML"), before);
  });

  it("paints stored highlights again after a reload", async () => {
    await open();
    const selected = await select("#one", 25, "#one", 75);
    await clickButton("Highlight (yellow)");
    const painted = await browser.evaluate("window.__markText()");
    assert.equal(painted, collapse(selected));

    const seed = await browser.evaluate("window.__dump()");
    await open({ seed, reload: true });
    assert.equal(await browser.evaluate("window.__markText()"), painted, "the highlight did not come back");
  });

  it("finds its quote again when the page has moved underneath it", async () => {
    await open();
    const selected = await select("#three", 10, "#three", 60);
    await clickButton("Highlight (yellow)");
    const painted = await browser.evaluate("window.__markText()");
    assert.equal(painted, collapse(selected), "nothing was painted to begin with");
    const seed = await browser.evaluate("window.__dump()");

    // an ad above, and a reworded paragraph beside it
    const moved = FIXTURE.replace(
      '<h1 id="title">',
      '<aside><p>Subscribe to our newsletter for more of this sort of thing.</p></aside>\n<h1 id="title">'
    ).replace("The first paragraph is ordinary prose", "The opening paragraph is plain prose");
    await open({ html: moved, seed, reload: true });
    assert.equal(await browser.evaluate("window.__markText()"), painted, "relocation lost the quote");
  });

  it("paints nothing when the quote is gone", async () => {
    await open();
    const selected = await select("#three", 10, "#three", 60);
    await clickButton("Highlight (yellow)");
    assert.equal(await browser.evaluate("window.__covered()").then(collapse), collapse(selected));
    const seed = await browser.evaluate("window.__dump()");

    const gone = FIXTURE.replace(/<p id="three">[\s\S]*?<\/p>/, '<p id="three">Something else entirely.</p>');
    await open({ html: gone, seed, reload: true });
    assert.deepEqual(await marks(), [], "a highlight was painted on text that is not the quote");
  });

  it("takes a note and keeps it on the mark", async () => {
    await open();
    await select("#one", 20, "#one", 60);
    const answered = browser.answerPrompt("worth remembering");
    await clickButton("Highlight and add a note");
    await answered;
    await browser.evaluate("new Promise((r) => setTimeout(r, 120))");

    const found = await marks();
    assert.ok(found.length >= 1, "no mark after taking a note");
    assert.equal(found[0].title, "worth remembering");
    assert.match(await browser.evaluate("window.__dump()"), /worth remembering/);
  });

  it("opens and closes the panel without leaking nodes or listeners", async () => {
    await open();
    const nodes = () => browser.evaluate("document.getElementsByTagName('*').length");
    const baseline = await nodes();
    const listeners = async () => {
      const { result } = await browser.session.send("Runtime.evaluate", { expression: "document" });
      const { listeners: found } = await browser.session.send("DOMDebugger.getEventListeners", {
        objectId: result.objectId,
      });
      return found.length;
    };
    const listenersBefore = await listeners();

    for (let i = 0; i < 5; i++) {
      await select("#one", 20, "#one", 60);
      const open_ = await browser.evaluate("document.querySelectorAll('#monoagent-highlight-ui').length");
      assert.equal(open_, 1, "the panel did not open, or opened twice");
      await browser.evaluate("document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))");
      assert.equal(await browser.evaluate("document.querySelectorAll('#monoagent-highlight-ui').length"), 0);
    }
    assert.equal(await nodes(), baseline, "the panel left nodes behind");
    assert.equal(await listeners(), listenersBefore, "the panel left listeners on the document");
  });

  it("keeps significant whitespace inside a pre block", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('pre').textContent");
    const selected = await select("#pre", 2, "#pre", 26);
    assert.ok(selected.length > 5, `selection too short: ${JSON.stringify(selected)}`);
    await clickButton("Highlight (yellow)");

    assert.ok((await marks()).length >= 1, "nothing was painted inside the pre");
    assert.equal(await browser.evaluate("document.getElementById('pre').textContent"), before);
  });

  it("holds two adjacent highlights, and both come off cleanly", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('one').innerHTML");
    await select("#one", 4, "#one", 24);
    await clickButton("Highlight (yellow)");
    await select("#one", 30, "#one", 55);
    await clickButton("Highlight (green)");

    const ids = new Set((await marks()).map((m) => m.id));
    assert.equal(ids.size, 2, `expected two highlights, got ${ids.size}`);

    for (const id of ids) {
      const at = JSON.parse(
        await browser.evaluate(`JSON.stringify(window.__center('mark[data-monoagent-highlight="${id}"]'))`)
      );
      await browser.click(at.x, at.y);
      await clickButton("Remove this highlight");
    }
    assert.deepEqual(await marks(), []);
    assert.equal(await browser.evaluate("document.getElementById('one').innerHTML"), before);
  });

  it("does not paint the same highlight twice when restore runs again", async () => {
    await open();
    await select("#one", 20, "#one", 60);
    await clickButton("Highlight (yellow)");
    const once = (await marks()).length;

    await browser.evaluate(`
      new Promise((resolve) => {
        chrome.runtime.sendMessage({ type: "highlight_list", url: location.href }, (reply) => {
          window.__onMessage({ type: "highlight_restore", records: reply.records }, null, resolve);
        });
      })
    `);
    assert.equal((await marks()).length, once, "restore painted the highlight a second time");
  });

  it("keeps two blocks apart when the markup has no whitespace", async () => {
    await open({ html: AWKWARD });
    const selected = await select("#min", 10, "#min", 70);
    assert.match(selected, /minified|next block/, `unexpected selection ${JSON.stringify(selected)}`);
    await clickButton("Highlight (yellow)");
    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
    assert.ok(await browser.evaluate("window.__gapless()"), "the mark skipped part of the selection");
  });

  it("reads a line break as a space", async () => {
    await open({ html: AWKWARD });
    const selected = await select("#broken", 5, "#broken", 40);
    assert.match(selected, /break/, `the selection did not cross the <br>: ${JSON.stringify(selected)}`);
    await clickButton("Highlight (yellow)");
    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
  });

  it("does not swallow text the reader cannot see", async () => {
    await open({ html: AWKWARD });
    const selected = await select("#veiled", 5, "#veiled", 50);
    await clickButton("Highlight (yellow)");

    const painted = await browser.evaluate("window.__markText()");
    assert.ok(painted.length > 10, `nothing was painted: ${JSON.stringify(painted)}`);
    assert.ok(collapse(selected).includes(painted) || painted.includes(collapse(selected).slice(0, 20)), painted);
    assert.ok(!painted.includes("IS NOT SHOWN"), "the highlight swallowed hidden text");
    assert.equal(
      await browser.evaluate("document.querySelector('#veiled span').textContent"),
      "IS NOT SHOWN TO ANYONE",
      "the hidden span was disturbed"
    );
  });

  it("survives a second highlight overlapping the first", async () => {
    await open();
    const before = await browser.evaluate("document.getElementById('one').innerHTML");
    const text = await browser.evaluate("document.getElementById('one').textContent");
    await select("#one", 4, "#one", 45);
    await clickButton("Highlight (yellow)");
    await select("#one", 30, "#one", 70);
    await clickButton("Highlight (blue)");

    assert.equal(new Set((await marks()).map((m) => m.id)).size, 2, "the second highlight did not land");
    assert.equal(await browser.evaluate("document.getElementById('one').textContent"), text);

    // Remove whatever is under the pointer until nothing is left, which is
    // the only way a reader can undo overlapping marks.
    for (let i = 0; i < 6 && (await marks()).length; i++) {
      const at = JSON.parse(await browser.evaluate("JSON.stringify(window.__center('mark.monoagent-highlight'))"));
      await browser.click(at.x, at.y);
      await clickButton("Remove this highlight");
    }
    assert.deepEqual(await marks(), [], "a highlight could not be removed");
    assert.equal(await browser.evaluate("document.getElementById('one').innerHTML"), before);
  });

  it("highlights the selection when the worker asks it to", async () => {
    await open();
    const selected = await select("#one", 10, "#one", 50);
    // The keyboard shortcut path: the worker relays the command, there is
    // no panel and no click.
    const reply = await browser.evaluate(`
      new Promise((resolve) => window.__onMessage({ type: "highlight_selection" }, null, resolve))
    `);
    assert.deepEqual(reply, { ok: true });
    await browser.evaluate("new Promise((r) => setTimeout(r, 150))");

    assert.equal(collapse(await browser.evaluate("window.__covered()")), collapse(selected));
    assert.match(await browser.evaluate("window.__dump()"), /"color":"yellow"/);
  });

  it("says so when there is nothing selected", async () => {
    await open();
    const reply = await browser.evaluate(`
      new Promise((resolve) => window.__onMessage({ type: "highlight_selection" }, null, resolve))
    `);
    assert.deepEqual(reply, { ok: false, error: "nothing is selected" });
    assert.deepEqual(await marks(), []);
  });

  it("brings back several highlights from one paragraph in a single pass", async () => {
    await open();
    for (const [from, to, color] of [[4, 24, "yellow"], [30, 55, "green"], [62, 90, "blue"]]) {
      await select("#one", from, "#one", to);
      await clickButton(`Highlight (${color})`);
    }
    const seed = await browser.evaluate("window.__dump()");
    const stored = JSON.parse(seed).flatMap(([key, records]) => (key.startsWith("mono-hl:") ? records : []));
    assert.equal(stored.length, 3, "three highlights were not stored");

    // All three live in one text node, so painting the first one cuts the
    // node the other two were measured against.
    await open({ seed, reload: true });
    for (const record of stored) {
      const joined = JSON.parse(
        await browser.evaluate(
          `JSON.stringify(window.__marks().filter((m) => m.id === ${JSON.stringify(record.id)}).map((m) => m.text).join(""))`
        )
      );
      assert.equal(collapse(joined), record.text, `highlight ${record.id} came back on the wrong text`);
    }
  });
});
