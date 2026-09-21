// Shared harness for the extension's node tests.
//
// The extension's scripts are plain (non-module) scripts — that is what an
// MV3 service worker's importScripts and chrome.scripting.executeScript both
// want, and what `node --check` in CI validates. Each one is an IIFE that
// hangs its namespace off `globalThis`, so a test loads it by calling it with
// a sandbox object in place of the real global and reading back what it
// defined.
//
// Deliberately not node:vm (which pair_bridge.test.mjs needs, to shadow
// `location` and `fetch`): a vm context is a separate realm, and objects
// built there fail assert.deepEqual against plain host objects for no reason
// a reader of the test would ever guess.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));

/**
 * loadExtensionScripts evaluates the named extension scripts, in order,
 * against one shared fake global and returns it. `extras` seeds that global
 * with whatever the scripts under test reach for (a fake `chrome`, say).
 *
 * Note what `extras` does and does not do. Each key is bound as a parameter,
 * so a script's bare `chrome` finds the fake — but ONLY the names passed are
 * shadowed. Every other free reference resolves against node's own globals,
 * which is why a script reaching for `crypto`, `URL`, `TextEncoder` or
 * `btoa` works here without anyone arranging it: node's are faithful enough
 * to stand in for a worker's. The trap is the browser-only ones. A test that
 * means to fake `document` or `location` and forgets to pass them does not
 * fail loudly — it gets a ReferenceError from somewhere unrelated, or worse,
 * node's `navigator`. Pass what the script touches, explicitly.
 *
 * What still has no coverage, and why: capture_page.js's `prepare` and
 * `extract`. Both drive a live DOM — querySelectorAll over every element,
 * getComputedStyle, IntersectionObserver, and MonoReadable.snapshot walking
 * real nodes — so faking them by hand would mean writing enough of a DOM
 * that the fake, not the code, is what the test proves. That needs either a
 * dependency (jsdom) or the browser harness browser_harness.mjs already sets
 * up for highlight_page.browser.test.mjs, which is the cheaper of the two
 * and where those two functions belong. capture_page.test.mjs covers the
 * rest.
 */
export function loadExtensionScripts(files, extras = {}) {
  const sandbox = Object.assign({}, extras);
  // Each extra is also bound as a parameter, so a script that reaches for a
  // bare `chrome` gets the fake one rather than the host's (missing) global.
  const names = Object.keys(extras);
  const values = names.map((n) => extras[n]);
  for (const file of files) {
    const source = readFileSync(join(HERE, file), "utf8");
    // eslint-disable-next-line no-new-func
    new Function("globalThis", ...names, `${source}\n//# sourceURL=${file}`)(sandbox, ...values);
  }
  return sandbox;
}
