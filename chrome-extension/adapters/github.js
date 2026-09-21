/**
 * MonoAgent Bridge — GitHub adapter (CLIP-10)
 *
 * One host, three documents, and the generic pipeline gets none of them
 * right: it saves the file tree and the nav, and on a blob page it saves the
 * line-number column interleaved with the source.
 *
 *   repo  — the README, plus the facts nobody re-finds later (description,
 *           stars at the moment of capture, primary language)
 *   file  — the code, in a fence tagged with the language its extension
 *           implies, under its repository path
 *   PR /
 *   issue — title, state, and the whole discussion in posted order
 *
 * The owner, repo, path and number all come from the URL, which is the one
 * part of GitHub that does not get redesigned. Everything read from markup is
 * tried in several shapes — the React views and the older server-rendered
 * ones are both still served, sometimes for different paths of the same repo.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  // Top-level paths that look like an owner but are not one.
  const RESERVED = new Set([
    "settings", "features", "marketplace", "topics", "collections", "sponsors",
    "explore", "notifications", "pulls", "issues", "codespaces", "orgs",
    "about", "pricing", "login", "join", "new", "search", "apps", "users",
    "site", "security", "enterprise", "team", "contact", "readme", "dashboard",
    "account", "organizations", "trending", "events", "stars", "watching",
  ]);

  // extension -> [fence language, display name]
  const LANGUAGES = {
    go: ["go", "Go"], js: ["javascript", "JavaScript"], mjs: ["javascript", "JavaScript"],
    cjs: ["javascript", "JavaScript"], jsx: ["jsx", "JavaScript"], ts: ["typescript", "TypeScript"],
    tsx: ["tsx", "TypeScript"], py: ["python", "Python"], rb: ["ruby", "Ruby"],
    rs: ["rust", "Rust"], java: ["java", "Java"], kt: ["kotlin", "Kotlin"],
    swift: ["swift", "Swift"], c: ["c", "C"], h: ["c", "C"], cc: ["cpp", "C++"],
    cpp: ["cpp", "C++"], hpp: ["cpp", "C++"], cs: ["csharp", "C#"], php: ["php", "PHP"],
    scala: ["scala", "Scala"], sh: ["bash", "Shell"], bash: ["bash", "Shell"],
    zsh: ["bash", "Shell"], sql: ["sql", "SQL"], html: ["html", "HTML"],
    css: ["css", "CSS"], scss: ["scss", "CSS"], json: ["json", "JSON"],
    yml: ["yaml", "YAML"], yaml: ["yaml", "YAML"], toml: ["toml", "TOML"],
    xml: ["xml", "XML"], md: ["markdown", "Markdown"], vue: ["vue", "Vue"],
    dockerfile: ["dockerfile", "Dockerfile"], mod: ["go", "Go"], sum: ["", "Go"],
  };

  /** route reads everything reliable straight out of the URL. */
  function route(url) {
    if (U.hostOf(url) !== "github.com") return null;
    // safeDecode, not decodeURIComponent: route() runs via match() on every
    // page the extension captures, and one stray `%ZZ` in any URL would
    // otherwise throw straight out of the adapter registry.
    const parts = U.pathOf(url).split("/").filter(Boolean).map(U.safeDecode);
    if (parts.length < 2) return null;
    const [owner, repo, section, ...rest] = parts;
    if (RESERVED.has(owner.toLowerCase())) return null;
    const slug = `${owner}/${repo}`;

    if (section === "blob" && rest.length > 1) {
      return { kind: "file", owner, repo, slug, ref: rest[0], path: rest.slice(1).join("/") };
    }
    if ((section === "pull" || section === "pulls") && /^\d+$/.test(rest[0] || "")) {
      return { kind: "pull", owner, repo, slug, number: Number(rest[0]) };
    }
    if (section === "issues" && /^\d+$/.test(rest[0] || "")) {
      return { kind: "issue", owner, repo, slug, number: Number(rest[0]) };
    }
    if (!section || section === "tree") return { kind: "repo", owner, repo, slug };
    return null;
  }

  const match = (ctx) => !!route((ctx && ctx.url) || "");

  function languageFor(path) {
    const name = String(path).split("/").pop() || "";
    const ext = name.includes(".") ? name.split(".").pop().toLowerCase() : name.toLowerCase();
    return LANGUAGES[ext] || ["", ""];
  }

  // --- repo ---------------------------------------------------------------

  const SCALE = { k: 1e3, m: 1e6, b: 1e9 };

  /**
   * starCount reads GitHub's own label. "4,182" is exact; "4.2k" is rounded
   * and has to be scaled, because stripping its non-digits gives 42 — off by
   * two orders of magnitude and indistinguishable from a real count.
   */
  function starCount(label) {
    const s = String(label).replace(/[\s, ]/g, "");
    if (/^\d+$/.test(s)) return Number(s);
    const m = s.match(/^(\d+(?:\.\d+)?)([kmb])$/i);
    return m ? Math.round(Number(m[1]) * SCALE[m[2].toLowerCase()]) : null;
  }

  function starsOf(tree) {
    const counter = U.pick(tree, [
      { id: "repo-stars-counter-star" },
      { tag: "a", attrs: { href: /\/stargazers$/ } },
    ]);
    if (!counter) return null;
    // The visible label is rounded ("4.2k"); the title attribute is exact,
    // and a capture is a record of a moment, so exact is what belongs in it.
    const exact = U.clean(U.attr(counter, "title")) || U.text(counter);
    const count = starCount(exact);
    // A label with no number in it is the Star *button*, not a count.
    if (count === null && !/\d/.test(exact)) return null;
    return { label: exact, count };
  }

  function languageOf(tree) {
    const link = U.pick(tree, [{ tag: "a", attrs: { href: /[?&]l=/ } }]);
    if (link) {
      const span = U.pick(link, [{ class: "text-bold" }, { tag: "span" }]);
      const name = span ? U.text(span) : "";
      if (name && !/^\d/.test(name)) return name;
    }
    return U.pickText(tree, [{ class: "repo-language-color" }]);
  }

  function repoDocument(ctx, at) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const description =
      U.pickText(tree, [{ tag: "p", class: /(^|\s)f4(\s|$)/ }, { attrs: { itemprop: "about" } }]) ||
      U.metaContent(tree, ["og:description", "description"]).replace(
        /^\s*(GitHub - )?[\w.-]+\/[\w.-]+:\s*/i,
        ""
      );
    const stars = starsOf(tree);
    const language = languageOf(tree);
    const readme = U.pick(tree, [
      { tag: "article", class: "markdown-body" },
      { tag: "article", attrs: { itemprop: "text" } },
      { id: "readme" },
    ]);
    const body = readme ? U.markdownOf(readme, baseUrl) : "";

    if (!description && !stars && !language && !body) return null;

    const warnings = [];
    if (!body) warnings.push(`github: ${at.slug} has no rendered README on this page, so the capture is its facts only`);

    // Slug, description, stars and language all come off the page or out of
    // the URL, so none of them are concatenated into Markdown unescaped.
    const facts = [`**Repository:** ${U.mdText(ctx.url)}`];
    if (stars) facts.push(`**Stars:** ${U.mdText(stars.label)}`);
    if (language) facts.push(`**Language:** ${U.mdText(language)}`);

    return {
      markdown: U.blocks([
        `# ${U.mdText(at.slug)}`,
        U.mdText(description),
        facts.join("  \n"),
        body ? `## README\n\n${body}` : "",
      ]),
      title: at.slug,
      meta: {
        kind: "repo",
        repo: at.slug,
        owner: at.owner,
        description: description || null,
        stars: stars ? stars.count : null,
        language: language || null,
      },
      warnings,
    };
  }

  // --- file ---------------------------------------------------------------

  /**
   * sourceOf prefers the React view's read-only <textarea>, which holds the
   * file's exact bytes. The older view splits each line across syntax spans
   * in a <td>, so those are re-joined in order; the line-number column is a
   * sibling <td> and is skipped by class.
   */
  function sourceOf(tree) {
    const textarea = U.pick(tree, [
      { tag: "textarea", testid: "read-only-cursor-text-area" },
      { tag: "textarea", attrs: { "data-testid": /cursor-text-area/ } },
    ]);
    if (textarea) {
      const raw = U.textOf(textarea).replace(/\n+$/, "");
      if (raw.trim()) return raw;
    }

    const lines = U.pickAll(tree, [
      { tag: "td", class: "blob-code-inner" },
      { tag: "td", class: "js-file-line" },
      { class: "react-code-text" },
    ]);
    if (!lines.length) return "";
    return lines.map((td) => U.textOf(td).replace(/\s+$/, "")).join("\n").replace(/\n+$/, "");
  }

  function fileDocument(ctx, at) {
    const source = sourceOf(ctx.tree);
    if (!source) return null;
    const [fence, language] = languageFor(at.path);
    // A loop rather than Math.max(...runs): the spread throws RangeError past
    // about 125k arguments, and a blob page is whatever bytes the repo holds.
    let longest = 0;
    for (const run of source.match(/`+/g) || []) if (run.length > longest) longest = run.length;
    const ticks = "`".repeat(Math.max(3, longest + 1));

    return {
      markdown: U.blocks([
        `# ${U.mdText(at.path)}`,
        [
          `**Repository:** ${U.mdLink(at.slug, `https://github.com/${at.slug}`)}`,
          `**Ref:** ${U.mdText(at.ref)}`,
          `**URL:** ${U.mdText(ctx.url)}`,
        ].join("  \n"),
        `${ticks}${fence}\n${source}\n${ticks}`,
      ]),
      title: `${at.slug} — ${at.path}`,
      meta: {
        kind: "file",
        repo: at.slug,
        owner: at.owner,
        path: at.path,
        ref: at.ref,
        language: language || null,
        lineCount: source.split("\n").length,
      },
      warnings: [],
    };
  }

  // --- pull request / issue -----------------------------------------------

  function commentsOf(tree, baseUrl) {
    const blocks = U.pickAll(tree, [
      { class: /(^|\s)timeline-comment(\s|$)/ },
      { class: "js-comment-container" },
      { testid: "comment-viewer-outer-box" },
    ]);
    const out = [];
    for (const block of blocks) {
      const body = U.pick(block, [{ class: "comment-body" }, { class: "markdown-body" }]);
      const rendered = body ? U.markdownOf(body, baseUrl) : "";
      if (!rendered) continue;
      const author = U.pickText(block, [
        { tag: "a", class: /(^|\s)author(\s|$)/ },
        { tag: "a", class: "Link--primary" },
      ]);
      const time = U.pick(block, [
        { tag: "relative-time", attrs: { datetime: true } },
        { tag: "time", attrs: { datetime: true } },
      ]);
      out.push({ author, at: time ? U.attr(time, "datetime") : "", body: rendered });
    }
    return out;
  }

  function stateOf(tree) {
    const pill = U.pick(tree, [
      { testid: "header-state" },
      { class: /(^|\s)State(\s|$)/ },
      { class: /State--/ },
    ]);
    const text = pill ? U.text(pill) : "";
    return text.replace(/^status:\s*/i, "");
  }

  function threadDocument(ctx, at) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const title = U.pickText(tree, [
      { tag: "bdi", class: "js-issue-title" },
      { class: "js-issue-title" },
      { tag: "h1", class: "gh-header-title" },
    ]);
    const state = stateOf(tree);
    const comments = commentsOf(tree, baseUrl);
    if (!title && !comments.length) return null;

    const warnings = [];
    if (!comments.length) {
      warnings.push(`github: no discussion was rendered on ${at.slug}#${at.number}, so the capture is its header only`);
    }

    const heading = title || `${at.slug}#${at.number}`;
    const facts = [`**Repository:** ${U.mdLink(at.slug, `https://github.com/${at.slug}`)}`];
    if (state) facts.push(`**State:** ${U.mdText(state)}`);
    facts.push(`**URL:** ${U.mdText(ctx.url)}`);

    // A comment author's own name is page text, and it is what a heading is
    // built out of — so it is escaped rather than interpolated.
    const thread = comments
      .map((c) =>
        U.blocks([`## ${U.mdText(c.author) || "unknown"}${c.at ? ` — ${U.mdText(c.at)}` : ""}`, c.body])
      )
      .join("\n\n");

    return {
      markdown: U.blocks([`# ${U.mdText(heading)}`, facts.join("  \n"), thread]),
      title: `${at.slug}#${at.number}: ${heading}`,
      meta: {
        kind: at.kind,
        repo: at.slug,
        owner: at.owner,
        number: at.number,
        state: state || null,
        commentCount: comments.length,
        publishedAt: comments.length && comments[0].at ? comments[0].at : undefined,
      },
      warnings,
    };
  }

  function extract(ctx) {
    const at = route(ctx.url);
    if (!at) return null;
    if (at.kind === "repo") return repoDocument(ctx, at);
    if (at.kind === "file") return fileDocument(ctx, at);
    return threadDocument(ctx, at);
  }

  root.MonoAdapters.register({ name: "github", match, extract });
})(globalThis);
