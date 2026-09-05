# Profile Documents & Knowledge Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (`mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per the two prior phase plans' precedent.)

**Goal:** Upload a profile document into the vault, index it into monomind's Second Brain, and let mono-agent's chat search it via a new tool call.

**Architecture:** A new `vault_documents` table + `vault.RegisterDocument` (mirroring the existing image vault exactly); two new `internal/monomind` functions (`IngestDocument`, `SearchKnowledge`) mirroring `kgsync.go`'s subprocess pattern; CLI commands under `profile`; one new tool in the existing chat tool-calling registry.

**Tech Stack:** No new dependencies — pure Go stdlib (`os/exec`, `encoding/json`) plus the existing `monomind` CLI binary as a subprocess, exactly as `kgsync.go` already does.

## Global Constraints

- Go toolchain at `~/.local/go/bin`, not on default PATH: `export PATH="$HOME/.local/go/bin:$PATH"` before any `go` command.
- Migrations: numbered SQL files in `data/migrations/`. Next number is 034 (033 reserved below turned out unnecessary — see Task 1).
- Per-profile isolation for every `monomind` subprocess call: `MONOMIND_CWD` must be set to the profile's monomind directory (`profiledir.MonomindDir(db, profileID)`), and that directory must exist on disk *before* the subprocess runs — see `internal/monomind/kgsync.go`'s `SyncToKnowledgeGraph` comment for why (a missing directory makes monomind's project-root resolution walk past it and silently merge profiles).
- `monomind mcp exec -t <tool> -p '<json>'` with `--format json` prints `{"result":{"content":[{"text":"<JSON string>"}]}}` — a double-encoded response (outer CLI envelope, inner tool payload). Only `SearchKnowledge` needs to decode the inner payload; `IngestDocument` only needs the subprocess exit code, so it omits `--format json` entirely (matching `SyncToKnowledgeGraph`, which does the same).
- `FilteredEnviron()` (existing, `internal/monomind/exec.go`) strips any ambient `MONOMIND_*` env vars before the explicit `MONOMIND_CWD=...` is appended — reuse it exactly as `kgsync.go` does, do not build the subprocess environment any other way.
- Test fixture: `internal/monomind/testdata/fake-monomind.sh` is the shared fake binary for every test in this package (`fakeBin(t, "fake-monomind.sh")` helper in `exec_test.go`) — new cases are appended to its existing `if`/`elif` chain, never a second fixture file.
- TDD: every behavior gets a failing test before its implementation. No real `monomind` binary invocation or real filesystem-outside-`t.TempDir()` access in any test.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `data/migrations/034_vault_documents.sql` | New `vault_documents` table. |
| `internal/vault/documents.go` | `RegisterDocument`, `ListDocuments`, `DeleteDocument`. |
| `internal/vault/documents_test.go` | Tests for the above. |
| `internal/monomind/docsync.go` | `IngestDocument`, `SearchKnowledge`, `KnowledgeResult`. |
| `internal/monomind/docsync_test.go` | Tests against the fake binary. |
| `internal/monomind/testdata/fake-monomind.sh` | Modified: new `mcp exec` cases for `knowledge_ingest`/`knowledge_search`. |
| `cmd/monoagentcli/profile_documents.go` | `profile upload-document`/`documents list`/`documents rm`/`search-knowledge` subcommands. |
| `cmd/monoagentcli/profile_documents_test.go` | CLI integration tests. |
| `cmd/monoagentcli/profile.go` | Modified: register the new subcommands. |
| `internal/ai/chat/monoagent_tools.go` | Modified: add `search_profile_documents` tool. |
| `internal/ai/chat/monoagent_tools_test.go` | Modified: add a test for the new tool. |

---

### Task 1: `vault_documents` table + `RegisterDocument`

**Files:**
- Create: `data/migrations/034_vault_documents.sql`
- Create: `internal/vault/documents.go`
- Create: `internal/vault/documents_test.go`

**Interfaces:**
- Consumes: `vault.{VaultDir,EnsureVaultDir,ProfileIDFromContext}` (existing, `internal/vault/vault.go`).
- Produces: `vault.RegisterDocument(ctx context.Context, db *sql.DB, src, source string) (string, error)`, `vault.ListDocuments(ctx context.Context, db *sql.DB, profileID string) ([]vault.DocumentEntry, error)`, `vault.DeleteDocument(ctx context.Context, db *sql.DB, profileID, id string) error`, `vault.DocumentEntry{ID,Path,Filename string; SizeBytes int64; Source string; CreatedAt string}`. Consumed by Task 4 (CLI) and, indirectly via the stored `Path`, Task 2 (`IngestDocument`).

- [ ] **Step 1: Write the migration**

```sql
-- data/migrations/034_vault_documents.sql
-- A profile-scoped document vault, sibling to vault_images, for uploaded
-- profile files (résumés, cover-letter drafts, LinkedIn exports, etc.)
-- that get indexed into monomind's Second Brain.

CREATE TABLE IF NOT EXISTS vault_documents (
    id          TEXT PRIMARY KEY,   -- "doc-001", "doc-002", ...
    seq         INTEGER NOT NULL UNIQUE,
    path        TEXT NOT NULL,
    filename    TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    source      TEXT NOT NULL DEFAULT 'upload',
    profile_id  TEXT NOT NULL DEFAULT 'default',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vault_documents_profile ON vault_documents(profile_id);
```

- [ ] **Step 2: Write the failing tests**

```go
// internal/vault/documents_test.go
package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vault-documents-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestRegisterDocumentRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	src := writeTestFile(t, "Experienced backend engineer.")

	id, err := vault.RegisterDocument(ctx, db.DB, src, "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	if id != "doc-001" {
		t.Fatalf("expected first document id doc-001, got %q", id)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id || docs[0].Filename != "resume.txt" {
		t.Fatalf("unexpected documents list: %+v", docs)
	}
	if _, err := os.Stat(docs[0].Path); err != nil {
		t.Fatalf("expected copied file to exist at %q: %v", docs[0].Path, err)
	}
}

func TestRegisterDocumentIncrementingSeq(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	id1, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "one"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument 1: %v", err)
	}
	id2, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "two"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument 2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("expected distinct ids, got %q twice", id1)
	}
}

func TestDeleteDocument(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}

	if err := vault.DeleteDocument(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no documents after delete, got %d", len(docs))
	}
}

func TestListDocumentsScopedToProfile(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	if _, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload"); err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "other-profile")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no documents for other-profile, got %d", len(docs))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/vault/... -run 'TestRegisterDocument|TestDeleteDocument|TestListDocuments' -v`
Expected: FAIL with "undefined: vault.RegisterDocument" (compile error).

- [ ] **Step 4: Write documents.go**

```go
// internal/vault/documents.go
package vault

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// DocumentEntry is one row from vault_documents.
type DocumentEntry struct {
	ID        string
	Path      string
	Filename  string
	SizeBytes int64
	Source    string
	CreatedAt string
}

// RegisterDocument copies src into the profile's vault (under a
// documents/ subdirectory of the same VaultDir used for images) and
// inserts a vault_documents row. Returns the new vault ID (e.g. "doc-001").
// Mirrors Register's structure exactly (same BEGIN IMMEDIATE seq-allocation
// pattern — see Register's doc comment in vault.go for why a deferred
// transaction would race two concurrent Registers onto the same seq).
func RegisterDocument(ctx context.Context, db *sql.DB, src, source string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("vault.RegisterDocument: db is nil")
	}
	if src == "" {
		return "", fmt.Errorf("vault.RegisterDocument: src path is empty")
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: invalid src path: %w", err)
	}

	profileID := ProfileIDFromContext(ctx)
	docsDir := filepath.Join(VaultDir(db, profileID), "documents")
	if err := os.MkdirAll(docsDir, 0700); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: ensure documents dir: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: get conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var seq int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM vault_documents`).Scan(&seq); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: get next seq: %w", err)
	}

	id := fmt.Sprintf("doc-%03d", seq)
	filename := filepath.Base(absSrc)
	destPath := filepath.Join(docsDir, fmt.Sprintf("%s%s", id, filepath.Ext(filename)))

	if err := copyFile(absSrc, destPath); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: copy file: %w", err)
	}
	fi, err := os.Stat(destPath)
	if err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: stat dest: %w", err)
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_documents (id, seq, path, filename, size_bytes, source, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		id, seq, destPath, filename, fi.Size(), source, profileID,
	)
	if err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: insert: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: commit: %w", err)
	}
	committed = true
	return id, nil
}

