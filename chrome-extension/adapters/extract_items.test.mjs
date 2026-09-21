// Point-and-extract for repeating elements (CLIP-12).
//
// The user clicks one thing; the extractor has to work out what "one of
// these" means, find the rest, and turn them into rows. Everything below the
// click is pure — the live DOM only ever contributes a marked-up snapshot —
// so the inference is testable from fixtures.

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { loadExtensionScripts } from "../test_helpers.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => readFileSync(join(HERE, "fixtures", name), "utf8");

const g = loadExtensionScripts([
  "domlite.js",
  "markdown.js",
  "adapters/util.js",
  "adapters/extract_items.js",
]);

const { MonoExtractItems: X, MonoDomLite } = g;
const PAGE1 = "https://auctions.example/lots/14";
const PAGE2 = "https://auctions.example/lots/14?page=2";

const parse = (name) => MonoDomLite.parse(fixture(name));
const fromTree = (name, opts) =>
  X.fromTree(parse(name), Object.assign({ url: PAGE1, baseUrl: PAGE1 }, opts));

test("clicking a heading infers the card, not the heading", () => {
  const out = fromTree("items_page1.html");

  assert.equal(out.selector, "li.card");
  assert.equal(out.items.length, 4);
  // The `featured` card differs by one class and must not be dropped.
  assert.ok(out.items.some((i) => /Keeper's ledger/.test(i.title)), "the odd-classed sibling is still an item");
});

test("clicking a nested repeating child still infers the outer item", () => {
  // A tag list repeats inside each card. Clicking one tag is a click on
  // "this card", not on "these two words".
  const html = fixture("items_page1.html")
    .replace(' data-mono-pick="1"', "")
    .replace('<li class="tag">brass</li>', '<li class="tag" data-mono-pick="1">brass</li>');
  const out = X.fromTree(MonoDomLite.parse(html), { url: PAGE1, baseUrl: PAGE1 });

  assert.equal(out.selector, "li.card");
  assert.equal(out.items.length, 4);
});

test("a field per stable child, named by what it is", () => {
  const out = fromTree("items_page1.html");
  const names = out.fields.map((f) => f.name);

  for (const expected of ["title", "url", "image", "price", "date"]) {
    assert.ok(names.includes(expected), `expected a ${expected} column, got ${JSON.stringify(names)}`);
  }

  const first = out.items[0];
  assert.equal(first.title, "Brass lamp");
  assert.equal(first.url, "https://auctions.example/lot/14/brass-lamp", "hrefs are absolute");
  assert.equal(first.image, "https://auctions.example/img/lamp.jpg");
  assert.equal(first.price, "£42.00");
  assert.equal(first.date, "2024-03-02", "the machine-readable datetime, not the rendered label");
});

test("a child missing from one item leaves an empty cell, not a shifted row", () => {
  const out = fromTree("items_page1.html");
  // The last card has one tag where the others have two.
  const tagColumns = out.fields.filter((f) => f.name.startsWith("text"));
  assert.ok(tagColumns.length >= 1);
  assert.equal(out.items.length, 4);
  for (const item of out.items) {
    for (const field of out.fields) assert.ok(field.name in item, `${field.name} present on every row`);
  }
});

test("items.csv is RFC4180 and items.json is the same rows", () => {
  const out = fromTree("items_page1.html");
  const artifacts = X.artifactsFor(out);
  assert.deepEqual(artifacts.map((a) => a.name), ["items.csv", "items.json"]);

  const csv = artifacts[0].text;
  const lines = csv.trimEnd().split("\n");
  assert.equal(lines.length, 5, "a header and four rows");
  assert.equal(lines[0], out.fields.map((f) => f.name).join(","));
  // A comma inside a value must be quoted, not allowed to invent a column.
  assert.match(csv, /"£1,250\.00"/);

  const rows = JSON.parse(artifacts[1].text);
  assert.equal(rows.length, 4);
  assert.equal(rows[2].price, "£1,250.00");
});

test("a quote inside a value is doubled, per RFC4180", () => {
  const csv = X.toCsv([{ a: 'He said "no"', b: "plain" }], [{ name: "a" }, { name: "b" }]);
  assert.equal(csv, 'a,b\n"He said ""no""",plain\n');
});

test("pagination follows the next link and stops when there is none", async () => {
  const fetched = [];
  const out = await X.fromTreeWithPagination(parse("items_page1.html"), {
    url: PAGE1,
    baseUrl: PAGE1,
    maxPages: 5,
    fetchPage: async (url) => {
      fetched.push(url);
      return fixture("items_page2.html");
    },
  });

  assert.deepEqual(fetched, ["https://auctions.example/lots/14?page=2"]);
  assert.equal(out.items.length, 6);
  assert.equal(out.pagesFetched, 2);
  assert.match(out.items[5].title, /Logbook shelf/);
});

test("maxPages is a hard stop even when the site keeps offering more", async () => {
  // An endless paginator with genuinely new rows on every page — so the
  // "no new items" stop cannot fire and only the cap can end this.
  let calls = 0;
  const out = await X.fromTreeWithPagination(parse("items_page1.html"), {
    url: PAGE1,
    baseUrl: PAGE1,
    maxPages: 3,
    fetchPage: async () => {
      calls++;
      return fixture("items_page1.html")
        .replace("page=2", `page=${calls + 2}`)
        .replace(/\/lot\/14\//g, `/lot/${calls + 14}/`)
        .replace(/(<h3 class="name"[^>]*>)([^<]+)/g, (m, open, text) => `${open}${text} #${calls}`);
    },
  });

  assert.equal(out.pagesFetched, 3, "the first page plus two fetches");
  assert.equal(out.items.length, 12);
  assert.ok(out.warnings.some((w) => /page limit/i.test(w)));
});

test("a page that yields no new items ends the crawl early", async () => {
  const out = await X.fromTreeWithPagination(parse("items_page1.html"), {
    url: PAGE1,
    baseUrl: PAGE1,
    maxPages: 8,
    fetchPage: async () => fixture("items_page1.html"), // the same four items forever
  });

  assert.equal(out.items.length, 4, "duplicates are dropped");
  assert.equal(out.pagesFetched, 2, "and the crawl stops rather than looping");
  assert.ok(out.warnings.some((w) => /no new items/i.test(w)));
});

test("pagination never leaves the origin", async () => {
  const html = fixture("items_page1.html").replace(
    'href="/lots/14?page=2" rel="next"',
    'href="https://tracker.example/next" rel="next"'
  );
  let called = false;
  const out = await X.fromTreeWithPagination(MonoDomLite.parse(html), {
    url: PAGE1,
    baseUrl: PAGE1,
    maxPages: 5,
    fetchPage: async () => {
      called = true;
      return "";
    },
  });

  assert.equal(called, false);
  assert.equal(out.pagesFetched, 1);
  assert.ok(out.warnings.some((w) => /off-origin/i.test(w)));
});

test("a fetch that fails ends the crawl with what it already has", async () => {
  const out = await X.fromTreeWithPagination(parse("items_page1.html"), {
    url: PAGE1,
    baseUrl: PAGE1,
    maxPages: 5,
    fetchPage: async () => {
      throw new Error("503 Service Unavailable");
    },
  });

  assert.equal(out.items.length, 4);
  assert.ok(out.warnings.some((w) => /503/.test(w)));
});

test("an explicit selector overrides the inference", () => {
  const out = fromTree("items_page1.html", { selector: "li.tag" });
  assert.equal(out.selector, "li.tag");
  assert.equal(out.items.length, 7);
});

test("nothing repeating means an honest empty result, not a guess", () => {
  const tree = MonoDomLite.parse(`<html><body><div id="only"><p data-mono-pick="1">One thing.</p></div></body></html>`);
  const out = X.fromTree(tree, { url: PAGE1, baseUrl: PAGE1 });

  assert.equal(out.items.length, 0);
  assert.equal(out.selector, null);
  assert.ok(out.warnings.some((w) => /no repeating/i.test(w)));
  assert.deepEqual(X.artifactsFor(out), [], "no rows, no artifacts");
});

test("the artifact names are ones the Go receiver accepts", () => {
  const out = fromTree("items_page1.html");
  const g2 = loadExtensionScripts(["adapters/registry.js"]);
  for (const artifact of X.artifactsFor(out)) {
    assert.ok(g2.MonoAdapters.validArtifactName(artifact.name), `${artifact.name} is a legal envelope artifact name`);
  }
});
