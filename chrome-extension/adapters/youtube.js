/**
 * MonoAgent Bridge — YouTube adapter (CLIP-10)
 *
 * The generic pipeline saves a YouTube page as a player shell: a title, a
 * subscribe button and a rail of unrelated recommendations. What the page is
 * about — the talk itself — is in the transcript panel, which scores as
 * furniture and gets pruned.
 *
 * This adapter reads only what the page already rendered: the metadata block,
 * the description expander, and the transcript panel when the viewer has
 * opened it. It never calls the YouTube API, never fetches a timedtext URL,
 * and never touches a third-party host — so a capture stays exactly as
 * private as the tab it came from.
 *
 * When the transcript panel is closed there is nothing to read and the
 * capture says so in a warning rather than inventing one. `ext-ux` can raise
 * the hit rate by opening the panel before capture; that is a UI decision,
 * not this file's.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  const VIDEO_HOSTS = /^(m\.|music\.)?youtube\.com$|^youtube-nocookie\.com$/;

  /** videoId pulls the eleven-character id out of every URL shape YouTube uses. */
  function videoId(url) {
    let u;
    try {
      u = new URL(url);
    } catch {
      return "";
    }
    const host = u.hostname.replace(/^www\./i, "").toLowerCase();
    if (host === "youtu.be") return u.pathname.slice(1).split("/")[0] || "";
    const v = u.searchParams.get("v");
    if (v) return v;
    const m = u.pathname.match(/^\/(?:shorts|embed|live|v)\/([^/?#]+)/);
    return m ? m[1] : "";
  }

  function match(ctx) {
    const url = (ctx && ctx.url) || "";
    const host = U.hostOf(url);
    if (host !== "youtu.be" && !VIDEO_HOSTS.test(host)) return false;
    return !!videoId(url);
  }

  /** seconds turns "1:02:30" or "0:14" into 3750 / 14 for a &t= deep link. */
  function seconds(stamp) {
    const parts = String(stamp).trim().split(":").map((p) => parseInt(p, 10));
    if (!parts.length || parts.some((p) => !Number.isFinite(p))) return null;
    return parts.reduce((total, part) => total * 60 + part, 0);
  }

  const TIMESTAMP = /^\d{1,2}(:\d{2}){1,2}$/;

  /**
   * transcript reads the rendered transcript panel. Each segment carries a
   * timestamp and a line; both live in elements whose *class* is stable even
   * though the custom-element names around them are not, so match on class.
   */
  function transcript(tree) {
    const segments = U.pickAll(tree, [
      { tag: "ytd-transcript-segment-renderer" },
      { class: /(^|\s)segment(\s|$)/, attrs: { role: "button" } },
      { class: "transcript-segment" },
    ]);

    const lines = [];
    for (const segment of segments) {
      const stamp = U.pickText(segment, [
        { class: "segment-timestamp" },
        { class: "timestamp" },
        (n) => TIMESTAMP.test(U.text(n)) && !U.kids(n).some((c) => !U.isText(c)),
      ]);
      const said = U.pickText(segment, [{ class: "segment-text" }, { tag: "yt-formatted-string" }]);
      if (!stamp || !said || !TIMESTAMP.test(stamp)) continue;
      lines.push({ stamp, at: seconds(stamp), text: said });
    }
    return lines;
  }

  function channelOf(tree, baseUrl) {
    const owner =
      U.pick(tree, [{ tag: "ytd-channel-name" }, { id: "owner" }, { tag: "ytd-video-owner-renderer" }]) || tree;
    const link = U.pick(owner, [
      (n) => n.tag === "a" && /^\/(@|channel\/|c\/|user\/)/.test(U.attr(n, "href")),
      (n) => n.tag === "a" && /youtube\.com\/(@|channel\/)/.test(U.attr(n, "href")),
    ]);
    const name =
      (link && U.text(link)) ||
      U.metaContent(tree, ["channelname", "author"]) ||
      U.pickText(tree, [{ tag: "ytd-channel-name" }]);
    return {
      name: name || "",
      url: link ? U.absolute(U.attr(link, "href"), baseUrl) : "",
    };
  }

  function descriptionOf(tree, baseUrl) {
    const node = U.pick(tree, [
      { id: "description-inline-expander" },
      { tag: "ytd-text-inline-expander" },
      { id: "description" },
      { tag: "yt-attributed-string" },
    ]);
    const rendered = node ? U.markdownOf(node, baseUrl) : "";
    if (rendered) return rendered;
    return U.metaContent(tree, ["og:description", "description"]);
  }

  function titleOf(tree) {
    const heading = U.pick(tree, [
      (n) => n.tag === "h1" && /ytd-watch-metadata/.test(U.attr(n, "class")),
      { tag: "ytd-watch-metadata" },
    ]);
    const fromPage = heading && heading.tag === "h1" ? U.text(heading) : "";
    return (
      fromPage ||
      U.metaContent(tree, ["og:title", "twitter:title", "title"]) ||
      U.pickText(tree, [{ tag: "h1" }])
    );
  }

  function extract(ctx) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const id = videoId(ctx.url);
    const title = titleOf(tree);
    const channel = channelOf(tree, baseUrl);
    const description = descriptionOf(tree, baseUrl);
    const lines = transcript(tree);

    // A page with none of the three is a redesign we do not understand. Say
    // nothing rather than emit a title over an empty document.
    if (!title && !description && !lines.length) return null;

    const watchUrl = id ? `https://www.youtube.com/watch?v=${id}` : ctx.url;
    const parts = [title ? `# ${title}` : ""];

    const facts = [];
    if (channel.name) {
      facts.push(channel.url ? `**Channel:** [${channel.name}](${channel.url})` : `**Channel:** ${channel.name}`);
    }
    facts.push(`**Video:** ${watchUrl}`);
    parts.push(facts.join("  \n"));

    if (description) parts.push(`## Description\n\n${description}`);

    const warnings = [];
    if (lines.length) {
      const body = lines
        .map((line) =>
          line.at === null
            ? `${line.stamp} ${line.text}`
            : `[${line.stamp}](${watchUrl}&t=${line.at}s) ${line.text}`
        )
        .join("\n\n");
      parts.push(`## Transcript\n\n${body}`);
    } else {
      warnings.push(
        "youtube: no transcript panel was open on this page, so the capture has none — open “Show transcript” before saving"
      );
    }

    return {
      markdown: U.blocks(parts),
      title: title || null,
      meta: {
        videoId: id || null,
        channel: channel.name || null,
        channelUrl: channel.url || null,
        transcriptSegments: lines.length,
        canonicalUrl: id ? watchUrl : undefined,
      },
      warnings,
    };
  }

  root.MonoAdapters.register({ name: "youtube", match, extract });
})(globalThis);
