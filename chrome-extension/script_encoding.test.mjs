// Chrome refuses to inject a script file it does not consider UTF-8
// ("Could not load file 'x.js'. It isn't UTF-8 encoded."), and its check
// (base::IsStringUTF8) also rejects Unicode noncharacters such as U+FFFF,
// which are valid UTF-8 to Node and to every editor. One raw U+FFFF in a
// regex made the recorder fail to inject on every page in a real browser
// while all the node tests passed, so every script the extension ships is
// checked here with Chrome's rule. Write such characters as \uXXXX escapes.

import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const DIR = dirname(fileURLToPath(import.meta.url));

/** isNoncharacter matches the code points base::IsStringUTF8 refuses. */
function isNoncharacter(cp) {
  return (cp >= 0xfdd0 && cp <= 0xfdef) || (cp & 0xfffe) === 0xfffe;
}

/** problems returns "line:col U+XXXX" for every byte sequence Chrome rejects. */
function problems(buf) {
  const out = [];
  let text;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(buf);
  } catch {
    return ["not valid UTF-8"];
  }
  let line = 1;
  let col = 0;
  for (const ch of text) {
    const cp = ch.codePointAt(0);
    col++;
    if (cp === 0x0a) {
      line++;
      col = 0;
    } else if (isNoncharacter(cp)) {
      out.push(`${line}:${col} U+${cp.toString(16).toUpperCase()}`);
    }
  }
  return out;
}

test("the checker flags what Chrome flags", () => {
  assert.deepEqual(problems(Buffer.from("const a = /[ -￿]/;\n")), ["1:15 U+FFFF"]);
  assert.deepEqual(problems(Buffer.from("x\n﷐")), ["2:1 U+FDD0"]);
  assert.deepEqual(problems(Buffer.from([0x61, 0xff])), ["not valid UTF-8"]);
  assert.deepEqual(problems(Buffer.from("const a = /[\\u00a0-\\uffff]/; // — é ✓\n")), []);
});

test("every extension script is loadable by Chrome", () => {
  const files = readdirSync(DIR).filter((f) => f.endsWith(".js"));
  assert.ok(files.includes("recorder_selectors.js"), "expected to scan the recorder scripts");
  const bad = [];
  for (const f of files) {
    for (const p of problems(readFileSync(join(DIR, f)))) bad.push(`${f} ${p}`);
  }
  assert.deepEqual(bad, [], "Chrome would refuse to load these scripts; use \\uXXXX escapes");
});
