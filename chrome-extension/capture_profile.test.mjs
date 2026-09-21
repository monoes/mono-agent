// Tests for the profile a capture is saved into: the sticky choice, the
// per-save override, and every way the answer can be "just save it".
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoCaptureProfile: P } = loadExtensionScripts(["capture_profile.js"]);

function fakeStorage(seed = {}) {
  const data = Object.assign({}, seed);
  return {
    data,
    get: async (keys) => {
      const wanted = Array.isArray(keys) ? keys : [keys];
      const out = {};
      for (const k of wanted) if (k in data) out[k] = data[k];
      return out;
    },
    set: async (values) => Object.assign(data, values),
  };
}

/** brokenStorage is a private window: every call throws. */
const brokenStorage = {
  get: async () => {
    throw new Error("no storage");
  },
  set: async () => {
    throw new Error("no storage");
  },
};

const PROFILES = [
  { id: "p-home", name: "Personal", default: false },
  { id: "p-work", name: "Work", default: true },
];

/** fakeAsk stands in for MonoAsk. */
function fakeAsk(reply, opts = {}) {
  const calls = [];
  return {
    calls,
    supports: async () => opts.supports !== false,
    request: async (method, params, options) => {
      calls.push({ method, params, options });
      if (opts.fail) throw Object.assign(new Error("offline"), { code: "offline" });
      return reply;
    },
  };
}

// --- the id is a directory name on the other side -------------------------

test("a profile id that could escape the profiles root is refused", () => {
  for (const id of ["../evil", "../../etc/passwd", "a/b", "a\\b", "..", "a..b", "", "   ", null, undefined, 7, {}]) {
    assert.equal(P.isValidProfileId(id), false, `accepted ${JSON.stringify(id)}`);
  }
  for (const id of ["default", "p-work", "org_1", "a.b", "6f1c2f18-0e5b-4a4c-8f3a-8a2d9a1b0c11"]) {
    assert.equal(P.isValidProfileId(id), true, `rejected ${id}`);
  }
});

test("a hostile id never reaches the envelope", () => {
  assert.deepEqual(P.applyToMeta({ url: "https://e.com" }, "../evil"), { url: "https://e.com" });
  assert.deepEqual(P.applyToMeta({ url: "https://e.com" }, "p-work"), {
    url: "https://e.com",
    profile: "p-work",
  });
  // An earlier stamp is removed rather than left behind when the choice
  // becomes "no profile".
  assert.deepEqual(P.applyToMeta({ profile: "p-work" }, ""), {});
});

test("a list from the backend is filtered, not trusted", () => {
  const got = P.normalizeProfiles([
    { id: "../evil", name: "Evil" },
    { id: "p-work", name: "  Work  ", default: true },
    { id: "p-work", name: "Duplicate" },
    { id: "p-home" },
    "nonsense",
    null,
  ]);
  assert.deepEqual(got, [
    { id: "p-work", name: "Work", default: true },
    { id: "p-home", name: "p-home", default: false },
  ]);
});

// --- choosing -------------------------------------------------------------

test("with nothing stored, the default profile is chosen", () => {
  const got = P.choose("", PROFILES);
  assert.equal(got.id, "p-work");
  assert.equal(got.changed, false);
});

test("a stored profile that still exists is kept, silently", () => {
  const got = P.choose("p-home", PROFILES);
  assert.deepEqual([got.id, got.name, got.changed], ["p-home", "Personal", false]);
});

test("a stored profile that is gone falls back to the default, and says so", () => {
  const got = P.choose("p-deleted", PROFILES);
  assert.equal(got.id, "p-work");
  assert.equal(got.changed, true);
  assert.match(got.reason, /gone/);
  assert.match(got.reason, /Work/);
});

test("no profiles configured means no profile on the capture", () => {
  const got = P.choose("", []);
  assert.deepEqual([got.id, got.changed], ["", false]);
});

test("with the bridge down the remembered choice is used as-is", () => {
  const got = P.choose("p-home", [], { offline: true });
  assert.deepEqual([got.id, got.changed], ["p-home", false]);
  // …and with nothing remembered, the capture is simply unprofiled.
  assert.equal(P.choose("", [], { offline: true }).id, "");
});

test("a stored id that is not usable is treated as nothing stored", () => {
  assert.equal(P.choose("../evil", PROFILES).id, "p-work");
  assert.equal(P.choose("../evil", PROFILES).changed, false);
  assert.equal(P.choose("../evil", [], { offline: true }).id, "");
});

// --- storage --------------------------------------------------------------

test("the choice is remembered, and a hostile one is not", async () => {
  const storage = fakeStorage();
  assert.equal(await P.remember(storage, "p-work"), "p-work");
  assert.equal(storage.data[P.PROFILE_KEY], "p-work");
  assert.equal(await P.remember(storage, "../evil"), "");
  assert.equal(storage.data[P.PROFILE_KEY], "");
});

test("a profile-less browser still captures", async () => {
  assert.deepEqual(await P.load(brokenStorage), { profile: "", profiles: [], asked: false });
  assert.equal(await P.remember(brokenStorage, "p-work"), "p-work");
  assert.equal(await P.sticky(brokenStorage), "");
});

