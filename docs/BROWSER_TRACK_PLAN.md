# Browser Track — Build Plan

Capture the web into monomind's document brain, and turn the browser into an
instrumented test rig agents can read. 44 user stories, five tracks (Trust
deferred by request), two repos.

Backlog page (pickable, live status):
https://claude.ai/artifact/K4XkTQ5cqgbt2JE3m9qP64

| Repo | Branch | Stack |
|---|---|---|
| `monoes/mono-agent` | `feat/browser-capture-track` | Go + `chrome-extension/` (MV3) |
| `monoes/monomind` | `feat/browser-track` | TypeScript (pnpm workspace) |

## Why these two repos touch

- `mono-agent/chrome-extension/` drives the user's **real, logged-in Chrome**
  over a WS bridge (`ws://127.0.0.1:9222/monoagent`) and already holds the
  `debugger` permission — so CDP `Page.captureSnapshot` (MHTML) and
  `Page.printToPDF` need **no new permission**.
- `monomind/packages/@monoes/monobrowse/` drives a headless Chrome and already
  implements console capture, HAR, web vitals, CPU profiling, tracing, device
  emulation, recording and PDF — none of it reachable from the 23 `browser_*`
  MCP tools.
- `monomind/packages/@monomind/cli/src/capabilities/cap-documents.ts` ingests
  `.pdf`, `.docx`, `.epub` … and **no HTML type at all**, which blocks every
  capture story.

## Contract: the capture envelope

Every capture, wherever it comes from, lands in `~/.monomind/inbox/<ts>-<slug>/`:

```
page.mhtml        # byte-fidelity archive (CDP Page.captureSnapshot)
page.pdf          # optional, print fidelity (Page.printToPDF)
readable.md       # Readability-cleaned Markdown — this is what gets chunked
screenshot.png    # full-page, for the document card
meta.json         # provenance (below)
```

`meta.json`:

```json
{
  "url": "…", "canonicalUrl": "…", "title": "…", "byline": null,
  "publishedAt": null, "capturedAt": "ISO-8601", "httpStatus": 200,
  "contentHash": "sha256:…", "favicon": "…", "selection": null,
  "note": null, "tags": [], "collection": null, "source": "extension|monobrowse|crawl"
}
```

Dedupe key is `canonicalUrl` + `contentHash`. Same URL + new hash = new
**version** of the same document, never a duplicate row.

## Waves

Each wave is a set of agents with disjoint file ownership. Nothing in a later
wave starts before the wave it depends on lands.

### Wave 1 — foundations (unblocks everything)

| Agent | Repo | Owns | Stories |
|---|---|---|---|
| `ingest` | monomind | `cli/src/capabilities/cap-documents.ts`, `cli/src/knowledge/document-pipeline.ts` | RCL-01, RCL-06, RCL-07 |
| `instruments` | monomind | `cli/src/mcp-tools/browser-tools.ts` | RIG-01, RIG-02, RIG-03, RIG-04, RIG-09 |
| `report` | monomind | `monobrowse/src/report/**`, `monobrowse/src/cli/commands.ts` | RIG-05, RIG-06, RIG-08 |
| `ext-capture` | mono-agent | `chrome-extension/**` | CLIP-01, CLIP-03, CLIP-04, CLIP-05, CLIP-09 |
| `go-bridge` | mono-agent | `internal/extension/**`, `cmd/monoagentcli/capture*.go` | CLIP-01 (Go half), CLIP-08 |

### Wave 2 — the loop closes

| Agent | Repo | Owns | Stories |
|---|---|---|---|
| `ext-ux` | mono-agent | `chrome-extension/popup*`, `content.js` | CLIP-06, CLIP-07, CLIP-08 (UI), CLIP-11 |
| `adapters` | mono-agent | `chrome-extension/adapters/**` | CLIP-10, CLIP-12 |
| `recall` | monomind | `cli/src/knowledge/**`, `cli/src/commands/doc.ts` | RCL-03, RCL-09, RCL-10 |
| `rig-2` | monomind | `monobrowse/src/report/**` (ext), `monobrowse/src/browser/diff.ts` | RIG-10, RIG-11, RIG-13, RIG-14 |
| `glue-1` | both | `cmd/monoagentcli/crawl.go`, ingest bridge | GLU-03, GLU-06, GLU-08 |

### Wave 3 — integration and the hard ones

| Agent | Repo | Owns | Stories |
|---|---|---|---|
| `already-saved` | both | extension badge + `doc lookup` API | RCL-02, RCL-05 |
| `highlights` | both | `content.js` highlighter + notes ingest | RCL-04 |
| `provider` | both | unified browser abstraction, extension-as-provider | GLU-01, RIG-07, RIG-12 |
| `workflow` | mono-agent | action recorder → workflow nodes | GLU-02, GLU-04, GLU-05 |
| `resources` | monomind | MCP resources for captures | GLU-07 |

## Rules for every agent

- TDD: test alongside implementation, co-located (`*_test.go`, `*.test.ts`).
- Go: `go build ./... && go test ./... && go vet ./... && gofmt -l .`
- TS: `pnpm typecheck`, `npx vitest run <path>`, Biome for format.
- Files stay under 500 lines; split before that.
- Touch only the files your row owns. Do not commit.
- No new runtime dependency without saying so in the report.