// ListDocuments returns profileID's uploaded documents, newest first.
func ListDocuments(ctx context.Context, db *sql.DB, profileID string) ([]DocumentEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, path, filename, size_bytes, source, created_at
		 FROM vault_documents WHERE profile_id = ? ORDER BY seq DESC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("vault.ListDocuments: %w", err)
	}
	defer rows.Close()
	docs := []DocumentEntry{}
	for rows.Next() {
		var d DocumentEntry
		if err := rows.Scan(&d.ID, &d.Path, &d.Filename, &d.SizeBytes, &d.Source, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("vault.ListDocuments: scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// DeleteDocument removes id's vault_documents row and its file, scoped to
// profileID. Returns an error if the row does not exist.
func DeleteDocument(ctx context.Context, db *sql.DB, profileID, id string) error {
	var path string
	err := db.QueryRowContext(ctx,
		`SELECT path FROM vault_documents WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&path)
	if err == sql.ErrNoRows {
		return fmt.Errorf("vault.DeleteDocument: id %q not found", id)
	}
	if err != nil {
		return fmt.Errorf("vault.DeleteDocument: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM vault_documents WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("vault.DeleteDocument: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: document %s deleted from index but its file could not be removed: %v\n", id, err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/vault/... -v 2>&1 | tail -30`
Expected: PASS (all new tests, plus every pre-existing `internal/vault` test unaffected).

- [ ] **Step 6: Commit**

```bash
git add data/migrations/034_vault_documents.sql internal/vault/documents.go internal/vault/documents_test.go
git commit -m "$(cat <<'EOF'
feat(vault): add vault_documents table and RegisterDocument

A profile-scoped document vault, sibling to the existing image vault, for
uploaded profile files (résumés, cover letters, etc.) that Phase 3's
knowledge ingestion will index.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 2: `internal/monomind.IngestDocument`

**Files:**
- Create: `internal/monomind/docsync.go`
- Create: `internal/monomind/docsync_test.go`
- Modify: `internal/monomind/testdata/fake-monomind.sh`

**Interfaces:**
- Consumes: `monomind.{Find,FilteredEnviron}` (existing, `internal/monomind/find.go`/`exec.go`), `profiledir.MonomindDir` (existing).
- Produces: `monomind.IngestDocument(ctx context.Context, db *sql.DB, profileID, path string) error`. Consumed by Task 4 (CLI upload command).

- [ ] **Step 1: Add the `mcp exec` case to the fake binary**

Modify `internal/monomind/testdata/fake-monomind.sh`: insert this block after the existing `if [ "$1" = "org" ] && [ "$2" = "events" ]...` block and before the final `echo "fake-monomind: unsupported invocation..."` fallback:

```sh
if [ "$1" = "mcp" ] && [ "$2" = "exec" ]; then
  tool=""
  fmt=""
  while [ $# -gt 0 ]; do
    case "$1" in
      -t) tool="$2"; shift 2 ;;
      --format) fmt="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  case "$tool" in
    knowledge_ingest)
      if [ "$INGEST_FAIL" = "1" ]; then
        echo "fake-monomind: knowledge_ingest forced failure" >&2
        exit 1
      fi
      echo '{"tool":"knowledge_ingest","result":{"content":[{"type":"text","text":"{\"success\":true,\"filePath\":\"/fake/path\",\"chunksIndexed\":3}"}]},"duration":1.0}'
      exit 0
      ;;
    knowledge_search)
      echo '{"tool":"knowledge_search","result":{"content":[{"type":"text","text":"{\"success\":true,\"count\":2,\"results\":[{\"kind\":\"excerpt\",\"filePath\":\"/fake/resume.txt\",\"text\":\"Experienced backend engineer with 8 years in distributed systems.\",\"similarity\":0.91},{\"kind\":\"rule\",\"key\":\"r1\",\"text\":\"unrelated rule entry\"}]}"}]},"duration":1.0}'
      exit 0
      ;;
    *)
      echo "fake-monomind: unsupported mcp tool: $tool" >&2
      exit 2
      ;;
  esac
fi
```

- [ ] **Step 2: Write the failing test**

```go
// internal/monomind/docsync_test.go
package monomind_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func newDocsyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "docsync-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func setFakeMonomindOnPath(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()
	src, err := filepath.Abs("testdata/fake-monomind.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(fakeDir, "monomind")
	if err := os.Symlink(src, linkPath); err != nil {
		t.Fatalf("symlink fake monomind onto PATH: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestIngestDocumentSucceeds(t *testing.T) {
	setFakeMonomindOnPath(t)
	db := newDocsyncTestDB(t)
	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := monomind.IngestDocument(context.Background(), db, "default", docPath); err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}
}

func TestIngestDocumentPropagatesFailure(t *testing.T) {
	setFakeMonomindOnPath(t)
	t.Setenv("INGEST_FAIL", "1")
	db := newDocsyncTestDB(t)

	if err := monomind.IngestDocument(context.Background(), db, "default", "/fake/path"); err == nil {
		t.Fatal("expected error when the fake binary reports failure, got nil")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/monomind/... -run TestIngestDocument -v`
Expected: FAIL with "undefined: monomind.IngestDocument" (compile error).

- [ ] **Step 4: Write IngestDocument**

```go
// internal/monomind/docsync.go
package monomind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Mirrors
// SyncToKnowledgeGraph's subprocess/env-var pattern exactly — see that
// function's comment in kgsync.go for why MONOMIND_CWD and creating the
// profile dir first are both required, not optional. Does not need
// --format json: only the exit code matters here, the inner payload is
// unused (matching SyncToKnowledgeGraph's own fire-and-forget style).
func IngestDocument(ctx context.Context, db interface{ Ping() error }, profileID, path string) error {
	return ingestDocument(ctx, db, profileID, path)
}
```

Wait — `db` must be `*sql.DB` to match `profiledir.MonomindDir`'s signature, not an interface. Use the exact signature below instead of the placeholder above (the block above is intentionally wrong to make this correction explicit — copy the corrected version, not the one above it):

```go
// internal/monomind/docsync.go
package monomind

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Mirrors
// SyncToKnowledgeGraph's subprocess/env-var pattern exactly — see that
// function's comment in kgsync.go for why MONOMIND_CWD and creating the
// profile dir first are both required, not optional. Does not pass
// --format json: only the exit code matters here, the inner payload is
// unused (matching SyncToKnowledgeGraph's own fire-and-forget style).
func IngestDocument(ctx context.Context, db *sql.DB, profileID, path string) error {
	bin, err := Find()
	if err != nil {
		return err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("monomind.IngestDocument: creating profile monomind dir: %w", err)
	}

	params, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return fmt.Errorf("monomind.IngestDocument: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_ingest", "-p", string(params))
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("monomind.IngestDocument: knowledge_ingest: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/monomind/... -run TestIngestDocument -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/monomind/docsync.go internal/monomind/docsync_test.go internal/monomind/testdata/fake-monomind.sh
git commit -m "$(cat <<'EOF'
feat(monomind): add IngestDocument

Shells out to monomind mcp exec -t knowledge_ingest, mirroring
SyncToKnowledgeGraph's per-profile MONOMIND_CWD isolation pattern exactly.
Fake binary extended with an mcp exec case for tests.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 3: `internal/monomind.SearchKnowledge`

**Files:**
- Modify: `internal/monomind/docsync.go`
- Modify: `internal/monomind/docsync_test.go`

**Interfaces:**
- Consumes: everything from Task 2, plus the fake binary's `knowledge_search` case (already added in Task 2 Step 1).
- Produces: `monomind.KnowledgeResult{Path,Excerpt string; Score float64}`, `monomind.SearchKnowledge(ctx context.Context, db *sql.DB, profileID, query string) ([]KnowledgeResult, error)`. Consumed by Task 4 (CLI `search-knowledge`) and Task 5 (chat tool).

- [ ] **Step 1: Write the failing test**

```go
// append to internal/monomind/docsync_test.go

func TestSearchKnowledgeReturnsExcerptsOnly(t *testing.T) {
	setFakeMonomindOnPath(t)
	db := newDocsyncTestDB(t)

	results, err := monomind.SearchKnowledge(context.Background(), db, "default", "backend engineer")
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 excerpt result (the fake's \"rule\" kind entry must be filtered out), got %d: %+v", len(results), results)
	}
	if results[0].Path != "/fake/resume.txt" {
		t.Fatalf("unexpected path: %q", results[0].Path)
	}
	if results[0].Excerpt != "Experienced backend engineer with 8 years in distributed systems." {
		t.Fatalf("unexpected excerpt: %q", results[0].Excerpt)
	}
	if results[0].Score != 0.91 {
		t.Fatalf("unexpected score: %v", results[0].Score)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/monomind/... -run TestSearchKnowledge -v`
Expected: FAIL with "undefined: monomind.SearchKnowledge" (compile error).

- [ ] **Step 3: Write SearchKnowledge**

Append to `internal/monomind/docsync.go`:

```go
// KnowledgeResult is one matching excerpt from SearchKnowledge.
type KnowledgeResult struct {
	Path    string
	Excerpt string
	Score   float64
}

// cliEnvelope is the outer JSON `monomind mcp exec ... --format json` prints.
type cliEnvelope struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

// knowledgeSearchPayload is the inner JSON, itself encoded as a string
// inside cliEnvelope.Result.Content[0].Text — see docs/mastermind/specs/
// 2026-09-05-profile-documents-design.md's "Protocol note" section for why
// this is decoded twice, not once.
type knowledgeSearchPayload struct {
	Success bool `json:"success"`
	Results []struct {
		Kind       string  `json:"kind"`
		FilePath   string  `json:"filePath"`
		Text       string  `json:"text"`
		Similarity float64 `json:"similarity"`
	} `json:"results"`
}

// SearchKnowledge queries profileID's Second Brain via
// `monomind mcp exec -t knowledge_search`, scoped to store="project" (this
// profile's own ingested documents — never "global"/"all", which would
// pull in the user's personal cross-project brain). Only "excerpt"-kind
// results are returned; knowledge-graph/rule/memory result kinds are
// filtered out (out of scope — see the design spec).
func SearchKnowledge(ctx context.Context, db *sql.DB, profileID, query string) ([]KnowledgeResult, error) {
	bin, err := Find()
	if err != nil {
		return nil, err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: creating profile monomind dir: %w", err)
	}

	params, err := json.Marshal(map[string]string{"query": query, "store": "project"})
	if err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_search", "-p", string(params), "--format", "json")
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: knowledge_search: %w", err)
	}

	var envelope cliEnvelope
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: decoding CLI envelope: %w", err)
	}
	if len(envelope.Result.Content) == 0 {
		return nil, fmt.Errorf("monomind.SearchKnowledge: empty response content")
	}

	var payload knowledgeSearchPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: decoding tool payload: %w", err)
	}

	results := make([]KnowledgeResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.Kind != "excerpt" {
			continue
		}
		results = append(results, KnowledgeResult{Path: r.FilePath, Excerpt: r.Text, Score: r.Similarity})
	}
	return results, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/monomind/... -v 2>&1 | tail -30`
Expected: PASS (all `internal/monomind` tests, including the 3 from this and Task 2, and every pre-existing test in this package unaffected).

- [ ] **Step 5: Commit**

```bash
git add internal/monomind/docsync.go internal/monomind/docsync_test.go
git commit -m "$(cat <<'EOF'
feat(monomind): add SearchKnowledge

Decodes the mcp exec --format json double-encoded response (CLI envelope,
then the tool's own JSON payload) and filters to excerpt-kind results only.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 4: CLI commands

**Files:**
- Create: `cmd/monoagentcli/profile_documents.go`
- Create: `cmd/monoagentcli/profile_documents_test.go`
- Modify: `cmd/monoagentcli/profile.go`

**Interfaces:**
- Consumes: `vault.{RegisterDocument,ListDocuments,DeleteDocument,ContextWithDB}` (Task 1), `monomind.{IngestDocument,SearchKnowledge}` (Tasks 2-3); `initDB`, `errNotFound`, `errInvalidInput` (existing CLI conventions).
- Produces: `newProfileUploadDocumentCmd`, `newProfileDocumentsCmd`, `newProfileSearchKnowledgeCmd`, added to `newProfileCmd`'s subcommand list. No other task depends on this.

- [ ] **Step 1: Write the failing CLI tests**

```go
// cmd/monoagentcli/profile_documents_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProfileDocsCLITestDB(t *testing.T) string {
	t.Helper()
	return newApplicationCLITestDB(t) // reuses the shared migration-seeding helper from application_test.go
}

func setFakeMonomindOnPathCLI(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()
	src, err := filepath.Abs("../../internal/monomind/testdata/fake-monomind.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(fakeDir, "monomind")); err != nil {
		t.Fatalf("symlink fake monomind onto PATH: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runProfileCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newProfileCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestProfileUploadListDeleteDocument(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	dbPath := newProfileDocsCLITestDB(t)

	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	uploadOut, err := runProfileCmd(t, dbPath, "upload-document", docPath)
	if err != nil {
		t.Fatalf("upload-document: %v (%s)", err, uploadOut)
	}
	if !strings.Contains(uploadOut, `"id"`) {
		t.Fatalf("expected JSON id in output, got: %s", uploadOut)
	}

	listOut, err := runProfileCmd(t, dbPath, "documents", "list")
	if err != nil {
		t.Fatalf("documents list: %v", err)
	}
	if !strings.Contains(listOut, "resume.txt") {
		t.Fatalf("expected filename in list output, got: %s", listOut)
	}
}

func TestProfileSearchKnowledge(t *testing.T) {
	setFakeMonomindOnPathCLI(t)
	dbPath := newProfileDocsCLITestDB(t)

	out, err := runProfileCmd(t, dbPath, "search-knowledge", "backend engineer")
	if err != nil {
		t.Fatalf("search-knowledge: %v (%s)", err, out)
	}
	if !strings.Contains(out, "distributed systems") {
		t.Fatalf("expected excerpt text in output, got: %s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run 'TestProfileUploadListDeleteDocument|TestProfileSearchKnowledge' -v`
Expected: FAIL — `newProfileCmd` doesn't recognize `upload-document`/`documents`/`search-knowledge` subcommands yet.

- [ ] **Step 3: Write the commands**

```go
// cmd/monoagentcli/profile_documents.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/vault"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

func newProfileUploadDocumentCmd(cfg *globalConfig) *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:     "upload-document <path>",
		Short:   "Upload a profile document and index it for chat search",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli profile upload-document ~/resume.pdf`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			ctx := vault.ContextWithProfileID(cmd.Context(), cfg.ProfileID)
			id, err := vault.RegisterDocument(ctx, db.DB, args[0], source)
			if err != nil {
				return fmt.Errorf("uploading document: %w", err)
			}

			docs, err := vault.ListDocuments(ctx, db.DB, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("looking up uploaded document: %w", err)
			}
			var storedPath string
			for _, d := range docs {
				if d.ID == id {
					storedPath = d.Path
				}
			}
			if ingestErr := monomind.IngestDocument(cmd.Context(), db.DB, cfg.ProfileID, storedPath); ingestErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: document uploaded but indexing failed: %v\n", ingestErr)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"id": id})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %q as %s.\n", args[0], id)
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "upload", "Where this document came from")
	return cmd
}

func newProfileDocumentsCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "documents",
		Short: "Manage uploaded profile documents",
	}
	cmd.AddCommand(newProfileDocumentsListCmd(cfg), newProfileDocumentsRmCmd(cfg))
	return cmd
}

func newProfileDocumentsListCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List uploaded profile documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			docs, err := vault.ListDocuments(cmd.Context(), db.DB, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("listing documents: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(docs)
			}
			if len(docs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No documents uploaded.")
				return nil
			}
			table := tablewriter.NewWriter(cmd.OutOrStdout())
			table.SetHeader([]string{"ID", "Filename", "Source", "Uploaded"})
			table.SetBorder(false)
			for _, d := range docs {
				table.Append([]string{d.ID, d.Filename, d.Source, d.CreatedAt})
			}
			table.Render()
			return nil
		},
	}
}

func newProfileDocumentsRmCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete an uploaded profile document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			if err := vault.DeleteDocument(cmd.Context(), db.DB, cfg.ProfileID, args[0]); err != nil {
				return errNotFound("%v", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"id": args[0]})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %q.\n", args[0])
			return nil
		},
	}
}

func newProfileSearchKnowledgeCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "search-knowledge <query>",
		Short:   "Search uploaded profile documents (for testing — chat uses this automatically)",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli profile search-knowledge "backend frameworks"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			results, err := monomind.SearchKnowledge(cmd.Context(), db.DB, cfg.ProfileID, args[0])
			if err != nil {
				return fmt.Errorf("searching profile documents: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}
			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching content found.")
				return nil
			}
			for _, r := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "[%.2f] %s: %s\n", r.Score, r.Path, r.Excerpt)
			}
			return nil
		},
	}
}
```

- [ ] **Step 4: Register the subcommands**

In `cmd/monoagentcli/profile.go`, add to `newProfileCmd`'s `cmd.AddCommand(...)` list:

```go
	cmd.AddCommand(
		newProfileUploadDocumentCmd(cfg),
		newProfileDocumentsCmd(cfg),
		newProfileSearchKnowledgeCmd(cfg),
	)
```

(Add these as additional arguments to whatever `AddCommand` call already exists in that file — do not replace the existing subcommands, append to the list.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run 'TestProfileUploadListDeleteDocument|TestProfileSearchKnowledge' -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing.

- [ ] **Step 7: Commit**

```bash
git add cmd/monoagentcli/profile_documents.go cmd/monoagentcli/profile_documents_test.go cmd/monoagentcli/profile.go
git commit -m "$(cat <<'EOF'
feat(cli): add profile document upload/list/rm/search-knowledge commands

