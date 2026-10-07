// The worker's wiring, read from background.js (task board, spec 11). Every
// other test drives the task bridge directly, so a missing importScripts,
// install or connected() call would pass them all. Crude on purpose.
// `CHROME_PATH=/nonexistent node --test chrome-extension/task_wiring.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));
const background = readFileSync(join(HERE, "background.js"), "utf8");
const count = (needle) => background.split(needle).length - 1;

test("the worker loads the task outbox and bridge after ask.js", () => {
  const ask = background.indexOf('importScripts("ask.js"');
  const tasks = background.indexOf('importScripts("task_outbox.js", "task_bridge.js"');
  assert.ok(ask !== -1 && tasks > ask, "task_outbox.js and task_bridge.js are not imported after ask.js");
});

test("the worker installs the task bridge once, after the recall group installed ask.js", () => {
  assert.equal(count("MonoTaskBridge.install("), 1);
  assert.ok(background.indexOf("MonoTaskBridge.install(") > background.indexOf("MonoRecall.install("));
});

test("the worker sends what waited when the socket opens", () => {
  const start = background.indexOf("ws.onopen = () => {");
  const end = background.indexOf("ws.onmessage = ", start);
  assert.ok(start !== -1 && end > start, "ws.onopen not found");
  assert.match(background.slice(start, end), /MonoTaskBridge\.connected\(\);/);
});
