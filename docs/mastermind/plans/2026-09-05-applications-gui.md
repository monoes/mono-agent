# Applications GUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (`mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per the prior six phase plans' precedent.)

**Goal:** A Wails GUI "Applications" page: list/filter/detail view, manual add, job discovery, fit evaluation, and a guided "Process Pending" flow (evaluate → apply → send) with an Auto/Confirm mode toggle — a thin presentation layer over the CLI built in Phases 1-6.

**Architecture:** `wails-app/app_applications.go` (new file, sibling to `app_vault.go`) shells out to `monoagentcli --json`, exactly like `app_vault.go`'s established convention — no internal Go packages imported. `wails-app/frontend/src/pages/Applications.jsx` (list/detail/add/discover) + `ApplicationsProcessPendingFlow.jsx` (the guided flow, its own component per the design spec) consume the auto-generated `wailsjs/go/main/App` bindings directly.

**Tech Stack:** Go (`main` package, Wails v2.15.0), React 19 + `lucide-react` (frontend, Vite + Vitest). No new dependencies on either side.

## Global Constraints

- `wails-app/app_applications.go` must never import `internal/applications`, `internal/discovery`, `internal/matching`, `internal/documents`, or `internal/apply` — every operation shells out to `monoagentcli` via `os/exec`, matching `app_vault.go`'s explicit "CLI is the single implementation surface" comment.
- The GUI's subprocess calls to `monoagentcli` are non-interactive — `ApplyToApplication` always passes `--mode auto`; confirm-vs-auto UX is a frontend-only concern (an upfront `confirm()` dialog + per-item branching in `ApplicationsProcessPendingFlow.jsx`), never something the CLI subprocess call itself varies.
- **Verified JSON shapes per CLI command** (some PascalCase raw-struct encoding since the underlying Go types have no `json` tags, some lowercase explicit maps — checked against the actual committed source, not assumed uniform):
  - `application list --json` → `[]Application` (PascalCase: `ID,ProfileID,Kind,Status,Tags,CreatedAt,UpdatedAt,Job,Tender`; nested `Job`/`Tender` also PascalCase).
  - `application get --json` → `{"application": Application(PascalCase), "status_log": []StatusLogEntry(PascalCase: ID,FromStatus,ToStatus,Actor,Note,CreatedAt)}`.
  - `application add/status/tag/send --json` → lowercase explicit maps (`id`, `kind`, `status`, `tag`, `action`).
  - `application discover --json` → lowercase: `{"imported","skipped","failed","applications":[{"id","title","company","url"}]}`.
  - `application evaluate --json` → `FitVerdict` directly, which DOES have `json` tags (snake_case: `eligibility_pass`, `language_pass`, `location_pass`, `technical_score`, `experience_score`, `behavioral_score`, `career_score`, `overall_score`, `verdict`, `rationale`).
  - `application evaluate-pending --json` → lowercase: `{"evaluated","verdicts"}`.
  - `application apply --json` → lowercase: `{"cv_html_document_id","cv_pdf_document_id","cover_letter_html_document_id","cover_letter_pdf_document_id"}`.
  - `profile documents list --json` → `[]vault.DocumentEntry` (PascalCase: `ID,Path,Filename,SizeBytes,Source,ApplicationID,CreatedAt`).
