// Tests for what the side panel says about the page it is following and the
// profile a capture will land in.
//
// The bug the tab half exists to prevent is specific to a side panel: it
// stays open while the person switches tabs, so anything it says about
// "this page" can be about a page that is no longer in front of them.
//
// `node --test 'chrome-extension/**/*.test.mjs'`

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoPanelView: V, MonoPanelStatus: S } = loadExtensionScripts([
  "sidepanel_status.js",
  "sidepanel_view.js",
]);

// ── the page ────────────────────────────────────────────────────────

test("a web page is named by its title and its site", () => {
  const view = V.describeTab({
    id: 4,
    url: "https://www.example.com/a/b?q=1#part",
    title: "  Example Domain ",
    favIconUrl: "https://www.example.com/favicon.ico",
    status: "complete",
    groupId: 9,
  });
  assert.equal(view.capturable, true);
  assert.equal(view.title, "Example Domain");
  assert.equal(view.host, "example.com");
  assert.equal(view.favicon, "https://www.example.com/favicon.ico");
  assert.equal(view.groupId, 9);
  assert.equal(view.why, "");
});

test("the page key ignores the fragment but not the tab or the query", () => {
  const a = V.describeTab({ id: 1, url: "https://x.test/p?a=1#one" });
  const b = V.describeTab({ id: 1, url: "https://x.test/p?a=1#two" });
  const c = V.describeTab({ id: 2, url: "https://x.test/p?a=1#one" });
  const d = V.describeTab({ id: 1, url: "https://x.test/p?a=2" });
  assert.equal(a.key, b.key, "scrolling to an anchor is not a new page");
  assert.notEqual(a.key, c.key, "the same page in another tab is another tab");
  assert.notEqual(a.key, d.key, "a query string distinguishes pages");
});

test("browser pages are said to be closed, not offered a button that fails", () => {
  for (const url of [
    "edge://newtab/",
    "chrome://settings",
    "about:blank",
    "chrome-extension://abc/sidepanel.html",
    "view-source:https://x.test",
    "",
  ]) {
    const view = V.describeTab({ id: 3, url, title: "" });
    assert.equal(view.capturable, false, url);
    assert.match(view.why, /closed to extensions/, url);
    assert.ok(view.title, `a title is always drawn for ${JSON.stringify(url)}`);
  }
});

test("a local file can be saved and says where it is from", () => {
  const view = V.describeTab({ id: 5, url: "file:///home/me/notes%20v2.html", title: "" });
  assert.equal(view.capturable, true);
  assert.equal(view.title, "notes v2.html");
  assert.equal(view.host, "File on this computer");
});

test("no tab at all is a state, not a crash", () => {
  const view = V.describeTab(null);
  assert.equal(view.capturable, false);
  assert.equal(view.key, "");
  assert.match(view.why, /Open a web page/);
});

test("a tab mid-navigation is described by where it is going", () => {
  const view = V.describeTab({ id: 6, url: "https://old.test/", pendingUrl: "https://new.test/", status: "loading" });
  assert.equal(view.host, "new.test");
  assert.equal(view.loading, true);
});

test("an icon from anywhere but the web or a data URL is not loaded", () => {
  const view = V.describeTab({ id: 7, url: "https://x.test/", favIconUrl: "chrome://favicon/x" });
  assert.equal(view.favicon, "");
});

test("a new URL or a finished load means asking about the page again", () => {
  assert.deepEqual(V.tabChange({ url: "https://x.test/2" }), { redraw: true, recheck: true });
  assert.deepEqual(V.tabChange({ status: "complete" }), { redraw: true, recheck: true });
  assert.deepEqual(V.tabChange({ status: "loading" }), { redraw: true, recheck: false });
  assert.deepEqual(V.tabChange({ title: "New title" }), { redraw: true, recheck: false });
  assert.deepEqual(V.tabChange({ groupId: 3 }), { redraw: true, recheck: false });
  // An audible or muted change is not the panel's business.
  assert.deepEqual(V.tabChange({ audible: true }), { redraw: false, recheck: false });
});

// ── the profile ─────────────────────────────────────────────────────

const PROFILES = [
  { id: "default", name: "Default", default: true },
  { id: "p-work", name: "work" },
];

