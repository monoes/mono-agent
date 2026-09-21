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
