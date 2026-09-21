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
  // The label survives, but unlinked: `[this](` would mean a link was emitted,
  // and the old `/\[this\]|this/` passed on the very output it claimed to rule
  // out, because "this" matches itself.
  assert.match(markdown, /and this\./, "the link text itself is kept as plain text");
  assert.doesNotMatch(markdown, /\[this\]\(/, "…but not as a link");
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

// --- snapshot: the live-DOM signals, and the tree shape they must share ----

/**
 * fakeDom is the smallest stand-in for the part of the DOM snapshot() reads.
 * `styles` maps an element's id (or tag, when it has none) to the computed
 * style it reports, which is the only way to exercise the hidden/fixed
 * detection outside Chrome.
 */
function fakeDom(spec, styles = {}) {
  const build = (node) => {
    if (typeof node === "string") return { nodeType: 3, nodeValue: node };
    const attrs = node.attrs || {};
    return {
      nodeType: 1,
      tagName: node.tag.toUpperCase(),
      attributes: Object.keys(attrs).map((name) => ({ name, value: attrs[name] })),
      childNodes: (node.children || []).map(build),
      textContent: "",
      __key: attrs.id || node.tag,
    };
  };
  return { el: build(spec), win: { getComputedStyle: (n) => styles[n.__key] || {} } };
}

test("hidden and floating elements recorded at snapshot time are dropped", () => {
  // The real path: snapshot() reads the computed style and sets the flags,
  // rather than the test hand-setting `fixed` on a node snapshot never saw.
  const prose = "The visible paragraph carries the whole article and then some. ".repeat(6);
  const nag = "Subscribe now for unlimited access to everything. ".repeat(6);
  const { el, win } = fakeDom(
    {
      tag: "body",
      children: [
        {
          tag: "div",
          attrs: { class: "wrap" },
          children: [
            { tag: "p", attrs: { id: "prose" }, children: [prose] },
            {
              tag: "div",
              attrs: { class: "bar", id: "bar" },
              children: [{ tag: "p", attrs: { id: "nag" }, children: [nag] }],
            },
          ],
        },
      ],
    },
    { bar: { position: "fixed" } }
  );

  const tree = MonoReadable.snapshot(el, win);
  const bar = tree.children[0].children[1];
  assert.equal(bar.fixed, true, "snapshot records the sticky overlay");

  const { markdown } = MonoReadable.extract(tree, {});
  assert.match(markdown, /visible paragraph/);
  assert.doesNotMatch(markdown, /Subscribe now/);
});

test("snapshot and domlite agree on a bare boolean attribute", () => {
  // These tests lean on domlite standing in for snapshot(). That only holds
  // if both spell a valueless attribute the same way.
  const { el, win } = fakeDom({ tag: "div", attrs: { hidden: "" }, children: ["x"] });
  const snapped = MonoReadable.snapshot(el, win);
  const parsed = MonoDomLite.parse("<div hidden>x</div>").children[0];
  assert.deepEqual(snapped.attrs, parsed.attrs);
});

test("a bare `hidden` attribute hides its subtree", () => {
  const prose = "The article proper runs for several sentences and carries the page. ".repeat(6);
  const secret = "The draft nobody was meant to read went out anyway. ".repeat(6);
  const html = `<body><main><article>
    <p>${prose}</p>
    <div hidden><p>${secret}</p></div>
    <div hidden="hidden"><p>${secret.replace("draft", "memo")}</p></div>
    <div hidden="until-found"><p>${"Collapsed but findable prose lives here too. ".repeat(6)}</p></div>
  </article></main></body>`;
  const { markdown } = MonoReadable.fromHTML(html, {});

  assert.match(markdown, /article proper/);
  assert.doesNotMatch(markdown, /nobody was meant to read/, "<div hidden> is hidden");
  assert.doesNotMatch(markdown, /memo nobody/, '<div hidden="hidden"> is hidden');
  assert.match(markdown, /Collapsed but findable/, 'hidden="until-found" is only collapsed');
});

// --- defangHtmlSource: an honest name for a source-level scrub -------------

test("defangHtmlSource removes the obvious executable markup", () => {
  const dirty = `<div onclick="alert(1)"><script src="x.js"></script><a href="javascript:go()">go</a><p>text</p></div>`;
  const clean = MonoReadable.defangHtmlSource(dirty);
  assert.doesNotMatch(clean, /<script/i);
  assert.doesNotMatch(clean, /onclick/i);
  assert.doesNotMatch(clean, /javascript:/i);
  assert.match(clean, /<p>text<\/p>/);
});

test("defangHtmlSource is not a sanitizer, and no longer claims to be", () => {
  // Pinned on purpose: these are the shapes a regex over source cannot see,
  // each one verified to come out the other side intact. If real sanitization
  // is ever needed it has to happen on the parsed tree — this test exists so
  // the gap cannot be mistaken for a guarantee, and so that a future tightening
  // of the regexes has to come here and say which of these it now covers.
  const bypasses = {
    "a tab entity inside the scheme": `<a href="jav&#x09;ascript:alert(1)">x</a>`,
    "the first letter as an entity": `<a href="&#106;avascript:alert(1)">x</a>`,
    "a letter mid-scheme as an entity": `<a href="java&#115;cript:alert(1)">x</a>`,
    "the colon as an entity": `<a href="javascript&colon;alert(1)">x</a>`,
    "markup carried inside an attribute": `<iframe srcdoc="&lt;script&gt;alert(1)&lt;/script&gt;"></iframe>`,
    "an SVG attribute named by another attribute": `<svg><set attributeName="onload" to="alert(1)"/></svg>`,
    "a scheme outside href/src/action": `<div style="background:url(javascript:alert(1))">x</div>`,
  };
  for (const [name, html] of Object.entries(bypasses)) {
    assert.match(MonoReadable.defangHtmlSource(html), /alert\(1\)/, `${name} is NOT defanged, and never was`);
  }
  assert.equal(MonoReadable.stripScripts, undefined, "the name that promised safety is gone");
});

test("the parser survives the markup real pages actually ship", () => {
  const tree = MonoDomLite.parse(`<ul><li>one<li>two</ul><p>a &amp; b<p>c<br>d<img src=x.png alt='a > b'>`);
  const md = MonoMarkdown.toMarkdown(tree, {});
  assert.match(md, /^- one$/m);
  assert.match(md, /^- two$/m);
  assert.match(md, /a \\& b|a & b/);
  assert.match(md, /!\[a > b\]\(x\.png\)/);
});

// --- the extractor must stay linear ---------------------------------------

const SENTENCE =
  "The keeper kept a ledger of every ship that passed the point, and the ledger was the only record. ";

/** nestedArticle is the shape that used to melt: prose at every nesting level. */
function nestedArticle(depth, perLevel) {
  let inner = "";
  for (let d = depth; d > 0; d--) {
    inner = `<section class="level-${d}"><p>${SENTENCE.repeat(perLevel)}</p>${inner}</section>`;
  }
  return `<body><main><article>${inner}</article></main></body>`;
}

test("a deeply nested 200 KiB page extracts in well under two seconds", () => {
  const html = nestedArticle(500, 4);
  assert.ok(html.length > 200 * 1024, `fixture is only ${html.length} bytes`);

  const started = Date.now();
  const { text } = MonoReadable.fromHTML(html, { baseUrl: "https://paper.test/x" });
  const elapsed = Date.now() - started;

  assert.ok(text.length > 150000, `expected the prose back, got ${text.length} chars`);
  assert.ok(elapsed < 1500, `extraction took ${elapsed}ms — textOf is quadratic again`);
});

test("four times the nesting does not cost sixteen times the work", () => {
  // The blow-up was in depth, not in bytes: textOf rebuilt every ancestor's
  // whole subtree, so cost ran with depth x text, i.e. depth squared here.
  const time = (html) => {
    const started = Date.now();
    MonoReadable.fromHTML(html, {});
    return Date.now() - started;
  };
  const shallow = Math.max(time(nestedArticle(125, 4)), 1);
  const deep = time(nestedArticle(500, 4));
  assert.ok(deep < shallow * 8, `125 levels took ${shallow}ms, 500 took ${deep}ms — that is not linear`);
});

test("a pathologically deep tree does not overflow the stack", () => {
  const html = `<body>${"<div>".repeat(6000)}<p>${SENTENCE.repeat(4)}</p>${"</div>".repeat(6000)}</body>`;
  const { text } = MonoReadable.fromHTML(html, {});
  assert.equal(typeof text, "string");
});

// --- markdown: the URL policy (TRU-03) -------------------------------------

test("dangerous schemes are refused however they are spelled", () => {
  const hostile = [
    "javascript:alert(1)",
    "jav\tascript:alert(1)",
    "java\nscript:alert(1)",
    "java\rscript:alert(1)",
    "\u0000javascript:alert(1)",
    "  JaVaScRiPt:alert(1)",
    "vbscript:msgbox(1)",
    "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
    "data:text/html,<script>alert(1)</script>",
    "data:application/xhtml+xml,<html/>",
    "file:///etc/passwd",
    "blob:https://paper.test/abc",
    "chrome-extension://abc/x.html",
  ];
  for (const href of hostile) {
    assert.equal(MonoMarkdown.resolveUrl(href, "https://paper.test/a"), "", `resolved ${JSON.stringify(href)}`);
    assert.equal(MonoMarkdown.resolveUrl(href, ""), "", `resolved bare ${JSON.stringify(href)}`);
  }
});

test("the schemes a saved page may link to are the ones it can address", () => {
  const base = "https://paper.test/a/b";
  assert.equal(MonoMarkdown.resolveUrl("/c", base), "https://paper.test/c");
  assert.equal(MonoMarkdown.resolveUrl("http://other.test/x", base), "http://other.test/x");
  assert.equal(MonoMarkdown.resolveUrl("mailto:ada@paper.test", base), "mailto:ada@paper.test");
  // No base at all: a relative href carries no scheme, so it is passed on.
  assert.equal(MonoMarkdown.resolveUrl("x.png", ""), "x.png");
});

test("a hostile href cannot close the link early and inject markdown", () => {
  const html = `<body><main><article><p>${SENTENCE.repeat(4)}</p>
    <p>See <a href="/ok) [CLICK ME](https://evil.test/steal">the archive</a> for more.</p>
    </article></main></body>`;
  const { markdown } = MonoReadable.fromHTML(html, { baseUrl: "https://paper.test/x" });

  assert.doesNotMatch(markdown, /\[CLICK ME\]\(/, "the href must not be able to open a second link");
  const links = markdown.match(/\[[^\]]*\]\([^)]*\)/g) || [];
  assert.equal(links.length, 1, `expected one link, got ${JSON.stringify(links)}`);
  assert.match(links[0], /^\[the archive\]\(/);
});

test("a data: image becomes a placeholder, never an inline payload", () => {
  const tree = MonoDomLite.parse(`<p><img src="data:image/svg+xml,<svg onload=alert(1)/>" alt="pic"></p>`);
  const md = MonoMarkdown.toMarkdown(tree, { baseUrl: "https://paper.test/a" });
  assert.match(md, /!\[pic\]\(#embedded-image\)/);
  assert.doesNotMatch(md, /alert\(1\)/);
});

// --- markdown: bounded table rendering -------------------------------------

test("a monstrous table renders bounded, and fast", () => {
  const row = `<tr>${"<td>cell</td>".repeat(40)}</tr>`;
  const html = `<table>${row.repeat(40000)}</table>`;
  const tree = MonoDomLite.parse(html);

  const started = Date.now();
  const md = MonoMarkdown.toMarkdown(tree, {});
  const elapsed = Date.now() - started;

  assert.ok(md.length < 2 * 1024 * 1024, `${md.length} bytes of Markdown out of one table`);
  assert.ok(elapsed < 5000, `rendering took ${elapsed}ms`);
});

// --- domlite: entities and parse cost --------------------------------------

test("an out-of-range numeric entity is left alone, not thrown on", () => {
  const cases = {
    "&#1114112;": "&#1114112;",
    "&#x110000;": "&#x110000;",
    "&#99999999999999999999;": "&#99999999999999999999;",
    "&#xD800;": "&#xD800;",
    "&#0;": "&#0;",
    "&#65;": "A",
    "&#x1F600;": "\u{1F600}",
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(MonoDomLite.decodeEntities(input), want, `decoding ${input}`);
  }
  // And through the parser, which is where extract_items.js meets it.
  assert.doesNotThrow(() => MonoDomLite.parse("<p>&#1114112; &#xFFFFFFFF;</p>"));
});

test("a page full of script blocks parses in linear time", () => {
  const filler = "<p>Ordinary prose that fills the document out to a realistic size.</p>";
  const html = `<body>${(filler + "<script>var x = 1;</script>").repeat(4000)}</body>`;
  assert.ok(html.length > 300 * 1024, `fixture is only ${html.length} bytes`);

  const started = Date.now();
  MonoDomLite.parse(html);
  const elapsed = Date.now() - started;
  assert.ok(elapsed < 1500, `parsing ${(html.length / 1024) | 0} KiB took ${elapsed}ms`);
});
