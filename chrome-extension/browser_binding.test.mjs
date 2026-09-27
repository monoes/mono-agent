// Tests for which monoagent profile this browser runs automations for.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoBrowserBinding: B } = loadExtensionScripts(["browser_binding.js"]);

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const out = {};
      for (const k of Array.isArray(keys) ? keys : [keys]) if (k in data) out[k] = data[k];
      return out;
    },
    set: async (values) => Object.assign(data, values),
  };
}

let n = 0;
const fakeCrypto = { randomUUID: () => `0000000${++n}-0000-4000-8000-000000000000` };
const EDGE_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36 Edg/140.0";

test("the worker creates an instance id once and keeps it", async () => {
  const storage = fakeStorage();
  const first = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  const again = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.match(first.instance, /^[A-Za-z0-9-]{8,64}$/);
  assert.equal(again.instance, first.instance);
  assert.equal(storage.data[B.INSTANCE_KEY], first.instance);
});

test("the side panel never mints an id (only the worker does)", async () => {
  const storage = fakeStorage();
  const state = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA });
  assert.equal(state.instance, "");
  assert.equal(B.INSTANCE_KEY in storage.data, false);
});

test("a corrupt stored id is replaced", async () => {
  const storage = fakeStorage({ [B.INSTANCE_KEY]: "../x" });
  const state = await B.load(storage, { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.notEqual(state.instance, "../x");
});

test("default label names the browser", async () => {
  const state = await B.load(fakeStorage(), { crypto: fakeCrypto, userAgent: EDGE_UA, create: true });
  assert.match(state.label, /^Edge /);
});

test("auth fields leave out an empty profile", () => {
  const f = B.authFields({ instance: "abcdefgh-1", profile: "", label: "Edge" }, "1.5.0");
  assert.deepEqual(f, { instance: "abcdefgh-1", label: "Edge", version: "1.5.0" });
  const g = B.authFields({ instance: "abcdefgh-1", profile: "p-work", label: "Edge" }, "1.5.0");
  assert.equal(g.profile, "p-work");
});

test("setBinding stores a valid profile and a trimmed label", async () => {
  const storage = fakeStorage();
  const out = await B.setBinding(storage, { profile: " p-work ", label: "  Edge   Work " });
  assert.deepEqual(out, { profile: "p-work", label: "Edge Work" });
  assert.equal(storage.data[B.PROFILE_KEY], "p-work");
});

test("setBinding refuses a path-like profile and keeps the old one", async () => {
  const storage = fakeStorage({ [B.PROFILE_KEY]: "p-home" });
  await assert.rejects(() => B.setBinding(storage, { profile: "../etc" }), /invalid profile/);
  assert.equal(storage.data[B.PROFILE_KEY], "p-home");
});

test("unbinding stores an empty profile and leaves the label alone", async () => {
  const storage = fakeStorage({ [B.PROFILE_KEY]: "p-home", [B.LABEL_KEY]: "Mine" });
  await B.setBinding(storage, { profile: "" });
  assert.equal(storage.data[B.PROFILE_KEY], "");
  assert.equal(storage.data[B.LABEL_KEY], "Mine");
});

test("options: 'Any profile' first, the bound one selected, an unknown bound id kept", () => {
  const profiles = [{ id: "p-home", name: "Personal" }, { id: "p-work", name: "Work" }];
  const opts = B.options(profiles, "p-work");
  assert.equal(opts[0].id, "");
  assert.equal(opts.find((o) => o.selected).id, "p-work");
  const stale = B.options(profiles, "p-gone");
  assert.equal(stale.find((o) => o.selected).id, "p-gone");
  assert.match(stale.find((o) => o.id === "p-gone").name, /no longer exists/);
});

test("binding frame and change detection", () => {
  assert.deepEqual(B.bindingFrame({ profile: "p-work", label: "Edge" }), { kind: "binding", profile: "p-work", label: "Edge" });
  assert.equal(B.isBindingChange({ [B.PROFILE_KEY]: {} }, "local"), true);
  assert.equal(B.isBindingChange({ [B.LABEL_KEY]: {} }, "local"), true);
  assert.equal(B.isBindingChange({ captureProfile: {} }, "local"), false);
  assert.equal(B.isBindingChange({ [B.PROFILE_KEY]: {} }, "session"), false);
});