- `wails-app/frontend/src/components/Sidebar.jsx` reads `t(\`sidebar.nav.${item.labelKey}\`)` — locale keys nest under `"sidebar"."nav"`, not a flat `sidebar.<id>`.
- Regenerating `wailsjs` bindings after adding new `*App` methods requires the `wails` CLI (not installed by default in a fresh environment) at the exact version this project pins: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0` (checked against `wails-app/go.mod`). It also requires `wails-app/frontend/dist/` to exist first (`//go:embed`-ed by `main.go`) — run `cd wails-app/frontend && npm install && npm run build` if missing, then `cd wails-app && wails generate module`.
- Frontend commands run from `wails-app/frontend/`: `npm install` (once, if `node_modules` is missing — verify with `ls node_modules` before assuming it's already there), `npm test` (Vitest), `npm run build` (Vite, produces `dist/`, also a build-correctness check).
- `go build ./...` from the repo root must still pass after adding `wails-app/app_applications.go` (part of the `main` package).
- Per this project's existing precedent (`app_vault.go` has no `_test.go` file), the thin `app_applications.go` wrapper layer is not unit-tested — its correctness is covered by (a) `go build`/`go vet` succeeding and (b) the frontend's own tests against a mocked `WailsApp` module. This matches, not deviates from, existing practice.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `wails-app/app_applications.go` | All `*App` methods for the Applications page — shells out to `monoagentcli`. |
| `wails-app/frontend/src/pages/Applications.jsx` | List/filter/detail/add/discover UI. |
| `wails-app/frontend/src/pages/Applications.test.js` | Unit tests for the extracted pure filter function. |
| `wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.jsx` | The guided evaluate→apply→send flow with Auto/Confirm modes. |
| `wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.test.js` | Unit tests for the flow's per-item state machine, mocked `WailsApp`. |
| `wails-app/frontend/src/components/Sidebar.jsx` | Modified: new nav item. |
| `wails-app/frontend/src/App.jsx` | Modified: new `persistentPages` entry. |
| `wails-app/frontend/src/locales/en.json`, `es.json` | Modified: new `sidebar.nav.applications` key. |
| `wails-app/frontend/src/wailsjs/go/main/App.js`, `.d.ts` | Regenerated (not hand-edited) via `wails generate module`. |

---

### Task 0: Backend — `wails-app/app_applications.go`

**Files:**
- Create: `wails-app/app_applications.go`

**Interfaces:**
- Consumes: `findMonoAgentCLI()` (existing, `wails-app/app.go:755`), `a.ctx`/`a.getActiveProfileID()` (existing `*App` fields/methods), `github.com/wailsapp/wails/v2/pkg/runtime` (existing dependency, already used in `app_vault.go`).
- Produces: `ApplicationSummary`, `ApplicationDetail`, `StatusLogEntry`, `DiscoveredApplication`, `DiscoverResult`, `FitVerdictInfo`, `EvaluateBatchResult`, `ApplyResult` (all with explicit lowercase `json` tags — this is the GUI's own stable contract to the frontend); `GetApplications`, `GetApplication`, `AddApplication`, `SetApplicationStatus`, `TagApplication`, `DiscoverJobs`, `EvaluateApplication`, `EvaluatePendingApplications`, `ApplyToApplication`, `SendApplication`, `HasGeneratedDocuments`, `OpenJSONFilePicker`, `ReadJSONFile`. Consumed by Task 1 and Task 2 (via the regenerated `wailsjs` bindings, Task 3).

- [ ] **Step 1: Write the file**

```go
// wails-app/app_applications.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// runMonoCLI runs monoagentcli with --profile <active> --json prefixed to
// args, capturing stdout into result (nil to ignore). Mirrors
// app_vault.go's runVaultCLI, generalized to any subcommand (this file
// spans `application`, `profile`) rather than one hardcoded subcommand.
// This file intentionally does not import internal/applications,
// internal/discovery, internal/matching, internal/documents, or
// internal/apply -- the CLI's --json output is the contract.
func (a *App) runMonoCLI(stdin string, result interface{}, args ...string) error {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return err
	}
	fullArgs := append([]string{"--profile", a.getActiveProfileID(), "--json"}, args...)
	cmd := exec.CommandContext(a.ctx, cliBin, fullArgs...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(out, result)
}

// ── Raw CLI shapes (unmarshal targets only, PascalCase, no tags -- these
// match internal/applications.Application/JobDetails/TenderDetails/
// StatusLogEntry exactly, which have no json tags of their own) ─────────

type rawJobDetails struct {
	Title, Company, URL, Location, Description string
	CompensationMin, CompensationMax           *float64
	Currency, JobType                          string
	IsRemote                                   *bool
	Source, PostedAt                           string
}

type rawTenderDetails struct {
	Title, IssuingOrg, URL, Description, SubmissionDeadline string
	EstimatedValue                                          *float64
	Currency, RequiredCertifications, BidDocumentsRequired  string
	Source, PublishedAt                                     string
}

type rawApplication struct {
	ID, ProfileID, Kind, Status string
	Tags                        []string
	CreatedAt, UpdatedAt        string
	Job                         *rawJobDetails
	Tender                      *rawTenderDetails
}

type rawStatusLogEntry struct {
	ID, FromStatus, ToStatus, Actor, Note, CreatedAt string
}

// ── Clean DTOs returned to the frontend (explicit json tags -- the
// GUI's own stable, consistently-lowercase contract) ────────────────────

// ApplicationSummary is one row in the Applications list.
type ApplicationSummary struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Title     string   `json:"title"`
	Company   string   `json:"company"` // company (job) or issuing org (tender)
	URL       string   `json:"url"`
	UpdatedAt string   `json:"updated_at"`
	Tags      []string `json:"tags"`
}

func summarize(raw rawApplication) ApplicationSummary {
	s := ApplicationSummary{ID: raw.ID, Kind: raw.Kind, Status: raw.Status, UpdatedAt: raw.UpdatedAt, Tags: raw.Tags}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	if raw.Job != nil {
		s.Title, s.Company, s.URL = raw.Job.Title, raw.Job.Company, raw.Job.URL
	}
	if raw.Tender != nil {
		s.Title, s.Company, s.URL = raw.Tender.Title, raw.Tender.IssuingOrg, raw.Tender.URL
	}
	return s
}

// GetApplications lists applications, optionally filtered. An empty
// string argument means "no filter on that dimension" (mirrors
// `application list`'s own flag defaults).
func (a *App) GetApplications(kind, status, tag string) ([]ApplicationSummary, error) {
	args := []string{"application", "list"}
	if kind != "" {
		args = append(args, "--kind", kind)
	}
	if status != "" {
		args = append(args, "--status", status)
	}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	var raws []rawApplication
	if err := a.runMonoCLI("", &raws, args...); err != nil {
		return nil, err
	}
	out := make([]ApplicationSummary, 0, len(raws))
	for _, r := range raws {
		out = append(out, summarize(r))
	}
	return out, nil
}

// StatusLogEntry is one row of an application's transition history.
type StatusLogEntry struct {
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	Actor      string `json:"actor"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at"`
}

// ApplicationDetail is one application's full detail. Job/Tender fields
// are flattened onto the same struct (Description/Location/JobType for
// jobs; Description/IssuingOrg/SubmissionDeadline for tenders) -- the
// frontend branches on Kind to decide which subset to show, matching the
// design spec's stated approach.
type ApplicationDetail struct {
	ApplicationSummary
	Description        string           `json:"description"`
	Location            string           `json:"location"`
	JobType             string           `json:"job_type"`
	IssuingOrg          string           `json:"issuing_org"`
	SubmissionDeadline  string           `json:"submission_deadline"`
	StatusLog           []StatusLogEntry `json:"status_log"`
}

// GetApplication returns one application's full detail and status history.
func (a *App) GetApplication(id string) (*ApplicationDetail, error) {
	var result struct {
		Application rawApplication      `json:"application"`
		StatusLog   []rawStatusLogEntry `json:"status_log"`
	}
	if err := a.runMonoCLI("", &result, "application", "get", id); err != nil {
		return nil, err
	}
	detail := &ApplicationDetail{ApplicationSummary: summarize(result.Application)}
	if result.Application.Job != nil {
		detail.Description = result.Application.Job.Description
		detail.Location = result.Application.Job.Location
		detail.JobType = result.Application.Job.JobType
	}
	if result.Application.Tender != nil {
		detail.Description = result.Application.Tender.Description
		detail.IssuingOrg = result.Application.Tender.IssuingOrg
		detail.SubmissionDeadline = result.Application.Tender.SubmissionDeadline
	}
	detail.StatusLog = make([]StatusLogEntry, 0, len(result.StatusLog))
	for _, e := range result.StatusLog {
		detail.StatusLog = append(detail.StatusLog, StatusLogEntry{
			FromStatus: e.FromStatus, ToStatus: e.ToStatus, Actor: e.Actor, Note: e.Note, CreatedAt: e.CreatedAt,
		})
	}
	return detail, nil
}

// AddApplication creates a new job or tender application. kind must be
// "job" or "tender". For "job", company is required in addition to url;
// for "tender", issuingOrg and submissionDeadline are required in
// addition to url -- the CLI's own validation surfaces a clear error if
// a required field is missing, this method does not duplicate that
// validation.
func (a *App) AddApplication(kind, title, company, url, issuingOrg, submissionDeadline string) (string, error) {
	args := []string{"application", "add", "--kind", kind, "--title", title, "--url", url}
	if kind == "job" {
		args = append(args, "--company", company)
	}
	if kind == "tender" {
		args = append(args, "--issuing-org", issuingOrg, "--submission-deadline", submissionDeadline)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return "", err
	}
	return result.ID, nil
}

// SetApplicationStatus transitions an application's status.
func (a *App) SetApplicationStatus(id, status, note string) error {
	args := []string{"application", "status", id, "set", status}
	if note != "" {
		args = append(args, "--note", note)
	}
	return a.runMonoCLI("", nil, args...)
}

// TagApplication adds or removes a tag. action must be "add" or "remove".
func (a *App) TagApplication(id, tag, action string) error {
	return a.runMonoCLI("", nil, "application", "tag", id, action, tag)
}

// DiscoveredApplication is one newly-imported job from DiscoverJobs.
type DiscoveredApplication struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Company string `json:"company"`
	URL     string `json:"url"`
}

// DiscoverResult is DiscoverJobs' return value.
type DiscoverResult struct {
	Imported     int                      `json:"imported"`
	Skipped      int                      `json:"skipped"`
	Failed       int                      `json:"failed"`
	Applications []DiscoveredApplication  `json:"applications"`
}

