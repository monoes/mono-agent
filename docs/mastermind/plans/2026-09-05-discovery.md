# Job Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (Note: `mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per `docs/mastermind/plans/2026-09-05-applications-foundation.md`'s precedent.)

**Goal:** Given keywords + location, scrape real LinkedIn job postings and write new ones into the Phase 1 `applications` pipeline as `pending`, deduplicated against what's already stored.

**Architecture:** A source-agnostic `internal/discovery` package (types, `Source` interface, dedup, orchestration) with zero dependency on any concrete source; a separate `internal/discoveryregistry` package (mirroring `internal/noderegistry`'s pattern) that imports both `internal/discovery` and the concrete `internal/discovery/sources/linkedin` package to avoid an import cycle; a workflow node and a CLI command on top.

**Tech Stack:** Go 1.27 toolchain at `~/.local/go/bin` (not on default PATH). `github.com/PuerkitoBio/goquery` (already a direct dependency) for HTML parsing. `internal/nodes/ai/crawl.FetchPage` (already exists) for the HTTP fetch layer — no new third-party dependency needed.

## Global Constraints

- Go toolchain at `~/.local/go/bin`, not on default PATH: `export PATH="$HOME/.local/go/bin:$PATH"` before any `go` command.
- Migrations: numbered SQL files in `data/migrations/`, applied via `internal/storage.Database.ApplyMigrations()`. Next number is 032.
- `crawl.FetchPage(ctx, pageURL, FetchOptions{RenderMode: "static"})` is the only fetch mechanism — `RenderMode: "browser"` is explicitly disabled (`internal/nodes/ai/crawl/engine.go` returns an error for it). Default timeout is 30s if `FetchOptions.Timeout` is zero.
- LinkedIn guest endpoint: `https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search`, path `/jobs-guest/jobs/api/seeMoreJobPostings/search` for robots.txt checks. `robots.txt` at `https://www.linkedin.com/robots.txt`.
- Pacing: 1.5–3.0s random delay between paginated fetches, one retry after a 2s backoff on a transient failure, hard stop (no bypass) if `robots.txt` disallows the endpoint, results capped at 100 per search.
- Dedup: exact `JobDetails.URL` match, OR exact match on `(normalize(Title), normalize(Company))` where `normalize` lowercases, strips all non-alphanumeric/non-space characters, and collapses whitespace.
- Tender discovery is explicitly out of scope this phase.
- Node+schema pairing convention, CLI cobra convention, exit-code sentinels (`errNotFound`/`errInvalidInput`), test-harness conventions (temp SQLite DB + `ApplyMigrations`, `httptest.Server` for HTTP doubles) — all identical to Phase 1, see `docs/mastermind/plans/2026-09-05-applications-foundation.md`'s Global Constraints for the exact precedents.
- TDD: every behavior gets a failing test before its implementation. No real network calls in any test.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `data/migrations/032_application_titles.sql` | Add `title` column to `job_details` and `tender_details`. |
| `internal/applications/applications.go` | Modified: add `Title string` to `JobDetails` and `TenderDetails`. |
| `internal/applications/store.go` | Modified: `Create`/`Get` read/write the new `title` column; `validateAndDefault` requires it. |
| `internal/applications/store_test.go` | Modified: add `Title` to literals that must succeed; new required-field test cases. |
| `internal/nodes/applications/create.go` / `create_schema.go` | Modified: read/describe a `title` config key. |
| `internal/nodes/applications/list_test.go`, `set_status_test.go`, `tag_test.go` | Modified: add `Title` to setup literals. |
| `cmd/monoagentcli/application.go` | Modified: `add` command gets a required `--title` flag. |
| `internal/discovery/discovery.go` | `SearchQuery`, `Result`, `Source` interface, `Search` orchestration function. |
| `internal/discovery/dedup.go` | `normalize`, `IsDuplicate`. |
| `internal/discovery/sources/linkedin/linkedin.go` | The concrete LinkedIn `Source` implementation. |
| `internal/discoveryregistry/registry.go` | `Get(name)`, `Names()` — imports both `internal/discovery` and the linkedin source, avoiding the cycle a same-package registry would create. |
| `internal/nodes/discovery/search_jobs.go` / `search_jobs_schema.go` | `discovery.search_jobs` workflow node. |
| `internal/noderegistry/registry.go` | Modified: register the new node package. |
| `internal/tools/schemagen/manifest.go` | Modified: one new manifest entry. |
| `internal/workflow/schemas/discovery.search_jobs.json` | Generated. |
| `cmd/monoagentcli/application_discover.go` | `monoagentcli application discover` command. |

---

### Task 0: Add `Title` to job/tender applications (Phase 1 schema fix)

**Files:**
- Create: `data/migrations/032_application_titles.sql`
- Modify: `internal/applications/applications.go`
- Modify: `internal/applications/store.go`
- Modify: `internal/applications/store_test.go`
- Modify: `internal/nodes/applications/create.go`
- Modify: `internal/nodes/applications/create_schema.go`
- Modify: `internal/nodes/applications/list_test.go`
- Modify: `internal/nodes/applications/set_status_test.go`
- Modify: `internal/nodes/applications/tag_test.go`
- Modify: `cmd/monoagentcli/application.go`
- Modify: `internal/workflow/schemas/applications.create.json` (regenerated, not hand-edited)

**Interfaces:**
- Consumes: everything from Phase 1 (`applications.Store`, `applications.Application`, etc.) as already committed.
- Produces: `applications.JobDetails.Title string` (required, non-empty), `applications.TenderDetails.Title string` (required, non-empty) — consumed by every later task in this plan (`discovery.Search` populates it from a scraped result's title).

- [ ] **Step 1: Write the migration**

```sql
-- data/migrations/032_application_titles.sql
-- Adds a title/position field to job and tender applications, distinct
-- from company/issuing_org — a gap discovered while designing Phase 2
-- (discovery), which needs to store the actual scraped job title.

ALTER TABLE job_details ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE tender_details ADD COLUMN title TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: Add `Title` to the domain structs**

In `internal/applications/applications.go`, modify the `JobDetails` struct:

```go
// JobDetails holds job-kind-specific fields. Title, Company, and URL are
// required.
type JobDetails struct {
	Title            string
	Company          string
	URL              string
	Location         string
	Description      string
	CompensationMin  *float64
	CompensationMax  *float64
	Currency         string
	JobType          string
	IsRemote         *bool
	Source           string
	PostedAt         string // free-form date string, e.g. RFC3339 or "2026-01-15"
}
```

And the `TenderDetails` struct:

```go
// TenderDetails holds tender-kind-specific fields. Title, IssuingOrg, URL,
// and SubmissionDeadline are required.
type TenderDetails struct {
	Title                  string
	IssuingOrg             string
	URL                    string
	Description            string
	SubmissionDeadline     string // free-form date string, required
	EstimatedValue         *float64
	Currency               string
	RequiredCertifications string // comma-separated for Phase 1
	BidDocumentsRequired   string // comma-separated for Phase 1
	Source                 string
	PublishedAt            string
}
```

- [ ] **Step 3: Write the failing tests for required-Title validation**

Append to `internal/applications/store_test.go`:

```go
func TestStoreCreateRejectsMissingJobTitle(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Job:       &applications.JobDetails{Company: "Acme", URL: "https://acme.example/1"}, // missing Title
	}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for missing job title, got nil")
	}
}

