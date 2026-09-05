# Profile Documents & Knowledge Ingestion — Design Spec

Date: 2026-09-05
Status: Approved (Phase 3 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

Phase 3 of the multi-phase job/tender-applier feature. The user's original
request: upload profile files (résumé, cover-letter drafts, LinkedIn
exports, etc.) and images, have monomind build a knowledge graph from them,
and have mono-agent's chat always able to access that knowledge.

### Reused infrastructure (verified by reading the actual code — both
mono-agent's and, for the exact MCP tool contracts, the sibling `monomind`
CLI source at `~/Desktop/monoes/repos/monomind`, read for reference only,
not modified)

- `internal/monomind/kgsync.go`'s `SyncToKnowledgeGraph` — already shells
  out to `monomind mcp exec -t memory_kg_ingest`, with per-profile isolation
  solved via `MONOMIND_CWD` (a documented gotcha in that file: without it,
  every profile's data silently lands in the same shared location). This
  phase's document-ingestion wrapper mirrors this function's structure
  exactly.
- `monomind`'s `knowledge_ingest` MCP tool (`packages/@monomind/cli/src/mcp-tools/knowledge-tools.ts`):
  takes `{path: string}` (a file path), extracts text, chunks, embeds, and
  stores for semantic search — already handles PDF/Office/plain-text
  parsing internally. No capability-flag gate in the handler itself (the
  `.monomind/capabilities.json` "documents" entry is advisory only, read by
  CLAUDE.md generation and `doctor`, not enforced by the tool) — nothing to
  "activate" before calling it.
- `knowledge_search` MCP tool: takes `{query, store?, includeSuperseded?}`,
  returns matching chunks.
- `internal/vault/vault.go`'s `Register`/`VaultDir`/`EnsureVaultDir` — the
  existing per-profile file-vault pattern (currently used for images via
  `vault_images`). This phase adds a sibling `vault_documents` table and
  `RegisterDocument` function in the same package, reusing `VaultDir`.
- `internal/ai/chat/monoagent_tools.go`'s `MonoagentTools` — mono-agent's
  chat already has an LLM tool-calling registry (`ToolDefs()`/`Execute()`)
  covering vault, secrets, people, workflows. This phase adds one more tool,
  `search_profile_documents`, following the exact same pattern (`strParam`
  helpers, a `ToolDefs()` entry, an `Execute` dispatch case, a handler
  method returning a JSON string).
- Images are already fully handled by the existing image vault (a
  lightweight KG node syncs automatically on every image upload via
  `vault.Register`'s existing `monomind.SyncToKnowledgeGraph` call) — no
  new work needed for images this phase.

## Requirements

- Upload a profile document (résumé, cover letter draft, LinkedIn export,
  etc.) via CLI; it's stored in the profile's vault and indexed into
  monomind's Second Brain for semantic search.
- List/remove uploaded profile documents.
- Chat can search the profile's ingested documents as a tool call — this is
  the concrete meaning of "chat can always access that."
- Per-profile isolation: one profile's documents must never appear in
  another profile's search results (mirrors every other Phase 1-2 store).

## Architecture

### Storage: `vault_documents` table (new, in `internal/vault`)

```sql
-- data/migrations/033_vault_documents.sql
CREATE TABLE IF NOT EXISTS vault_documents (
    id          TEXT PRIMARY KEY,   -- "doc-001", following vault_images' "img-NNN" convention
    seq         INTEGER NOT NULL,
    path        TEXT NOT NULL,
    filename    TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL,
    source      TEXT,               -- "upload", "manual", etc.
    profile_id  TEXT NOT NULL DEFAULT 'default',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vault_documents_profile ON vault_documents(profile_id);
```

`internal/vault/vault.go` gets a sibling function to `Register` (which
inserts into `vault_images`):

```go
// RegisterDocument copies src into the profile's vault (under a
// documents/ subdirectory of the same VaultDir used for images) and
// inserts a vault_documents row. Returns the new vault ID (e.g. "doc-001").
// Mirrors Register's structure exactly — same seq-allocation transaction
// pattern (BEGIN IMMEDIATE, same reasoning as Register's comment about
// avoiding a MAX(seq) race), same nullable-source handling.
func RegisterDocument(ctx context.Context, db *sql.DB, src, source string) (string, error)
```

### Knowledge ingestion: two new functions in `internal/monomind`

`internal/monomind/docsync.go`, structured identically to `kgsync.go`:

```go
// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Same
// MONOMIND_CWD isolation as SyncToKnowledgeGraph — see that function's
// comment for why it's required, not optional.
func IngestDocument(ctx context.Context, db *sql.DB, profileID, path string) error

// KnowledgeResult is one matching chunk from SearchKnowledge.
type KnowledgeResult struct {
	Path    string
	Excerpt string
	Score   float64
}

// SearchKnowledge queries profileID's Second Brain via
// `monomind mcp exec -t knowledge_search --format json`, scoped to
// store="project" (the per-profile monomind dir set via MONOMIND_CWD IS
// the project root for this call, so "project" here means "this profile's
// own documents," not mono-agent's own source code) — never "global" or
// "all", which would leak the user's personal cross-project brain into a
// job-application context that has no business reading it. See the
// protocol note below for why the response must be decoded twice.
func SearchKnowledge(ctx context.Context, db *sql.DB, profileID, query string) ([]KnowledgeResult, error)
```

### Protocol note: the `mcp exec --format json` response is double-encoded

Verified by reading `monomind`'s actual CLI source
(`packages/@monomind/cli/src/commands/mcp.ts`'s `exec` command and
`src/mcp-client.ts`'s `callMCPTool`, and the tool handler itself in
`src/mcp-tools/knowledge-tools.ts`) rather than assumed:

`monomind mcp exec -t <tool> -p '<json>' --format json` prints
`{"tool":...,"params":...,"result":{"content":[{"type":"text","text":"<JSON string>"}]},"duration":...}`
— the outer JSON's `result.content[0].text` is *itself* a JSON-encoded
string (the tool handler's actual payload, e.g.
`{"success":true,"results":[{"kind":"excerpt","filePath":...,"text":...,"similarity":...}, ...]}`
for `knowledge_search`). `SearchKnowledge` must decode twice: once for the
CLI's wrapper envelope, once for `content[0].text`. `IngestDocument` only
needs to check the outer command's exit code (mirroring
`SyncToKnowledgeGraph`'s fire-and-forget style — it doesn't need the inner
payload at all, so it does not need `--format json`). For
`knowledge_search`, only `results` entries with `"kind":"excerpt"` map to
`KnowledgeResult` (`filePath`→`Path`, `text`→`Excerpt`,
`similarity`→`Score`) — the other kinds (`triplet`, `rule`, `memory`) are
knowledge-graph/pattern-store results outside this phase's scope (the KG
side is already handled separately by the existing image-vault sync via
`internal/monomind/kgsync.go`).