// DiscoverJobs searches a job board and imports new postings as pending
// applications. source defaults to "linkedin" if empty; limit defaults to
// 25 (the CLI's own default) if zero or negative.
func (a *App) DiscoverJobs(keywords, location, source string, limit int) (*DiscoverResult, error) {
	args := []string{"application", "discover", "--keywords", keywords}
	if location != "" {
		args = append(args, "--location", location)
	}
	if source != "" {
		args = append(args, "--source", source)
	}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	var result DiscoverResult
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return nil, err
	}
	if result.Applications == nil {
		result.Applications = []DiscoveredApplication{}
	}
	return &result, nil
}

// FitVerdictInfo mirrors internal/matching.FitVerdict's own json tags
// exactly (that struct already has them, unlike Application/JobDetails).
type FitVerdictInfo struct {
	EligibilityPass bool    `json:"eligibility_pass"`
	LanguagePass    bool    `json:"language_pass"`
	LocationPass    bool    `json:"location_pass"`
	TechnicalScore  float64 `json:"technical_score"`
	ExperienceScore float64 `json:"experience_score"`
	BehavioralScore float64 `json:"behavioral_score"`
	CareerScore     float64 `json:"career_score"`
	OverallScore    float64 `json:"overall_score"`
	Verdict         string  `json:"verdict"`
	Rationale       string  `json:"rationale"`
}

// EvaluateApplication scores one job application's fit via a local AI
// agent. runtime defaults to "claude" if empty.
func (a *App) EvaluateApplication(id, runtime string) (*FitVerdictInfo, error) {
	if runtime == "" {
		runtime = "claude"
	}
	var result FitVerdictInfo
	if err := a.runMonoCLI("", &result, "application", "evaluate", id, "--runtime", runtime); err != nil {
		return nil, err
	}
	return &result, nil
}

// EvaluateBatchResult is EvaluatePendingApplications' return value.
type EvaluateBatchResult struct {
	Evaluated int            `json:"evaluated"`
	Verdicts  map[string]int `json:"verdicts"`
}

// EvaluatePendingApplications evaluates every not-yet-evaluated pending
// job application. limit of 0 means no limit. runtime defaults to
// "claude" if empty.
func (a *App) EvaluatePendingApplications(evalRuntime string, limit int) (*EvaluateBatchResult, error) {
	if evalRuntime == "" {
		evalRuntime = "claude"
	}
	args := []string{"application", "evaluate-pending", "--runtime", evalRuntime}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	var result EvaluateBatchResult
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return nil, err
	}
	if result.Verdicts == nil {
		result.Verdicts = map[string]int{}
	}
	return &result, nil
}

// ApplyResult is ApplyToApplication's return value.
type ApplyResult struct {
	CVHTMLDocumentID          string `json:"cv_html_document_id"`
	CVPDFDocumentID           string `json:"cv_pdf_document_id"`
	CoverLetterHTMLDocumentID string `json:"cover_letter_html_document_id"`
	CoverLetterPDFDocumentID  string `json:"cover_letter_pdf_document_id"`
}

// ApplyToApplication prepares documents (reusing existing ones if already
// generated for this application) and opens the job URL in a real,
// visible browser window. Always invokes `application apply --mode
// auto` -- this subprocess call has no TTY for the CLI's own confirm-mode
// y/N prompt to read from, so confirm-vs-auto UX is implemented entirely
// in the frontend (an upfront confirm() dialog before this method is
// ever called) -- see the design spec's "non-obvious integration detail".
func (a *App) ApplyToApplication(id string, cvData, coverLetterData map[string]interface{}) (*ApplyResult, error) {
	cvFile, err := writeTempJSON(cvData)
	if err != nil {
		return nil, fmt.Errorf("writing cv data: %w", err)
	}
	defer os.Remove(cvFile)
	letterFile, err := writeTempJSON(coverLetterData)
	if err != nil {
		return nil, fmt.Errorf("writing cover letter data: %w", err)
	}
	defer os.Remove(letterFile)

	var result ApplyResult
	if err := a.runMonoCLI("", &result, "application", "apply", id, "--mode", "auto",
		"--cv-data-file", cvFile, "--cover-letter-data-file", letterFile); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendApplication records that the user submitted this application
// themselves -- the only path in this entire feature that ever marks an
// application "applied".
func (a *App) SendApplication(id, note string) error {
	args := []string{"application", "send", id}
	if note != "" {
		args = append(args, "--note", note)
	}
	return a.runMonoCLI("", nil, args...)
}

// HasGeneratedDocuments reports whether id already has generated (not
// manually-uploaded) documents in the vault -- lets the frontend skip
// re-opening a browser tab for an application a prior "Process Pending"
// run already handled. No dedicated CLI flag exists for this filter (one
// caller doesn't justify adding one yet -- see the design spec's YAGNI
// note); this method filters client-side in Go instead.
func (a *App) HasGeneratedDocuments(id string) (bool, error) {
	var docs []struct {
		ApplicationID string
		Source        string
	}
	if err := a.runMonoCLI("", &docs, "profile", "documents", "list"); err != nil {
		return false, err
	}
	for _, d := range docs {
		if d.ApplicationID == id && d.Source == "generated" {
			return true, nil
		}
	}
	return false, nil
}

// OpenJSONFilePicker opens a native file picker for a JSON file and
// returns the selected path (empty string if cancelled).
func (a *App) OpenJSONFilePicker(title string) string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   title,
		Filters: []runtime.FileFilter{{DisplayName: "JSON files", Pattern: "*.json"}},
	})
	if err != nil {
		return ""
	}
	return path
}

// ReadJSONFile reads and parses a JSON file, returning it as a generic
// map -- used by the frontend to load CV/cover-letter data files without
// needing browser-side filesystem access (a Wails webview cannot read
// local files directly; only the Go side can).
func (a *App) ReadJSONFile(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing %s as JSON: %w", path, err)
	}
	return result, nil
}

