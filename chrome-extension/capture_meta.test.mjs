// Tests for the capture envelope's meta.json builder.
// `node --test 'chrome-extension/*.test.mjs'`.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoMeta, MonoDomLite } = loadExtensionScripts(["domlite.js", "capture_meta.js"]);

const parse = (html) => MonoDomLite.parse(html);

const RICH = `<html lang="en-GB"><head>
  <title>The Lighthouse at Dunmore | The Paper</title>
  <link rel="canonical" href="/lighthouse">
  <link rel="shortcut icon" href="/static/fav.png">
  <meta property="og:title" content="The Lighthouse at Dunmore">
  <meta property="og:site_name" content="The Paper">
  <meta name="description" content="Forty years of ledgers.">
  <meta property="article:published_time" content="2024-03-02T09:30:00Z">
  <meta name="author" content="Ada Renn">
</head><body><h1>Headline</h1></body></html>`;

test("declared provenance is preferred over anything inferred", async () => {
  const meta = await MonoMeta.build({
    tree: parse(RICH),
    url: "https://paper.test/lighthouse?utm_source=news#part-2",
    text: "the readable prose",
  });

  assert.equal(meta.canonicalUrl, "https://paper.test/lighthouse");
  assert.equal(meta.title, "The Lighthouse at Dunmore");
  assert.equal(meta.byline, "Ada Renn");
  assert.equal(meta.publishedAt, "2024-03-02T09:30:00.000Z");
  assert.equal(meta.favicon, "https://paper.test/static/fav.png");
  assert.equal(meta.siteName, "The Paper");
  assert.equal(meta.lang, "en-GB");
  assert.equal(meta.source, "extension");
});

test("without a canonical, the address bar is normalized into one", async () => {
  const meta = await MonoMeta.build({
    tree: parse("<html><head><title>Plain</title></head><body></body></html>"),
    url: "https://shop.test/item?id=7&utm_campaign=spring&fbclid=abc#reviews",
    text: "x",
  });
  // The campaign that sent you is not part of the document's identity, and
  // dedupe keys on canonicalUrl — keep `id`, drop the rest.
  assert.equal(meta.canonicalUrl, "https://shop.test/item?id=7");
  assert.equal(meta.favicon, "https://shop.test/favicon.ico");
});

test("JSON-LD fills in what the meta tags leave out", async () => {
  const html = `<html><head><title>T</title>
    <script type="application/ld+json">{"@context":"https://schema.org","@graph":[
      {"@type":"NewsArticle","datePublished":"2023-11-05","author":{"@type":"Person","name":"Kit Moreau"}}]}
    </script></head><body></body></html>`;
  const meta = await MonoMeta.build({ tree: parse(html), url: "https://x.test/a", text: "x" });

  assert.equal(meta.byline, "Kit Moreau");
  assert.match(meta.publishedAt, /^2023-11-05T/);
});

test("malformed JSON-LD does not sink the capture", async () => {
  const html = `<html><head><title>T</title>
    <script type="application/ld+json">{ this is not json </script></head>
    <body><time datetime="2022-01-09">Jan 9</time></body></html>`;
  const meta = await MonoMeta.build({ tree: parse(html), url: "https://x.test/a", text: "x" });

  assert.equal(meta.title, "T");
  assert.match(meta.publishedAt, /^2022-01-09T/, "falls through to <time datetime>");
});

test("an unparseable date is reported as absent, never as a guess", async () => {
  const html = `<html><head><meta name="date" content="sometime last spring"></head><body></body></html>`;
  const meta = await MonoMeta.build({ tree: parse(html), url: "https://x.test/a", text: "x" });
  assert.equal(meta.publishedAt, null);
  assert.equal(meta.byline, null);
});

test("the content hash covers the prose, so ad churn does not fork a document", async () => {
  const tree = parse(RICH);
  const a = await MonoMeta.build({ tree, url: "https://paper.test/l", text: "identical prose" });
  const b = await MonoMeta.build({ tree, url: "https://paper.test/l", text: "identical prose" });
  const c = await MonoMeta.build({ tree, url: "https://paper.test/l", text: "edited prose" });

  assert.match(a.contentHash, /^sha256:[0-9a-f]{64}$/);
  assert.equal(a.contentHash, b.contentHash);
  assert.notEqual(a.contentHash, c.contentHash);
});

test("the user's own annotations ride along untouched", async () => {
  const meta = await MonoMeta.build({
    tree: parse(RICH),
    url: "https://paper.test/l",
    text: "x",
    note: "read before the Thursday meeting",
    tags: ["  coast ", "history"],
    collection: "inbox/reading",
    httpStatus: 200,
    selection: { text: "the keeper kept a ledger", path: "article > p:nth-of-type(1)" },
  });

  assert.equal(meta.note, "read before the Thursday meeting");
  assert.deepEqual(meta.tags, ["coast", "history"]);
  assert.equal(meta.collection, "inbox/reading");
  assert.equal(meta.httpStatus, 200);
  assert.equal(meta.selection.text, "the keeper kept a ledger");
});

test("meta carries every field the envelope contract names", async () => {
  const meta = await MonoMeta.build({ tree: parse(RICH), url: "https://paper.test/l", text: "x" });
  for (const field of [
    "url", "canonicalUrl", "title", "byline", "publishedAt", "capturedAt",
    "httpStatus", "contentHash", "favicon", "selection", "note", "tags",
    "collection", "source",
  ]) {
    assert.ok(field in meta, `meta.json is missing ${field}`);
  }
  assert.match(meta.capturedAt, /^\d{4}-\d{2}-\d{2}T.*Z$/);
});


// --- the canonical URL is the dedupe key, so the page does not get to lie ---

test("a canonical with an unaddressable scheme is refused", () => {
  for (const href of ["javascript:alert(1)", "data:text/html,<b>x", "file:///etc/passwd", "jav\tascript:alert(1)"]) {
    const tree = parse(`<html><head><link rel="canonical" href="${href.replace(/"/g, "&quot;")}"></head><body></body></html>`);
    const canonical = MonoMeta.canonicalOf(tree, "https://paper.test/lighthouse?utm_source=x#a");
    assert.equal(canonical, "https://paper.test/lighthouse", `accepted ${JSON.stringify(href)}`);
  }
});

test("a cross-site canonical cannot hijack another page's dedupe key", () => {
  // capture.js keys highlights on meta.canonicalUrl, so a page that claims a
  // canonical on someone else's host pulls their annotations into its capture.
  const tree = parse(`<html><head><link rel="canonical" href="https://bank.test/account"></head><body></body></html>`);
  assert.equal(MonoMeta.canonicalOf(tree, "https://evil.test/page"), "https://evil.test/page");

  const viaOg = parse(`<html><head><meta property="og:url" content="https://bank.test/account"></head><body></body></html>`);
  assert.equal(MonoMeta.canonicalOf(viaOg, "https://evil.test/page"), "https://evil.test/page");
});

test("a same-host canonical is still honoured, including an https upgrade", () => {
  const relative = parse(`<html><head><link rel="canonical" href="/clean"></head><body></body></html>`);
  assert.equal(MonoMeta.canonicalOf(relative, "https://paper.test/messy?utm_source=x"), "https://paper.test/clean");

  const upgrade = parse(`<html><head><link rel="canonical" href="https://paper.test/clean"></head><body></body></html>`);
  assert.equal(MonoMeta.canonicalOf(upgrade, "http://paper.test/messy"), "https://paper.test/clean");
});
