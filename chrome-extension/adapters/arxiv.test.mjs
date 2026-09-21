// arXiv / DOI adapter (CLIP-10).
//
// This is a research capture: the abstract and a citation you can paste into
// a bibliography are the entire point. A saved abs page without them is a
// saved navigation bar.

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
  "adapters/arxiv.js",
]);

const run = (url, html) =>
  g.MonoAdapters.run({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" });

const ABS = "https://arxiv.org/abs/2403.00219";

test("it claims arXiv by URL, and any page that carries citation metadata", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "arxiv");
  const bare = g.MonoDomLite.parse("<html><body>hi</body></html>");
  const cited = g.MonoDomLite.parse(fixture("doi.html"));

  assert.ok(ad.match({ url: ABS, tree: bare }), "arxiv.org matches on the URL alone");
  assert.ok(ad.match({ url: "https://arxiv.org/pdf/2403.00219v2", tree: bare }));
  assert.ok(ad.match({ url: "https://journal.example/articles/44", tree: cited }), "citation_* is the signal");
  assert.equal(ad.match({ url: "https://journal.example/articles/44", tree: bare }), false);
  assert.equal(ad.match({ url: "https://news.test/story", tree: bare }), false);
});

test("an arXiv abs page becomes title, authors, subjects, abstract and BibTeX", () => {
  const out = run(ABS, fixture("arxiv.html"));

  assert.equal(out.adapter, "arxiv");
  assert.equal(out.title, "Ledger-scale weather reconstruction from lighthouse logbooks");
  assert.match(out.markdown, /^# Ledger-scale weather reconstruction from lighthouse logbooks/);
  // Descriptors ("Title:", "Abstract:") are page furniture, not content.
  assert.doesNotMatch(out.markdown, /Abstract:Keepers/);
  assert.match(out.markdown, /\*\*Authors:\*\* Ada Renn, Kit Moreau, Ifeoma Okonkwo/);
  assert.match(out.markdown, /\*\*Subjects:\*\* Atmospheric and Oceanic Physics \(physics\.ao-ph\); Digital Libraries \(cs\.DL\)/);
  assert.match(out.markdown, /## Abstract\n\nKeepers of manned lighthouses recorded visibility/);

  assert.match(out.markdown, /```bibtex\n@misc\{renn2024ledger,/);
  assert.match(out.markdown, /\n {2}title\s+= \{Ledger-scale weather reconstruction from lighthouse logbooks\},/);
  assert.match(out.markdown, /\n {2}author\s+= \{Ada Renn and Kit Moreau and Ifeoma Okonkwo\},/);
  assert.match(out.markdown, /\n {2}eprint\s+= \{2403\.00219\},/);
  assert.match(out.markdown, /\n {2}archivePrefix = \{arXiv\},/);
  assert.match(out.markdown, /\n```$/);

  assert.equal(out.meta.citationKey, "renn2024ledger");
  assert.equal(out.meta.arxivId, "2403.00219");
  assert.deepEqual(out.meta.authors, ["Ada Renn", "Kit Moreau", "Ifeoma Okonkwo"]);
  assert.equal(out.meta.resolvedPdfUrl, "https://arxiv.org/pdf/2403.00219");
});

test("the abstract is emitted whole, never truncated to an excerpt", () => {
  const out = run(ABS, fixture("arxiv.html"));
  assert.match(out.markdown, /mean bias of\s+0\.3 m\/s against the nearest instrumented station\./);
});

test("a publisher page reached by DOI yields an @article entry", () => {
  const out = run("https://journal.example/articles/44", fixture("doi.html"));

  assert.equal(out.adapter, "arxiv");
  assert.equal(out.title, "Painted-shut drawers: survival bias in archival climate records");
  assert.match(out.markdown, /\*\*Authors:\*\* Ifeoma Okonkwo, Ada Renn/);
  assert.match(out.markdown, /\*\*DOI:\*\* \[10\.1234\/jcoasthist\.2023\.0044\]\(https:\/\/doi\.org\/10\.1234\/jcoasthist\.2023\.0044\)/);
  assert.match(out.markdown, /## Abstract\n\nArchival climate series are assembled/);

  assert.match(out.markdown, /@article\{okonkwo2023painted,/);
  assert.match(out.markdown, /journal\s+= \{Journal of Coastal History\},/);
  assert.match(out.markdown, /volume\s+= \{12\},/);
  assert.match(out.markdown, /pages\s+= \{311--338\},/);
  assert.match(out.markdown, /doi\s+= \{10\.1234\/jcoasthist\.2023\.0044\},/);
  assert.equal(out.meta.doi, "10.1234/jcoasthist.2023.0044");
});

test("the reference list is not mistaken for the abstract", () => {
  const out = run("https://journal.example/articles/44", fixture("doi.html"));
  assert.doesNotMatch(out.markdown, /## References/);
});

test("braces and BibTeX-hostile characters in a title are escaped", () => {
  const html = `<html><head>
    <meta name="citation_title" content="On {curly} braces, 100% coverage &amp; other hazards">
    <meta name="citation_author" content="Van Der Berg, Wil">
    <meta name="citation_publication_date" content="2021">
    <meta name="citation_doi" content="10.5555/x">
    </head><body><div class="abstract"><p>Short.</p></div></body></html>`;
  const out = run("https://doi.org/10.5555/x", html);

  assert.match(out.markdown, /title\s+= \{On \\\{curly\\\} braces, 100\\% coverage & other hazards\},/);
  // A multi-word surname stays one token in the key, lowercased and stripped.
  assert.equal(out.meta.citationKey, "vanderberg2021curly");
});

test("an arXiv page with no metadata at all declines instead of emitting a stub", () => {
  const out = run(ABS, "<html><head><title>arXiv.org</title></head><body><nav>Search</nav></body></html>");
  assert.equal(out, null);
  assert.ok(g.MonoAdapters.lastWarnings().some((w) => /arxiv declined/.test(w)));
});

test("metadata without an abstract still yields a usable citation", () => {
  const html = `<html><head>
    <meta name="citation_title" content="A paper whose landing page hides the abstract">
    <meta name="citation_author" content="Solo, Han">
    <meta name="citation_publication_date" content="2020/06/01">
    <meta name="citation_doi" content="10.1/abc">
    </head><body></body></html>`;
  const out = run("https://journal.example/a", html);

  assert.match(out.markdown, /# A paper whose landing page hides the abstract/);
  assert.match(out.markdown, /@article\{solo2020paper,/);
  assert.ok(out.warnings.some((w) => /abstract/i.test(w)));
});