// writeTempJSON writes data as a temp .json file and returns its path.
func writeTempJSON(data map[string]interface{}) (string, error) {
	f, err := os.CreateTemp("", "mono-agent-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
```

- [ ] **Step 2: Verify it builds**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./...` (from the repo root)
Expected: succeeds with no output. This confirms the new file compiles as part of the `main` package alongside the existing `app.go`/`app_vault.go` — there is no automated test for this thin-wrapper layer, matching `app_vault.go`'s own precedent (no `_test.go` file exists for it either).

- [ ] **Step 3: Run `go vet`**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go vet ./wails-app/...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add wails-app/app_applications.go
git commit -m "$(cat <<'EOF'
feat(gui): add Applications backend methods

Shells out to monoagentcli --json for every operation, exactly matching
app_vault.go's established convention -- no internal Go packages
imported. Normalizes several CLI commands' inconsistent JSON shapes
(some PascalCase raw-struct encoding, some lowercase explicit maps) into
one consistent lowercase-tagged contract for the frontend.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 1: `Applications.jsx` — list, filter, detail, add, discover

**Files:**
- Create: `wails-app/frontend/src/pages/Applications.jsx`
- Create: `wails-app/frontend/src/pages/Applications.test.js`

**Interfaces:**
- Consumes: `WailsApp.{GetApplications,GetApplication,AddApplication,SetApplicationStatus,TagApplication,DiscoverJobs}` (Task 0, via the `wailsjs` bindings regenerated in Task 3 — write this task's code against the method signatures as designed; the bindings will exist by the time Task 3's verification runs), `confirm` (existing, `components/ConfirmDialog.jsx`), `notify` (existing, `services/api.js`).
- Produces: `filterApplications(applications, { statusTab, search })` (pure, exported, unit-tested), `export default function Applications()`. Consumed by Task 3 (`App.jsx` registration) and, by import, Task 2's flow component (which renders inside a modal launched from this page).

- [ ] **Step 1: Write the failing test**

```js
// wails-app/frontend/src/pages/Applications.test.js
import { describe, it, expect } from 'vitest'
import { filterApplications } from './Applications.jsx'

const app = (overrides) => ({
  id: overrides.id || 'a1',
  kind: 'job',
  status: 'pending',
  title: '',
  company: '',
  url: '',
  updated_at: '2026-01-01 00:00:00',
  tags: [],
  ...overrides,
})

describe('filterApplications', () => {
  const apps = [
    app({ id: '1', title: 'Backend Engineer', company: 'Acme', status: 'pending' }),
    app({ id: '2', title: 'Frontend Engineer', company: 'Beta', status: 'applied' }),
    app({ id: '3', title: 'Data Engineer', company: 'Acme', status: 'pending', tags: ['fit:strong-fit'] }),
  ]

  it('returns everything for statusTab "all" and empty search', () => {
    expect(filterApplications(apps, { statusTab: 'all', search: '' })).toHaveLength(3)
  })

  it('filters by status tab', () => {
    const out = filterApplications(apps, { statusTab: 'pending', search: '' })
    expect(out.map(a => a.id)).toEqual(['1', '3'])
  })

  it('filters by search matching title', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'backend' })
    expect(out.map(a => a.id)).toEqual(['1'])
  })

  it('filters by search matching company', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'acme' })
    expect(out.map(a => a.id).sort()).toEqual(['1', '3'])
  })

  it('filters by search matching a tag', () => {
    const out = filterApplications(apps, { statusTab: 'all', search: 'strong-fit' })
    expect(out.map(a => a.id)).toEqual(['3'])
  })

  it('combines status tab and search', () => {
    const out = filterApplications(apps, { statusTab: 'pending', search: 'acme' })
    expect(out.map(a => a.id).sort()).toEqual(['1', '3'])
  })

  it('does not mutate the input array', () => {
    const copy = [...apps]
    filterApplications(apps, { statusTab: 'pending', search: '' })
    expect(apps).toEqual(copy)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `wails-app/frontend/`): `npm test -- Applications.test.js`
Expected: FAIL — `Applications.jsx` doesn't exist yet.

- [ ] **Step 3: Write the page component**

```jsx
// wails-app/frontend/src/pages/Applications.jsx
import { useState, useEffect, useCallback, useMemo } from 'react'
import { Plus, Search, ExternalLink } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'
import { notify } from '../services/api.js'
import ApplicationsProcessPendingFlow from './ApplicationsProcessPendingFlow.jsx'

const STATUS_TABS = ['pending', 'applied', 'rejected', 'cancelled', 'all']

const STATUS_COLORS = {
  pending: { bg: 'rgba(245,158,11,0.12)', color: '#f59e0b' },
  applied: { bg: 'rgba(16,185,129,0.12)', color: '#10b981' },
  rejected: { bg: 'rgba(239,68,68,0.12)', color: '#ef4444' },
  cancelled: { bg: 'rgba(100,116,139,0.12)', color: '#64748b' },
}

// Pure so it's directly unit-testable (see Applications.test.js) without
// rendering the component -- mirrors Vault.jsx's filterAndSortEntries
// convention.
export function filterApplications(applications, { statusTab = 'all', search = '' } = {}) {
  const q = search.trim().toLowerCase()
  return applications.filter(a => {
    if (statusTab !== 'all' && a.status !== statusTab) return false
    if (!q) return true
    return (
      a.title?.toLowerCase().includes(q) ||
      a.company?.toLowerCase().includes(q) ||
      a.tags?.some(t => t.toLowerCase().includes(q))
    )
  })
}

const inputStyle = {
  background: '#060b11', border: '1px solid #1e3a4f', borderRadius: 5,
  padding: '6px 8px', color: '#e2e8f0', fontFamily: 'var(--font-mono)', fontSize: 11,
}
const headerBtnStyle = {
  background: 'rgba(0,180,216,0.1)', border: '1px solid rgba(0,180,216,0.3)',
  borderRadius: 6, padding: '6px 12px', color: '#00b4d8',
  fontFamily: 'var(--font-mono)', fontSize: 11, cursor: 'pointer',
  display: 'flex', alignItems: 'center', gap: 5,
}

function StatusBadge({ status }) {
  const s = STATUS_COLORS[status] || { bg: '#1a2332', color: '#64748b' }
  return (
    <span style={{ background: s.bg, borderRadius: 3, padding: '1px 6px', fontFamily: 'var(--font-mono)', fontSize: 9, color: s.color }}>
      {status}
    </span>
  )
}

function emptyAddForm() {
  return { kind: 'job', title: '', company: '', url: '', issuingOrg: '', submissionDeadline: '' }
}

export default function Applications() {
  const [applications, setApplications] = useState([])
  const [statusTab, setStatusTab] = useState('pending')
  const [search, setSearch] = useState('')
  const [error, setError] = useState(null)
  const [selectedId, setSelectedId] = useState(null)
  const [detail, setDetail] = useState(null)
  const [showAdd, setShowAdd] = useState(false)
  const [addForm, setAddForm] = useState(emptyAddForm())
  const [showDiscover, setShowDiscover] = useState(false)
  const [discoverForm, setDiscoverForm] = useState({ keywords: '', location: '', limit: 25 })
  const [showProcessPending, setShowProcessPending] = useState(false)

  const load = useCallback(async () => {
    try {
      const list = await WailsApp.GetApplications('', '', '')
      setApplications(list || [])
    } catch (e) {
      setError('Failed to load applications: ' + e)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const visible = useMemo(
    () => filterApplications(applications, { statusTab, search }),
    [applications, statusTab, search]
  )

  const openDetail = async (id) => {
    setSelectedId(id)
    try {
      const d = await WailsApp.GetApplication(id)
      setDetail(d)
    } catch (e) {
      setError('Failed to load detail: ' + e)
    }
  }

  const closeDetail = () => { setSelectedId(null); setDetail(null) }

  const handleAdd = async (e) => {
    e.preventDefault()
    setError(null)
    try {
      await WailsApp.AddApplication(
        addForm.kind, addForm.title, addForm.company, addForm.url,
        addForm.issuingOrg, addForm.submissionDeadline,
      )
      setAddForm(emptyAddForm())
      setShowAdd(false)
      load()
    } catch (e) {
      setError('Add failed: ' + e)
    }
  }

  const handleDiscover = async (e) => {
    e.preventDefault()
    setError(null)
    try {
      const result = await WailsApp.DiscoverJobs(discoverForm.keywords, discoverForm.location, '', Number(discoverForm.limit) || 25)
      notify('discover', `Imported ${result.imported} new job(s), skipped ${result.skipped} duplicate(s).`)
      setShowDiscover(false)
      load()
    } catch (e) {
      setError('Discovery failed: ' + e)
    }
  }

  const handleSetStatus = async (id, status) => {
    if (status === 'cancelled' && !(await confirm('Mark this application as cancelled?', { title: 'Cancel Application', confirmLabel: 'Cancel Application' }))) return
    try {
      await WailsApp.SetApplicationStatus(id, status, '')
      await load()
      if (selectedId === id) openDetail(id)
    } catch (e) {
      notify('application status', e?.message || String(e))
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: 16, gap: 12, overflow: 'hidden' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <h2 style={{ margin: 0, fontSize: 16, color: 'var(--text-primary)' }}>Applications</h2>
        <div style={{ flex: 1 }} />
        <button style={headerBtnStyle} onClick={() => setShowDiscover(true)}><Search size={13} /> Discover Jobs</button>
        <button style={headerBtnStyle} onClick={() => setShowAdd(true)}><Plus size={13} /> Add</button>
        <button style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }} onClick={() => setShowProcessPending(true)}>
          Process Pending
        </button>
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        {STATUS_TABS.map(tab => (
          <button
            key={tab}
            onClick={() => setStatusTab(tab)}
            style={{
              padding: '5px 12px', borderRadius: 5, cursor: 'pointer', fontSize: 11, fontFamily: 'var(--font-mono)',
              background: statusTab === tab ? 'rgba(0,180,216,0.15)' : 'transparent',
              border: `1px solid ${statusTab === tab ? 'rgba(0,180,216,0.4)' : 'rgba(255,255,255,0.1)'}`,
              color: statusTab === tab ? '#00b4d8' : 'var(--text-secondary)',
            }}
          >{tab}</button>
        ))}
        <input style={{ ...inputStyle, flex: 1, marginLeft: 8 }} placeholder="Search title, company, tags..." value={search} onChange={e => setSearch(e.target.value)} />
      </div>

      {error && <div style={{ color: '#ff6b6b', fontSize: 12 }}>{error}</div>}

      <div style={{ flex: 1, overflow: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ textAlign: 'left', color: 'var(--text-muted)', fontSize: 10, textTransform: 'uppercase' }}>
              <th style={{ padding: '6px 8px' }}>Title</th>
              <th style={{ padding: '6px 8px' }}>Company / Org</th>
              <th style={{ padding: '6px 8px' }}>Kind</th>
              <th style={{ padding: '6px 8px' }}>Status</th>
              <th style={{ padding: '6px 8px' }}>Tags</th>
              <th style={{ padding: '6px 8px' }}>Updated</th>
            </tr>
          </thead>
          <tbody>
            {visible.map(a => (
              <tr key={a.id} onClick={() => openDetail(a.id)} style={{ cursor: 'pointer', borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '8px' }}>{a.title}</td>
                <td style={{ padding: '8px' }}>{a.company}</td>
                <td style={{ padding: '8px' }}>{a.kind}</td>
                <td style={{ padding: '8px' }}><StatusBadge status={a.status} /></td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{(a.tags || []).join(', ')}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{a.updated_at}</td>
              </tr>
            ))}
            {visible.length === 0 && (
              <tr><td colSpan={6} style={{ padding: 16, textAlign: 'center', color: 'var(--text-muted)' }}>No applications.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {selectedId && detail && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && closeDetail()} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <div style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 520, maxHeight: '80vh', overflow: 'auto', fontFamily: 'var(--font-mono)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <h3 style={{ margin: 0, fontSize: 14 }}>{detail.title}</h3>
              <StatusBadge status={detail.status} />
            </div>
            <div style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 6 }}>
              {detail.kind === 'job' ? detail.company : detail.issuing_org}
              {' · '}
              <a href="#" onClick={(e) => { e.preventDefault(); WailsApp.OpenURL?.(detail.url) }} style={{ color: '#00b4d8' }}>
                {detail.url} <ExternalLink size={10} style={{ verticalAlign: 'middle' }} />
              </a>
            </div>
            {detail.description && <p style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 10 }}>{detail.description}</p>}
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              {detail.status === 'pending' && (
                <button style={headerBtnStyle} onClick={() => handleSetStatus(detail.id, 'cancelled')}>Cancel Application</button>
              )}
            </div>
            <h4 style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 16, textTransform: 'uppercase' }}>History</h4>
            {(detail.status_log || []).map((e, i) => (
              <div key={i} style={{ fontSize: 11, color: 'var(--text-muted)', padding: '3px 0' }}>
                {e.created_at}: {e.from_status || '(created)'} → {e.to_status} ({e.actor})
              </div>
            ))}
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
              <button style={headerBtnStyle} onClick={closeDetail}>Close</button>
            </div>
          </div>
        </div>
      )}

      {showAdd && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setShowAdd(false)} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <form onSubmit={handleAdd} style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 400, fontFamily: 'var(--font-mono)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <h3 style={{ margin: 0, fontSize: 14 }}>Add Application</h3>
            <select style={inputStyle} value={addForm.kind} onChange={e => setAddForm(f => ({ ...f, kind: e.target.value }))}>
              <option value="job">Job</option>
              <option value="tender">Tender</option>
            </select>
            <input style={inputStyle} placeholder="Title" required value={addForm.title} onChange={e => setAddForm(f => ({ ...f, title: e.target.value }))} />
            <input style={inputStyle} placeholder="URL" required value={addForm.url} onChange={e => setAddForm(f => ({ ...f, url: e.target.value }))} />
            {addForm.kind === 'job' ? (
              <input style={inputStyle} placeholder="Company" required value={addForm.company} onChange={e => setAddForm(f => ({ ...f, company: e.target.value }))} />
            ) : (
              <>
                <input style={inputStyle} placeholder="Issuing Organization" required value={addForm.issuingOrg} onChange={e => setAddForm(f => ({ ...f, issuingOrg: e.target.value }))} />
                <input style={inputStyle} placeholder="Submission Deadline" required value={addForm.submissionDeadline} onChange={e => setAddForm(f => ({ ...f, submissionDeadline: e.target.value }))} />
              </>
            )}
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
              <button type="button" style={headerBtnStyle} onClick={() => setShowAdd(false)}>Cancel</button>
              <button type="submit" style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }}>Add</button>
            </div>
          </form>
        </div>
      )}

      {showDiscover && (
        <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setShowDiscover(false)} style={{ position: 'fixed', inset: 0, zIndex: 9000, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.55)' }}>
          <form onSubmit={handleDiscover} style={{ background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 10, padding: 20, width: 400, fontFamily: 'var(--font-mono)', display: 'flex', flexDirection: 'column', gap: 8 }}>
            <h3 style={{ margin: 0, fontSize: 14 }}>Discover Jobs</h3>
            <input style={inputStyle} placeholder="Keywords" required value={discoverForm.keywords} onChange={e => setDiscoverForm(f => ({ ...f, keywords: e.target.value }))} />
            <input style={inputStyle} placeholder="Location (optional)" value={discoverForm.location} onChange={e => setDiscoverForm(f => ({ ...f, location: e.target.value }))} />
            <input style={inputStyle} type="number" placeholder="Limit" value={discoverForm.limit} onChange={e => setDiscoverForm(f => ({ ...f, limit: e.target.value }))} />
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 8 }}>
              <button type="button" style={headerBtnStyle} onClick={() => setShowDiscover(false)}>Cancel</button>
              <button type="submit" style={{ ...headerBtnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }}>Search</button>
            </div>
          </form>
        </div>
      )}

      {showProcessPending && (
        <ApplicationsProcessPendingFlow
          pendingApplications={applications.filter(a => a.status === 'pending')}
          onClose={() => { setShowProcessPending(false); load() }}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (from `wails-app/frontend/`): `npm test -- Applications.test.js`
Expected: PASS (7 tests). Note this task's component imports
`ApplicationsProcessPendingFlow.jsx`, which doesn't exist until Task 2 —
`npm test -- Applications.test.js` only imports the named
`filterApplications` export, not the default component, so this
specific test file passes without that file existing yet; a full
`npm test` (all files) or `npm run build` will not succeed until Task 2
lands. This is expected and matches this plan's task ordering — Task 2
is next.

- [ ] **Step 5: Commit**

```bash
cd wails-app/frontend
git add src/pages/Applications.jsx src/pages/Applications.test.js
git commit -m "$(cat <<'EOF'
feat(gui): add Applications page (list, filter, detail, add, discover)

Table with status tabs (matching Vault.jsx's existing list-page
convention), a detail modal, add/discover forms. filterApplications is
extracted as a pure, separately-tested function per the codebase's
established Vault.test.js pattern.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

(Run this `git add`/`git commit` from the repo root, or adjust paths — the `cd` above is shown for clarity on which files this commit contains; use whichever working-directory convention the rest of this session has been using.)

---

### Task 2: `ApplicationsProcessPendingFlow.jsx` — the guided evaluate→apply→send flow

**Files:**
- Create: `wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.jsx`
- Create: `wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.test.js`

**Interfaces:**
- Consumes: `WailsApp.{EvaluateApplication,HasGeneratedDocuments,ApplyToApplication,SendApplication,SetApplicationStatus,OpenJSONFilePicker,ReadJSONFile}` (Task 0), `confirm` (existing).
- Produces: `computeNextStep(item, mode, priorAction)` (pure, exported, unit-tested — the per-item state-machine decision function), `export default function ApplicationsProcessPendingFlow({ pendingApplications, onClose })`. Consumed by Task 1 (already imported there) and Task 3 (no direct import, but its existence is required for `Applications.jsx`'s full test suite / build to pass).

- [ ] **Step 1: Write the failing test**

```js
// wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.test.js
import { describe, it, expect } from 'vitest'
import { computeNextStep } from './ApplicationsProcessPendingFlow.jsx'

describe('computeNextStep', () => {
  it('in auto mode, always proceeds to apply after evaluate', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'evaluated' })).toBe('apply')
  })

  it('in confirm mode, waits for a user decision after evaluate', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated' })).toBe('await-decision')
  })

  it('an explicit "apply" decision in confirm mode proceeds to apply', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'apply' })).toBe('apply')
  })

  it('an explicit "skip" decision in confirm mode moves to next without applying', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'skip' })).toBe('next')
  })

  it('an explicit "not-interested" decision cancels the application', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'not-interested' })).toBe('cancel')
  })

  it('after a successful apply, the stage is "ready-to-send" regardless of mode', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'applied' })).toBe('ready-to-send')
    expect(computeNextStep({ mode: 'confirm', stage: 'applied' })).toBe('ready-to-send')
  })

  it('already-prepared applications skip straight to ready-to-send without re-applying', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'evaluated', alreadyPrepared: true })).toBe('ready-to-send')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `wails-app/frontend/`): `npm test -- ApplicationsProcessPendingFlow.test.js`