func TestStoreCreateRejectsMissingTenderTitle(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindTender,
		Tender: &applications.TenderDetails{
			IssuingOrg: "Ministry", URL: "https://t.example/1", SubmissionDeadline: "2026-12-01",
		}, // missing Title
	}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for missing tender title, got nil")
	}
}

func TestStoreGetJobIncludesTitle(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Job:       &applications.JobDetails{Title: "Senior Backend Engineer", Company: "Acme", URL: "https://acme.example/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "default", app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Job.Title != "Senior Backend Engineer" {
		t.Fatalf("expected title to round-trip, got %q", got.Job.Title)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/applications/... -run 'TestStoreCreateRejectsMissing(Job|Tender)Title|TestStoreGetJobIncludesTitle' -v`
Expected: FAIL. `TestStoreGetJobIncludesTitle` fails with `got.Job.Title != "Senior Backend Engineer"` (empty string), the two `RejectsMissing` tests fail because `Create` currently succeeds (no error) when Title is absent — the test itself reports failure via `t.Fatal`, not a compile error.

- [ ] **Step 5: Update `validateAndDefault` to require Title**

In `internal/applications/store.go`, modify the `KindJob` case of `validateAndDefault`:

```go
	case KindJob:
		if app.Tender != nil {
			return fmt.Errorf("%w: kind is %q but tender details were supplied", ErrInvalidInput, KindJob)
		}
		if app.Job == nil {
			return fmt.Errorf("%w: kind %q requires job details", ErrInvalidInput, KindJob)
		}
		if app.Job.Title == "" {
			return fmt.Errorf("%w: job.title is required", ErrInvalidInput)
		}
		if app.Job.Company == "" {
			return fmt.Errorf("%w: job.company is required", ErrInvalidInput)
		}
		if app.Job.URL == "" {
			return fmt.Errorf("%w: job.url is required", ErrInvalidInput)
		}
```

And the `KindTender` case:

```go
	case KindTender:
		if app.Job != nil {
			return fmt.Errorf("%w: kind is %q but job details were supplied", ErrInvalidInput, KindTender)
		}
		if app.Tender == nil {
			return fmt.Errorf("%w: kind %q requires tender details", ErrInvalidInput, KindTender)
		}
		if app.Tender.Title == "" {
			return fmt.Errorf("%w: tender.title is required", ErrInvalidInput)
		}
		if app.Tender.IssuingOrg == "" {
			return fmt.Errorf("%w: tender.issuing_org is required", ErrInvalidInput)
		}
		if app.Tender.URL == "" {
			return fmt.Errorf("%w: tender.url is required", ErrInvalidInput)
		}
		if app.Tender.SubmissionDeadline == "" {
			return fmt.Errorf("%w: tender.submission_deadline is required", ErrInvalidInput)
		}
```

- [ ] **Step 6: Update `Create`'s SQL to write `title`**

In `internal/applications/store.go`, modify the `job_details` insert inside `Create`:

```go
	case KindJob:
		j := app.Job
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO job_details (application_id, title, company, url, location, description,
			        compensation_min, compensation_max, currency, job_type, is_remote, source, posted_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, j.Title, j.Company, j.URL, j.Location, j.Description,
			nullFloat(j.CompensationMin), nullFloat(j.CompensationMax), j.Currency, j.JobType,
			nullBool(j.IsRemote), j.Source, j.PostedAt,
		); err != nil {
			return fmt.Errorf("applications.Create: insert job_details: %w", err)
		}
```

And the `tender_details` insert:

```go
	case KindTender:
		td := app.Tender
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tender_details (application_id, title, issuing_org, url, description,
			        submission_deadline, estimated_value, currency, required_certifications,
			        bid_documents_required, source, published_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, td.Title, td.IssuingOrg, td.URL, td.Description, td.SubmissionDeadline,
			nullFloat(td.EstimatedValue), td.Currency, td.RequiredCertifications,
			td.BidDocumentsRequired, td.Source, td.PublishedAt,
		); err != nil {
			return fmt.Errorf("applications.Create: insert tender_details: %w", err)
		}
```

- [ ] **Step 7: Update `Get`'s SQL to read `title`**

In `internal/applications/store.go`, modify the `KindJob` case inside `Get`:

```go
	case KindJob:
		var j JobDetails
		var compMin, compMax sql.NullFloat64
		var isRemote sql.NullBool
		err := s.db.QueryRowContext(ctx,
			`SELECT title, company, url, location, description, compensation_min, compensation_max,
			        currency, job_type, is_remote, source, posted_at
			 FROM job_details WHERE application_id = ?`, id,
		).Scan(&j.Title, &j.Company, &j.URL, &j.Location, &j.Description, &compMin, &compMax,
			&j.Currency, &j.JobType, &isRemote, &j.Source, &j.PostedAt)
		if err != nil {
			return nil, fmt.Errorf("applications.Get: job_details: %w", err)
		}
		j.CompensationMin = floatPtr(compMin)
		j.CompensationMax = floatPtr(compMax)
		j.IsRemote = boolPtr(isRemote)
		app.Job = &j
```

And the `KindTender` case:

```go
	case KindTender:
		var td TenderDetails
		var estValue sql.NullFloat64
		err := s.db.QueryRowContext(ctx,
			`SELECT title, issuing_org, url, description, submission_deadline, estimated_value,
			        currency, required_certifications, bid_documents_required, source, published_at
			 FROM tender_details WHERE application_id = ?`, id,
		).Scan(&td.Title, &td.IssuingOrg, &td.URL, &td.Description, &td.SubmissionDeadline, &estValue,
			&td.Currency, &td.RequiredCertifications, &td.BidDocumentsRequired, &td.Source, &td.PublishedAt)
		if err != nil {
			return nil, fmt.Errorf("applications.Get: tender_details: %w", err)
		}
		td.EstimatedValue = floatPtr(estValue)
		app.Tender = &td
```

- [ ] **Step 8: Fix existing test literals that must still succeed**

In `internal/applications/store_test.go`, add `Title:` to every `JobDetails`/`TenderDetails` literal that is expected to succeed (do NOT touch the two deliberately-incomplete literals in `TestStoreCreateRejectsMissingJobFields`/`TestStoreCreateRejectsMissingTenderFields`, or the kind-mismatch literal in `TestStoreCreateRejectsKindDetailMismatch` — those must keep failing for their own stated reason regardless of Title):

- `TestStoreCreateJob`'s `Job: &applications.JobDetails{Company: "Acme Corp", URL: "https://acme.example/1"}` → add `Title: "Senior Backend Engineer",` as the first field.
- `TestStoreCreateTender`'s `Tender: &applications.TenderDetails{IssuingOrg: "Ministry of Example", URL: "https://tenders.example/t/456", SubmissionDeadline: "2026-12-01"}` → add `Title: "Road Maintenance Tender",` as the first field.
- `createTestJob`'s `Job: &applications.JobDetails{Company: "Acme", URL: "https://acme.example/1"}` → add `Title: "Backend Engineer",` as the first field. (This single helper is used by most later `Store` tests — fixing it here fixes all of them at once.)
- `TestStoreGetTenderHydratesDetails`'s `Tender: &applications.TenderDetails{IssuingOrg: "Ministry", URL: "https://tenders.example/1", SubmissionDeadline: "2026-12-01"}` → add `Title: "Road Maintenance Tender",`.
- `TestStoreListFilters`'s `Tender: &applications.TenderDetails{IssuingOrg: "Ministry", URL: "https://t.example/1", SubmissionDeadline: "2026-12-01"}` → add `Title: "Road Maintenance Tender",`.

In `internal/nodes/applications/set_status_test.go`, both occurrences of `Job: &applications.JobDetails{Company: "Acme", URL: "https://a.example"}` → add `Title: "Backend Engineer",`.

In `internal/nodes/applications/tag_test.go`, the one occurrence → add `Title: "Backend Engineer",`.

In `internal/nodes/applications/list_test.go`:
- `Job: &applications.JobDetails{Company: "Acme", URL: "https://a.example"}` → add `Title: "Backend Engineer",`.
- `Tender: &applications.TenderDetails{IssuingOrg: "Min", URL: "https://t.example", SubmissionDeadline: "2026-12-01"}` → add `Title: "Road Maintenance Tender",`.

- [ ] **Step 9: Run all applications and nodes/applications tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/applications/... ./internal/nodes/applications/... -v 2>&1 | tail -60`
Expected: PASS — every test in both packages (the 3 new tests from Step 3, plus every pre-existing test now with `Title` set where needed).

- [ ] **Step 10: Wire `title` through the `applications.create` node**

In `internal/nodes/applications/create.go`, modify the `KindJob` case:

```go
	case applications.KindJob:
		app.Job = &applications.JobDetails{
			Title:           configString(config, "title", ""),
			Company:         configString(config, "company", ""),
			URL:             configString(config, "url", ""),
			Location:        configString(config, "location", ""),
			Description:     configString(config, "description", ""),
			CompensationMin: configFloatPtr(config, "compensation_min"),
			CompensationMax: configFloatPtr(config, "compensation_max"),
			Currency:        configString(config, "currency", ""),
			JobType:         configString(config, "job_type", ""),
			IsRemote:        configBoolPtr(config, "is_remote"),
			Source:          configString(config, "source", "manual"),
			PostedAt:        configString(config, "posted_at", ""),
		}
```

And the `KindTender` case:

```go
	case applications.KindTender:
		app.Tender = &applications.TenderDetails{
			Title:                  configString(config, "title", ""),
			IssuingOrg:             configString(config, "issuing_org", ""),
			URL:                    configString(config, "url", ""),
			Description:            configString(config, "description", ""),
			SubmissionDeadline:     configString(config, "submission_deadline", ""),
			EstimatedValue:         configFloatPtr(config, "estimated_value"),
			Currency:               configString(config, "currency", ""),
			RequiredCertifications: configString(config, "required_certifications", ""),
			BidDocumentsRequired:   configString(config, "bid_documents_required", ""),
			Source:                 configString(config, "source", "manual"),
			PublishedAt:            configString(config, "published_at", ""),
		}
```

- [ ] **Step 11: Add `Title` to the create node's schema and CLI flag**

In `internal/nodes/applications/create_schema.go`, add a `Title` field right after `Kind` (before `ProfileID`):

```go
	Title string `json:"title" schema:"label=Title,type=text,required,help=The job title or tender reference/name."`
```

In `cmd/monoagentcli/application.go`, add a `title` variable and flag to `newApplicationAddCmd`. Modify the `var` declaration line:

```go
	var kind, title, company, url, location, description, currency, jobType, source, postedAt string
```

Add `Title: title,` as the first field in both the `app.Job = &applications.JobDetails{...}` and `app.Tender = &applications.TenderDetails{...}` literals inside `newApplicationAddCmd`'s `RunE`. Add the flag registration (after `cmd.Flags().StringVar(&kind, "kind", "", ...)`):

```go
	cmd.Flags().StringVar(&title, "title", "", "Job title or tender reference/name (required)")
```

And mark it required alongside the existing ones:

```go
	cmd.MarkFlagRequired("kind")
	cmd.MarkFlagRequired("title")
	cmd.MarkFlagRequired("url")
```

- [ ] **Step 12: Regenerate schemas and run the full build/test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go run ./cmd/schemagen && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: schemagen writes `internal/workflow/schemas/applications.create.json` (now including the `title` field) among its full regeneration pass; build succeeds; the grep shows nothing (every package passes or has no tests).

Also update `cmd/monoagentcli/application_test.go`'s `TestApplicationAddListGetStatusTag` and `TestApplicationStatusRejectsInvalidTransition` calls to `application add` — both currently omit `--title`; add `"--title", "Backend Engineer",` to each `add` invocation's args list so they keep passing (the `add` command now requires it).

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestApplication -v`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add data/migrations/032_application_titles.sql internal/applications/applications.go internal/applications/store.go internal/applications/store_test.go internal/nodes/applications/create.go internal/nodes/applications/create_schema.go internal/nodes/applications/list_test.go internal/nodes/applications/set_status_test.go internal/nodes/applications/tag_test.go cmd/monoagentcli/application.go cmd/monoagentcli/application_test.go internal/workflow/schemas/applications.create.json
git commit -m "$(cat <<'EOF'
fix(applications): add required Title field to job/tender applications

JobDetails/TenderDetails had no field for the posting's own title/name,
distinct from company/issuing_org — a gap that would have blocked Phase 2
(discovery) from storing meaningful scraped data. Required at creation,
same rigor as company/url.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 1: `internal/discovery` types and dedup

**Files:**
- Create: `internal/discovery/discovery.go`
- Create: `internal/discovery/dedup.go`
- Create: `internal/discovery/dedup_test.go`

**Interfaces:**
- Consumes: `applications.{Store,Application,JobDetails,Kind,KindJob,ListFilter}` (Phase 1 + Task 0).
- Produces: `discovery.SearchQuery{Keywords,Location string; Limit int}`, `discovery.Result{Title,Company,URL,Location,Description,JobType,PostedAt string; IsRemote bool}`, `discovery.Source` interface (`Name() string`, `Search(ctx, SearchQuery) ([]Result, error)`), `discovery.normalize(string) string`, `discovery.IsDuplicate(ctx, *applications.Store, profileID string, Result) (bool, error)`. Consumed by Task 2 (linkedin implements `Source`), Task 3 (`discovery.Search` orchestration), Task 4/5 (node/CLI).

- [ ] **Step 1: Write the types file (no test needed — pure type declarations)**

```go
// internal/discovery/discovery.go

// Package discovery finds job postings from external sources and imports
// them into the applications pipeline as new pending applications. See
// docs/mastermind/specs/2026-09-05-discovery-design.md.
package discovery

import "context"

// SearchQuery is the source-agnostic search input.
type SearchQuery struct {
	Keywords string
	Location string
	Limit    int // max results to return; sources must not exceed this
}

// Result is one posting a Source found, in unified shape — ready to map
// onto applications.JobDetails.
type Result struct {
	Title       string
	Company     string
	URL         string
	Location    string
	Description string
	JobType     string
	IsRemote    bool
	PostedAt    string // free-form, as scraped
}

// Source is implemented by each pluggable job board.
type Source interface {
	// Name is the value stored in JobDetails.Source for results this
	// Source produces, e.g. "linkedin".
	Name() string
	// Search returns up to query.Limit results. Implementations own their
	// own pagination and pacing internally.
	Search(ctx context.Context, query SearchQuery) ([]Result, error)
}
```

- [ ] **Step 2: Write the failing dedup tests**

```go
// internal/discovery/dedup_test.go
package discovery_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "discovery-test.db")
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

func TestIsDuplicateURLMatch(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "Something Else Entirely", Company: "Different Co", URL: "https://acme.example/jobs/1",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if !dup {
		t.Fatal("expected URL match to be flagged as duplicate")
	}
}

func TestIsDuplicateNormalizedTitleCompanyMatch(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Senior Backend Engineer!", Company: "Acme Corp.", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "senior backend engineer", Company: "acme corp", URL: "https://acme.example/jobs/999-different",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if !dup {
		t.Fatal("expected normalized title+company match to be flagged as duplicate")
	}
}

