/**
 * MonoAgent Bridge — ChatGPT / Claude conversation adapter (CLIP-10)
 *
 * A conversation is a document with speakers, and the generic pipeline loses
 * exactly that: it returns one undifferentiated column in which you cannot
 * tell the question from the answer. Worse, ChatGPT nests the fence language
 * and a "Copy code" button *inside* the `<pre>`, so a plain text extraction
 * bakes them into the code block.
 *
 * So: turns, in order, with a role heading, and code blocks repaired.
 *
 * The two products mark up their turns completely differently — ChatGPT puts
 * `data-message-author-role` on every turn, Claude marks only the human turn
 * with a `data-testid` and the assistant turn with a class name. Both are
 * found in a single ordered pass, because interleaving is the one property of
 * a conversation that must not be lost, and two separate queries would have
 * to guess at how to merge their results.
 */

(function (root) {
  "use strict";

  const U = root.MonoAdapterUtil;

  const SERVICES = [
    { host: /(^|\.)chatgpt\.com$|(^|\.)chat\.openai\.com$/, path: /^\/(c|g|share)\//, name: "ChatGPT" },
    { host: /(^|\.)claude\.ai$/, path: /^\/(chat|share)\//, name: "Claude" },
  ];

  function serviceFor(url) {
    const host = U.hostOf(url);
    const path = U.pathOf(url);
    return SERVICES.find((s) => s.host.test(host) && s.path.test(path)) || null;
  }

  const match = (ctx) => !!serviceFor((ctx && ctx.url) || "");

  /** roleOf recognizes a turn by whichever signal the product happens to use. */
  function roleOf(node) {
    const declared = U.attr(node, "data-message-author-role").toLowerCase();
    if (declared === "user" || declared === "assistant") return declared;
    if (declared) return null; // system / tool turns are not part of the transcript
    if (U.attr(node, "data-testid") === "user-message") return "user";
    if (/(^|\s)font-claude-message(\s|$)/.test(U.attr(node, "class"))) return "assistant";
    return null;
  }

  /**
   * turnsOf walks once, in document order, and does not descend into a node
   * it has already claimed — nesting a turn inside a turn would double it.
   */
  function turnsOf(tree) {
    const out = [];
    (function descend(node) {
      for (const child of U.kids(node)) {
        if (U.isText(child)) continue;
        const role = roleOf(child);
        if (role) out.push({ role, node: child });
        else descend(child);
      }
    })(tree);
    return out;
  }

  function cloneNode(node) {
    if (U.isText(node)) return { tag: "#text", text: node.text };
    return {
      tag: node.tag,
      attrs: Object.assign({}, node.attrs),
      children: U.kids(node).map(cloneNode),
    };
  }

  /**
   * repairCodeBlocks flattens each <pre> to the shape the Markdown renderer
   * expects — `pre.language-x > code` holding only the source. ChatGPT's
   * toolbar divs live inside the <pre> and would otherwise be rendered as
   * the first lines of the fence.
   */
  function repairCodeBlocks(node) {
    if (U.isText(node)) return node;
    if (node.tag === "pre") {
      const code = U.find(node, (n) => n.tag === "code");
      if (code) {
        const lang = (`${U.attr(code, "class")} ${U.attr(node, "class")}`.match(/(?:language|lang)[-:]([\w+#-]+)/i) || [])[1] || "";
        node.attrs = lang ? { class: `language-${lang}` } : {};
        node.children = [{ tag: "code", attrs: {}, children: [{ tag: "#text", text: U.textOf(code) }] }];
      }
      return node;
    }
    for (const child of U.kids(node)) repairCodeBlocks(child);
    return node;
  }

  function renderTurn(node, baseUrl) {
    // Clone first: ctx.tree is shared with the generic pipeline, which still
    // has to be able to run on this page if something below goes wrong.
    return U.markdownOf(repairCodeBlocks(cloneNode(node)), baseUrl);
  }

  function titleOf(tree, service) {
    const raw =
      U.metaContent(tree, ["og:title"]) ||
      U.pickText(tree, [{ tag: "title" }]) ||
      "";
    return raw
      .replace(/\s*[\\|\-–—·]\s*(ChatGPT|Claude|OpenAI|Anthropic)\s*$/i, "")
      .replace(/^\s*(ChatGPT|Claude)\s*[\\|\-–—·]\s*/i, "")
      .trim() || (service ? `${service.name} conversation` : "");
  }

  function extract(ctx) {
    const service = serviceFor(ctx.url);
    const tree = ctx.tree;
    const baseUrl = ctx.baseUrl || ctx.url;
    const found = turnsOf(tree);
    if (!found.length) return null;

    const turns = [];
    let model = "";
    for (const { role, node } of found) {
      const body = renderTurn(node, baseUrl);
      if (!body) continue; // a streaming placeholder, or a turn that rendered nothing
      model = model || U.attr(node, "data-message-model-slug");
      turns.push({ role, body });
    }
    if (!turns.length) return null;

    const title = titleOf(tree, service);
    const facts = [`**Service:** ${service ? service.name : "Chat"} · ${turns.length} turn${turns.length === 1 ? "" : "s"}`];
    if (model) facts.push(`**Model:** ${model}`);
    facts.push(`**URL:** ${ctx.url}`);

    const body = turns
      .map((t) => `## ${t.role === "user" ? "User" : "Assistant"}\n\n${t.body}`)
      .join("\n\n");

    return {
      markdown: U.blocks([`# ${title}`, facts.join("  \n"), body]),
      title: title || null,
      meta: {
        service: service ? service.name : null,
        model: model || null,
        turnCount: turns.length,
      },
      warnings: [],
    };
  }

  root.MonoAdapters.register({ name: "chat", match, extract });
})(globalThis);
