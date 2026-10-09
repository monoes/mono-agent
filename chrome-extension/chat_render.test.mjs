// Rendering an assistant reply (chat_render.js): the Markdown it understands,
// and — the point of the file — that nothing a hostile page talked the model
// into saying can become markup or a script-bearing link.
// `CHROME_PATH=/nonexistent node --test chrome-extension/chat_render.test.mjs`

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { loadExtensionScripts } from "./test_helpers.mjs";

const { MonoChatRender: R } = loadExtensionScripts(["chat_render.js"]);
const HERE = dirname(fileURLToPath(import.meta.url));

// A DOM just big enough to build into, which also records every tag created
// and every attribute set, so the tests can state what the renderer may emit.
function fakeDocument() {
  const made = [];
  const node = (tag) => ({
    tag, children: [], attrs: {}, className: "", _text: "",
    setAttribute(k, v) { this.attrs[k] = v; },
    appendChild(c) { this.children.push(c); return c; },
    set textContent(v) { this._text = v; },
    get textContent() { return this._text + this.children.map((c) => c.textContent).join(""); },
  });
  return {
    made,
    createElement: (tag) => { const n = node(tag); made.push(n); return n; },
    createTextNode: (text) => ({ tag: "#text", textContent: text }),
    createDocumentFragment: () => node("#fragment"),
  };
}

const render = (text) => {
  const doc = fakeDocument();
  return { frag: R.toDom(doc, R.parse(text)), doc };
};

test("paragraphs, headings, lists, quotes and fences", () => {
  const blocks = R.parse("# Title\nsome text\nmore\n\n- a\n- b\n\n1. x\n2) y\n\n> quoted\n> twice\n\n```js\nlet a = 1;\n\nb\n```\nafter");
  assert.deepEqual(blocks.map((b) => b.type), ["h", "p", "list", "list", "quote", "code", "p"]);
  assert.equal(blocks[1].runs[0].text, "some text more", "soft line breaks join");
  assert.deepEqual([blocks[2].ordered, blocks[2].items.length, blocks[3].ordered], [false, 2, true]);
  assert.deepEqual([blocks[5].lang, blocks[5].text], ["js", "let a = 1;\n\nb"]);
});

test("a fence that has not closed yet (a streaming reply) is still code", () => {
  const blocks = R.parse("```\npartial");
  assert.deepEqual(blocks.map((b) => [b.type, b.text]), [["code", "partial"]]);
});

test("inline code, bold, italic and links", () => {
  const runs = R.inline("use `x` and **bold** and *it* see [docs](https://a.example/d) or https://b.example/z");
  assert.deepEqual(runs.filter((r) => r.t !== "text").map((r) => [r.t, r.text]), [
    ["code", "x"], ["strong", "bold"], ["em", "it"], ["link", "docs"],
  ]);
});

test("only http(s) and mailto become links; script-bearing ones are plain label text", () => {
  for (const bad of ["javascript:alert(1)", "JaVaScRiPt:alert(1)", "java\tscript:alert(1)", "data:text/html;base64,AAAA", "vbscript:x", "file:///etc/passwd", "//evil.example/", "chrome://settings"]) {
    assert.equal(R.safeHref(bad), "", bad);
    const runs = R.inline(`[click](${bad.replace(/\s/g, "")})`);
    assert.ok(runs.every((r) => r.t !== "link"), bad);
  }
  assert.equal(R.safeHref("https://a.example/x?y=1"), "https://a.example/x?y=1");
  assert.equal(R.safeHref("mailto:a@b.example"), "mailto:a@b.example");
});

test("raw HTML in a reply is shown as text, never built as elements", () => {
  const { frag, doc } = render('<img src=x onerror=alert(1)> and <script>alert(1)</script>\n\n<iframe src="javascript:1"></iframe>');
  const tags = new Set(doc.made.map((n) => n.tag));
  assert.deepEqual([...tags].sort(), ["p"]);
  assert.match(frag.textContent, /<img src=x onerror=alert\(1\)>/);
  assert.match(frag.textContent, /<script>alert\(1\)<\/script>/);
});

test("everything built is from a short allowlist, and links carry noopener", () => {
  const { doc } = render("# h\n\ntext [a](https://a.example/) `c` **b** *i*\n\n- l\n\n1. n\n\n> q\n\n```\ncode\n```");
  const allowed = new Set(["p", "a", "code", "strong", "em", "ul", "ol", "li", "blockquote", "pre"]);
  for (const n of doc.made) assert.ok(allowed.has(n.tag), n.tag);
  const a = doc.made.find((n) => n.tag === "a");
  assert.deepEqual(a.attrs, { href: "https://a.example/", target: "_blank", rel: "noopener noreferrer" });
  for (const n of doc.made) for (const k of Object.keys(n.attrs)) assert.ok(["href", "target", "rel"].includes(k), k);
});

// A bare address in a reply is text: the model can be talked into writing one
// with someone's data in its query, and a click must not be one tap away.
test("a bare https:// address is never turned into a link", () => {
  const bare = "see https://evil.example/?d=SECRET and http://x.example/y or www.z.example";
  assert.ok(R.inline(bare).every((r) => r.t === "text"));
  const { doc } = render(`${bare}\n\n- https://evil.example/?d=SECRET\n\n> https://evil.example/?d=SECRET`);
  assert.equal(doc.made.filter((n) => n.tag === "a").length, 0);
});

test("an explicit link shows its real host next to the text", () => {
  const { frag, doc } = render("Open [details](https://evil.example:8443/p?d=SECRET) or [https://good.example](https://evil.example/x) or [mail me](mailto:a@Corp.example)");
  const anchors = doc.made.filter((n) => n.tag === "a");
  assert.equal(anchors.length, 3);
  assert.equal(anchors[0].attrs.href, "https://evil.example:8443/p?d=SECRET");
  assert.match(frag.textContent, /details \(evil\.example:8443\)/);
  assert.match(frag.textContent, /https:\/\/good\.example \(evil\.example\)/);
  assert.match(frag.textContent, /mail me \(corp\.example\)/);
});

test("the shown host is the one the browser will use, not the one the text claims", () => {
  for (const [href, host] of [
    ["https://good.example@evil.example/x", "evil.example"],
    ["https://%65vil.example/", "evil.example"],
    ["https://еvil.example/", "xn--vil-qdd.example"],
  ]) {
    const runs = R.inline(`[safe](${href})`);
    assert.equal(runs[0].t, "link", href);
    assert.equal(runs[0].host, host, href);
  }
});

test("the renderer and the panel never use innerHTML or friends", () => {
  for (const f of ["chat_render.js", "sidepanel_chat.js", "chat_bridge.js", "chat_core.js"]) {
    const src = readFileSync(join(HERE, f), "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
    assert.doesNotMatch(src, /innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(|new Function/, f);
  }
});