// --- asking the backend ---------------------------------------------------

test("profile.list fills the picker", async () => {
  const ask = fakeAsk({ profiles: PROFILES, default: "p-work" });
  const got = await P.fetchProfiles(ask);
  assert.equal(got.offline, false);
  assert.deepEqual(got.profiles.map((p) => p.id), ["p-home", "p-work"]);
  assert.equal(ask.calls[0].method, "profile.list");
  assert.equal(ask.calls[0].options.timeoutMs, P.LIST_TIMEOUT_MS);
});

test("a bridge that is down, old or silent is 'offline', never a failure", async () => {
  assert.deepEqual(await P.fetchProfiles(fakeAsk(null, { fail: true })), { profiles: [], offline: true });
  assert.deepEqual(await P.fetchProfiles(fakeAsk(null, { supports: false })), { profiles: [], offline: true });
  assert.deepEqual(await P.fetchProfiles(null), { profiles: [], offline: true });
  assert.deepEqual(await P.fetchProfiles({}), { profiles: [], offline: true });
});

test("refresh caches the list and settles the choice", async () => {
  const storage = fakeStorage();
  const got = await P.refresh(fakeAsk({ profiles: PROFILES }), storage);
  assert.equal(got.id, "p-work");
  assert.equal(got.offline, false);
  assert.deepEqual(storage.data[P.PROFILES_KEY].map((p) => p.id), ["p-home", "p-work"]);
  // The fallback is remembered, so the next shortcut agrees with the popup.
  assert.equal(storage.data[P.PROFILE_KEY], "p-work");
});

test("refresh over a dead bridge draws from the cache and keeps the choice", async () => {
  const storage = fakeStorage({
    [P.PROFILE_KEY]: "p-home",
    [P.PROFILES_KEY]: PROFILES,
  });
  const got = await P.refresh(fakeAsk(null, { fail: true }), storage);
  assert.equal(got.offline, true);
  assert.equal(got.id, "p-home");
  assert.deepEqual(got.profiles.map((p) => p.id), ["p-home", "p-work"]);
});

test("refresh reports a stored profile that has since been deleted", async () => {
  const storage = fakeStorage({ [P.PROFILE_KEY]: "p-deleted" });
  const got = await P.refresh(fakeAsk({ profiles: PROFILES }), storage);
  assert.equal(got.id, "p-work");
  assert.equal(got.changed, true);
  // And it is remembered, so the shortcut does not keep using the ghost.
  assert.equal(storage.data[P.PROFILE_KEY], "p-work");
});

// --- the shortcut ---------------------------------------------------------

test("the shortcut path reads storage alone — zero interaction, no bridge", async () => {
  const storage = fakeStorage({ [P.PROFILE_KEY]: "p-home", [P.PROFILES_KEY]: PROFILES });
  assert.equal(await P.sticky(storage), "p-home");

  // A remembered profile that no longer exists still resolves to something
  // saveable rather than blocking the capture.
  const stale = fakeStorage({ [P.PROFILE_KEY]: "p-deleted", [P.PROFILES_KEY]: PROFILES });
  assert.equal(await P.sticky(stale), "p-work");

  // Nothing remembered and nothing cached: an unprofiled capture, exactly
  // as before this feature existed.
  assert.equal(await P.sticky(fakeStorage()), "");
});

test("the first shortcut capture on a fresh browser asks once, then never again", async () => {
  const storage = fakeStorage();
  const ask = fakeAsk({ profiles: PROFILES });

  // Nothing stored and nobody has ever asked: this one capture finds out.
  assert.equal(await P.stickyOrAsk(ask, storage, true), "p-work");
  assert.equal(ask.calls.length, 1);

  // From here it is storage alone, whatever else happens.
  assert.equal(await P.stickyOrAsk(ask, storage, true), "p-work");
  assert.equal(ask.calls.length, 1);
});

test("an install with no profiles asks once and then stops asking", async () => {
  const storage = fakeStorage();
  const ask = fakeAsk({ profiles: [] });

  assert.equal(await P.stickyOrAsk(ask, storage, true), "");
  assert.equal(await P.stickyOrAsk(ask, storage, true), "");
  assert.equal(ask.calls.length, 1, "an empty list is an answer, not a reason to keep asking");
});

test("a disconnected bridge is never asked, and never blocks the capture", async () => {
  const ask = fakeAsk(null, { fail: true });
  assert.equal(await P.stickyOrAsk(ask, fakeStorage(), false), "");
  assert.equal(ask.calls.length, 0);
  // Connected but broken is still just "no profile".
  assert.equal(await P.stickyOrAsk(ask, fakeStorage(), true), "");
});

test("a remembered choice is never re-litigated with the backend", async () => {
  const ask = fakeAsk({ profiles: PROFILES });
  const storage = fakeStorage({ [P.PROFILE_KEY]: "p-home" });
  assert.equal(await P.stickyOrAsk(ask, storage, true), "p-home");
  assert.equal(ask.calls.length, 0);
});
