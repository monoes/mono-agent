/**
 * MonoAgent Bridge — PDF adapter (CLIP-10)
 *
 * When the tab is a PDF, the document is an <embed> and nothing else: every
 * byte of the paper is inside a plugin, and `readable.md` for that tab is an
 * empty file with a title. Worse, it looks like a successful capture.
 *
 * This adapter cannot fix that, and does not pretend to. What it does is make
 * the envelope name the real artifact — `meta.resolvedPdfUrl` — so that a
 * receiver which *can* fetch bytes (the Go bridge, or the CDP side that
 * already has Page.printToPDF attached) stores the PDF instead of the shell.
 *
 * Why the bytes are not fetched here, explicitly:
 *
 *   - Chrome's built-in viewer is an extension origin. `chrome.scripting`
 *     refuses to inject into it, so on a top-level PDF navigation this code
 *     usually never runs at all; the detection matters for the *wrapper page*
 *     case and for whoever reads the meta afterwards.
 *   - From a page context a cross-origin `fetch` of the file is blocked, and
 *     from the service worker it is a second, fresh request — which loses any
 *     PDF behind a one-time token, a POST, or a session the viewer already
 *     spent. A refetched-and-different file recorded as "the captured page"
 *     is worse than an honest pointer.
 *
 * So: record the URL, mark `pdfFetched: false`, and warn. See the report for
 * the Go-side follow-up this implies.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  /** looksLikePdf tests the path only — a query string that mentions pdf is not one. */
  function looksLikePdf(url) {
    return /\.pdf$/i.test(U.pathOf(url));
  }

  function embedded(tree, baseUrl) {
    const node = U.pick(tree, [
      { tag: "embed", attrs: { type: /application\/(pdf|x-google-chrome-pdf)/i } },
      { tag: "object", attrs: { type: /application\/pdf/i } },
      (n) => (n.tag === "iframe" || n.tag === "embed" || n.tag === "object") && /\.pdf(\?|#|$)/i.test(U.attr(n, "src")),
      (n) => n.tag === "a" && /\.pdf(\?|$)/i.test(U.attr(n, "href")),
    ]);
    if (!node) return null;
    const href = U.attr(node, "src") || U.attr(node, "data") || U.attr(node, "href") || "";
    // `src="about:blank"` is Chrome's viewer: the embed is real, the address
    // is not — the tab's own URL is the file.
    if (!href || /^about:/i.test(href)) return { url: "", viewer: true };
    return { url: U.absolute(href, baseUrl), viewer: false };
  }

  function match(ctx) {
    const url = (ctx && ctx.url) || "";
    if (looksLikePdf(url)) return true;
    if (/application\/pdf/i.test((ctx && ctx.contentType) || "")) return true;
    return !!(ctx && ctx.tree && embedded(ctx.tree, url));
  }

  /** strip drops the viewer's own fragment state (#page=3&toolbar=0). */
  function strip(url) {
    const hash = url.indexOf("#");
    return hash < 0 ? url : url.slice(0, hash);
  }

  function extract(ctx) {
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const found = embedded(tree, baseUrl);
    const target = strip((found && found.url) || (looksLikePdf(ctx.url) ? ctx.url : "") || ctx.url);
    if (!target || !/\.pdf$/i.test(U.pathOf(target))) {
      // A PDF we cannot name is not something to write a pointer about.
      if (!/application\/pdf/i.test(ctx.contentType || "")) return null;
    }

    const title =
      U.metaContent(tree, ["og:title", "citation_title", "dc.title"]) ||
      U.pickText(tree, [{ tag: "h1" }, { tag: "title" }]) ||
      decodeURIComponent((U.pathOf(target).split("/").pop() || "document.pdf"));

    // Whatever prose the wrapper page does have is worth keeping — it is
    // usually the abstract, the licence and the citation line.
    const body = U.markdownOf(U.pick(tree, [{ tag: "main" }, { tag: "article" }]), baseUrl);

    const markdown = U.blocks([
      `# ${title}`,
      `This capture's real artifact is a PDF, which has **not been extracted to text** here.`,
      `**PDF:** [${target}](${target})`,
      body,
    ]);

    return {
      markdown,
      title,
      meta: {
        isPdf: true,
        resolvedPdfUrl: target,
        // Deliberately explicit rather than absent: a reader of meta.json
        // should never have to guess whether the bytes were captured.
        pdfFetched: false,
      },
      warnings: [
        `pdf: the extension cannot read the bytes of ${target} from a page context, so the capture records the URL rather than the file — fetch it receiver-side from meta.resolvedPdfUrl`,
      ],
    };
  }

  root.MonoAdapters.register({ name: "pdf", match, extract });
})(globalThis);
