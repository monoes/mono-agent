// Tests for the readable extractor (CLIP-05) and the Markdown renderer.
// No dependencies: `node --test 'chrome-extension/*.test.mjs'`.
//
// domlite.js parses the HTML fixtures into exactly the node tree that
// MonoReadable.snapshot() builds from a live DOM in Chrome, so what is
// asserted here is the same code path a real capture runs.

import test from "node:test";
import assert from "node:assert/strict";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoReadable, MonoMarkdown, MonoDomLite } = loadExtensionScripts([
  "domlite.js",
  "markdown.js",
  "readable.js",
]);

const ARTICLE = `<!doctype html>
<html><head><title>Ignored</title>
<script>window.tracker = 1;</script>
</head>
<body>
  <nav class="site-nav"><a href="/a">Home</a> <a href="/b">About</a></nav>
  <header id="masthead"><a href="/">The Paper</a></header>
  <div id="cookie-consent">We value your privacy. <button>Accept all</button></div>
  <main>
    <article class="post-body">
      <h1>The Lighthouse at Dunmore</h1>
      <p class="byline">By Ada Renn</p>
      <p>The keeper kept a ledger of every ship that passed, and for forty years
         the ledger was the only record anyone kept of that stretch of coast.</p>
      <p>When the light was automated in 1971 the ledger went into a drawer, and
         the drawer went into a cupboard, and the cupboard was painted shut.</p>
      <h2>What the ledger says</h2>
      <ul><li>Eleven wrecks</li><li>Two rescues by rowboat</li></ul>
      <blockquote><p>Fog from dusk. No traffic. — 3 March 1948</p></blockquote>
      <pre><code class="language-python">print("hello")</code></pre>
      <p>See the <a href="/archive">archive</a> and <a href="javascript:steal()">this</a>.</p>
      <figure><img src="/img/light.jpg" alt="The tower at dusk"><figcaption>Dusk, 1968</figcaption></figure>
      <table><tr><th>Year</th><th>Wrecks</th></tr><tr><td>1948</td><td>2</td></tr></table>
    </article>
  </main>
  <aside class="related"><a href="/1">More stories</a><a href="/2">And more</a></aside>
  <footer>© The Paper</footer>
</body></html>`;

test("the article body survives and the furniture does not", () => {
  const { markdown } = MonoReadable.fromHTML(ARTICLE, { baseUrl: "https://paper.test/lighthouse" });

  assert.match(markdown, /# The Lighthouse at Dunmore/);
  assert.match(markdown, /ledger of every ship/);
  assert.doesNotMatch(markdown, /Home/, "nav is gone");
  assert.doesNotMatch(markdown, /We value your privacy/, "cookie banner is gone");
  assert.doesNotMatch(markdown, /More stories/, "related-links rail is gone");
  assert.doesNotMatch(markdown, /© The Paper/, "footer is gone");
});

test("an archive is a photograph, not a program (TRU-03)", () => {
  const { markdown } = MonoReadable.fromHTML(ARTICLE, { baseUrl: "https://paper.test/x" });
  assert.doesNotMatch(markdown, /tracker/, "inline script text never reaches the archive");
  assert.doesNotMatch(markdown, /javascript:/, "javascript: URLs are never linked");
  assert.match(markdown, /\[this\]|this/, "the link text itself is kept as plain text");
});

test("block structure is converted to real Markdown", () => {
  const { markdown } = MonoReadable.fromHTML(ARTICLE, { baseUrl: "https://paper.test/lighthouse" });

  assert.match(markdown, /^## What the ledger says$/m);
  assert.match(markdown, /^- Eleven wrecks$/m);
  assert.match(markdown, /^> Fog from dusk/m);
  assert.match(markdown, /```python\nprint\("hello"\)\n```/);
  assert.match(markdown, /^\| Year \| Wrecks \|$/m);
  assert.match(markdown, /^\| --- \| --- \|$/m);
  assert.match(markdown, /!\[The tower at dusk\]\(https:\/\/paper\.test\/img\/light\.jpg\)/);
  assert.match(markdown, /\*Dusk, 1968\*/);
});

test("relative links are resolved against the page they came from", () => {
  const { markdown } = MonoReadable.fromHTML(ARTICLE, { baseUrl: "https://paper.test/lighthouse" });
  assert.match(markdown, /\[archive\]\(https:\/\/paper\.test\/archive\)/);
});

test("the content hash is computed over prose, not Markdown syntax", () => {
  const { text, wordCount } = MonoReadable.fromHTML(ARTICLE, {});
  assert.doesNotMatch(text, /[#*`|]/);
  assert.match(text, /The Lighthouse at Dunmore/);
  assert.ok(wordCount > 40, `expected real prose, got ${wordCount} words`);
});

test("a selection is taken as-is, with no scoring", () => {
  const selection = `<div><p>Only these two sentences were selected.</p><p>Nothing else.</p></div>`;
  const { markdown } = MonoReadable.fromHTML(selection, { selectionOnly: true });
  assert.match(markdown, /Only these two sentences/);
  assert.match(markdown, /Nothing else\./);
});

test("a page whose article wears an unlikely class name is still extracted", () => {
  // "content-widget" trips the unlikely-candidate filter; the second pass
  // must rescue it rather than returning a stub.
  const html = `<body><div class="content-widget"><h1>Shipping forecast</h1>
    <p>${"Dogger, Fisher, German Bight. Northwesterly five, occasionally six. ".repeat(4)}</p>
    <p>${"Rain later, moderate becoming good. Visibility moderate or poor. ".repeat(4)}</p>
    </div></body>`;
  const { markdown, text } = MonoReadable.fromHTML(html, {});
  assert.match(markdown, /# Shipping forecast/);
  assert.ok(text.length > 250, `expected the rescued body, got ${text.length} chars`);
});

test("hidden and floating elements recorded at snapshot time are dropped", () => {
  const tree = MonoDomLite.parse(`<body><div class="wrap">
    <p>${"The visible paragraph carries the whole article and then some. ".repeat(6)}</p>
    <div class="bar"><p>${"Subscribe now for unlimited access to everything. ".repeat(6)}</p></div>
  </div></body>`);
  // Simulate what MonoReadable.snapshot() records for a sticky banner: a
  // neutral class name, so only the live-DOM signal can disqualify it.
  const wrap = tree.children[0].children.find((n) => n.tag === "div");
  const bar = wrap.children.find((n) => n.attrs && n.attrs.class === "bar");
  bar.fixed = true;

  const { markdown } = MonoReadable.extract(tree, {});
  assert.match(markdown, /visible paragraph/);
  assert.doesNotMatch(markdown, /Subscribe now/);
});

test("stripScripts defangs HTML we pass on rather than render", () => {
  const dirty = `<div onclick="alert(1)"><script src="x.js"></script><a href="javascript:go()">go</a><p>text</p></div>`;
  const clean = MonoReadable.stripScripts(dirty);
  assert.doesNotMatch(clean, /<script/i);
  assert.doesNotMatch(clean, /onclick/i);
  assert.doesNotMatch(clean, /javascript:/i);
  assert.match(clean, /<p>text<\/p>/);
});

test("the parser survives the markup real pages actually ship", () => {
  const tree = MonoDomLite.parse(`<ul><li>one<li>two</ul><p>a &amp; b<p>c<br>d<img src=x.png alt='a > b'>`);
  const md = MonoMarkdown.toMarkdown(tree, {});
  assert.match(md, /^- one$/m);
  assert.match(md, /^- two$/m);
  assert.match(md, /a \\& b|a & b/);
  assert.match(md, /!\[a > b\]\(x\.png\)/);
});
