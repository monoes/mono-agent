// Chrome's script loader refuses a file that is not clean UTF-8 ("Could not
// load file … It isn't UTF-8 encoded"), and it treats Unicode noncharacters
// (U+FDD0..U+FDEF, U+xFFFE/U+xFFFF) as not UTF-8. recorder_selectors.js
// once had a raw U+FFFF inside a regex class: every node test passed, since
// node evaluates the source happily, and the recorder never ran in Chrome.
//
// So this checks the bytes, for every script the extension ships:
//   - valid UTF-8, no noncharacters, anywhere;
//   - the scripts chrome.scripting injects into pages for the recorder are
//     plain ASCII — escapes (\uXXXX) in code, ASCII in comments.

import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));

function scripts(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...scripts(path));
    // What Chrome loads. Node test files (.mjs) are never shipped, and may
    // hold noncharacters on purpose as test data (script_encoding.test.mjs).
    else if (/\.(js|html|css|json)$/.test(name)) out.push(path);
  }
  return out;
}

const isNonCharacter = (cp) => (cp >= 0xfdd0 && cp <= 0xfdef) || (cp & 0xfffe) === 0xfffe;

test("every extension file is valid UTF-8 with no noncharacters", () => {
  const files = scripts(HERE);
  assert.ok(files.length > 20);
  for (const path of files) {
    const name = relative(HERE, path);
    let text;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(readFileSync(path));
    } catch {
      assert.fail(`${name} is not valid UTF-8`);
    }
    let line = 1;
    for (const ch of text) {
      if (ch === "\n") line++;
      const cp = ch.codePointAt(0);
      assert.ok(!isNonCharacter(cp), `${name}:${line} has noncharacter U+${cp.toString(16).toUpperCase()} — write it as an escape`);
    }
  }
});

test("the recorder's page scripts are plain ASCII", () => {
  const { MonoRecorderWiring } = loadWiring();
  // Plus the element picker, injected by background.js's pick_element.
  for (const file of MonoRecorderWiring.RECORDER_FILES.concat(["recorder_picker.js"])) {
    const bytes = readFileSync(join(HERE, file));
    const at = bytes.findIndex((b) => b > 0x7f);
    if (at !== -1) {
      const line = bytes.subarray(0, at).toString("latin1").split("\n").length;
      assert.fail(`${file}:${line} has a non-ASCII byte — use a \\u escape`);
    }
  }
});

function loadWiring() {
  // RECORDER_FILES is the list chrome.scripting injects; read it rather than
  // repeat it, so a new page script is covered the day it is added.
  const src = readFileSync(join(HERE, "recorder_wiring.js"), "utf8");
  const g = {};
  new Function("globalThis", src)(g);
  return g;
}
