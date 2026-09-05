# CV / Cover Letter / Tender Document Generation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (`mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per the prior three phase plans' precedent.)

**Goal:** Render structured CV/cover-letter/tender-proposal data into real HTML documents, convert them to PDF via a headless browser, and save both into the vault, optionally linked to a job/tender application.

**Architecture:** A new `internal/documents` package (data types, embedded `html/template` templates, `Render`, `RenderPDF` via a standalone `go-rod` launch, `GenerateDocument` orchestration) on top of the existing Phase 3 vault, plus a CLI command and one generic workflow node.

**Tech Stack:** `github.com/go-rod/rod` + `github.com/go-rod/rod/lib/launcher` (already direct dependencies), Go stdlib `html/template` and `embed`. No new dependencies.

## Global Constraints

- Go toolchain at `~/.local/go/bin`, not on default PATH: `export PATH="$HOME/.local/go/bin:$PATH"` before any `go` command.
- Migrations: numbered SQL files in `data/migrations/`. Next number is 035.
- `RegisterDocument`'s new `applicationID` parameter is variadic (`applicationID ...string`), matching `internal/connections.Store.Get`'s existing `profileID ...string` convention — never a breaking signature change to the two existing call sites (Phase 3's CLI `upload-document` command).
- `RenderPDF` launches a **standalone, unauthenticated** headless browser via `launcher.New().Headless(true).Launch()` → `rod.New().ControlURL(...)` → `browser.Connect()`, the exact pattern already in `cmd/monoagentcli/crawl.go` — never the login-session infrastructure in `internal/nodes/browser_adapter.go`.
- `html/template` (not `text/template`) for every template — its auto-escaping is the actual security boundary this phase relies on (see the design spec's "LLM data never trusted as raw markup" rationale).
- Templates are real `.html` files under `internal/documents/templates/`, embedded via `//go:embed`, never inline Go string literals.
- TDD: every behavior gets a failing test before its implementation.
- `RenderPDF`'s tests require a real headless Chrome/Chromium binary — the same environmental requirement `crawl.go` already has, not a new one this phase introduces. If the implementing environment lacks one, `pdf_test.go`'s real-browser test and `generate_test.go`'s non-injected-`RenderPDF` case will fail for environmental reasons; diagnose and report this distinction explicitly rather than silently skip it or claim it as a code bug.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `data/migrations/035_vault_documents_application_link.sql` | Add nullable `application_id` column to `vault_documents`. |
| `internal/vault/documents.go` | Modified: `RegisterDocument` gets a variadic `applicationID` param; `DocumentEntry` gets `ApplicationID`; `ListDocuments` selects the new column. |
| `internal/vault/documents_test.go` | Modified: a test for the application-linked case. |
| `internal/documents/documents.go` | `DocType`, `CVData`, `CoverLetterData`, `TenderProposalData` (+ sub-structs), `Render`. |
| `internal/documents/templates/{cv,cover_letter,tender_proposal}.html` | The three templates. |
| `internal/documents/documents_test.go` | Tests for `Render`, including the escaping assertion. |
| `internal/documents/pdf.go` | `RenderPDF`. |
| `internal/documents/pdf_test.go` | Test against a real headless browser. |
| `internal/documents/generate.go` | `GenerateDocument`. |
| `internal/documents/generate_test.go` | Tests, including the injected-PDF-failure case. |
| `cmd/monoagentcli/documents.go` | `monoagentcli documents render` command. |
| `cmd/monoagentcli/documents_test.go` | CLI integration tests. |
| `cmd/monoagentcli/root.go` | Modified: register `newDocumentsCmd(cfg)`. |
| `internal/nodes/documents/render.go` / `render_schema.go` | `documents.render` workflow node. |
| `internal/tools/schemagen/manifest.go` | Modified: one new manifest entry. |
| `internal/noderegistry/registry.go` | Modified: register the new node package. |

---

### Task 0: `vault_documents.application_id` + `RegisterDocument` extension

**Files:**
- Create: `data/migrations/035_vault_documents_application_link.sql`
- Modify: `internal/vault/documents.go`
- Modify: `internal/vault/documents_test.go`

**Interfaces:**
- Consumes: everything from Phase 3's `internal/vault/documents.go` as already committed.
- Produces: `vault.RegisterDocument(ctx, db, src, source string, applicationID ...string) (string, error)` (backward-compatible — existing 4-arg call sites keep working unchanged), `vault.DocumentEntry.ApplicationID string`. Consumed by Task 3 (`GenerateDocument`).

- [ ] **Step 1: Write the migration**

```sql
-- data/migrations/035_vault_documents_application_link.sql
-- Links a generated document (CV, cover letter, tender proposal) back to
-- the job/tender application it was generated for. Nullable: a general
-- profile document (e.g. Phase 3's uploaded résumé) has none.

ALTER TABLE vault_documents ADD COLUMN application_id TEXT;
CREATE INDEX IF NOT EXISTS idx_vault_documents_application ON vault_documents(application_id);
```

- [ ] **Step 2: Write the failing test**

```go
// append to internal/vault/documents_test.go

func TestRegisterDocumentWithApplicationID(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "cv content"), "generated", "app-123")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id || docs[0].ApplicationID != "app-123" {
		t.Fatalf("expected application_id to round-trip, got %+v", docs)
	}
}

func TestRegisterDocumentWithoutApplicationIDStaysEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	if _, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload"); err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ApplicationID != "" {
		t.Fatalf("expected empty ApplicationID for a call with none supplied, got %+v", docs)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/vault/... -run TestRegisterDocumentWith -v`
Expected: FAIL — `TestRegisterDocumentWithApplicationID` fails to compile (too many arguments to `RegisterDocument`); `TestRegisterDocumentWithoutApplicationIDStaysEmpty` fails on `docs[0].ApplicationID` (undefined field) — both compile errors, confirming the extension doesn't exist yet.

- [ ] **Step 4: Extend `documents.go`**

Modify the `DocumentEntry` struct:

```go
// DocumentEntry is one row from vault_documents.
type DocumentEntry struct {
	ID            string
	Path          string
	Filename      string
	SizeBytes     int64
	Source        string
	ApplicationID string
	CreatedAt     string
}
```

Modify `RegisterDocument`'s signature and insert statement:

```go
// RegisterDocument copies src into the profile's vault (under a
// documents/ subdirectory of the same VaultDir used for images) and
// inserts a vault_documents row. Returns the new vault ID (e.g. "doc-001").
// applicationID is optional (variadic, matching internal/connections.Store
// .Get's ...string convention) — pass one string to link the document to
// a job/tender application, or omit it for a general profile document.
// Mirrors Register's structure exactly (same BEGIN IMMEDIATE seq-allocation
// pattern — see Register's doc comment in vault.go for why a deferred
// transaction would race two concurrent Registers onto the same seq).
func RegisterDocument(ctx context.Context, db *sql.DB, src, source string, applicationID ...string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("vault.RegisterDocument: db is nil")
	}
	if src == "" {
		return "", fmt.Errorf("vault.RegisterDocument: src path is empty")
	}
	var appID string
	if len(applicationID) > 0 {
		appID = applicationID[0]
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

	nullStr := func(s string) interface{} {
		if s == "" {
			return nil
		}
		return s
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_documents (id, seq, path, filename, size_bytes, source, application_id, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		id, seq, destPath, filename, fi.Size(), source, nullStr(appID), profileID,
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
```

Modify `ListDocuments`'s query and scan:

```go
// ListDocuments returns profileID's uploaded documents, newest first.
func ListDocuments(ctx context.Context, db *sql.DB, profileID string) ([]DocumentEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, path, filename, size_bytes, source, COALESCE(application_id, ''), created_at
		 FROM vault_documents WHERE profile_id = ? ORDER BY seq DESC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("vault.ListDocuments: %w", err)
	}
	defer rows.Close()
	docs := []DocumentEntry{}
	for rows.Next() {
		var d DocumentEntry
		if err := rows.Scan(&d.ID, &d.Path, &d.Filename, &d.SizeBytes, &d.Source, &d.ApplicationID, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("vault.ListDocuments: scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/vault/... -v 2>&1 | tail -30`
Expected: PASS — all pre-existing `internal/vault` tests (including Phase 3's, using `RegisterDocument`'s original 4-arg call form) plus the 2 new tests.

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds (confirms `cmd/monoagentcli/profile_documents.go`'s existing `RegisterDocument` call — 4 args, no `applicationID` — still compiles against the new variadic signature); grep shows nothing.

- [ ] **Step 7: Commit**

```bash
git add data/migrations/035_vault_documents_application_link.sql internal/vault/documents.go internal/vault/documents_test.go
git commit -m "$(cat <<'EOF'
feat(vault): link vault_documents to an application

RegisterDocument gains an optional variadic applicationID parameter
(matching internal/connections.Store.Get's ...string convention) so a
generated CV/cover-letter/tender-proposal can be traced back to the
application it was made for.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 1: `internal/documents` types, templates, and `Render`

**Files:**
- Create: `internal/documents/documents.go`
- Create: `internal/documents/templates/cv.html`
- Create: `internal/documents/templates/cover_letter.html`
- Create: `internal/documents/templates/tender_proposal.html`
- Create: `internal/documents/documents_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks in this plan.
- Produces: `documents.DocType` (`DocTypeCV`, `DocTypeCoverLetter`, `DocTypeTenderProposal`), `documents.{CVData,ExperienceEntry,EducationEntry,CoverLetterData,TenderProposalData,ProposalSection}` (all with `json:"..."` tags, camelCase), `documents.Render(docType DocType, data interface{}) (string, error)`. Consumed by Task 3 (`GenerateDocument`), Task 4 (CLI), Task 5 (node).

- [ ] **Step 1: Write the data types**

```go
// internal/documents/documents.go

// Package documents renders structured CV/cover-letter/tender-proposal
// data into HTML (via html/template's auto-escaping) and, via pdf.go, PDF.
// See docs/mastermind/specs/2026-09-05-document-generation-design.md.
package documents

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

// DocType selects which template/struct pair Render uses.
type DocType string

const (
	DocTypeCV             DocType = "cv"
	DocTypeCoverLetter    DocType = "cover_letter"
	DocTypeTenderProposal DocType = "tender_proposal"
)

// CVData is the structured input for the cv template.
type CVData struct {
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	Email      string            `json:"email"`
	Phone      string            `json:"phone"`
	Location   string            `json:"location"`
	Summary    string            `json:"summary"`
	Experience []ExperienceEntry `json:"experience"`
	Education  []EducationEntry  `json:"education"`
	Skills     []string          `json:"skills"`
}

// ExperienceEntry is one job history entry within CVData.
type ExperienceEntry struct {
	Company  string   `json:"company"`
	JobTitle string   `json:"jobTitle"`
	Dates    string   `json:"dates"`
	Bullets  []string `json:"bullets"`
}

// EducationEntry is one education entry within CVData.
type EducationEntry struct {
	School string `json:"school"`
	Degree string `json:"degree"`
	Dates  string `json:"dates"`
}

// CoverLetterData is the structured input for the cover_letter template.
type CoverLetterData struct {
	SenderName    string   `json:"senderName"`
	RecipientName string   `json:"recipientName"`
	CompanyName   string   `json:"companyName"`
	Date          string   `json:"date"`
	Paragraphs    []string `json:"paragraphs"`
}

// TenderProposalData is the structured input for the tender_proposal template.
type TenderProposalData struct {
	CompanyName string            `json:"companyName"`
	TenderTitle string            `json:"tenderTitle"`
	IssuingOrg  string            `json:"issuingOrg"`
	Date        string            `json:"date"`
	Sections    []ProposalSection `json:"sections"`
}

// ProposalSection is one section within TenderProposalData.
type ProposalSection struct {
	Heading string   `json:"heading"`
	Body    []string `json:"body"`
}

// Render executes the template for docType against data, returning the
// rendered HTML. Uses html/template, not text/template — its auto-escaping
// is the actual defense against a data field containing markup/script
// content (see the design spec's rationale).
func Render(docType DocType, data interface{}) (string, error) {
	filename, ok := map[DocType]string{
		DocTypeCV:             "cv.html",
		DocTypeCoverLetter:    "cover_letter.html",
		DocTypeTenderProposal: "tender_proposal.html",
	}[docType]
	if !ok {
		return "", fmt.Errorf("documents.Render: unknown doc type %q", docType)
	}

	tmpl, err := template.ParseFS(templatesFS, "templates/"+filename)
	if err != nil {
		return "", fmt.Errorf("documents.Render: parsing template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("documents.Render: executing template: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 2: Write the templates**

```html
<!-- internal/documents/templates/cv.html -->
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>{{.Name}} — CV</title>
<style>
  body { font-family: Georgia, serif; max-width: 800px; margin: 2em auto; color: #222; }
  h1 { margin-bottom: 0; }
  .contact { color: #555; margin-top: 0.2em; }
  h2 { border-bottom: 1px solid #ccc; padding-bottom: 0.2em; margin-top: 1.5em; }
  .entry { margin-bottom: 1em; }
  .entry-header { display: flex; justify-content: space-between; font-weight: bold; }
  ul { margin-top: 0.3em; }
  .skills span { display: inline-block; background: #f0f0f0; padding: 0.2em 0.6em; margin: 0.2em; border-radius: 3px; }
</style>
</head>
<body>
  <h1>{{.Name}}</h1>
  <div class="contact">{{.Title}} &middot; {{.Email}} &middot; {{.Phone}} &middot; {{.Location}}</div>

  {{if .Summary}}
  <h2>Summary</h2>
  <p>{{.Summary}}</p>
  {{end}}

  {{if .Experience}}
  <h2>Experience</h2>
  {{range .Experience}}
  <div class="entry">
    <div class="entry-header"><span>{{.JobTitle}}, {{.Company}}</span><span>{{.Dates}}</span></div>
    <ul>{{range .Bullets}}<li>{{.}}</li>{{end}}</ul>
  </div>
  {{end}}
  {{end}}

  {{if .Education}}
  <h2>Education</h2>
  {{range .Education}}
  <div class="entry">
    <div class="entry-header"><span>{{.Degree}}, {{.School}}</span><span>{{.Dates}}</span></div>
  </div>
  {{end}}
  {{end}}

  {{if .Skills}}
  <h2>Skills</h2>
  <div class="skills">{{range .Skills}}<span>{{.}}</span>{{end}}</div>
  {{end}}
</body>
</html>
```

```html
<!-- internal/documents/templates/cover_letter.html -->
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Cover Letter — {{.CompanyName}}</title>
<style>
  body { font-family: Georgia, serif; max-width: 700px; margin: 2em auto; color: #222; line-height: 1.5; }
  .date { text-align: right; color: #555; }
  .recipient { margin-top: 2em; }
  p { margin-bottom: 1em; }
  .sign-off { margin-top: 2em; }
</style>
</head>
<body>
  <div class="date">{{.Date}}</div>
  <div class="recipient">{{.RecipientName}}<br>{{.CompanyName}}</div>
  {{range .Paragraphs}}
  <p>{{.}}</p>
  {{end}}
  <div class="sign-off">Sincerely,<br>{{.SenderName}}</div>
</body>
</html>
```

```html
<!-- internal/documents/templates/tender_proposal.html -->
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>{{.TenderTitle}} — Proposal</title>
<style>
  body { font-family: Georgia, serif; max-width: 800px; margin: 2em auto; color: #222; }
  .meta { color: #555; margin-bottom: 2em; }
  h2 { border-bottom: 1px solid #ccc; padding-bottom: 0.2em; margin-top: 1.5em; }
</style>
</head>
<body>
  <h1>{{.TenderTitle}}</h1>
  <div class="meta">Submitted by {{.CompanyName}} to {{.IssuingOrg}} &middot; {{.Date}}</div>
  {{range .Sections}}
  <h2>{{.Heading}}</h2>
  {{range .Body}}<p>{{.}}</p>{{end}}
  {{end}}
</body>
</html>
```

- [ ] **Step 3: Write the failing tests**

```go
// internal/documents/documents_test.go
package documents_test

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
)

func TestRenderCV(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCV, documents.CVData{
		Name:    "Jane Doe",
		Title:   "Backend Engineer",
		Summary: "Experienced backend engineer.",
		Experience: []documents.ExperienceEntry{
			{Company: "Acme", JobTitle: "Senior Engineer", Dates: "2020-2025", Bullets: []string{"Built things"}},
		},
		Skills: []string{"Go", "SQL"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Jane Doe", "Backend Engineer", "Senior Engineer, Acme", "Built things", "Go"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered CV to contain %q, got:\n%s", want, html)
		}
	}
}

func TestRenderCVEscapesUntrustedContent(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCV, documents.CVData{
		Name: "<script>alert(1)</script>",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Fatal("expected html/template to escape script content, found raw <script> tag")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag in output, got:\n%s", html)
	}
}

func TestRenderCoverLetter(t *testing.T) {
	html, err := documents.Render(documents.DocTypeCoverLetter, documents.CoverLetterData{
		SenderName: "Jane Doe", RecipientName: "Hiring Manager", CompanyName: "Acme",
		Paragraphs: []string{"I am writing to apply for the role."},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Jane Doe", "Hiring Manager", "Acme", "I am writing to apply"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered cover letter to contain %q", want)
		}
	}
}

func TestRenderTenderProposal(t *testing.T) {
	html, err := documents.Render(documents.DocTypeTenderProposal, documents.TenderProposalData{
		CompanyName: "Acme Contracting", TenderTitle: "Road Maintenance Tender", IssuingOrg: "Ministry of Roads",
		Sections: []documents.ProposalSection{{Heading: "Technical Approach", Body: []string{"We propose..."}}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Road Maintenance Tender", "Acme Contracting", "Ministry of Roads", "Technical Approach", "We propose"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered tender proposal to contain %q", want)
		}
	}
}

func TestRenderUnknownDocType(t *testing.T) {
	if _, err := documents.Render(documents.DocType("nonsense"), nil); err == nil {
		t.Fatal("expected error for unknown doc type, got nil")
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -v`
Expected: FAIL — package doesn't exist yet (no non-test Go files).

- [ ] **Step 5: Run tests to verify they pass**

(Steps 1-2 above already contain the complete implementation — this step just confirms it.)

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -v`
Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/documents/documents.go internal/documents/templates/ internal/documents/documents_test.go
git commit -m "$(cat <<'EOF'
feat(documents): add CV/cover-letter/tender-proposal templates + Render

html/template (auto-escaping) renders each doc type's structured data
against a real, separately-editable .html template file.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 2: `RenderPDF`

**Files:**
- Create: `internal/documents/pdf.go`
- Create: `internal/documents/pdf_test.go`

**Interfaces:**
- Consumes: `github.com/go-rod/rod`, `github.com/go-rod/rod/lib/launcher`, `github.com/go-rod/rod/lib/proto` (existing dependencies, same import set as `cmd/monoagentcli/crawl.go`).
- Produces: `documents.RenderPDF(ctx context.Context, html string) ([]byte, error)`. Consumed by Task 3.

- [ ] **Step 1: Write the failing test**

```go
// internal/documents/pdf_test.go
package documents_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
)

func TestRenderPDFProducesValidPDF(t *testing.T) {
	pdfBytes, err := documents.RenderPDF(context.Background(), "<html><body><h1>Test</h1></body></html>")
	if err != nil {
		t.Fatalf("RenderPDF: %v (requires a headless Chrome/Chromium binary reachable by go-rod's launcher — if none is available in this environment, this is an environmental limitation, not a code defect; report it as such rather than treating it as a logic bug)", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatalf("expected output to start with the PDF magic number %%PDF-, got first bytes: %q", pdfBytes[:min(20, len(pdfBytes))])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -run TestRenderPDF -v`
Expected: FAIL with "undefined: documents.RenderPDF" (compile error).

- [ ] **Step 3: Write RenderPDF**

```go
// internal/documents/pdf.go
package documents

import (
	"context"
	"fmt"
	"io"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// RenderPDF launches a standalone, unauthenticated headless browser (the
// same launcher.New()/rod.New() pattern cmd/monoagentcli/crawl.go already
// uses — not the login-session infrastructure in
// internal/nodes/browser_adapter.go), loads html directly, and returns the
// rendered PDF bytes via Chrome's native "Print to PDF". The browser is
// launched and closed within this single call.
func RenderPDF(ctx context.Context, html string) ([]byte, error) {
	launchURL, err := launcher.New().Headless(true).Launch()
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: launch browser: %w", err)
	}

	browser := rod.New().ControlURL(launchURL).Context(ctx)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: connect to browser: %w", err)
	}
	defer browser.Close()

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: open page: %w", err)
	}
	defer page.Close()

	if err := page.SetDocumentContent(html); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: set document content: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: wait load: %w", err)
	}

	stream, err := page.PDF(&proto.PagePrintToPDF{PrintBackground: true})
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: print to pdf: %w", err)
	}
	pdfBytes, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("documents.RenderPDF: reading pdf stream: %w", err)
	}
	return pdfBytes, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -run TestRenderPDF -v`
Expected: PASS if a headless Chrome/Chromium binary is reachable in this environment (go-rod's launcher auto-downloads one on first use if none is found — this may take a minute the first time and requires outbound network access for that download only). If it fails with a browser-launch error specifically (not a Go compile/logic error), this is the environmental limitation the design spec and Global Constraints already flag — report the exact error and move on to Step 5 rather than attempting a workaround; do not weaken the test's assertion to work around a missing browser.

- [ ] **Step 5: Commit**

```bash
git add internal/documents/pdf.go internal/documents/pdf_test.go
git commit -m "$(cat <<'EOF'
feat(documents): add RenderPDF via a standalone headless browser

Reuses the exact launcher.New()/rod.New() pattern cmd/monoagentcli/crawl.go
already has for one-off, unauthenticated browser tasks.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

If Step 4's test failed for the environmental reason described above (no headless browser reachable), note this explicitly in the commit message body as an additional line, e.g.: "Note: pdf_test.go's real-browser assertion could not be verified in the implementing environment (no Chrome/Chromium binary reachable) — this is an environmental limitation, not a code defect; the code matches the reviewed design and crawl.go's established pattern."

---

### Task 3: `GenerateDocument` orchestration

**Files:**
- Create: `internal/documents/generate.go`
- Create: `internal/documents/generate_test.go`

**Interfaces:**
- Consumes: `documents.{DocType,Render}` (Task 1), `documents.RenderPDF` (Task 2, as a swappable package-level var — see below), `vault.RegisterDocument` (Task 0).
- Produces: `documents.GenerateDocument(ctx context.Context, db *sql.DB, profileID, applicationID string, docType DocType, data interface{}) (htmlDocID, pdfDocID string, err error)`. Consumed by Task 4 (CLI), Task 5 (node).

- [ ] **Step 1: Write the failing tests**

```go
// internal/documents/generate_test.go
package documents_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

func newGenerateTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "documents-generate-test.db")
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

func TestGenerateDocumentCreatesBothVaultEntries(t *testing.T) {
	// Inject a fake RenderPDF so this test doesn't need a real browser —
	// see documents.RenderPDFFunc's doc comment for why it's a swappable var.
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) {
		return []byte("%PDF-fake"), nil
	}
	t.Cleanup(func() { documents.RenderPDFFunc = orig })

	db := newGenerateTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	htmlID, pdfID, err := documents.GenerateDocument(ctx, db.DB, "default", "app-1", documents.DocTypeCV, documents.CVData{Name: "Jane Doe"})
	if err != nil {
		t.Fatalf("GenerateDocument: %v", err)
	}
	if htmlID == "" || pdfID == "" {
		t.Fatalf("expected both vault ids to be set, got html=%q pdf=%q", htmlID, pdfID)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 vault documents (html + pdf), got %d", len(docs))
	}
	for _, d := range docs {
		if d.ApplicationID != "app-1" {
			t.Errorf("expected application_id app-1, got %q for %s", d.ApplicationID, d.ID)
		}
	}
}

func TestGenerateDocumentKeepsHTMLWhenPDFFails(t *testing.T) {
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) {
		return nil, os.ErrInvalid
	}
	t.Cleanup(func() { documents.RenderPDFFunc = orig })

	db := newGenerateTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	htmlID, pdfID, err := documents.GenerateDocument(ctx, db.DB, "default", "", documents.DocTypeCV, documents.CVData{Name: "Jane Doe"})
	if err == nil {
		t.Fatal("expected an error when PDF rendering fails, got nil")
	}
	if htmlID == "" {
		t.Fatal("expected the HTML document to still be created even though PDF rendering failed")
	}
	if pdfID != "" {
		t.Fatalf("expected empty pdfDocID on PDF failure, got %q", pdfID)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -run TestGenerateDocument -v`
Expected: FAIL with "undefined: documents.GenerateDocument" / "undefined: documents.RenderPDFFunc" (compile errors).

- [ ] **Step 3: Add the swappable func var to pdf.go**

Modify `internal/documents/pdf.go`: rename the function body to a private implementation and expose it through a package-level var so tests can inject a fake:

```go
// RenderPDFFunc is documents.RenderPDF's implementation, exposed as a
// swappable package-level variable so tests (in this package's own test
// files, which can see unexported identifiers... actually this must stay
// exported since generate_test.go needs it from documents_test — see the
// corrected declaration below) can inject a fake without a real browser.
// RenderPDF itself just calls this var, so production code always goes
// through RenderPDFFunc's currently-assigned implementation.
var RenderPDFFunc = renderPDFImpl

// RenderPDF launches a standalone, unauthenticated headless browser...
// (keep this doc comment as already written)
func RenderPDF(ctx context.Context, html string) ([]byte, error) {
	return RenderPDFFunc(ctx, html)
}

func renderPDFImpl(ctx context.Context, html string) ([]byte, error) {
	// ... the exact body already written in Task 2 Step 3, unchanged ...
}
```

Concretely: rename Task 2's `func RenderPDF(ctx context.Context, html string) ([]byte, error) { ... }` to `func renderPDFImpl(ctx context.Context, html string) ([]byte, error) { ... }` (same body, same doc comment moved to describe the exported wrapper instead), then add the two-line var + wrapper shown above in its place.

- [ ] **Step 4: Write GenerateDocument**

```go
// internal/documents/generate.go
package documents

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/vault"
)

// GenerateDocument renders docType against data, saves the HTML and the
// PDF into profileID's vault (optionally linked to applicationID via
// vault.RegisterDocument's variadic parameter), and returns both vault
// IDs. If PDF rendering fails, the already-created HTML document is kept
// (htmlDocID is non-empty) and pdfDocID is empty — see the design spec's
// Error Handling section for why nothing is discarded on a partial failure.
func GenerateDocument(ctx context.Context, db *sql.DB, profileID, applicationID string, docType DocType, data interface{}) (htmlDocID, pdfDocID string, err error) {
	html, err := Render(docType, data)
	if err != nil {
		return "", "", fmt.Errorf("documents.GenerateDocument: %w", err)
	}

	htmlFile, err := os.CreateTemp("", string(docType)+"-*.html")
	if err != nil {
		return "", "", fmt.Errorf("documents.GenerateDocument: creating temp html file: %w", err)
	}
	defer os.Remove(htmlFile.Name())
	if _, err := htmlFile.WriteString(html); err != nil {
		htmlFile.Close()
		return "", "", fmt.Errorf("documents.GenerateDocument: writing temp html file: %w", err)
	}
	htmlFile.Close()

	ctxWithProfile := vault.ContextWithProfileID(ctx, profileID)
	if applicationID != "" {
		htmlDocID, err = vault.RegisterDocument(ctxWithProfile, db, htmlFile.Name(), "generated", applicationID)
	} else {
		htmlDocID, err = vault.RegisterDocument(ctxWithProfile, db, htmlFile.Name(), "generated")
	}
	if err != nil {
		return "", "", fmt.Errorf("documents.GenerateDocument: saving html: %w", err)
	}

	pdfBytes, err := RenderPDFFunc(ctx, html)
	if err != nil {
		return htmlDocID, "", fmt.Errorf("documents.GenerateDocument: rendering pdf: %w", err)
	}

	pdfFile, err := os.CreateTemp("", string(docType)+"-*.pdf")
	if err != nil {
		return htmlDocID, "", fmt.Errorf("documents.GenerateDocument: creating temp pdf file: %w", err)
	}
	defer os.Remove(pdfFile.Name())
	if _, err := pdfFile.Write(pdfBytes); err != nil {
		pdfFile.Close()
		return htmlDocID, "", fmt.Errorf("documents.GenerateDocument: writing temp pdf file: %w", err)
	}
	pdfFile.Close()

	if applicationID != "" {
		pdfDocID, err = vault.RegisterDocument(ctxWithProfile, db, pdfFile.Name(), "generated", applicationID)
	} else {
		pdfDocID, err = vault.RegisterDocument(ctxWithProfile, db, pdfFile.Name(), "generated")
	}
	if err != nil {
		return htmlDocID, "", fmt.Errorf("documents.GenerateDocument: saving pdf: %w", err)
	}

	return htmlDocID, pdfDocID, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/documents/... -v 2>&1 | tail -30`
Expected: PASS — all tests from Tasks 1-3 (the injected-`RenderPDFFunc` tests never touch a real browser, so they pass regardless of the environment's headless-Chrome availability).

- [ ] **Step 6: Commit**

```bash
git add internal/documents/pdf.go internal/documents/generate.go internal/documents/generate_test.go
git commit -m "$(cat <<'EOF'
feat(documents): add GenerateDocument orchestration

Render -> save HTML to vault -> RenderPDF -> save PDF to vault, both
optionally linked to an application. PDF-rendering is exposed as a
swappable RenderPDFFunc var so orchestration logic can be tested without
a real browser; a PDF failure keeps the already-created HTML document.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 4: CLI command

**Files:**
- Create: `cmd/monoagentcli/documents.go`
- Create: `cmd/monoagentcli/documents_test.go`
- Modify: `cmd/monoagentcli/root.go`

**Interfaces:**
- Consumes: `documents.{DocType,DocTypeCV,DocTypeCoverLetter,DocTypeTenderProposal,CVData,CoverLetterData,TenderProposalData,GenerateDocument,RenderPDFFunc}` (Tasks 0-3).
- Produces: `newDocumentsCmd(cfg *globalConfig) *cobra.Command`, registered in root. No other task depends on this.

- [ ] **Step 1: Write the failing CLI tests**

```go
// cmd/monoagentcli/documents_test.go
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/storage"
)

func newDocumentsCLITestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli-documents-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}
	return dbPath
}

func runDocumentsCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newDocumentsCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestDocumentsRenderCV(t *testing.T) {
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = orig })

	dbPath := newDocumentsCLITestDB(t)
	dataFile := filepath.Join(t.TempDir(), "cv.json")
	if err := os.WriteFile(dataFile, []byte(`{"name":"Jane Doe","title":"Backend Engineer"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runDocumentsCmd(t, dbPath, "render", "--type", "cv", "--data-file", dataFile)
	if err != nil {
		t.Fatalf("documents render: %v (%s)", err, out)
	}
	if !strings.Contains(out, "html_document_id") || !strings.Contains(out, "pdf_document_id") {
		t.Fatalf("expected both document ids in output, got: %s", out)
	}
}

func TestDocumentsRenderRejectsUnknownType(t *testing.T) {
	dbPath := newDocumentsCLITestDB(t)
	dataFile := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(dataFile, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDocumentsCmd(t, dbPath, "render", "--type", "nonsense", "--data-file", dataFile); err == nil {
		t.Fatal("expected error for unknown --type, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestDocumentsRender -v`
Expected: FAIL with "undefined: newDocumentsCmd" (compile error).

- [ ] **Step 3: Write the command**

```go
// cmd/monoagentcli/documents.go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/documents"

	"github.com/spf13/cobra"
)

// newDocumentsCmd returns the `documents` command group: renders
// structured CV/cover-letter/tender-proposal data into HTML+PDF and saves
// both into the vault. See
// docs/mastermind/specs/2026-09-05-document-generation-design.md.
func newDocumentsCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "documents",
		Short: "Generate CV, cover letter, and tender proposal documents",
	}
	cmd.AddCommand(newDocumentsRenderCmd(cfg))
	return cmd
}

func newDocumentsRenderCmd(cfg *globalConfig) *cobra.Command {
	var docType, dataFile, applicationID string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a document from structured JSON data",
		Example: `  monoagentcli documents render --type cv --data-file cv.json
  monoagentcli documents render --type cover_letter --data-file letter.json --application-id abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(dataFile)
			if err != nil {
				return fmt.Errorf("reading data file: %w", err)
			}

			var htmlDocID, pdfDocID string
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			switch documents.DocType(docType) {
			case documents.DocTypeCV:
				var data documents.CVData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeCV, data)
			case documents.DocTypeCoverLetter:
				var data documents.CoverLetterData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeCoverLetter, data)
			case documents.DocTypeTenderProposal:
				var data documents.TenderProposalData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeTenderProposal, data)
			default:
				return errInvalidInput("--type must be one of cv, cover_letter, tender_proposal, got %q", docType)
			}
			if err != nil {
				return fmt.Errorf("generating document: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"html_document_id": htmlDocID, "pdf_document_id": pdfDocID})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Generated %s: html=%s pdf=%s\n", docType, htmlDocID, pdfDocID)
			return nil
		},
	}
	cmd.Flags().StringVar(&docType, "type", "", "Document type: cv, cover_letter, or tender_proposal (required)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "Path to a JSON file matching the chosen type's fields (required)")
	cmd.Flags().StringVar(&applicationID, "application-id", "", "Optional job/tender application to link this document to")
	cmd.MarkFlagRequired("type")
	cmd.MarkFlagRequired("data-file")
	return cmd
}
```

- [ ] **Step 4: Register the command**

In `cmd/monoagentcli/root.go`, add `newDocumentsCmd(cfg),` to the `cmd.AddCommand(...)` list (after `newApplicationCmd(cfg),`):

```go
		newApplicationCmd(cfg),
		newDocumentsCmd(cfg),
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestDocumentsRender -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing (or only the environmental `pdf_test.go` note already flagged in Task 2, if applicable).

- [ ] **Step 7: Commit**

```bash
git add cmd/monoagentcli/documents.go cmd/monoagentcli/documents_test.go cmd/monoagentcli/root.go
git commit -m "$(cat <<'EOF'
feat(cli): add `monoagentcli documents render` command

Renders a CV/cover-letter/tender-proposal from a JSON data file into
HTML+PDF, saved into the vault, optionally linked to an application.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 5: Workflow node `documents.render`

**Files:**
- Create: `internal/nodes/documents/render.go`
- Create: `internal/nodes/documents/render_schema.go`
- Create: `internal/nodes/documents/render_test.go`
- Modify: `internal/tools/schemagen/manifest.go`
- Modify: `internal/noderegistry/registry.go`
- Create (generated, not hand-edited): `internal/workflow/schemas/documents.render.json`

**Interfaces:**
- Consumes: `documents.{DocType,DocTypeCV,DocTypeCoverLetter,DocTypeTenderProposal,CVData,CoverLetterData,TenderProposalData,GenerateDocument,RenderPDFFunc}` (Tasks 0-3); `workflow.{NodeExecutor,NodeInput,NodeOutput,Item,NewItem,NodeTypeRegistry}` (existing).
- Produces: node type `documents.render` registered in the global registry. No other task in this plan depends on it (a future phase's chat/apply-automation workflow will).

- [ ] **Step 1: Write the failing test**

```go
// internal/nodes/documents/render_test.go
package documentsnodes_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
	documentsnodes "github.com/monoes/mono-agent/internal/nodes/documents"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "documents-node-test.db")
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

func TestRenderNodeCreatesDocument(t *testing.T) {
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = orig })

	db := newTestDB(t)
	documentsnodes.SetGlobalDB(db.DB)

	node := &documentsnodes.RenderNode{}
	if node.Type() != "documents.render" {
		t.Fatalf("expected type documents.render, got %q", node.Type())
	}
	config := map[string]interface{}{
		"doc_type": "cv",
		"data":     map[string]interface{}{"name": "Jane Doe"},
	}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(outputs) != 1 || len(outputs[0].Items) != 1 {
		t.Fatalf("expected exactly one output item, got %+v", outputs)
	}
	if outputs[0].Items[0].JSON["html_document_id"] == "" {
		t.Fatal("expected html_document_id in output")
	}
}

func TestRenderNodeRejectsUnknownDocType(t *testing.T) {
	db := newTestDB(t)
	documentsnodes.SetGlobalDB(db.DB)

	node := &documentsnodes.RenderNode{}
	config := map[string]interface{}{"doc_type": "nonsense", "data": map[string]interface{}{}}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error for unknown doc_type, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/documents/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the node**

```go
// internal/nodes/documents/render.go

// Package documentsnodes exposes internal/documents as a workflow node
// type: documents.render.
package documentsnodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/workflow"
)

// globalDB is the process-wide SQLite DB used by RenderNode. Set at startup.
var globalDB *sql.DB

// SetGlobalDB wires the shared SQLite connection into this package's node(s).
func SetGlobalDB(db *sql.DB) {
	globalDB = db
}

// RegisterAll registers documents.render into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalDB(db)
	r.Register("documents.render", func() workflow.NodeExecutor { return &RenderNode{} })
}

// RenderNode generates a CV/cover-letter/tender-proposal document from
// config and saves it into the vault.
// Type: "documents.render"
type RenderNode struct{}

func (n *RenderNode) Type() string { return "documents.render" }

func (n *RenderNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalDB == nil {
		return nil, fmt.Errorf("documents.render: database not available (call SetGlobalDB at startup)")
	}
	docTypeStr, _ := config["doc_type"].(string)
	rawData, ok := config["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("documents.render: config \"data\" must be an object")
	}
	dataJSON, err := json.Marshal(rawData)
	if err != nil {
		return nil, fmt.Errorf("documents.render: re-marshaling data: %w", err)
	}

	profileID, _ := config["profile_id"].(string)
	if profileID == "" {
		profileID = "default"
	}
	applicationID, _ := config["application_id"].(string)

	docType := documents.DocType(docTypeStr)
	var typedData interface{}
	switch docType {
	case documents.DocTypeCV:
		var d documents.CVData
		if err := json.Unmarshal(dataJSON, &d); err != nil {
			return nil, fmt.Errorf("documents.render: parsing data as CVData: %w", err)
		}
		typedData = d
	case documents.DocTypeCoverLetter:
		var d documents.CoverLetterData
		if err := json.Unmarshal(dataJSON, &d); err != nil {
			return nil, fmt.Errorf("documents.render: parsing data as CoverLetterData: %w", err)
		}
		typedData = d
	case documents.DocTypeTenderProposal:
		var d documents.TenderProposalData
		if err := json.Unmarshal(dataJSON, &d); err != nil {
			return nil, fmt.Errorf("documents.render: parsing data as TenderProposalData: %w", err)
		}
		typedData = d
	default:
		return nil, fmt.Errorf("documents.render: config \"doc_type\" must be one of cv, cover_letter, tender_proposal, got %q", docTypeStr)
	}

	htmlDocID, pdfDocID, err := documents.GenerateDocument(ctx, globalDB, profileID, applicationID, docType, typedData)
	if err != nil {
		return nil, fmt.Errorf("documents.render: %w", err)
	}

	out := map[string]interface{}{"html_document_id": htmlDocID, "pdf_document_id": pdfDocID, "doc_type": docTypeStr}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{workflow.NewItem(out)}}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/documents/... -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Write the schema struct**

```go
// internal/nodes/documents/render_schema.go
package documentsnodes

// RenderNodeSchema documents the config keys RenderNode.Execute reads out
// of its map[string]interface{} config.
type RenderNodeSchema struct {
	DocType       string `json:"doc_type" schema:"label=Document Type,type=select,required,options=cv|cover_letter|tender_proposal,help=Which document to generate."`
	Data          string `json:"data" schema:"label=Data,type=code,language=json,required,help=A JSON object matching the chosen document type's fields."`
	ApplicationID string `json:"application_id" schema:"label=Application ID,type=text,help=Optional job/tender application to link this document to."`
	ProfileID     string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the generated document."`
}
```

- [ ] **Step 6: Register in the schemagen manifest**

In `internal/tools/schemagen/manifest.go`, add a new section after the `discovery.*` entry:

```go
	// --- documents.* ---
	{NodeType: "documents.render", GoFile: "internal/nodes/documents/render_schema.go", StructName: "RenderNodeSchema"},
```

- [ ] **Step 7: Generate the schema and register the node package**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go run ./cmd/schemagen`
Expected: writes `internal/workflow/schemas/documents.render.json` among its full regeneration pass.

In `internal/noderegistry/registry.go`, add the import (alphabetically) and register call:

```go
	documentsnodes "github.com/monoes/mono-agent/internal/nodes/documents"
```

```go
	documentsnodes.RegisterAll(registry, db)
```

- [ ] **Step 8: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing (or only the Task 2 environmental note, if applicable). Completes Phase 4.

- [ ] **Step 9: Commit**

```bash
git add internal/nodes/documents/ internal/tools/schemagen/manifest.go internal/noderegistry/registry.go internal/workflow/schemas/documents.render.json
git commit -m "$(cat <<'EOF'
feat(documents): add documents.render workflow node

Wraps documents.GenerateDocument for use in workflows; registered in
noderegistry.Build alongside the Phase 1-3 node packages. Completes
Phase 4 (Document Generation).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

## Self-Review

**1. Spec coverage:**
- CV/cover-letter/tender-proposal templates + render → Task 1. ✅
- PDF conversion via standalone go-rod → Task 2. ✅
- Vault save + application link → Task 0, Task 3. ✅
- CLI surface → Task 4. ✅
- Workflow node → Task 5. ✅
- Escaping/security invariant → tested explicitly in Task 1 (`TestRenderCVEscapesUntrustedContent`). ✅
- PDF-failure-keeps-HTML behavior → tested explicitly in Task 3 (`TestGenerateDocumentKeepsHTMLWhenPDFFails`). ✅

**2. Placeholder scan:** No "TBD"/"TODO". Task 3's Step 3 shows an explanatory rename-and-wrap instruction rather than a fresh code block for `pdf.go`'s modification — this is because it's editing Task 2's already-fully-specified code, and the instruction is precise (rename this exact function, add this exact two-line wrapper) rather than vague ("refactor as needed").

**3. Type consistency:** `documents.DocType`/`CVData`/`CoverLetterData`/`TenderProposalData` (Task 1) are used with identical field names (matching their `json:` tags) across Task 3's tests, Task 4's CLI, and Task 5's node. `GenerateDocument`'s signature (`ctx, db, profileID, applicationID string, docType DocType, data interface{}) (htmlDocID, pdfDocID string, err error)`, introduced in Task 3, is called with matching argument order in Task 4 and Task 5. `RenderPDFFunc`'s swappable-var pattern (Task 3) is used identically in Task 4's and Task 5's tests.

One acknowledged, explicitly-flagged risk carried from the design spec: `RenderPDF`'s real-browser test (Task 2) depends on headless Chrome/Chromium being reachable in whatever environment executes this plan — not a gap in the plan itself, but an environmental precondition already flagged in Global Constraints, the design spec's Known Limitation, and Task 2's own step instructions, with an explicit non-workaround path (report the environmental failure, don't weaken the test).