Expected: FAIL — file doesn't exist yet.

- [ ] **Step 3: Write the component**

```jsx
// wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.jsx
import { useState } from 'react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'

// computeNextStep is the flow's per-item state-machine decision function,
// extracted pure and exported for direct unit testing (see
// ApplicationsProcessPendingFlow.test.js) without rendering the
// component. Mirrors Vault.jsx's filterAndSortEntries convention.
//
// - "auto" mode always proceeds through evaluate -> apply -> ready-to-send
//   with no per-item pause.
// - "confirm" mode pauses after evaluate ("await-decision") until the
//   caller supplies one of "apply" | "skip" | "not-interested".
// - An application already fully prepared (alreadyPrepared) in a prior
//   run skips straight to "ready-to-send" without re-invoking apply,
//   avoiding a duplicate browser tab.
export function computeNextStep({ mode, stage, decision, alreadyPrepared }) {
  if (stage === 'evaluated') {
    if (alreadyPrepared) return 'ready-to-send'
    if (mode === 'auto') return 'apply'
    if (!decision) return 'await-decision'
    if (decision === 'apply') return 'apply'
    if (decision === 'skip') return 'next'
    if (decision === 'not-interested') return 'cancel'
  }
  if (stage === 'applied') return 'ready-to-send'
  return 'next'
}

const boxStyle = {
  background: 'var(--surface, #0d1520)', border: '1px solid rgba(255,255,255,0.1)',
  borderRadius: 10, padding: 20, width: 480, fontFamily: 'var(--font-mono)',
}
const btnStyle = {
  padding: '7px 14px', borderRadius: 6, cursor: 'pointer', fontSize: 12,
  background: 'rgba(0,180,216,0.1)', border: '1px solid rgba(0,180,216,0.3)', color: '#00b4d8',
}

export default function ApplicationsProcessPendingFlow({ pendingApplications, onClose }) {
  const [mode, setMode] = useState('confirm')
  const [started, setStarted] = useState(false)
  const [cvPath, setCvPath] = useState('')
  const [letterPath, setLetterPath] = useState('')
  const [index, setIndex] = useState(0)
  const [stage, setStage] = useState('idle') // idle | evaluating | evaluated | applying | applied | done
  const [verdict, setVerdict] = useState(null)
  const [applyResult, setApplyResult] = useState(null)
  const [summary, setSummary] = useState({ applied: 0, skipped: 0, notInterested: 0, sent: 0 })
  const [error, setError] = useState(null)

  const current = pendingApplications[index]

  const pickCv = async () => { const p = await WailsApp.OpenJSONFilePicker('Select CV data JSON'); if (p) setCvPath(p) }
  const pickLetter = async () => { const p = await WailsApp.OpenJSONFilePicker('Select cover letter data JSON'); if (p) setLetterPath(p) }

  const start = async () => {
    if (!(await confirm(`Process ${pendingApplications.length} pending application(s)?`, { title: 'Process Pending', confirmLabel: 'Start', danger: false }))) return
    setStarted(true)
    processCurrent()
  }

  const finishItem = (patch) => setSummary(s => ({ ...s, ...patch }))

  const advance = () => {
    if (index + 1 >= pendingApplications.length) { setStage('done'); return }
    setIndex(i => i + 1)
    setStage('idle')
    setVerdict(null)
    setApplyResult(null)
    processCurrent(index + 1)
  }

  const processCurrent = async (i = index) => {
    const item = pendingApplications[i]
    if (!item) { setStage('done'); return }
    setStage('evaluating')
    setError(null)
    try {
      const v = await WailsApp.EvaluateApplication(item.id, '')
      setVerdict(v)
      const alreadyPrepared = await WailsApp.HasGeneratedDocuments(item.id)
      const next = computeNextStep({ mode, stage: 'evaluated', alreadyPrepared })
      setStage('evaluated')
      if (next === 'apply') { applyCurrent(item) }
      else if (next === 'ready-to-send') { setApplyResult({}); setStage('applied') }
      // 'await-decision' -- stay in 'evaluated' stage, wait for a button click below.
    } catch (e) {
      setError(String(e))
    }
  }

  const applyCurrent = async (item) => {
    setStage('applying')
    try {
      const cvData = cvPath ? await WailsApp.ReadJSONFile(cvPath) : {}
      const letterData = letterPath ? await WailsApp.ReadJSONFile(letterPath) : {}
      const result = await WailsApp.ApplyToApplication(item.id, cvData, letterData)
      setApplyResult(result)
      setStage('applied')
      finishItem({ applied: summary.applied + 1 })
    } catch (e) {
      setError(String(e))
    }
  }

  const decide = (decision) => {
    const next = computeNextStep({ mode, stage: 'evaluated', decision })
    if (next === 'apply') applyCurrent(current)
    else if (next === 'cancel') {
      WailsApp.SetApplicationStatus(current.id, 'cancelled', '').finally(() => { finishItem({ notInterested: summary.notInterested + 1 }); advance() })
    } else if (next === 'next') {
      finishItem({ skipped: summary.skipped + 1 })
      advance()
    }
  }

  const sendCurrent = async () => {
    try {
      await WailsApp.SendApplication(current.id, '')
      finishItem({ sent: summary.sent + 1 })
    } catch (e) {
      setError(String(e))
    }
    advance()
  }

  return (
    <div className="modal-overlay" style={{ position: 'fixed', inset: 0, zIndex: 9500, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.6)' }}>
      <div style={boxStyle}>
        <h3 style={{ margin: 0, fontSize: 14 }}>Process Pending Applications</h3>

        {!started && (
          <>
            <div style={{ display: 'flex', gap: 12, marginTop: 12, fontSize: 12 }}>
              <label><input type="radio" checked={mode === 'confirm'} onChange={() => setMode('confirm')} /> Confirm each one</label>
              <label><input type="radio" checked={mode === 'auto'} onChange={() => setMode('auto')} /> Auto (AI handles it)</label>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 12 }}>
              <button style={btnStyle} onClick={pickCv}>{cvPath ? `CV: ${cvPath}` : 'Select CV data file'}</button>
              <button style={btnStyle} onClick={pickLetter}>{letterPath ? `Cover letter: ${letterPath}` : 'Select cover letter data file'}</button>
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
              <button style={btnStyle} onClick={onClose}>Cancel</button>
              <button style={{ ...btnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }} onClick={start}>Start</button>
            </div>
          </>
        )}

        {started && stage !== 'done' && current && (
          <div style={{ marginTop: 12, fontSize: 12 }}>
            <div style={{ color: 'var(--text-muted)' }}>{index + 1} / {pendingApplications.length}</div>
            <div style={{ fontWeight: 600, marginTop: 4 }}>{current.title} — {current.company}</div>
            {stage === 'evaluating' && <div style={{ marginTop: 8 }}>Evaluating fit…</div>}
            {verdict && (
              <div style={{ marginTop: 8, padding: 8, background: 'rgba(255,255,255,0.04)', borderRadius: 6 }}>
                <div><strong>{verdict.verdict}</strong> (overall {verdict.overall_score?.toFixed(1)})</div>
                <div style={{ color: 'var(--text-muted)', marginTop: 4 }}>{verdict.rationale}</div>
              </div>
            )}
            {stage === 'evaluated' && mode === 'confirm' && (
              <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                <button style={btnStyle} onClick={() => decide('apply')}>Apply</button>
                <button style={btnStyle} onClick={() => decide('skip')}>Skip</button>
                <button style={{ ...btnStyle, color: '#ef4444', border: '1px solid rgba(239,68,68,0.3)' }} onClick={() => decide('not-interested')}>Not Interested</button>
              </div>
            )}
            {stage === 'applying' && <div style={{ marginTop: 8 }}>Preparing documents and opening browser…</div>}
            {stage === 'applied' && (
              <div style={{ marginTop: 12 }}>
                <div style={{ color: '#10b981' }}>Ready to send.</div>
                <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                  <button style={{ ...btnStyle, background: 'rgba(16,185,129,0.15)', border: '1px solid rgba(16,185,129,0.4)', color: '#10b981' }} onClick={sendCurrent}>Send Now</button>
                  <button style={btnStyle} onClick={advance}>Next (send later)</button>
                </div>
              </div>
            )}
            {error && <div style={{ color: '#ff6b6b', marginTop: 8 }}>{error}</div>}
          </div>
        )}

        {stage === 'done' && (
          <div style={{ marginTop: 12, fontSize: 12 }}>
            <div>Done. Applied: {summary.applied}, Skipped: {summary.skipped}, Not interested: {summary.notInterested}, Sent now: {summary.sent}.</div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
              <button style={btnStyle} onClick={onClose}>Close</button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run (from `wails-app/frontend/`): `npm test`
Expected: PASS — all test files (`Applications.test.js` and
`ApplicationsProcessPendingFlow.test.js`), since `Applications.jsx`'s
import of this file now resolves.

- [ ] **Step 5: Manual-verification-only note**

The visual layout of the modal (spacing, the radio-button mode selector,
the multi-stage per-item view) is **not covered by an automated test** —
`computeNextStep`'s logic is tested, but rendering correctness is not.
This is a deliberate, stated limitation (per the design spec's testing
section) rather than a gap silently left uncovered: verify visually via
`wails dev` (from `wails-app/`) once Task 3's bindings are regenerated,
if a live check is wanted before considering this phase fully done.

- [ ] **Step 6: Commit**

```bash
git add wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.jsx wails-app/frontend/src/pages/ApplicationsProcessPendingFlow.test.js
git commit -m "$(cat <<'EOF'
feat(gui): add the guided Process Pending flow