test("with profiles to choose from, the header is a switcher showing the choice", () => {
  const view = V.describeProfiles({ profiles: PROFILES, profile: "p-work", profilesOffline: false });
  assert.equal(view.interactive, true);
  assert.equal(view.current.id, "p-work");
  assert.equal(view.current.name, "work");
  assert.equal(view.current.initial, "W");
  assert.equal(view.saveLabel, "Save to work");
  assert.equal(view.note, null);
  // Every profile, then the shared inbox as the explicit "no profile".
  assert.deepEqual(view.options.map((o) => o.id), ["default", "p-work", ""]);
  assert.equal(view.options[0].isDefault, true);
});

test("choosing no profile is shown as the shared inbox, not as nothing", () => {
  const view = V.describeProfiles({ profiles: PROFILES, profile: "", profilesOffline: false });
  assert.equal(view.current.id, "");
  assert.equal(view.current.name, V.SHARED_INBOX);
  assert.equal(view.saveLabel, "Save this page");
});

test("a remembered profile that was deleted is replaced out loud", () => {
  const view = V.describeProfiles({
    profiles: PROFILES,
    profile: "default",
    profileChanged: true,
    profileReason: "the profile you last saved into is gone — using Default",
    profilesOffline: false,
  });
  assert.equal(view.current.id, "default");
  assert.equal(view.note.tone, "warn");
  assert.equal(view.note.text, "The profile you last saved into is gone — using Default.");
});

test("with the bridge down, the cached list still works and says it is old", () => {
  const view = V.describeProfiles({ profiles: PROFILES, profile: "p-work", profilesOffline: true });
  assert.equal(view.interactive, true);
  assert.equal(view.current.id, "p-work");
  assert.match(view.note.text, /last list it gave/);
});

test("no profiles configured is the shared inbox and the command to make one", () => {
  const view = V.describeProfiles({ profiles: [], profile: "", profilesOffline: false });
  assert.equal(view.interactive, false);
  assert.equal(view.current.name, V.SHARED_INBOX);
  assert.equal(view.note.command, S.PROFILE_COMMAND);
  assert.match(view.note.text, /No profiles yet/);
});

test("offline with nothing cached keeps the remembered choice, by name when known", () => {
  const view = V.describeProfiles({ profiles: [], profile: "p-work", profileName: "work", profilesOffline: true });
  assert.equal(view.interactive, false);
  assert.equal(view.current.id, "p-work");
  assert.equal(view.current.name, "work");
  assert.equal(view.saveLabel, "Save to work");
  assert.match(view.note.text, /chose last/);
});

test("offline with nothing cached and nothing chosen says profiles come with the bridge", () => {
  const view = V.describeProfiles({ profiles: [], profile: "", profilesOffline: true });
  assert.equal(view.current.id, "");
  assert.match(view.note.text, /once the bridge is running/);
});

test("a profile's colour is its own and does not move", () => {
  const a = V.avatarHue("711ef586-9f4b-4b1f-b2fd-cad23eec0a03");
  assert.equal(a, V.avatarHue("711ef586-9f4b-4b1f-b2fd-cad23eec0a03"));
  assert.ok(a >= 0 && a < 360 && a % 30 === 0);
  assert.equal(V.avatarHue(""), -1, "the shared inbox has no colour of its own");
});

test("an avatar letter is a letter, whatever the name starts with", () => {
  assert.equal(V.initialOf("monomind"), "M");
  assert.equal(V.initialOf("  _édition"), "É");
  assert.equal(V.initialOf("--"), "?");
});

test("no two profiles in one list share a colour", () => {
  // Enough profiles that hashing alone is bound to collide.
  const many = Array.from({ length: 12 }, (_, i) => ({ id: `p-${i}`, name: `P${i}` }));
  const view = V.describeProfiles({ profiles: many, profile: "p-0", profilesOffline: false });
  const hues = view.options.filter((o) => o.id).map((o) => o.hue);
  assert.equal(new Set(hues).size, hues.length);
  // And the answer is the same every time for the same list.
  const again = V.describeProfiles({ profiles: many, profile: "p-3", profilesOffline: false });
  assert.deepEqual(again.options.map((o) => o.hue), view.options.map((o) => o.hue));
});

test("offline with only a machine-made id, the header does not print a UUID", () => {
  const id = "2c16787b-bb41-4eee-9b51-a05b5f984d92";
  const view = V.describeProfiles({ profiles: [], profile: id, profileName: id, profilesOffline: true });
  assert.equal(view.current.id, id, "it still saves into that profile");
  assert.equal(view.current.name, V.LAST_PROFILE);
  assert.equal(V.displayName(view.current.name), "Your last profile");
  assert.equal(view.saveLabel, "Save to your last profile");
  assert.doesNotMatch(view.saveLabel, /2c16787b/);
});
