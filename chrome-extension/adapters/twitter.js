/**
 * MonoAgent Bridge — X / Twitter thread adapter (CLIP-10)
 *
 * A thread is a document that the site refuses to present as one. Saved
 * generically it becomes a wall of interleaved spans with no boundary between
 * posts, no author and no time. This adapter unrolls it: one section per
 * post, in document order — which on a conversation timeline is reply order —
 * each with its author, its ISO timestamp and its own permalink.
 *
 * Only what is rendered. X loads a thread lazily, so what gets captured is
 * what the reader had actually scrolled through; capture_page.js's prepare()
 * pass is what makes that most of the thread rather than the first screen.
 * Nothing here calls the API, and images stay links — the bytes belong in
 * page.mhtml, not inlined into readable.md.
 *
 * `data-testid` is the only durable hook on this site: class names are
 * generated per build, and the DOM shape changes constantly. Everything below
 * matches on testids, with the ARIA role as a second chance.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  const HOSTS = /(^|\.)(x\.com|twitter\.com)$/;

  function match(ctx) {
    const url = (ctx && ctx.url) || "";
    if (!HOSTS.test(U.hostOf(url))) return false;
    return /\/status\/\d+/.test(U.pathOf(url));
  }

  function postsOf(tree) {
    return U.pickAll(tree, [
      { tag: "article", testid: "tweet" },
      { testid: "tweet" },
      { tag: "article", role: "article" },
    ]);
  }

  /**
   * authorOf reads the User-Name block: a display name, an @handle, and a
   * <time> whose datetime attribute is the only timestamp worth keeping —
   * the visible label is relative ("2h") and meaningless once archived.
   */
  function authorOf(post, baseUrl) {
    const block = U.pick(post, [{ testid: "User-Name" }, { testid: "User-Names" }]) || post;
    const links = U.findAll(block, (n) => n.tag === "a");

    let handle = "";
    let name = "";
    for (const link of links) {
      const text = U.text(link);
      if (!handle && /^@[A-Za-z0-9_]+$/.test(text)) handle = text;
      else if (!name && text && !/^@/.test(text) && !/^\d/.test(text)) name = text;
    }
    if (!handle) {
      const span = U.pick(block, [(n) => /^@[A-Za-z0-9_]+$/.test(U.text(n))]);
      if (span) handle = U.text(span);
    }

    const time = U.pick(block, [{ tag: "time", attrs: { datetime: true } }]);
    const permalink = U.pick(block, [(n) => n.tag === "a" && /\/status\/\d+/.test(U.attr(n, "href"))]);

    return {
      name,
      handle,
      at: time ? U.attr(time, "datetime") : "",
      url: permalink ? U.absolute(U.attr(permalink, "href"), baseUrl) : "",
    };
  }

  const label = (a) => (a.name && a.handle ? `${a.name} (${a.handle})` : a.name || a.handle || "");

  function bodyOf(post, baseUrl) {
    const text = U.pick(post, [{ testid: "tweetText" }]);
    const rendered = text ? U.markdownOf(text, baseUrl) : "";
    // Real markup packs sibling spans tight, but a reformatted or
    // server-rendered page leaves whitespace between them; either way the
    // prose should read as one sentence.
    return rendered.replace(/[ \t]{2,}/g, " ").trim();
  }

  function imagesOf(post, baseUrl) {
    const wrappers = U.pickAll(post, [{ testid: "tweetPhoto" }, { testid: "card.wrapper" }]);
    const out = [];
    for (const wrapper of wrappers) {
      for (const img of U.findAll(wrapper, (n) => n.tag === "img")) {
        const src = U.absolute(U.attr(img, "src") || U.attr(img, "data-src"), baseUrl);
        if (!src || out.some((i) => i.src === src)) continue;
        out.push({ src, alt: U.clean(U.attr(img, "alt")) || "Image" });
      }
    }
    return out;
  }

  function firstLine(body, max) {
    const flat = body.replace(/\s+/g, " ").trim();
    if (flat.length <= max) return flat;
    const cut = flat.slice(0, max);
    const space = cut.lastIndexOf(" ");
    return `${(space > max * 0.6 ? cut.slice(0, space) : cut).trim()}…`;
  }

  function extract(ctx) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const found = postsOf(tree);
    if (!found.length) return null;

    const warnings = [];
    let anonymous = 0;
    const posts = [];

    for (const node of found) {
      const author = authorOf(node, baseUrl);
      const body = bodyOf(node, baseUrl);
      const images = imagesOf(node, baseUrl);
      if (!body && !images.length) continue;
      if (!label(author)) anonymous++;
      posts.push({ author, body, images });
    }
    if (!posts.length) return null;

    if (anonymous) {
      warnings.push(
        `twitter: ${anonymous} of ${posts.length} post(s) had no author block and are recorded without attribution`
      );
    }

    const lead = posts[0];
    const leadLabel = label(lead.author);
    const sections = posts.map((post, i) => {
      const who = label(post.author) || `Post ${i + 1}`;
      const heading = post.author.at ? `## ${who} — ${post.author.at}` : `## ${who}`;
      const media = post.images.map((img) => `![${img.alt}](${img.src})`).join("\n\n");
      return U.blocks([heading, post.body, media, post.author.url]);
    });

    const participants = [...new Set(posts.map((p) => p.author.handle).filter(Boolean))];
    const title = leadLabel ? `${leadLabel}: ${firstLine(lead.body, 90)}` : U.metaContent(tree, ["og:title"]) || "";

    return {
      markdown: U.blocks([
        `# ${leadLabel || U.metaContent(tree, ["og:title"]) || "Thread"}`,
        `**Thread:** ${posts.length} post${posts.length === 1 ? "" : "s"} · ${ctx.url}`,
        sections.join("\n\n"),
      ]),
      title: title || null,
      meta: {
        author: lead.author.handle || null,
        participants,
        postCount: posts.length,
        publishedAt: lead.author.at || undefined,
      },
      warnings,
    };
  }

  root.MonoAdapters.register({ name: "twitter", match, extract });
})(globalThis);
