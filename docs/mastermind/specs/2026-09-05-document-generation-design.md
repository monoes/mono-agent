# CV / Cover Letter / Tender Document Generation — Design Spec

Date: 2026-09-05
Status: Approved (Phase 4 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

Phase 4 of the multi-phase feature. Per the user's original request: "For
application CV should have html template that also will get saved in file
vault and also cover letter too." The original phase breakdown also
included tender-document generation.

This phase builds the **substrate**: HTML templates, a deterministic
render pipeline, PDF conversion, and vault storage. It does not generate
the *content* of a CV/cover letter from a job posting — that's Phase 5/6's
job (chat-driven matching and application content generation). Phase 4
exposes a CLI command and a workflow node that take already-assembled
structured data (a JSON object) and produce a real, saved CV/cover-letter/
tender-proposal document — so it's independently usable and testable now,
and Phase 6 later calls the same function with LLM-generated data.

### Reused infrastructure (verified by reading the code)

- `career-ops`'s researched pattern (already approved in Phase 1's
  research): the LLM/caller emits a small structured **JSON payload**,
  never raw HTML — a deterministic renderer does all HTML escaping. This
  phase's data structs + `html/template` (Go's auto-escaping template
  engine) implement this directly.
- `github.com/go-rod/rod` — already a direct dependency, already used
  throughout `internal/bot/*` for authenticated browser sessions.
  `cmd/monoagentcli/crawl.go` already has a **standalone, unauthenticated**
  headless-browser launch (`launcher.New().Headless(...).Launch()` →
  `rod.New().ControlURL(...)` → `browser.Connect()`) for one-off tasks —
  this phase reuses that exact launch pattern for PDF rendering, not the
  login-session infrastructure in `internal/nodes/browser_adapter.go`.
  `*rod.Page` has `SetDocumentContent(html string) error` (load HTML
  directly, no temp file/URL needed) and
  `PDF(req *proto.PagePrintToPDF) (*rod.StreamReader, error)` (Chrome's
  native "Print to PDF", `*rod.StreamReader` implements `io.Reader`) —
  verified by reading `go-rod`'s actual source
  (`~/go/pkg/mod/github.com/go-rod/rod@v0.116.2/page.go`), not assumed.
- `internal/vault`'s `RegisterDocument`/`ListDocuments`/`DeleteDocument`
  (Phase 3) — reused directly for saving both the generated HTML and PDF
  files. Phase 1's design spec already reserved this exact use case:
  "Vault documents (introduced in Phase 4) will carry an optional
  `application_id` link" — this phase adds that column now.
- `internal/connections`'s variadic-optional-parameter convention (e.g.
  `Get(ctx, id string, profileID ...string)`) — this phase's
  `RegisterDocument` extension for an optional `applicationID` follows the
  same style rather than a new overload or a breaking signature change.

## Requirements

- Given structured CV data (name, summary, experience, education, skills),
  render a real HTML document from a template, convert it to PDF, and save
  both into the vault.
- Same for a cover letter (sender, recipient, company, paragraphs) and a
  generic tender proposal (issuing org, tender title, sections).
