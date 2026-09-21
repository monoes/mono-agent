// RCL-02 — "you've already saved this", the part that decides when to ask.
//
// The badge itself is one line of Chrome API. What is worth testing is
// everything around it: that a navigation storm costs one lookup, that a tab
// switch to a known page costs none, that a page the pipeline could never
// have captured costs none, and that a backend which is down leaves no badge
// rather than a wrong one.
//
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

/** harness wires MonoSaved to a scripted backend and a recording badge. */
function harness(answers = {}) {
  const { MonoSaved } = loadExtensionScripts(["saved.js"]);
  const asked = [];
  const painted = [];
  let clock = 1_000_000;
  let fail = null;

  MonoSaved.install({
    ask: async (method, params) => {
      asked.push({ method, url: params.url });
      if (fail) throw Object.assign(new Error(fail.message), { code: fail.code });
      const answer = answers[params.url];
      return answer || { saved: false, url: params.url };
    },
    badge: (spec) => painted.push(spec),
    now: () => clock,
  });

  return {
    MonoSaved,
    asked,
    painted,
    advance: (ms) => (clock += ms),
    breakBackend: (message, code) => (fail = { message, code }),
    fixBackend: () => (fail = null),
    lastBadge: () => painted[painted.length - 1],
  };
}

const SAVED = {
  saved: true,
  url: "https://example.com/post",
  title: "Sprocket Calibration",
  site: "example.com",
  capturedAt: "2026-09-20T09:00:00.000Z",
  versions: 3,
  note: "torque figures worth keeping",
  tags: ["mechanics", "reading"],
  collection: "bench",
  path: "/inbox/2026-09-20T09-00-00Z-post/readable.md",
  envelope: "/inbox/2026-09-20T09-00-00Z-post",
};

test("a saved page badges the tab and carries its record", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  const { record, source } = await h.MonoSaved.check(7, "https://example.com/post", {
    debounceMs: 0,
  });

  assert.equal(source, "backend");
  assert.equal(record.saved, true);
  assert.equal(record.versions, 3);
  assert.equal(record.note, "torque figures worth keeping");
  assert.deepEqual(record.tags, ["mechanics", "reading"]);
  assert.equal(record.envelope, "/inbox/2026-09-20T09-00-00Z-post");

  const badge = h.lastBadge();
  assert.equal(badge.tabId, 7);
  assert.equal(badge.text, "✓");
  assert.match(badge.title, /3 versions/);
  assert.match(badge.title, /2026-09-20/);
});

test("an unsaved page leaves no badge", async () => {
  const h = harness();

  const { record } = await h.MonoSaved.check(7, "https://example.com/never", { debounceMs: 0 });
  assert.equal(record.saved, false);
  assert.equal(h.lastBadge().text, "");
});

test("the tab's URL is asked about without its fragment", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  const { record } = await h.MonoSaved.check(7, "https://example.com/post#section-3", {
    debounceMs: 0,
  });
  assert.equal(record.saved, true);
  assert.deepEqual(
    h.asked.map((a) => a.url),
    ["https://example.com/post"],
    "#section-3 is a scroll position, not a different document"
  );
});

test("a redirect chain costs one lookup, for the URL the tab settles on", async () => {
  const h = harness({ "https://example.com/final": SAVED });

  const first = h.MonoSaved.check(7, "https://example.com/a", { debounceMs: 20 });
  const second = h.MonoSaved.check(7, "https://example.com/b", { debounceMs: 20 });
  const last = h.MonoSaved.check(7, "https://example.com/final", { debounceMs: 20 });

  const results = await Promise.all([first, second, last]);
  // The superseded calls SETTLE — an abandoned promise on the navigation
  // path is a panel that spins forever.
  assert.deepEqual(
    results.map((r) => r.source),
    ["superseded", "superseded", "backend"]
  );
  assert.deepEqual(
    h.asked.map((a) => a.url),
    ["https://example.com/final"],
    "only the settled URL is worth a Node process"
  );
});

test("a second visit inside the TTL is answered from cache", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  assert.equal(h.asked.length, 1);

  // A different tab, same page — the cache is keyed by URL, which is the
  // case that actually happens.
  const again = await h.MonoSaved.check(9, "https://example.com/post", { debounceMs: 0 });
  assert.equal(again.source, "cache");
  assert.equal(again.record.saved, true);
  assert.equal(h.asked.length, 1);
  assert.equal(h.lastBadge().tabId, 9, "the cached answer still paints the new tab");
});

