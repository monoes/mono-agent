// Tests for "mark as data" (§8.2): one pick is one value; a second pick on
// a similar element proposes a list with container, item and field
// selectors and up to five samples.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";
import { h, countingEnv } from "./recorder_fake_dom.mjs";

const { MonoRecorderList: L } = loadExtensionScripts(["recorder_selectors.js", "recorder_list.js"]);

/** stories builds a Hacker-News-shaped list of n rows. */
function stories(n) {
  const rows = [];
  for (let i = 1; i <= n; i++) {
    rows.push(
      h(
        "tr",
        { class: "athing submission", id: String(1000 + i) },
        h("td", { class: "title" }, h("span", { class: "titleline" }, h("a", { href: `https://s${i}.test/` }, `Story ${i}`))),
        h("td", { class: "score" }, `${i * 10} points`)
      )
    );
  }
  const tbody = h("tbody", {}, ...rows);
  h("html", {}, h("body", {}, h("table", { id: "stories" }, tbody)));
  return { tbody, rows };
}

const titleOf = (row) => row.children[0].children[0].children[0];

test("two similar picks become a list with item and field selectors", () => {
  const { rows } = stories(7);
  const mark = L.proposeList(titleOf(rows[0]), titleOf(rows[2]), countingEnv());
  assert.ok(mark);
  assert.equal(mark.list, true);
  assert.equal(mark.itemSelector, "tr.athing.submission");
  assert.equal(mark.fieldSelector, "td.title > span.titleline > a");
  assert.equal(mark.attribute, "");
  assert.deepEqual(mark.samples, ["Story 1", "Story 2", "Story 3", "Story 4", "Story 5"], "five samples, in page order");
  assert.equal(mark.containerSelector, "#stories > tbody");
  assert.equal(mark.field, "link_text");
});

test("the list mark has only ExtractMark's JSON keys", async () => {
  const { goJsonTags } = await import("./recorder_go_types.mjs");
  const allowed = goJsonTags().ExtractMark;
  const { rows } = stories(3);
  const mark = L.proposeList(titleOf(rows[0]), titleOf(rows[1]), countingEnv());
  for (const k of Object.keys(mark)) assert.ok(allowed.has(k), `ExtractMark.${k}`);
  for (const k of Object.keys(L.single(titleOf(rows[0])))) assert.ok(allowed.has(k), `ExtractMark.${k}`);
});

test("picking the items themselves gives an empty field selector", () => {
  const items = [1, 2, 3].map((i) => h("li", { class: "tag" }, `t${i}`));
  h("html", {}, h("body", {}, h("ul", { class: "tags" }, ...items)));
  const mark = L.proposeList(items[0], items[1], countingEnv());
  assert.equal(mark.fieldSelector, "");
  assert.equal(mark.itemSelector, "li.tag");
  assert.deepEqual(mark.samples, ["t1", "t2", "t3"]);
});

test("unrelated picks are two single values, not a list", () => {
  const { rows } = stories(3);
  const score = rows[1].children[1];
  assert.equal(L.proposeList(titleOf(rows[0]), score, countingEnv()), null, "different paths below the item");
  assert.equal(L.proposeList(titleOf(rows[0]), titleOf(rows[0]), countingEnv()), null, "the same element twice");
  assert.equal(L.proposeList(rows[0], titleOf(rows[0]), countingEnv()), null, "an ancestor of the other");
});

test("images extract src and links without text extract href", () => {
  const img = h("img", { src: "https://x.test/a.png", alt: "" });
  const bare = h("a", { href: "https://x.test/p" });
  assert.deepEqual(L.extractValue(img), { attribute: "src", value: "https://x.test/a.png" });
  assert.deepEqual(L.extractValue(bare), { attribute: "href", value: "https://x.test/p" });
  assert.equal(L.single(img).field, "image");
  assert.equal(L.single(bare).field, "url");
});

test("field names come from itemprop / class before falling back to the tag", () => {
  assert.equal(L.suggestField(h("span", { itemprop: "price" }, "$3"), ""), "price");
  assert.equal(L.suggestField(h("span", { class: "css-1x2y3z author-name" }, "Ann"), ""), "author_name");
  assert.equal(L.suggestField(h("h2", {}, "Hello"), ""), "title");
});