Auto/Confirm mode toggle, per-item evaluate -> (confirm: pause for a
decision | auto: proceed) -> apply -> always-manual send. Skips
re-applying to an application a prior run already prepared.
computeNextStep is the pure, separately-tested state-machine core.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 3: Navigation registration, binding regeneration, final verification

**Files:**
- Modify: `wails-app/frontend/src/components/Sidebar.jsx`
- Modify: `wails-app/frontend/src/App.jsx`
- Modify: `wails-app/frontend/src/locales/en.json`
- Modify: `wails-app/frontend/src/locales/es.json`
- Regenerate (not hand-edited): `wails-app/frontend/src/wailsjs/go/main/App.js`, `App.d.ts`

**Interfaces:**
- Consumes: `Applications` default export (Task 1).
- Produces: the page reachable from the sidebar. Terminal task — nothing depends on this.

- [ ] **Step 1: Add the nav item**

In `wails-app/frontend/src/components/Sidebar.jsx`, add `Briefcase` to the `lucide-react` import list:

```js
import {
  LayoutDashboard, Users,
  Terminal, PlayCircle, Settings, Image, Mail, KeyRound,
  ChevronDown, Plus, Check, Building2, FolderOpen, FolderCog, Loader2, Briefcase
} from 'lucide-react'
```