func TestIsDuplicateNoFalsePositive(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/jobs/2",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if dup {
		t.Fatal("expected a genuinely different posting not to be flagged as duplicate")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/... -v`
Expected: FAIL with "undefined: discovery.IsDuplicate" (compile error).

- [ ] **Step 4: Write dedup.go**

```go
// internal/discovery/dedup.go
package discovery

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/monoes/mono-agent/internal/applications"
)

// normalize lowercases s, strips every character that isn't a letter,
// digit, or space, and collapses whitespace — used to compare a scraped
// title/company against what's already stored despite punctuation or
// casing differences.
func normalize(s string) string {
	var b strings.Builder
	lastWasSpace := true // trims leading space
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastWasSpace = false
		case unicode.IsSpace(r):
			if !lastWasSpace {
				b.WriteRune(' ')
			}
			lastWasSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// IsDuplicate reports whether r matches an existing job-kind application
// for profileID, either by exact URL or by normalized (title, company).
func IsDuplicate(ctx context.Context, store *applications.Store, profileID string, r Result) (bool, error) {
	existing, err := store.List(ctx, profileID, applications.ListFilter{Kind: applications.KindJob})
	if err != nil {
		return false, fmt.Errorf("discovery.IsDuplicate: %w", err)
	}
	normTitle, normCompany := normalize(r.Title), normalize(r.Company)
	for _, app := range existing {
		if app.Job == nil {
			continue
		}
		if app.Job.URL == r.URL {
			return true, nil
		}
		if normalize(app.Job.Title) == normTitle && normalize(app.Job.Company) == normCompany {
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/... -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/discovery/discovery.go internal/discovery/dedup.go internal/discovery/dedup_test.go
git commit -m "$(cat <<'EOF'
feat(discovery): add source-agnostic types and dedup logic

SearchQuery/Result/Source define the pluggable-source contract; IsDuplicate
checks a scraped result against existing job applications by exact URL or
normalized title+company match before it's ever inserted.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 2: LinkedIn source

**Files:**
- Create: `internal/discovery/sources/linkedin/linkedin.go`
- Create: `internal/discovery/sources/linkedin/robots.go`
- Create: `internal/discovery/sources/linkedin/linkedin_test.go`
- Create: `internal/discovery/sources/linkedin/testdata/search_page.html`

**Interfaces:**
- Consumes: `discovery.{SearchQuery,Result,Source}` (Task 1), `crawl.FetchPage`/`crawl.FetchOptions` (existing, `internal/nodes/ai/crawl/engine.go`).
- Produces: `linkedin.New() *linkedin.Source` implementing `discovery.Source`. Consumed by Task 3's `internal/discoveryregistry`.

- [ ] **Step 1: Write the sample fixture HTML**

```html
<!-- internal/discovery/sources/linkedin/testdata/search_page.html -->
<!-- Hand-built fixture matching LinkedIn's documented guest job-search
     result markup (see docs/mastermind/specs/2026-09-05-discovery-design.md
     "Known Limitation" section — this validates parsing logic against this
     documented shape, not live-site accuracy). -->
<ul>
  <li>
    <div class="base-card">
      <h3 class="base-search-card__title">Senior Backend Engineer</h3>
      <h4 class="base-search-card__subtitle">Acme Corp</h4>
      <span class="job-search-card__location">Berlin, Germany</span>
      <a class="base-card__full-link" href="https://www.linkedin.com/jobs/view/1111?trk=abc">Link</a>
      <time class="job-search-card__listdate">2 days ago</time>
    </div>
  </li>
  <li>
    <div class="base-card">
      <h3 class="base-search-card__title">Platform Engineer</h3>
      <h4 class="base-search-card__subtitle">Beta Industries</h4>
      <span class="job-search-card__location">Remote</span>
      <a class="base-card__full-link" href="https://www.linkedin.com/jobs/view/2222">Link</a>
      <time class="job-search-card__listdate">1 week ago</time>
    </div>
  </li>
  <li>
    <!-- Not a job card — must be skipped, not treated as an empty result. -->
    <div class="unrelated-card">Ad content</div>
  </li>
</ul>
```

- [ ] **Step 2: Write the failing parsing test**

```go
// internal/discovery/sources/linkedin/linkedin_test.go
package linkedin_test

import (
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/discovery/sources/linkedin"
)

func TestParseSearchPageExtractsFields(t *testing.T) {
	html, err := os.ReadFile("testdata/search_page.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	results, err := linkedin.ParseSearchPage(string(html))
	if err != nil {
		t.Fatalf("ParseSearchPage: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (unrelated card skipped), got %d", len(results))
	}
	if results[0].Title != "Senior Backend Engineer" || results[0].Company != "Acme Corp" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].URL != "https://www.linkedin.com/jobs/view/1111" {
		t.Fatalf("expected tracking query param stripped, got %q", results[0].URL)
	}
	if results[0].Location != "Berlin, Germany" {
		t.Fatalf("unexpected location: %q", results[0].Location)
	}
	if results[1].Title != "Platform Engineer" || results[1].Company != "Beta Industries" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/sources/linkedin/... -v`
Expected: FAIL with "undefined: linkedin.ParseSearchPage" (compile error).

- [ ] **Step 4: Write the robots.txt helper**

```go
// internal/discovery/sources/linkedin/robots.go
package linkedin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fetchRobotsTxt fetches robotsURL directly via net/http — not via
// crawl.FetchPage, which parses/re-serializes as HTML and would corrupt
// robots.txt's line-based plain-text structure. A missing robots.txt
// (404) means nothing is disallowed.
func fetchRobotsTxt(ctx context.Context, robotsURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return "", fmt.Errorf("linkedin: robots.txt request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MonoAgent/1.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("linkedin: robots.txt fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("linkedin: robots.txt returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("linkedin: reading robots.txt: %w", err)
	}
	return string(body), nil
}

// isDisallowedByRobots is a minimal, prefix-match check of a robots.txt
// body for whether path is disallowed under a "User-agent: *" block — not
// a full RFC9309 implementation, sufficient for a hard stop/no-bypass gate.
func isDisallowedByRobots(robotsTxt, path string) bool {
	lines := strings.Split(robotsTxt, "\n")
	inWildcardBlock := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			inWildcardBlock = val == "*"
		case "disallow":
			if inWildcardBlock && val != "" && strings.HasPrefix(path, val) {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 5: Write the LinkedIn source (parsing, pagination, pacing, retry)**

```go
// internal/discovery/sources/linkedin/linkedin.go

// Package linkedin implements discovery.Source against LinkedIn's public
// unauthenticated "guest" job-search endpoint — no login required. See
// docs/mastermind/specs/2026-09-05-discovery-design.md.
package linkedin

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/nodes/ai/crawl"
)

const (
	guestSearchBase = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"
	guestSearchPath = "/jobs-guest/jobs/api/seeMoreJobPostings/search"
	robotsURL       = "https://www.linkedin.com/robots.txt"
	pageSize        = 25
	maxLimit        = 100
)

// Source scrapes LinkedIn's public guest job-search endpoint.
type Source struct{}

// New creates a LinkedIn Source.
func New() *Source { return &Source{} }

// Name identifies results from this Source as coming from "linkedin".
func (s *Source) Name() string { return "linkedin" }

// Search fetches up to query.Limit (capped at 100) job postings matching
// query.Keywords/query.Location, paginating the guest endpoint, pacing
// requests 1.5-3.0s apart, retrying a transient page failure once.
func (s *Source) Search(ctx context.Context, query discovery.SearchQuery) ([]discovery.Result, error) {
	limit := query.Limit
	if limit <= 0 || limit > maxLimit {
		limit = maxLimit
	}

	robotsTxt, err := fetchRobotsTxt(ctx, robotsURL)
	if err != nil {
		return nil, fmt.Errorf("linkedin: checking robots.txt: %w", err)
	}
	if isDisallowedByRobots(robotsTxt, guestSearchPath) {
		return nil, fmt.Errorf("linkedin: robots.txt disallows %s — refusing to scrape", guestSearchPath)
	}

	var results []discovery.Result
	for start := 0; len(results) < limit; start += pageSize {
		pageURL := fmt.Sprintf("%s?keywords=%s&location=%s&start=%d",
			guestSearchBase, url.QueryEscape(query.Keywords), url.QueryEscape(query.Location), start)

		html, err := fetchPageWithRetry(ctx, pageURL)
		if err != nil {
			if len(results) > 0 {
				return results, fmt.Errorf("linkedin: search truncated after %d result(s): %w", len(results), err)
			}
			return nil, fmt.Errorf("linkedin: search: %w", err)
		}

		pageResults, err := ParseSearchPage(html)
		if err != nil {
			return results, fmt.Errorf("linkedin: parsing page at start=%d: %w", start, err)
		}
		if len(pageResults) == 0 {
			break
		}
		results = append(results, pageResults...)
		if len(results) >= limit {
			break
		}

		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case <-time.After(paceDelay()):
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func paceDelay() time.Duration {
	return time.Duration(1500+rand.Intn(1500)) * time.Millisecond
}

func fetchPageWithRetry(ctx context.Context, pageURL string) (string, error) {
	result, err := crawl.FetchPage(ctx, pageURL, crawl.FetchOptions{RenderMode: "static"})
	if err == nil {
		return result.HTML, nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Second):
	}
	result, err = crawl.FetchPage(ctx, pageURL, crawl.FetchOptions{RenderMode: "static"})
	if err != nil {
		return "", err
	}
	return result.HTML, nil
}

// ParseSearchPage extracts job postings from one page of LinkedIn guest
// search-result HTML. Exported so it can be unit-tested against a fixture
// without a network call.
func ParseSearchPage(html string) ([]discovery.Result, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	var results []discovery.Result
	doc.Find("li").Each(func(_ int, li *goquery.Selection) {
		card := li.Find(".base-card").First()
		if card.Length() == 0 {
			return
		}
		title := strings.TrimSpace(card.Find(".base-search-card__title").First().Text())
		company := strings.TrimSpace(card.Find(".base-search-card__subtitle").First().Text())
		location := strings.TrimSpace(card.Find(".job-search-card__location").First().Text())
		jobURL, _ := card.Find(".base-card__full-link").First().Attr("href")
		postedAt := strings.TrimSpace(card.Find(".job-search-card__listdate").First().Text())
		if title == "" || jobURL == "" {
			return
		}
		results = append(results, discovery.Result{
			Title:    title,
			Company:  company,
			URL:      strings.SplitN(jobURL, "?", 2)[0],
			Location: location,
			PostedAt: postedAt,
		})
	})
	return results, nil
}
```

- [ ] **Step 6: Run parsing test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/sources/linkedin/... -run TestParseSearchPage -v`
Expected: PASS.

- [ ] **Step 7: Write the failing robots.txt gate test**

```go
// append to internal/discovery/sources/linkedin/linkedin_test.go
package linkedin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discovery/sources/linkedin"
)

// Note: Source.Search calls the real https://www.linkedin.com/robots.txt
// and guest endpoint by hard-coded URL, so it cannot be redirected to a
// test server without a constructor parameter. These tests exercise the
// exported helpers directly (ParseSearchPage above) and the package's
// pure logic; Source.Search itself is integration-level and is not
// exercised against the live network in this test suite — see the design
// spec's "Known Limitation" section. This test asserts the constructor
// and interface satisfaction instead.
func TestSourceImplementsDiscoverySource(t *testing.T) {
	var _ discovery.Source = linkedin.New()
	if linkedin.New().Name() != "linkedin" {
		t.Fatalf("expected Name() to be \"linkedin\", got %q", linkedin.New().Name())
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/sources/linkedin/... -v`
Expected: PASS (both tests). This confirms `*linkedin.Source` satisfies `discovery.Source` at compile time (the `var _ discovery.Source = ...` line fails to compile otherwise).

- [ ] **Step 9: Commit**

```bash
git add internal/discovery/sources/linkedin/
git commit -m "$(cat <<'EOF'
feat(discovery): add LinkedIn guest-endpoint source

Unauthenticated scraping of LinkedIn's public job-search guest endpoint:
robots.txt-gated, paginated, rate-limited (1.5-3.0s between pages), one
retry on transient failure. Parsing validated against a fixture matching
the documented guest-endpoint markup.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 3: Search orchestration + discoveryregistry

**Files:**
- Modify: `internal/discovery/discovery.go`
- Create: `internal/discovery/discovery_test.go`
- Create: `internal/discoveryregistry/registry.go`
- Create: `internal/discoveryregistry/registry_test.go`

**Interfaces:**
- Consumes: `discovery.{SearchQuery,Result,Source,IsDuplicate}` (Task 1), `linkedin.New` (Task 2), `applications.{Store,Application,JobDetails,KindJob}` (Phase 1 + Task 0).
- Produces: `discovery.Search(ctx, source Source, store *applications.Store, profileID string, query SearchQuery) (created []applications.Application, skipped, failed int, searchErr error)`; `discoveryregistry.Get(name string) (discovery.Source, bool)`, `discoveryregistry.Names() []string`. Consumed by Task 4 (node), Task 5 (CLI).

- [ ] **Step 1: Write the failing orchestration tests**

```go
// internal/discovery/discovery_test.go
package discovery_test

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
)

// fakeSource is a discovery.Source test double — no network calls.
type fakeSource struct {
	results []discovery.Result
	err     error
}

func (f *fakeSource) Name() string { return "fake" }
func (f *fakeSource) Search(ctx context.Context, query discovery.SearchQuery) ([]discovery.Result, error) {
	return f.results, f.err
}

func TestSearchCreatesNewApplications(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	source := &fakeSource{results: []discovery.Result{
		{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"},
		{Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/2"},
	}}

	created, skipped, failed, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}
	if skipped != 0 || failed != 0 {
		t.Fatalf("expected 0 skipped/failed, got skipped=%d failed=%d", skipped, failed)
	}
	for _, app := range created {
		if app.Job.Source != "fake" {
			t.Fatalf("expected Source to be set to the source's Name(), got %q", app.Job.Source)
		}
	}
}

func TestSearchSkipsDuplicates(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	existing := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"},
	}
	if err := store.Create(ctx, existing); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	source := &fakeSource{results: []discovery.Result{
		{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"}, // exact URL dup
		{Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/2"}, // new
	}}

	created, skipped, failed, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(created))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", skipped)
	}
	if failed != 0 {
		t.Fatalf("expected 0 failed, got %d", failed)
	}
}

func TestSearchPropagatesSourceErrorWithPartialResults(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	sourceErr := context.DeadlineExceeded
	source := &fakeSource{
		results: []discovery.Result{{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1"}},
		err:     sourceErr,
	}

	created, _, _, err := discovery.Search(ctx, source, store, "default", discovery.SearchQuery{Keywords: "engineer"})
	if err == nil {
		t.Fatal("expected the source's error to propagate")
	}
	if len(created) != 1 {
		t.Fatalf("expected the partial result to still be created, got %d", len(created))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/... -run TestSearch -v`
Expected: FAIL with "undefined: discovery.Search" (compile error).

- [ ] **Step 3: Write the orchestration function**

Append to `internal/discovery/discovery.go`:

```go
import (
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/applications"
)

// Search runs source.Search, then for each result not already a duplicate
// (per IsDuplicate), creates a pending job application via store.Create.
// A source error is returned alongside whatever was already collected and
// processed — a caller sees both the partial success and that it's
// incomplete, never a silent partial success or a total failure that
// discards real results. A per-result Create failure is counted in failed
// and logged, and does not abort the rest of the batch.
func Search(ctx context.Context, source Source, store *applications.Store, profileID string, query SearchQuery) (created []applications.Application, skipped, failed int, searchErr error) {
	results, err := source.Search(ctx, query)
	searchErr = err

	for _, r := range results {
		dup, dErr := IsDuplicate(ctx, store, profileID, r)
		if dErr != nil {
			return created, skipped, failed, fmt.Errorf("discovery.Search: checking duplicate: %w", dErr)
		}
		if dup {
			skipped++
			continue
		}

		isRemote := r.IsRemote
		app := &applications.Application{
			ProfileID: profileID,
			Kind:      applications.KindJob,
			Job: &applications.JobDetails{
				Title:       r.Title,
				Company:     r.Company,
				URL:         r.URL,
				Location:    r.Location,
				Description: r.Description,
				JobType:     r.JobType,
				IsRemote:    &isRemote,
				Source:      source.Name(),
				PostedAt:    r.PostedAt,
			},
		}
		if createErr := store.Create(ctx, app); createErr != nil {
			fmt.Fprintf(os.Stderr, "discovery.Search: creating application for %q: %v\n", r.URL, createErr)
			failed++
			continue
		}
		created = append(created, *app)
	}
	return created, skipped, failed, searchErr
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discovery/... -v`
Expected: PASS (all dedup tests from Task 1 plus 3 new `Search` tests).

- [ ] **Step 5: Write the failing registry test**

```go
// internal/discoveryregistry/registry_test.go
package discoveryregistry_test

import (
	"testing"

	"github.com/monoes/mono-agent/internal/discoveryregistry"
)

func TestGetLinkedIn(t *testing.T) {
	src, ok := discoveryregistry.Get("linkedin")
	if !ok {
		t.Fatal("expected \"linkedin\" to be registered")
	}
	if src.Name() != "linkedin" {
		t.Fatalf("expected Name() linkedin, got %q", src.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	_, ok := discoveryregistry.Get("does-not-exist")
	if ok {
		t.Fatal("expected unknown source name to return ok=false")
	}
}

func TestNamesIncludesLinkedIn(t *testing.T) {
	names := discoveryregistry.Names()
	found := false
	for _, n := range names {
		if n == "linkedin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Names() to include \"linkedin\", got %v", names)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discoveryregistry/... -v`
Expected: FAIL — package `internal/discoveryregistry` does not exist yet.

- [ ] **Step 7: Write the registry**

```go
// internal/discoveryregistry/registry.go

// Package discoveryregistry is the single place that knows about every
// concrete discovery.Source implementation, mirroring internal/noderegistry's
// relationship to internal/workflow — kept separate from internal/discovery
// itself so internal/discovery/sources/* can import internal/discovery
// (for its types) without creating an import cycle back through a registry
// living inside internal/discovery.
package discoveryregistry

import (
	"sort"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discovery/sources/linkedin"
)

var sources = map[string]discovery.Source{
	"linkedin": linkedin.New(),
}

// Get returns the registered Source for name, or ok=false if unknown.
func Get(name string) (discovery.Source, bool) {
	s, ok := sources[name]
	return s, ok
}

// Names returns every registered source name, sorted.
func Names() []string {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/discoveryregistry/... -v`
Expected: PASS (3 tests).

- [ ] **Step 9: Commit**

```bash
git add internal/discovery/discovery.go internal/discovery/discovery_test.go internal/discoveryregistry/
git commit -m "$(cat <<'EOF'
feat(discovery): add Search orchestration and discoveryregistry

Search ties a Source, dedup, and applications.Store.Create together;
discoveryregistry is a separate package (avoiding an import cycle) that
knows about every concrete Source, starting with linkedin.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 4: Workflow node `discovery.search_jobs`

**Files:**
- Create: `internal/nodes/discovery/search_jobs.go`
- Create: `internal/nodes/discovery/search_jobs_schema.go`
- Create: `internal/nodes/discovery/search_jobs_test.go`
- Modify: `internal/tools/schemagen/manifest.go`
- Modify: `internal/noderegistry/registry.go`
- Create (generated, not hand-edited): `internal/workflow/schemas/discovery.search_jobs.json`

**Interfaces:**
- Consumes: `discovery.{Search,SearchQuery}` (Task 3), `discoveryregistry.Get` (Task 3), `applications.NewStore` (Phase 1), `workflow.{NodeExecutor,NodeInput,NodeOutput,Item,NewItem,NodeTypeRegistry}` (existing).
- Produces: node type `discovery.search_jobs` registered in the global registry; `discoverynodes.RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB)`. No other task in this plan depends on it (Phase 5's chat-matching will, in a future phase).

- [ ] **Step 1: Write the failing test**

```go
// internal/nodes/discovery/search_jobs_test.go
package discoverynodes_test

import (
	"context"
	"path/filepath"
	"testing"

	discoverynodes "github.com/monoes/mono-agent/internal/nodes/discovery"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "discovery-node-test.db")
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

func TestSearchJobsNodeRejectsUnknownSource(t *testing.T) {
	db := newTestDB(t)
	discoverynodes.SetGlobalStore(db.DB)

	node := &discoverynodes.SearchJobsNode{}
	if node.Type() != "discovery.search_jobs" {
		t.Fatalf("expected type discovery.search_jobs, got %q", node.Type())
	}
	config := map[string]interface{}{"keywords": "engineer", "source": "does-not-exist"}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
}

func TestSearchJobsNodeRequiresKeywords(t *testing.T) {
	db := newTestDB(t)
	discoverynodes.SetGlobalStore(db.DB)

	node := &discoverynodes.SearchJobsNode{}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing keywords, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/discovery/... -v`
Expected: FAIL with "undefined: discoverynodes.SetGlobalStore" (compile error) — package doesn't exist yet.

- [ ] **Step 3: Write the node**

```go
// internal/nodes/discovery/search_jobs.go

// Package discoverynodes exposes internal/discovery as a workflow node
// type: discovery.search_jobs.
package discoverynodes

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discoveryregistry"
	"github.com/monoes/mono-agent/internal/workflow"
)

// globalStore is the process-wide applications.Store used by this node.
var globalStore *applications.Store

// SetGlobalStore wires the shared SQLite connection into this package's
// node(s).
func SetGlobalStore(db *sql.DB) {
	globalStore = applications.NewStore(db)
}

// RegisterAll registers discovery.search_jobs into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalStore(db)
	r.Register("discovery.search_jobs", func() workflow.NodeExecutor { return &SearchJobsNode{} })
}

func configString(config map[string]interface{}, key, def string) string {
	if v, ok := config[key].(string); ok && v != "" {
		return v
	}
	return def
}

// SearchJobsNode searches one discovery.Source and imports non-duplicate
// results as new pending job applications.
// Type: "discovery.search_jobs"
type SearchJobsNode struct{}

func (n *SearchJobsNode) Type() string { return "discovery.search_jobs" }

func (n *SearchJobsNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalStore == nil {
		return nil, fmt.Errorf("discovery.search_jobs: store not available (call SetGlobalStore at startup)")
	}
	keywords := configString(config, "keywords", "")
	if keywords == "" {
		return nil, fmt.Errorf("discovery.search_jobs: config \"keywords\" is required")
	}
	sourceName := configString(config, "source", "linkedin")
	source, ok := discoveryregistry.Get(sourceName)
	if !ok {
		return nil, fmt.Errorf("discovery.search_jobs: unknown source %q (available: %v)", sourceName, discoveryregistry.Names())
	}
	profileID := configString(config, "profile_id", "default")
	location := configString(config, "location", "")
	limit := 25
	if v, ok := config["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	created, _, _, err := discovery.Search(ctx, source, globalStore, profileID, discovery.SearchQuery{
		Keywords: keywords, Location: location, Limit: limit,
	})
	if err != nil && len(created) == 0 {
		return nil, fmt.Errorf("discovery.search_jobs: %w", err)
	}

	items := make([]workflow.Item, 0, len(created))
	for _, app := range created {
		items = append(items, workflow.NewItem(map[string]interface{}{
			"id": app.ID, "kind": string(app.Kind), "status": string(app.Status),
			"title": app.Job.Title, "company": app.Job.Company, "url": app.Job.URL,
		}))
	}
	return []workflow.NodeOutput{{Handle: "main", Items: items}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/discovery/... -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Write the schema struct**

```go
// internal/nodes/discovery/search_jobs_schema.go
package discoverynodes

// SearchJobsNodeSchema documents the config keys SearchJobsNode.Execute
// reads out of its map[string]interface{} config.
type SearchJobsNodeSchema struct {
	Keywords  string `json:"keywords" schema:"label=Keywords,type=text,required,help=Job search keywords, e.g. 'backend engineer'."`
	Location  string `json:"location" schema:"label=Location,type=text,help=Job location filter."`
	Source    string `json:"source" schema:"label=Source,type=select,default=linkedin,options=linkedin,help=Which job board to search."`
	Limit     int    `json:"limit" schema:"label=Limit,type=number,default=25,help=Maximum results to import (capped at 100)."`
	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the imported applications."`
}
```

- [ ] **Step 6: Register in the schemagen manifest**

In `internal/tools/schemagen/manifest.go`, add a new section after the `applications.*` entries:

```go
	// --- discovery.* ---
	{NodeType: "discovery.search_jobs", GoFile: "internal/nodes/discovery/search_jobs_schema.go", StructName: "SearchJobsNodeSchema"},
```

- [ ] **Step 7: Generate the schema and register the node package**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go run ./cmd/schemagen`
Expected: writes `internal/workflow/schemas/discovery.search_jobs.json` among its full regeneration pass.

In `internal/noderegistry/registry.go`, add the import (alphabetically among the `internal/nodes/*` aliased imports):

```go
	discoverynodes "github.com/monoes/mono-agent/internal/nodes/discovery"
```

And add this line inside `Build`, next to `applicationsnodes.RegisterAll(registry, db)`:

```go
	discoverynodes.RegisterAll(registry, db)
```

- [ ] **Step 8: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/nodes/discovery/ internal/tools/schemagen/manifest.go internal/noderegistry/registry.go internal/workflow/schemas/discovery.search_jobs.json
git commit -m "$(cat <<'EOF'
feat(discovery): add discovery.search_jobs workflow node

Wraps discovery.Search + discoveryregistry for workflow use; registered
in noderegistry.Build alongside the Phase 1 applications nodes.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 5: CLI command `monoagentcli application discover`

**Files:**
- Create: `cmd/monoagentcli/application_discover.go`
- Create: `cmd/monoagentcli/application_discover_test.go`
- Modify: `cmd/monoagentcli/application.go`

**Interfaces:**
- Consumes: `discovery.{Search,SearchQuery}` (Task 3), `discoveryregistry.{Get,Names}` (Task 3), `applications.NewStore` (Phase 1); `initDB`, `errInvalidInput` (existing CLI conventions).
- Produces: `newApplicationDiscoverCmd(cfg *globalConfig) *cobra.Command`, added to `newApplicationCmd`'s subcommand list. No other task depends on it.

- [ ] **Step 1: Write the failing CLI test**

```go
// cmd/monoagentcli/application_discover_test.go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func runApplicationDiscoverCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(append([]string{"discover"}, args...))
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestApplicationDiscoverRejectsUnknownSource(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	_, err := runApplicationDiscoverCmd(t, dbPath, "--keywords", "engineer", "--source", "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
}

func TestApplicationDiscoverRequiresKeywords(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	_, err := runApplicationDiscoverCmd(t, dbPath)
	if err == nil {
		t.Fatal("expected error for missing --keywords, got nil")
	}
	if !strings.Contains(err.Error(), "keywords") {
		t.Fatalf("expected error to mention keywords, got: %v", err)
	}
}
```

(This test suite intentionally does not exercise a successful discovery run — that would require a real or injected network call, which the LinkedIn source doesn't support redirecting in tests per Task 2 Step 7's note. It covers the CLI's own validation, which is what this task adds beyond Task 4's already-tested node logic.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestApplicationDiscover -v`
Expected: FAIL with "cmd.SetArgs" / unknown command "discover" — `newApplicationCmd` doesn't have a `discover` subcommand yet (the command executes but cobra reports an unknown subcommand error, which the first test's `err == nil` check catches as a pass by accident; the second test explicitly checks the error mentions "keywords", which will fail since the actual error will be about the unknown "discover" command instead — confirming the subcommand needs to be added).

- [ ] **Step 3: Write the discover command**

```go
// cmd/monoagentcli/application_discover.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discoveryregistry"

	"github.com/spf13/cobra"
)

// newApplicationDiscoverCmd returns the `application discover` command:
// searches one discovery.Source and imports non-duplicate results as new
// pending job applications.
func newApplicationDiscoverCmd(cfg *globalConfig) *cobra.Command {
	var keywords, location, source string
	var limit int
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Search a job board and import new postings as pending applications",
		Example: `  monoagentcli application discover --keywords "backend engineer" --location Berlin
  monoagentcli application discover --keywords "platform engineer" --limit 50`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if keywords == "" {
				return errInvalidInput("--keywords is required")
			}
			src, ok := discoveryregistry.Get(source)
			if !ok {
				return errInvalidInput("unknown --source %q (available: %v)", source, discoveryregistry.Names())
			}

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()
			store := applications.NewStore(db.DB)

			created, skipped, failed, searchErr := discovery.Search(cmd.Context(), src, store, cfg.ProfileID, discovery.SearchQuery{
				Keywords: keywords, Location: location, Limit: limit,
			})
			if searchErr != nil && len(created) == 0 {
				return fmt.Errorf("discovering jobs: %w", searchErr)
			}

			if cfg.JSONOutput {
				type createdApp struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					Company string `json:"company"`
					URL     string `json:"url"`
				}
				out := make([]createdApp, 0, len(created))
				for _, app := range created {
					out = append(out, createdApp{ID: app.ID, Title: app.Job.Title, Company: app.Job.Company, URL: app.Job.URL})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]interface{}{
					"imported": len(created), "skipped": skipped, "failed": failed, "applications": out,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported %d new job(s), skipped %d duplicate(s).\n", len(created), skipped)
			if failed > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%d result(s) failed to save.\n", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&keywords, "keywords", "", "Job search keywords (required)")
	cmd.Flags().StringVar(&location, "location", "", "Job location filter")
	cmd.Flags().StringVar(&source, "source", "linkedin", "Job board to search")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum results to import (capped at 100)")
	return cmd
}
```

- [ ] **Step 4: Register the subcommand**

In `cmd/monoagentcli/application.go`, add `newApplicationDiscoverCmd(cfg),` to `newApplicationCmd`'s `cmd.AddCommand(...)` list:

```go
	cmd.AddCommand(
		newApplicationAddCmd(cfg),
		newApplicationListCmd(cfg),
		newApplicationGetCmd(cfg),
		newApplicationStatusCmd(cfg),
		newApplicationTagCmd(cfg),
		newApplicationDiscoverCmd(cfg),
	)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestApplicationDiscover -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing.

- [ ] **Step 7: Commit**

```bash
git add cmd/monoagentcli/application_discover.go cmd/monoagentcli/application_discover_test.go cmd/monoagentcli/application.go
git commit -m "$(cat <<'EOF'
feat(cli): add `monoagentcli application discover` command

Searches a discovery.Source (linkedin by default) and imports new postings
as pending job applications, reporting imported/skipped/failed counts.
Completes Phase 2 (Discovery).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

## Self-Review

**1. Spec coverage:**
- Fix the Title gap → Task 0. ✅
- Scraping technique (unauthenticated HTTP via `crawl.FetchPage`) → Task 2. ✅
- Pluggable `Source` architecture mirroring `noderegistry` → Task 1 (interface) + Task 3 (`discoveryregistry`, corrected from the spec's single-package sketch to a separate package to avoid an import cycle the spec's own code sketch would have created — see Task 3's package doc comment explaining why). ✅
- Dedup (URL + normalized title/company) → Task 1. ✅
- Tender discovery out of scope → confirmed, no task touches tenders beyond Task 0's parallel `Title` fix (which the spec explicitly calls for, for consistency). ✅
- CLI surface → Task 5. ✅
- Workflow node surface → Task 4. ✅
- Rate limiting/robots.txt → Task 2. ✅

**2. Placeholder scan:** No "TBD"/"TODO"/"implement later"; every step has complete code; Task 8 of Task 0 lists exact literals with exact insertions rather than "similar edits elsewhere."

**3. Type consistency:** `discovery.SearchQuery`/`discovery.Result`/`discovery.Source` (Task 1) are used with identical field/method names in Task 2 (`linkedin.Source` implements them), Task 3 (`Search`'s signature and body), Task 4 (node config mapping), and Task 5 (CLI mapping) — no renaming drift. `applications.JobDetails.Title` (Task 0) is populated identically in Task 3's `Search` and read identically in Task 4/5's output construction (`app.Job.Title`).

One deviation from the spec worth flagging explicitly (not a gap, a correction): the spec's own code sketch put the registry map directly in `internal/discovery/registry.go`, which — as written, importing the `linkedin` subpackage — would create an import cycle, since `internal/discovery/sources/linkedin` must import `internal/discovery` for the `Source`/`Result`/`SearchQuery` types. This plan fixes that by moving the registry into a sibling package, `internal/discoveryregistry`, exactly mirroring how `internal/noderegistry` already relates to `internal/nodes`/`internal/workflow` in this codebase. No spec requirement is lost — `Get`/`Names` have the same signatures the spec described, just in a different (and cycle-free) package.