**Known limitation**: this protocol detail was derived by reading
`monomind`'s TypeScript source directly, not by running the real binary
against a live query (no such capability in this environment). The
implementation's tests validate the parsing logic against a fake `monomind`
binary emitting this exact shape — they prove the Go code correctly
unwraps a response shaped like this, not that today's live `monomind`
binary still behaves exactly this way. This is the same category of
limitation Phase 2 already flagged for LinkedIn's selectors.

### CLI

```
monoagentcli profile upload-document <path> [--source manual]
monoagentcli profile documents list
monoagentcli profile documents rm <id>
monoagentcli profile search-knowledge "<query>"
```

`upload-document` calls `vault.RegisterDocument` then `monomind.IngestDocument`
with the stored path — ingestion failure is logged as a warning (matching
`kgsync.go`'s doc comment: "safe to call from a fire-and-forget goroutine...
never on KG-side outcomes") but does not fail the upload itself, since the
file is already safely stored regardless of whether indexing succeeded.
`search-knowledge` exists for manual testing/debugging — the primary
consumer is the chat tool below.

### Chat tool: `search_profile_documents`

In `internal/ai/chat/monoagent_tools.go`: add a `ToolDefs()` entry (name
`search_profile_documents`, one string parameter `query`), an `Execute`
dispatch case, and a handler:

```go
func (mt *MonoagentTools) searchProfileDocuments(args string) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("parsing args: %w", err)
	}
	if params.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	results, err := monomind.SearchKnowledge(context.Background(), mt.db, mt.ProfileID(), params.Query)
	if err != nil {
		return "", fmt.Errorf("searching profile documents: %w", err)
	}
	return marshalJSON(results)
}
```

No `fenceUntrusted` wrapping — these are the user's own uploaded documents
(résumé, cover letters), not adversarial scraped content, so they're
trusted the same way vault/people data already returned by other tools in
this file is trusted.

## Data Flow

1. `monoagentcli profile upload-document ~/resume.pdf` →
   `vault.RegisterDocument` copies the file into the vault, inserts a
   `vault_documents` row, returns `doc-001`.
2. `monomind.IngestDocument(ctx, db, profileID, storedPath)` runs — best
   effort, logged on failure, never blocks the CLI command's success.
3. Later, in chat, the LLM calls `search_profile_documents` with a query
   (e.g. "what backend frameworks does the user have experience with") →
   `monomind.SearchKnowledge` → matching chunks from the ingested résumé
   returned to the chat model as tool output.

## Error Handling

- `vault.RegisterDocument` failing (file not found, copy error) → CLI
  surfaces the error directly, nothing is indexed (matches `Register`'s
  existing image-upload error handling).
- `monomind.IngestDocument` failing (monomind binary missing, non-zero
  exit) → logged as a warning to stderr, upload command still reports
  success (the file is safely stored; indexing can be retried later — no
  retry mechanism this phase, matches YAGNI, the file remains in the vault
  so this isn't a lost-data situation).
- `monomind.SearchKnowledge` failing → the chat tool call returns an error
  string to the LLM (visible in the conversation as a tool error, same as
  any other `MonoagentTools` handler failure) rather than crashing the chat
  session.
- Per-profile scoping is enforced entirely by `MONOMIND_CWD` pointing at
  the profile's own monomind directory — there is no cross-profile query
  path to accidentally expose.

## Testing

- `internal/vault/documents_test.go` — `RegisterDocument` round-trip (file
  copied, row inserted, ID format, seq increments), scoped-by-profile
  listing.
- `internal/monomind/docsync_test.go` — `IngestDocument`/`SearchKnowledge`
  against a fake `monomind` binary. `kgsync.go` (the function this phase's
  code mirrors) has no dedicated test file of its own to copy from, but
  `internal/monomind/exec_test.go`'s `fakeBin(t *testing.T, name string) string`
  helper (which builds a real executable test double from a fixture script)
  is the established pattern in this package — reuse it rather than
  inventing a new fake-binary mechanism. `internal/monomind/testdata/fake-monomind.sh`
  is the existing fixture script (this session already fixed its executable
  bit in Phase 1) — check whether it already handles a `mcp exec -t
  knowledge_ingest`/`knowledge_search` case or needs a small addition for
  these two new tool names.
- `cmd/monoagentcli/profile_documents_test.go` — CLI integration tests for
  `upload-document`/`documents list`/`documents rm`/`search-knowledge`
  against the same fake-binary fixture.
- `internal/ai/chat/monoagent_tools_test.go` — extend with a test for the
  new `search_profile_documents` tool dispatch (existing file, existing
  pattern — see e.g. `listVaultItems`'s test for the shape to match).

## Out of Scope (this phase)

- Images: already fully handled by the existing image vault + its existing
  `SyncToKnowledgeGraph` call — nothing new needed.
- OCR of scanned/image-based PDFs — `knowledge_ingest`'s own extraction
  capabilities are whatever monomind itself supports; this phase does not
  add any extraction logic of its own.
- Automatic re-ingestion on file change — a document is indexed once at
  upload time; re-uploading (or a future `profile documents reindex`
  command) is how you'd refresh it. Not needed for this phase's scope.
- Using this knowledge for job matching (Phase 5) or CV generation
  (Phase 4) — this phase only builds the ingest/search substrate.
