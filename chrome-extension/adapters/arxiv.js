/**
 * MonoAgent Bridge — arXiv / DOI adapter (CLIP-10)
 *
 * A research capture is judged on two things: does it contain the abstract,
 * and can you cite it. The generic pipeline gets neither reliably — an arXiv
 * abs page is mostly a metadata table, which scores as furniture, and a
 * publisher landing page is mostly a cookie wall.
 *
 * So this adapter reads the page's own bibliographic claims. arXiv has its
 * own markup; every other journal platform emits Highwire Press
 * `citation_*` meta tags for Google Scholar, which is why one adapter covers
 * both — the URL decides which extras exist (eprint id, primary class), the
 * meta tags carry the rest.
 *
 * The BibTeX block is generated locally from those fields. No call to
 * arXiv's API, Crossref, or anything else: a capture must work offline and
 * must not tell a third party what you are reading.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  // Title words too common to identify a paper in a citation key.
  const STOPWORDS = new Set([
    "a", "an", "the", "on", "of", "in", "for", "to", "and", "with", "from",
    "at", "by", "is", "are", "we", "our", "into", "over", "under", "as",
    "its", "this", "that", "towards", "toward", "using", "via", "when",
  ]);

  function arxivId(url, tree) {
    const declared = U.metaContent(tree, ["citation_arxiv_id", "citation_technical_report_number"]);
    if (declared) return declared.replace(/^arxiv:/i, "").replace(/v\d+$/i, "");
    const m = U.pathOf(url).match(/^\/(?:abs|pdf)\/(.+?)(?:v\d+)?(?:\.pdf)?$/i);
    return m ? m[1] : "";
  }

  function match(ctx) {
    const tree = (ctx && ctx.tree) || null;
    const host = U.hostOf((ctx && ctx.url) || "");
    if (host === "arxiv.org" && /^\/(abs|pdf)\//i.test(U.pathOf(ctx.url))) return true;
    if (!tree) return false;
    // Any page that publishes Highwire citation metadata is a paper landing
    // page, whoever hosts it — that is exactly the DOI case.
    if (!U.metaContent(tree, ["citation_title"])) return false;
    return !!(U.metaAll(tree, "citation_author").length || U.metaContent(tree, ["citation_doi"]));
  }

  /** person splits "Renn, Ada" or "Ada Renn" into a display name and a surname. */
  function person(raw) {
    const name = U.clean(raw);
    if (!name) return null;
    if (name.includes(",")) {
      const [last, first] = name.split(",", 2).map((p) => U.clean(p));
      return { display: first ? `${first} ${last}` : last, last };
    }
    const parts = name.split(" ");
    return { display: name, last: parts[parts.length - 1] };
  }

  function authorsOf(tree) {
    const declared = U.metaAll(tree, "citation_author").map(person).filter(Boolean);
    if (declared.length) return declared;
    // arXiv's own markup, for the rare page whose meta tags did not render.
    const block = U.pick(tree, [{ class: /(^|\s)authors(\s|$)/ }]);
    if (!block) return [];
    return U.findAll(block, (n) => n.tag === "a")
      .map((a) => person(U.text(a)))
      .filter(Boolean);
  }

  function yearOf(tree) {
    const raw = U.metaContent(tree, [
      "citation_publication_date", "citation_date", "citation_online_date",
      "citation_year", "dc.date", "article:published_time",
    ]);
    const m = raw.match(/\d{4}/);
    return m ? m[0] : "";
  }

  function titleOf(tree) {
    const declared = U.metaContent(tree, ["citation_title"]);
    if (declared) return declared;
    const heading = U.pick(tree, [{ tag: "h1", class: /(^|\s)title(\s|$)/ }, { tag: "h1" }]);
    const text = heading ? U.text(heading) : "";
    return text.replace(/^title\s*:?\s*/i, "") || U.metaContent(tree, ["og:title"]);
  }

  /**
   * abstractOf prefers the page's own abstract element and falls back to the
   * metadata. arXiv wraps it in a <blockquote>, which would render as a
   * quote; its children are rendered instead so the prose stays prose.
   */
  function abstractOf(tree, baseUrl) {
    const node = U.pick(tree, [
      { tag: "blockquote", class: /(^|\s)abstract(\s|$)/ },
      { class: /(^|\s)abstract(\s|$)/ },
      { id: /(^|-)abstract$/ },
    ]);
    let body = "";
    if (node) {
      const inner = node.tag === "blockquote" ? { tag: "#root", attrs: {}, children: U.kids(node) } : node;
      body = U.markdownOf(inner, baseUrl);
    }
    body = body
      .replace(/^#{1,6}\s*abstract\b[:\s]*/i, "")
      .replace(/^\*{0,2}abstract\*{0,2}\s*:\s*/i, "")
      .trim();
    if (body) return body;
    return U.metaContent(tree, ["citation_abstract", "dc.description", "og:description", "description"]);
  }

  function subjectsOf(tree) {
    const cell = U.pick(tree, [{ tag: "td", class: /(^|\s)subjects(\s|$)/ }, { class: /(^|\s)subjects(\s|$)/ }]);
    const fromPage = cell ? U.text(cell).replace(/^subjects\s*:?\s*/i, "") : "";
    if (fromPage) return fromPage;
    const keywords = U.metaAll(tree, "citation_keywords").concat(U.metaAll(tree, "keywords"));
    return keywords.join("; ");
  }

  function primaryClassOf(tree) {
    const primary = U.pick(tree, [{ class: "primary-subject" }]);
    const m = primary ? U.text(primary).match(/\(([^)]+)\)\s*$/) : null;
    return m ? m[1] : "";
  }

  // BibTeX is TeX: braces group, and % starts a comment. Ampersands are legal
  // inside a field and escaping them only makes the entry uglier.
  function bib(value) {
    return String(value || "").replace(/([\\{}%$#_])/g, "\\$1");
  }

  const slug = (s) => String(s || "").toLowerCase().replace(/[^a-z0-9]/g, "");

  function citationKey(authors, year, title) {
    const surname = authors.length ? slug(authors[0].last) : "";
    const word = (String(title).match(/[A-Za-z0-9]+/g) || [])
      .map((w) => w.toLowerCase())
      .find((w) => !STOPWORDS.has(w) && w.length > 1) || "";
    const key = `${surname}${year}${word}`;
    return key || "capture";
  }

  function bibtex(entry) {
    const fields = entry.fields.filter(([, value]) => value !== "" && value !== null && value !== undefined);
    if (!fields.length) return "";
    const pad = Math.max(...fields.map(([name]) => name.length));
    const lines = fields.map(([name, value]) => `  ${name.padEnd(pad)} = {${value}},`);
    return `@${entry.type}{${entry.key},\n${lines.join("\n")}\n}`;
  }

  function extract(ctx) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const title = titleOf(tree);
    const authors = authorsOf(tree);
    const abstract = abstractOf(tree, baseUrl);
    const doi = U.metaContent(tree, ["citation_doi", "dc.identifier.doi"]).replace(/^doi:\s*/i, "");
    const eprint = U.hostOf(ctx.url) === "arxiv.org" || U.metaContent(tree, ["citation_arxiv_id"])
      ? arxivId(ctx.url, tree)
      : "";

    // Nothing identifiable: this is a redesign, or a listing page that only
    // looked like a paper. Let the generic pipeline have it.
    if (!title || (!authors.length && !abstract && !doi && !eprint)) return null;

    const year = yearOf(tree);
    const subjects = subjectsOf(tree);
    const journal = U.metaContent(tree, ["citation_journal_title", "citation_conference_title"]);
    const pdfUrl =
      U.metaContent(tree, ["citation_pdf_url"]) || (eprint ? `https://arxiv.org/pdf/${eprint}` : "");
    const absUrl = eprint ? `https://arxiv.org/abs/${eprint}` : "";
    const doiUrl = doi ? `https://doi.org/${doi}` : "";
    const key = citationKey(authors, year, title);

    const first = U.metaContent(tree, ["citation_firstpage"]);
    const last = U.metaContent(tree, ["citation_lastpage"]);
    const entry = journal || doi
      ? {
          type: "article",
          key,
          fields: [
            ["title", bib(title)],
            ["author", authors.map((a) => bib(a.display)).join(" and ")],
            ["journal", bib(journal)],
            ["year", year],
            ["volume", U.metaContent(tree, ["citation_volume"])],
            ["number", U.metaContent(tree, ["citation_issue"])],
            ["pages", first && last ? `${first}--${last}` : first],
            ["publisher", bib(U.metaContent(tree, ["citation_publisher"]))],
            ["doi", doi],
            ["url", doiUrl || ctx.url],
          ],
        }
      : {
          type: "misc",
          key,
          fields: [
            ["title", bib(title)],
            ["author", authors.map((a) => bib(a.display)).join(" and ")],
            ["year", year],
            ["eprint", eprint],
            ["archivePrefix", eprint ? "arXiv" : ""],
            ["primaryClass", primaryClassOf(tree)],
            ["url", absUrl || ctx.url],
          ],
        };

    const facts = [];
    if (authors.length) facts.push(`**Authors:** ${authors.map((a) => a.display).join(", ")}`);
    if (subjects) facts.push(`**Subjects:** ${subjects}`);
    if (eprint) facts.push(`**arXiv:** [${eprint}](${absUrl})`);
    if (doi) facts.push(`**DOI:** [${doi}](${doiUrl})`);
    if (pdfUrl) facts.push(`**PDF:** ${pdfUrl}`);

    const warnings = [];
    if (!abstract) warnings.push("arxiv: this landing page did not expose an abstract, so the capture has only the citation");

    const rendered = bibtex(entry);
    const markdown = U.blocks([
      `# ${title}`,
      facts.join("  \n"),
      abstract ? `## Abstract\n\n${abstract}` : "",
      rendered ? `## Citation\n\n\`\`\`bibtex\n${rendered}\n\`\`\`` : "",
    ]);

    return {
      markdown,
      title,
      meta: {
        authors: authors.map((a) => a.display),
        arxivId: eprint || null,
        doi: doi || null,
        citationKey: key,
        publishedAt: year ? `${year}` : undefined,
        resolvedPdfUrl: pdfUrl || undefined,
        canonicalUrl: absUrl || doiUrl || undefined,
      },
      warnings,
    };
  }

  root.MonoAdapters.register({ name: "arxiv", match, extract });
})(globalThis);