test("the cache expires", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  h.advance(h.MonoSaved.CACHE_TTL_MS + 1);
  const again = await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });

  assert.equal(again.source, "backend");
  assert.equal(h.asked.length, 2);
});

test("the cache is bounded", async () => {
  const h = harness();

  for (let i = 0; i < h.MonoSaved.MAX_CACHE + 25; i++) {
    await h.MonoSaved.check(1, `https://example.com/p${i}`, { debounceMs: 0 });
  }
  assert.equal(h.MonoSaved.cacheSize(), h.MonoSaved.MAX_CACHE);
});

test("a page the pipeline could never have captured is never asked about", async () => {
  const h = harness();

  for (const url of ["chrome://newtab", "about:blank", "file:///etc/hosts", "", "chrome-extension://abc/popup.html"]) {
    const { source, record } = await h.MonoSaved.check(7, url, { debounceMs: 0 });
    assert.equal(source, "skipped", url);
    assert.equal(record.saved, false, url);
  }
  assert.equal(h.asked.length, 0);
  assert.equal(h.lastBadge().text, "");
});

test("a backend that is down leaves no badge and says why", async () => {
  const h = harness();
  h.breakBackend("the monoagent bridge is not connected", "offline");

  const { record, source } = await h.MonoSaved.check(7, "https://example.com/post", {
    debounceMs: 0,
  });

  assert.equal(source, "offline");
  assert.equal(record.saved, false);
  assert.equal(record.unavailable, true, "the popup needs to know this was not an answer");
  assert.match(record.reason, /not connected/);
  assert.equal(h.lastBadge().text, "", "a wrong badge is worse than no badge");
});

test("a failed lookup is not cached, so the next visit tries again", async () => {
  const h = harness({ "https://example.com/post": SAVED });
  h.breakBackend("monomind not found", "unavailable");

  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  h.fixBackend();
  const again = await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });

  assert.equal(again.source, "backend");
  assert.equal(again.record.saved, true);
});

test("a backend answering nonsense is treated as no answer, not believed", async () => {
  const { MonoSaved } = loadExtensionScripts(["saved.js"]);
  const painted = [];
  MonoSaved.install({ ask: async () => "surprise!", badge: (s) => painted.push(s) });

  const { record } = await MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  assert.equal(record.saved, false);
  assert.equal(painted[painted.length - 1].text, "");
});

test("an answer that arrives after the tab moved on does not paint the new page", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  const stale = h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 5 });
  // The tab navigates away before the debounce fires.
  const fresh = h.MonoSaved.check(7, "https://example.com/other", { debounceMs: 5 });

  const staleResult = await stale;
  await fresh;
  assert.equal(staleResult.source, "superseded");
  assert.equal(h.lastBadge().text, "", "the new page is not saved, whatever the old one was");
});

test("the popup reads a tab's record without asking anything", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  assert.equal(h.MonoSaved.get(7), null);
  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });

  const before = h.asked.length;
  assert.equal(h.MonoSaved.get(7).saved, true);
  assert.equal(h.asked.length, before, "opening the popup is not a navigation");
});

test("capturing this page re-asks and does not serve the old answer", async () => {
  const answers = { "https://example.com/post": { saved: false, url: "https://example.com/post" } };
  const h = harness(answers);

  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  assert.equal(h.MonoSaved.get(7).saved, false);

  // The capture lands; the stored answer is now wrong.
  answers["https://example.com/post"] = SAVED;
  const { record } = await h.MonoSaved.capturedNow(7, "https://example.com/post", 0);

  assert.equal(record.saved, true);
  assert.equal(h.lastBadge().text, "✓");
  // The badge was cleared first, so the capture's own transient badge had
  // its moment before this one took over.
  assert.equal(h.painted[h.painted.length - 2].text, "");
});

test("forgetting a tab drops its state", async () => {
  const h = harness({ "https://example.com/post": SAVED });

  await h.MonoSaved.check(7, "https://example.com/post", { debounceMs: 0 });
  h.MonoSaved.forget(7);
  assert.equal(h.MonoSaved.get(7), null);
});

test("identityUrl matches what the ingest side keys on", () => {
  const { MonoSaved } = loadExtensionScripts(["saved.js"]);
  const cases = {
    "https://example.com/post#section-3": "https://example.com/post",
    "https://example.com/post?ref=hn": "https://example.com/post?ref=hn",
    "https://example.com/post/": "https://example.com/post/",
    "  https://example.com/post  ": "https://example.com/post",
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(MonoSaved.identityUrl(input), want, input);
  }
});