Add one entry to `NAV_ITEMS` (grouped with the other `DATA`-section items):

```js
  { id: 'applications', labelKey: 'applications', icon: Briefcase, section: 'DATA' },
```

- [ ] **Step 2: Add the locale keys**

In `wails-app/frontend/src/locales/en.json`, add `"applications": "Applications"` inside the existing `"sidebar"."nav"` object (alongside `"vault"`, `"secretsVault"`, etc.):

```json
      "secretsVault": "Vault",
      "applications": "Applications",
```

In `wails-app/frontend/src/locales/es.json`, find the equivalent `"sidebar"."nav"` object and add the Spanish translation in the same position:

```json
      "applications": "Postulaciones",
```

(Read `es.json`'s existing `secretsVault`/`vault` translations first to match its exact key ordering and quoting style before inserting — do not guess at the surrounding JSON structure without looking.)

- [ ] **Step 3: Register the page in `App.jsx`**

In `wails-app/frontend/src/App.jsx`, add the import:

```js
import Applications from './pages/Applications.jsx'
```

Add one entry to the `persistentPages` object (alongside `vault`, `secretsVault`):

```js
    applications: <Applications />,
```

- [ ] **Step 4: Ensure the frontend is built, then regenerate wailsjs bindings**

Run:
```bash
cd wails-app/frontend
ls node_modules >/dev/null 2>&1 || npm install
npm run build
cd ..
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
which wails >/dev/null 2>&1 || go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
wails generate module
```
Expected: `npm run build` succeeds (produces/updates `frontend/dist/`); `wails generate module` completes (diagnostic `Not found: time.Time` lines in its output are expected/harmless — verified during this plan's own research). Confirm `wails-app/frontend/src/wailsjs/go/main/App.js` and `App.d.ts` now reference the new methods added in Task 0 (e.g. `grep -c GetApplications wails-app/frontend/src/wailsjs/go/main/App.js` should report at least 1).

- [ ] **Step 5: Run the full frontend test suite and build**

Run (from `wails-app/frontend/`): `npm test`
Expected: PASS — every test file.

Run (from `wails-app/frontend/`): `npm run build`
Expected: succeeds (re-run after the bindings regenerated in Step 4, to confirm the page compiles against the real generated bindings, not just against the hand-written call sites).

- [ ] **Step 6: Run the full Go build**

Run (from the repo root): `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing. Confirms `wails-app/app_applications.go` still compiles correctly and no other package regressed.

- [ ] **Step 7: Commit**

```bash
git add wails-app/frontend/src/components/Sidebar.jsx wails-app/frontend/src/App.jsx wails-app/frontend/src/locales/en.json wails-app/frontend/src/locales/es.json wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts
git commit -m "$(cat <<'EOF'
feat(gui): register the Applications page in navigation

New sidebar nav item, locale keys (en/es), App.jsx routing entry, and
regenerated wailsjs bindings for the Task 0 backend methods. Completes
Phase 6b (Applications GUI) -- the final piece of the "ultimate job
applier" feature.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

Note: depending on what else changed in `wailsjs/runtime/*` incidentally during `wails generate module` (harmless file-mode-only changes were observed during this plan's own research, e.g. `644 -> 755` on `runtime.js`/`runtime.d.ts`/`package.json`), `git status` may show those three files touched too — include them in this same commit if present (they are a normal, expected side effect of running the tool, not unrelated work).

---

## Self-Review

**1. Spec coverage:**
- List/filter/detail → Task 1. ✅
- Add application → Task 1. ✅
- Discover jobs → Task 1. ✅
- Evaluate (single, via the flow) → Task 2 (batch `EvaluatePendingApplications` is exposed on the backend in Task 0 but not wired to a dedicated UI button this phase — the flow evaluates each pending item individually as it processes them, which covers the "review pendings" use case the design spec centers on; a standalone "Evaluate All" button wired to the batch endpoint was not in the approved design's UI surface and is not silently added here).
- Process Pending flow with Auto/Confirm modes, one upfront confirmation, per-item confirm-mode pause, always-manual send → Task 2. ✅
- GUI always calls `apply --mode auto` under the hood → Task 0's `ApplyToApplication`. ✅
- Navigation, i18n, `app_applications.go` never importing internal packages → Task 0, Task 3. ✅

**2. Placeholder scan:** No "TBD"/"TODO". Every step has complete code. Task 2 Step 5 is explicitly labeled as manual-verification-only rather than claiming false test coverage for visual layout.

**3. Type consistency:** Every Go struct's `json` tag in Task 0 is matched exactly by the corresponding JS property name read in Task 1/Task 2 (e.g. `overall_score`, `from_status`, `to_status`, `cv_html_document_id`) — cross-checked against the actual CLI source in this plan's Global Constraints section, not assumed. `computeNextStep`'s return values (`'apply'`, `'await-decision'`, `'next'`, `'cancel'`, `'ready-to-send'`) are used identically in both the test file (Task 2 Step 1) and the component's own call sites (Task 2 Step 3).

One process note carried from this plan's own research, not a spec gap: `node_modules` and `frontend/dist` did not exist at the start of this work and had to be created (`npm install`, `npm run build`) before the `wails` CLI could even run — Task 3 Step 4 makes this an explicit, idempotent check (`ls node_modules || npm install`) rather than assuming a clean environment already has them.
