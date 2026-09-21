// ChatGPT / Claude conversation adapter (CLIP-10).
//
// A conversation is a document with speakers. Saved generically it is a
// single undifferentiated column of prose in which you cannot tell what you
// asked from what you were told — and the code blocks come out with the
// "Copy code" button baked into the fence.

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
  "adapters/chat.js",
]);

const run = (url, html) =>
  g.MonoAdapters.run({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" });

const GPT = "https://chatgpt.com/c/6c1f2a00-0000-4000-8000-000000000001";
const CLAUDE = "https://claude.ai/chat/8d2e3b11-0000-4000-8000-000000000002";

test("it claims conversation URLs on both products and nothing else", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "chat");
  const claims = (url) => ad.match({ url });

  assert.ok(claims(GPT));
  assert.ok(claims("https://chat.openai.com/c/abc"));
  assert.ok(claims(CLAUDE));
  assert.ok(claims("https://claude.ai/chat/abc"));
  assert.equal(claims("https://claude.ai/"), false, "the launcher is not a conversation");
  assert.equal(claims("https://chatgpt.com/"), false);
  assert.equal(claims("https://claude.ai/code/artifacts"), false);
  assert.equal(claims("https://example.test/c/abc"), false);
});

test("a ChatGPT conversation becomes structured turns in order", () => {
  const out = run(GPT, fixture("chatgpt.html"));

  assert.equal(out.adapter, "chat");
  assert.equal(out.meta.turnCount, 3);
  assert.equal(out.meta.service, "ChatGPT");
  assert.equal(out.meta.model, "gpt-4o");

  const turns = out.markdown.split(/^## /m).slice(1);
  assert.equal(turns.length, 3);
  assert.match(turns[0], /^User\n/);
  assert.match(turns[0], /Why did the Dunmore ledger survive/);
  assert.match(turns[1], /^Assistant\n/);
  assert.match(turns[1], /Survival bias, mostly\./);
  assert.match(turns[2], /^User\n/);
  assert.match(turns[2], /Is that documented anywhere I can cite\?/);
});

test("a ChatGPT code block keeps its language and loses the toolbar", () => {
  const out = run(GPT, fixture("chatgpt.html"));

  assert.match(
    out.markdown,
    /```python\nsurvivors = \[s for s in stations if s\.decommissioned < 1975\]\nprint\(len\(survivors\)\)\n```/
  );
  // The language label and the copy button are siblings inside the <pre>.
  assert.doesNotMatch(out.markdown, /Copy code/);
  assert.doesNotMatch(out.markdown, /```python\npython/);
});

test("prose structure around the code survives too", () => {
  const out = run(GPT, fixture("chatgpt.html"));
  assert.match(out.markdown, /- Stations closed before 1975 kept their logbooks on site\./);
});

test("a Claude conversation interleaves both roles in document order", () => {
  const out = run(CLAUDE, fixture("claude_chat.html"));

  assert.equal(out.meta.service, "Claude");
  assert.equal(out.meta.turnCount, 3);

  const turns = out.markdown.split(/^## /m).slice(1);
  assert.deepEqual(
    turns.map((t) => t.split("\n")[0]),
    ["User", "Assistant", "User"],
    "roles found by two different signals still come back in one order"
  );
  assert.match(turns[1], /Archival climate series are built from the logbooks/);
  assert.match(out.markdown, /```go\nbias := calm \/ total\nfmt\.Println\(bias\)\n```/);
});

test("the title comes from the page, not from the first turn", () => {
  const out = run(CLAUDE, fixture("claude_chat.html"));
  assert.equal(out.title, "Survival bias in logbooks");
  assert.match(out.markdown, /^# Survival bias in logbooks/);
});

test("a conversation page that renders no turns falls back", () => {
  const out = run(GPT, `<html><head><title>ChatGPT</title></head><body><div id="__next"></div></body></html>`);
  assert.equal(out, null);
  assert.ok(g.MonoAdapters.lastWarnings().some((w) => /chat declined/.test(w)));
});

test("an empty turn is skipped rather than emitted as a blank heading", () => {
  const html = `<html><head><title>T \\ Claude</title></head><body>
    <div data-testid="user-message"><p>Only this one.</p></div>
    <div class="font-claude-message"><div></div></div>
  </body></html>`;
  const out = run(CLAUDE, html);

  assert.equal(out.meta.turnCount, 1);
  assert.doesNotMatch(out.markdown, /## Assistant/);
});