upload-document copies into the vault then best-effort indexes via
monomind.IngestDocument; search-knowledge exists for manual testing (chat
uses the same SearchKnowledge function automatically, added next task).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 5: Chat tool `search_profile_documents`

**Files:**
- Modify: `internal/ai/chat/monoagent_tools.go`
- Modify: `internal/ai/chat/monoagent_tools_test.go`

**Interfaces:**
- Consumes: `monomind.SearchKnowledge` (Task 3); `MonoagentTools.{db,ProfileID}` (existing fields/methods on the receiver, see `monoagent_tools.go`'s existing handlers like `listVaultItems` for the exact receiver-field access pattern), `marshalJSON` (existing helper in this same file).
- Produces: a new `ai.ToolDef` named `search_profile_documents` in `ToolDefs()`, dispatched in `Execute`/`ExecuteContext`. No other task depends on this — it is the final consumer in this plan.

- [ ] **Step 1: Write the failing test**

The existing test-DB helper in this file is `newMonoagentTestDB(t *testing.T) *storage.Database` (verified — see e.g. `TestMonoagentTools_ProfileIsolation`'s usage: `db := newMonoagentTestDB(t)`, then `NewMonoagentTools(db.DB, "")`, `mt.SetProfileID(...)`, `mt.Execute(name, argsJSON)`). Follow that exact pattern:

```go
// append to internal/ai/chat/monoagent_tools_test.go

func TestSearchProfileDocumentsToolRequiresQuery(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	mt.SetProfileID("default")

	_, err := mt.Execute("search_profile_documents", `{}`)
	if err == nil {
		t.Fatal("expected error for missing query, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/ai/chat/... -run TestSearchProfileDocuments -v`
Expected: FAIL — `Execute` returns an "unknown tool" error rather than a "query is required" error, since the tool isn't registered yet.

- [ ] **Step 3: Add the tool definition, dispatch case, and handler**

In `internal/ai/chat/monoagent_tools.go`, add to the slice returned by `ToolDefs()` (append near the end, alongside the other data-lookup tools like `listVaultItems`):

```go
		{
			Name:        "search_profile_documents",
			Description: "Search the user's uploaded profile documents (résumé, cover letters, etc.) for relevant content.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"query": strParam("Search query")},
				"required":   []string{"query"},
			},
		},
```

In `Execute`'s (and `ExecuteContext`'s, if the dispatch switch is duplicated between them — check both) dispatch switch, add a case:

```go
	case "search_profile_documents":
		return mt.searchProfileDocuments(args)
```

Add the handler method (place it near `listVaultItems` for locality):

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

Add `"github.com/monoes/mono-agent/internal/monomind"` to this file's import block if not already present (check first — `monoagent_tools.go` may already import other `internal/*` packages in a similar style; match the existing import grouping/ordering).

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/ai/chat/... -run TestSearchProfileDocuments -v`
Expected: PASS.

- [ ] **Step 5: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing. Completes Phase 3.

- [ ] **Step 6: Commit**

```bash
git add internal/ai/chat/monoagent_tools.go internal/ai/chat/monoagent_tools_test.go
git commit -m "$(cat <<'EOF'
feat(chat): add search_profile_documents tool

Wires monomind.SearchKnowledge into the chat LLM's existing tool-calling
registry, fulfilling "chat can always access" uploaded profile documents.
Completes Phase 3 (Profile Documents).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

## Self-Review

**1. Spec coverage:**
- `vault_documents` table + upload → Task 1, Task 4. ✅
- Knowledge ingestion (`IngestDocument`) → Task 2. ✅
- Knowledge search (`SearchKnowledge`), double-decode protocol → Task 3. ✅
- CLI surface → Task 4. ✅
- Chat tool access → Task 5. ✅
- Images out of scope (already handled) → confirmed, no task touches images. ✅
- Per-profile isolation → every function takes/threads `profileID` through to `MONOMIND_CWD` or a `WHERE profile_id = ?` clause; tested explicitly in Task 1 (`TestListDocumentsScopedToProfile`).

**2. Placeholder scan:** No "TBD"/"TODO". One deliberate self-correcting example in Task 2 Step 4 (showing a wrong signature then the corrected one) is pedagogical, not a placeholder — the final code block is complete and correct; an implementer following the steps in order ends up with only the corrected version in the file.

**3. Type consistency:** `vault.DocumentEntry` (Task 1) fields (`ID,Path,Filename,SizeBytes,Source,CreatedAt`) are read identically in Task 4's CLI list/table code. `monomind.KnowledgeResult` (Task 3) fields (`Path,Excerpt,Score`) are read identically in Task 4's `search-knowledge` command and Task 5's chat tool. `IngestDocument`/`SearchKnowledge` signatures introduced in Tasks 2-3 are called with matching argument order/types in Task 4 and Task 5.

One residual, explicitly-flagged risk (not a gap): the exact double-JSON-encoding behavior of `monomind mcp exec --format json` (Task 3) was derived by reading `monomind`'s TypeScript source rather than by running it, since this environment cannot execute the real binary — see the design spec's "Known limitation" note. The test suite validates the Go code's parsing logic against a fake binary emitting this exact shape; it does not validate that today's live `monomind` binary still behaves this way. If a live run later reveals a mismatch, only `internal/monomind/docsync.go`'s decoding logic needs to change — the rest of this phase (vault storage, CLI, chat tool) is independent of this detail.