## Story ledger

Status is mirrored onto the backlog page after each wave.

### Clip
- CLIP-01 one-shortcut save (MHTML) — W1
- CLIP-02 logged-in pages — W1 (falls out of 01)
- CLIP-03 PDF with stamped footer — W1
- CLIP-04 selection capture — W1
- CLIP-05 readable extraction for indexing — W1
- CLIP-06 save all tabs / tab group — W2
- CLIP-07 note + tags at save — W2
- CLIP-08 offline queue + flush — W1/W2
- CLIP-09 lazy-load prep before snapshot — W1
- CLIP-10 per-site adapters (YouTube, arXiv, X, GitHub) — W2
- CLIP-11 table → CSV — W2
- CLIP-12 repeating-element / paginated extraction — W2

### Recall
- RCL-01 HTML/MHTML ingest — W1 **blocker for all Clip**
- RCL-02 "already saved" badge — W3
- RCL-03 related-at-save — W2
- RCL-04 highlights as atomic notes — W3
- RCL-05 ask-my-brain side panel — W3
- RCL-06 hash dedupe + versioning — W1
- RCL-07 provenance in every result — W1
- RCL-08 watch page for change — W2 (rides RCL-06 diff)
- RCL-09 library filters in GUI — W2
- RCL-10 paragraph-level citation — W2

### Test rig
- RIG-01 console errors as an MCP tool — W1
- RIG-02 network / failed requests — W1
- RIG-03 web vitals — W1
- RIG-04 CPU profile + heap snapshot — W1
- RIG-05 one-command HTML report — W1
- RIG-06 pass/fail budgets verdict — W1
- RIG-07 drive real logged-in Chrome — W3
- RIG-08 accessibility report from AX tree — W1
- RIG-09 device matrix screenshots — W1
- RIG-10 visual + AX diff between runs — W2
- RIG-11 recorded frames bug report — W2
- RIG-12 replay a user's capture + HAR — W3
- RIG-13 report history / trends — W2
- RIG-14 flake detection — W2

### Glue
- GLU-01 one browser abstraction, three backends — W3
- GLU-02 demonstrate → workflow — W3
- GLU-03 page → task with attachment — W2
- GLU-04 capture triggers a workflow — W3
- GLU-05 HIL approval badge in browser — W3
- GLU-06 crawl → document pipeline — W2
- GLU-07 captures as MCP resources — W3
- GLU-08 portable export of the library — W2

### Trust — deferred by request
TRU-01…06 not scheduled. TRU-03 (strip scripts from archives) and TRU-04
(size budget) are cheap and land inside the capture code anyway; note them
when that code is written.

## Pending integration tasks (do after every Wave 1 agent is idle)

These must land together — doing any one alone leaves the tree worse:

1. `packages/@monomind/cli/package.json`: `"@monoes/monobrowse": "^1.0.6"` → `"workspace:*"`,
   then `pnpm install`. Until this flips, the CLI and every `browser_*` MCP tool
   resolve the **published 1.0.6 tarball**, so nothing built in the workspace
   package (the whole `src/report/**` tree, the profiler fix) is reachable.
2. `browser-profile-tools.ts`: delete `captureHeapSnapshot` (~:371) and the
   `if (size === 0)` guard (~:172-178). With the profiler fix, a no-data
   snapshot throws before returning a path, so that branch is unreachable.
   Delete the `heapSnapshotBroken: true` test with it; keep the `false` one.
3. ~~Pass a 4th arg `120_000` to the `HeapProfiler.takeHeapSnapshot` send.~~
   **STRUCK 2026-09-21 — satisfied by the monobrowse fix, and mutually
   exclusive with task 2.** That send lived *inside* `captureHeapSnapshot`,
   which task 2 deletes; it was the only such call site in the CLI package.
   The CLI now reaches `startHeapSnapshot` in monobrowse, which already passes
   `HEAP_SNAPSHOT_TIMEOUT_MS` (`profiler.ts:12` = 120_000) over the 30s default
   at `cdp.ts:12`. The two paths agree because there is now only one path.
4. Re-run: `pnpm typecheck`, `npx vitest run` in both packages, `biome check`.

Verified 2026-09-21: root `pnpm typecheck` exits 0 on the current tree. A
report of a `TS2554` at `browser-profile-tools.ts:388` was a stale reading —
the 4th argument is not there yet (that is task 3, above).

### Wave 1 + integration: verified complete 2026-09-21

Dep is `workspace:*` (the repo's own convention for internal packages);
`packages/@monomind/cli/node_modules/@monoes/monobrowse` links to the
workspace copy at 1.0.12; all three publish guards pass; root `pnpm typecheck`
exits 0. The whole `src/report/**` surface is reachable from the CLI.

`@monoes/monodesign/package.json:54` still pins monobrowse `^1.0.7` from the
registry. Left alone deliberately — that package publishes against the
registry copy. Revisit only if monodesign needs the report API.