- Optionally link a generated document to a specific job/tender
  application (Phase 1's `applications` table).
- CLI-first per the project's stated requirement — a workflow node too,
  for later phases to call programmatically.

## Architecture

### `vault_documents` gets an `application_id` link (schema addition)

```sql
-- data/migrations/035_vault_documents_application_link.sql
ALTER TABLE vault_documents ADD COLUMN application_id TEXT;
```

Nullable, no foreign-key enforcement needed (SQLite FKs are advisory
without `PRAGMA foreign_keys=ON` scoping issues already documented
elsewhere in this codebase's migration comments) — a document generated
before Phase 1/4 integration, or a general profile document, has no
application. `vault.RegisterDocument` gets an optional trailing parameter:

```go
// RegisterDocument(ctx, db, src, source string, applicationID ...string) (string, error)
```

matching `internal/connections.Store.Get`'s existing
`profileID ...string` convention. `DocumentEntry` gets an `ApplicationID
string` field (empty when unset). `ListDocuments` is unchanged in
signature; a document's `ApplicationID` is just populated when present.

### Data types and templates (new package `internal/documents`)

```go
// internal/documents/documents.go
package documents

type DocType string

const (
	DocTypeCV              DocType = "cv"
	DocTypeCoverLetter     DocType = "cover_letter"
	DocTypeTenderProposal  DocType = "tender_proposal"
)

type CVData struct {
	Name, Title, Email, Phone, Location, Summary string
	Experience []ExperienceEntry
	Education  []EducationEntry
	Skills     []string
}

type ExperienceEntry struct {
	Company, JobTitle, Dates string
	Bullets []string
}

type EducationEntry struct {
	School, Degree, Dates string
}

type CoverLetterData struct {
	SenderName, RecipientName, CompanyName, Date string
	Paragraphs []string
}

type TenderProposalData struct {
	CompanyName, TenderTitle, IssuingOrg, Date string
	Sections []ProposalSection
}

type ProposalSection struct {
	Heading string
	Body    []string
}
```

Templates live as real, separately-editable `.html` files (not inline Go
strings), embedded via `//go:embed`:

```
internal/documents/templates/cv.html
internal/documents/templates/cover_letter.html
internal/documents/templates/tender_proposal.html
```

`Render(docType DocType, data interface{}) (string, error)` parses the
matching template (`html/template`, auto-escaping — the caller's data,
even if ultimately LLM-produced in a later phase, is always escaped, never
trusted as raw markup, per career-ops's XSS-safe pattern) and executes it
against `data`, returning the rendered HTML string.

### PDF conversion (`internal/documents/pdf.go`)

```go
// RenderPDF launches a standalone, unauthenticated headless browser
// (the same launcher.New()/rod.New() pattern as cmd/monoagentcli/crawl.go
// — not the login-session infrastructure in internal/nodes/browser_adapter.go),
// loads html directly via Page.SetDocumentContent (no temp file or URL
// needed), and returns the rendered PDF bytes via Chrome's native
// "Print to PDF". The browser is launched and closed within this single
// call — no session is kept alive across calls.
func RenderPDF(ctx context.Context, html string) ([]byte, error)
```

### Orchestration (`internal/documents/generate.go`)

```go
// GenerateDocument renders docType against data, saves the HTML and the
// PDF into profileID's vault (optionally linked to applicationID), and
// returns both vault IDs. Both files are saved even if one step partially
// fails after the other succeeds — see Error Handling.
func GenerateDocument(ctx context.Context, db *sql.DB, profileID, applicationID string, docType DocType, data interface{}) (htmlDocID, pdfDocID string, err error)
```

### CLI

```
monoagentcli documents render --type cv --data-file cv.json [--application-id <id>]
monoagentcli documents render --type cover_letter --data-file letter.json
monoagentcli documents render --type tender_proposal --data-file proposal.json
```

`--data-file` points at a JSON file matching the chosen type's Go struct
field names (`json` struct tags added to each data type — camelCase keys,
matching this codebase's existing JSON convention elsewhere, e.g.
`applications.Application`'s CLI/JSON output). Output: the created
`html_document_id`/`pdf_document_id`.

### Workflow node

One generic `documents.render` node (not three near-identical ones — the
only difference between doc types is which template/struct is used, which
a `doc_type` config field selects), config: `doc_type` (required,
`cv`|`cover_letter`|`tender_proposal`), `data` (required, a JSON object —
the workflow engine already auto-parses a config value that looks like
JSON into a native Go value, per the existing `ExpressionEngine
.resolveValue` behavior noted elsewhere in this codebase's node comments),
`application_id` (optional), `profile_id` (optional, default `default`).
The node round-trips `data` (already a `map[string]interface{}` once the
engine parses it) through `json.Marshal`/`json.Unmarshal` into the
doc-type-specific struct before calling `GenerateDocument` — a standard Go
technique for converting a generic map into a typed struct without writing
a manual field-by-field mapper per type.

## Data Flow

1. CLI/node provides `doc_type` + structured data (+ optional
   `application_id`).
2. `documents.Render(docType, data)` executes the matching `html/template`
   → HTML string.
3. `GenerateDocument` writes that HTML to a temp file, calls
   `vault.RegisterDocument(ctx, db, tmpPath, "generated", applicationID)`
   → `htmlDocID`.
4. `documents.RenderPDF(ctx, html)` launches a throwaway headless browser,
   loads the HTML directly, prints to PDF → bytes.
5. Those bytes are written to a second temp file, registered the same way
   → `pdfDocID`.
6. Both temp files are removed after `RegisterDocument` copies them into
   the vault (matching `vault.Register`'s own copy-then-original-can-go
   pattern for images).

## Error Handling

- Unknown `doc_type` → CLI: `errInvalidInput`; node: a plain error.
- `data` JSON not matching the target struct shape (e.g. `experience` is a
  string instead of an array) → the `json.Unmarshal` round-trip's error is
  surfaced directly (Go's own type-mismatch error message names the
  offending field), not swallowed or genericized.
- HTML render succeeds but PDF rendering fails (e.g. headless Chrome
  unavailable in this environment) → `GenerateDocument` returns the
  already-created `htmlDocID` alongside the error and an empty
  `pdfDocID` — the HTML document is not lost just because PDF conversion
  failed; the caller can retry PDF generation later from the saved HTML
  vault entry (no retry mechanism is built this phase — YAGNI — but
  nothing is silently discarded).
- `RenderPDF` itself: `launcher.New().Launch()` failing (e.g. no Chrome
  binary available in the runtime environment) is a hard error, surfaced
  verbatim — this phase does not attempt a fallback PDF engine.

## Testing

- `internal/documents/documents_test.go` — `Render` for each of the 3
  `DocType`s against representative data, asserting the output HTML
  contains expected escaped content (e.g. a name containing `<script>`
  renders as `&lt;script&gt;`, proving `html/template`'s auto-escaping is
  in effect — this is the concrete test of the "LLM data is always
  escaped" invariant).
- `internal/documents/pdf_test.go` — `RenderPDF` against a trivial HTML
  string, asserting the returned bytes start with `%PDF-` (the real PDF
  magic-number check, a genuine assertion the browser actually produced a
  PDF rather than just "no error"). This test requires a real headless
  Chrome/Chromium binary to be available — see the Known Limitation below.
- `internal/documents/generate_test.go` — `GenerateDocument` round-trip
  (both vault IDs created, both files exist on disk at the paths
  `vault.ListDocuments` reports), and the PDF-failure-still-keeps-HTML
  case (inject a `RenderPDF` failure via a package-level function variable
  the test can override, mirroring how other tests in this codebase inject
  fakes — see e.g. `internal/nodes/applications`'s `globalStore` pattern
  for the general shape, adapted here to a swappable func var since
  `RenderPDF` has no natural interface to mock against).
- `cmd/monoagentcli/documents_test.go` — CLI integration tests for `render`
  with each doc type, error cases (unknown type, malformed data file).

### Known Limitation: headless browser availability in CI/sandboxed environments

`RenderPDF`'s tests require a real Chrome/Chromium binary reachable by
`go-rod`'s launcher (the same requirement `crawl.go`'s existing headless
capture command already has — this is not a new environmental dependency
Phase 4 introduces). If no such binary is available in the environment
running the implementation/tests, `pdf_test.go` and any `generate_test.go`
case exercising the real (non-injected) `RenderPDF` path will fail for
environmental reasons, not code-correctness reasons — this should be
diagnosed as such rather than treated as a logic bug, and if genuinely
unavailable, `RenderPDF` still gets full coverage indirectly through
`generate_test.go`'s injected-failure path, and the plan's tasks should
report this distinction clearly rather than silently skip or paper over a
real failure.

## Out of Scope (this phase)

- Generating the actual CV/cover-letter *content* from a job posting or
  profile knowledge (Phase 5/6) — this phase only renders whatever
  structured data it's given.
- Multiple template *styles* per doc type (career-ops has ~7 CV template
  variants) — one template per type is sufficient for this phase; the
  embedded-template structure makes adding alternates later a bounded
  addition (a new `.html` file + a style-name lookup), not a redesign.
- Editing/regenerating a previously-created document — each call to
  `documents render` creates new vault entries; no update-in-place.
