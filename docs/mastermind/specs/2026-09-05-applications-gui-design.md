# Applications GUI Board — Design Spec

Date: 2026-09-05
Status: Approved (Phase 6b — the Wails GUI for the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

The final piece of the 6-phase feature: a Wails GUI page presenting the
job/tender application pipeline, matching the user's original request
("there is a button also there to review pendings and apply for them...
there should be also a mode... allows AI to 100% handle everything and
the other one to confirm everything with human"). Everything the GUI does
is a thin presentation layer over the CLI already built and verified in
Phases 1-6 — no new backend logic, no new Go business logic in the Wails
app at all.

### Confirmed GUI architecture (verified by reading `wails-app/app_vault.go`)

This app already has an explicit, established convention: **the CLI is
the single implementation surface.** `app_vault.go`'s own comment: "Every
method below shells out to `monoagentcli secret ...` instead of calling
`internal/secrets` directly — the CLI is the single implementation
surface for vault operations. This file intentionally does not import
`monoagent/internal/secrets`." This phase follows that exactly:
`wails-app/app_applications.go` (new file, sibling to `app_vault.go`, not
growing the existing 1272-line `app.go`) shells out to `monoagentcli` via
`os/exec`, parses `--json` output into typed structs that mirror the
CLI's JSON shape (not the internal Go packages' types), and never imports
`internal/applications`, `internal/discovery`, `internal/matching`,
`internal/documents`, or `internal/apply`.

### A non-obvious integration detail this phase must get right

The GUI's subprocess calls to `monoagentcli` are **non-interactive** —
there is no TTY for the CLI's own confirm-mode y/N prompt to read from.
So the GUI **always invokes `application apply --mode auto`** under the
hood (bypassing the CLI's own prompt, which cannot function via
`exec.Command`), and implements confirm-vs-auto itself in the frontend
using the existing `confirm()` promise-based dialog
(`wails-app/frontend/src/components/ConfirmDialog.jsx`, already used
elsewhere, e.g. `Vault.jsx`). The backend's hard invariant (apply never
submits; `send` is the only path to "applied") is unaffected — this is
purely about which layer owns the human-confirmation UX.

## Requirements

- A new "Applications" page, reachable from the sidebar, listing job/
  tender applications with status/kind/tag filtering.
- View one application's full detail: job/tender fields, tags, status
  history, fit verdict (if evaluated), generated documents.
- Add an application manually (mirrors `application add`).
- Discover jobs (mirrors `application discover`) via keywords/location/limit.
- Evaluate one application, or all not-yet-evaluated pending ones.
- A "Process Pending" flow with an Auto/Confirm mode toggle that
  evaluates and applies to pending applications, ending at "ready to
  send" — never auto-sending.
- A deliberate, always-manual "Send" action per application.

## Architecture

### Backend: `wails-app/app_applications.go`

A shared helper (new, mirrors `app_vault.go`'s `runVaultCLI` but
generalized — not hardcoded to one CLI subcommand, since this phase spans
`application`, `documents`, and `profile`):

```go
// runMonoCLI runs monoagentcli with --profile <active> --json prefixed
// to args, capturing stdout into result (nil to ignore). Mirrors
// app_vault.go's runVaultCLI exactly, generalized to any subcommand
// rather than one hardcoded to "secret".
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
```

Exported `*App` methods (each a thin wrapper, one CLI call, mirroring
`app_vault.go`'s per-method shape exactly):

```go
type ApplicationSummary struct {
	ID, Kind, Status, Title, Company, URL, UpdatedAt string
	Tags []string
}
func (a *App) GetApplications(kind, status, tag string) ([]ApplicationSummary, error)

type ApplicationDetail struct {
	Application ApplicationSummary
	// Job/Tender detail fields flattened for simplicity — the frontend
	// branches on Kind to decide which subset to display.
	Description, Location, JobType, IssuingOrg, SubmissionDeadline string
	StatusLog []StatusLogEntry
}
type StatusLogEntry struct{ FromStatus, ToStatus, Actor, Note, CreatedAt string }
func (a *App) GetApplication(id string) (*ApplicationDetail, error)

func (a *App) AddApplication(kind, title, company, url, issuingOrg, submissionDeadline string) (string, error)
func (a *App) SetApplicationStatus(id, status, note string) error
func (a *App) TagApplication(id, tag, action string) error // action: "add"|"remove"

type DiscoverResult struct {
	Imported, Skipped, Failed int
	Applications []ApplicationSummary
}
func (a *App) DiscoverJobs(keywords, location, source string, limit int) (*DiscoverResult, error)

type FitVerdictInfo struct {
	EligibilityPass, LanguagePass, LocationPass bool
	TechnicalScore, ExperienceScore, BehavioralScore, CareerScore, OverallScore float64
	Verdict, Rationale string
}
func (a *App) EvaluateApplication(id, runtime string) (*FitVerdictInfo, error)

type EvaluateBatchResult struct {
	Evaluated int
	Verdicts  map[string]int
}
func (a *App) EvaluatePendingApplications(runtime string, limit int) (*EvaluateBatchResult, error)

type ApplyResult struct {
	CVHTMLDocumentID, CVPDFDocumentID, CoverLetterHTMLDocumentID, CoverLetterPDFDocumentID string
}
// ApplyToApplication always calls `application apply --mode auto` under
// the hood — see the "non-obvious integration detail" above for why the
// mode toggle is a frontend-only concern.
func (a *App) ApplyToApplication(id string, cvData, coverLetterData map[string]interface{}) (*ApplyResult, error)

func (a *App) SendApplication(id, note string) error

// HasGeneratedDocuments reports whether id already has generated (not
// manually-uploaded) documents in the vault — used by the frontend to
// skip re-opening a browser tab for an application "Process Pending"
// already handled in an earlier run, instead offering a "re-open" action.
func (a *App) HasGeneratedDocuments(id string) (bool, error)
```

`HasGeneratedDocuments` is the one method with no direct CLI equivalent —
it's implemented as a thin wrapper over `monoagentcli profile documents
list --json`, filtering client-side (in Go, inside this method) for
`application_id == id && source == "generated"`, rather than adding a new
CLI flag for a GUI-only convenience query. If this filter need recurs
elsewhere later, promoting it to a real CLI flag (`documents list
--application-id`) is the natural next step — not done now, since one
caller doesn't justify a new CLI surface (YAGNI).

### Frontend: `wails-app/frontend/src/pages/Applications.jsx`

Structure mirrors `Vault.jsx`: a status-tab selector (Pending / Applied /
Rejected / Cancelled / All) above a table (columns: Title, Company/Org,
Kind, Tags, Updated), search/sort matching `Vault.jsx`'s
`filterAndSortEntries` pattern (a pure, separately-exported,
separately-unit-tested function — mirrors `Vault.test.js`'s convention).
Row click opens a detail panel/modal (job fields, status history, fit
verdict if present, generated document list with "open" links via
`OpenURL`).

Page-level actions: "Add" (form), "Discover Jobs" (modal: keywords,
location, limit), "Process Pending" (the flow below).

#### The "Process Pending" flow

A dedicated component, `ProcessPendingFlow`, opened as a modal from the
Pending tab:

1. A mode toggle (Auto / Confirm, default **Confirm** — matching this
   feature's established default-to-caution posture from every prior
   phase) and a "Start" button.
2. On Start: fetch the current pending list. In **both** modes, show one
   upfront `confirm()` dialog: "Process N pending application(s)?" — a
   single gate before anything happens, regardless of mode (auto mode
   skips ALL further prompts after this one; confirm mode still asks
   per item).
3. For each pending application, in order:
   a. Call `EvaluateApplication` (skip if `HasGeneratedDocuments` already
      true for a job that was previously fully processed — but always
      re-evaluate, since evaluation is cheap and re-running is harmless
      and potentially more current).
   b. **Confirm mode**: show the job + verdict inline in the flow, with
      three actions — "Apply" (proceeds to step c), "Skip" (leaves status
      untouched, moves to next), "Not Interested" (calls
      `SetApplicationStatus(id, "cancelled", ...)`, moves to next).
      **Auto mode**: always proceeds to step c (no per-item prompt).
   c. If `HasGeneratedDocuments` is already true for this application,
      skip straight to step d without calling `ApplyToApplication` again
      (avoids re-opening a duplicate browser tab for an application a
      prior "Process Pending" run already handled). Otherwise call
      `ApplyToApplication` (documents prepared, browser opened).
   d. Show "Ready to send" with a **Send** button, right there in the
      flow — clicking it calls `SendApplication` immediately. This is
      always an explicit click, in both modes, never automatic — but the
      flow doesn't force the user to click it before moving on: a
      "Next" button advances regardless, so the user can go back and
      send from the main table later once they've actually submitted the
      form in the opened browser tab.
4. End-of-flow summary: counts (applied-to, skipped, not-interested,
   sent-now).

This design deliberately keeps every "did I actually submit this"
judgment call in the human's hands, in both modes — the only difference
auto/confirm makes is whether the flow pauses to ask "apply to this one?"
before opening each browser tab, not whether sending is ever automatic
(it never is, in either mode).

### Navigation registration

`wails-app/frontend/src/components/Sidebar.jsx`'s `NAV_ITEMS` gets one
new entry (`{ id: 'applications', labelKey: 'applications', icon:
Briefcase, section: 'DATA' }`, grouped with `people`/`vault`/
`secretsVault`). `wails-app/frontend/src/locales/en.json` and `es.json`
get a `sidebar.applications` key each (matching the existing per-nav-item
translation convention — this app's i18n coverage is otherwise opt-in
per page, so the Applications page's own in-page text stays plain
English like `Vault.jsx`'s, `People.jsx`'s, etc. already do).
`wails-app/frontend/src/App.jsx`'s `persistentPages` map gets one new
entry: `applications: <Applications />`.

## Data Flow

1. Page mount → `GetApplications('', 'pending', '')` (default tab:
   Pending) → render table.
2. Row click → `GetApplication(id)` → render detail panel.
3. "Discover Jobs" modal submit → `DiscoverJobs(...)` → show summary
   toast → refresh the current tab's list.
4. "Process Pending" → the flow above, calling `EvaluateApplication`/
   `ApplyToApplication`/`SendApplication`/`SetApplicationStatus` per item
   → refresh the list on close.

## Error Handling

- Every `App` method surfaces the CLI's stderr verbatim on failure
  (`runMonoCLI`'s existing error-unwrapping, mirrored from
  `runVaultCLI`) — the frontend shows it via the existing Toasts
  component (`wails-app/frontend/src/components/Toasts.jsx`), matching
  how other pages already surface backend errors.
- A failure mid-`ProcessPendingFlow` (e.g. one application's evaluate
  call fails) logs that item as failed in the end-of-flow summary and
  continues to the next pending item — one bad job posting doesn't abort
  the whole batch, matching the CLI's own `evaluate-pending` behavior.

## Testing

- `Applications.test.js` — unit tests for the extracted pure filter/sort
  function (mirroring `Vault.test.js`), and for `ProcessPendingFlow`'s
  per-item state-machine logic (evaluate → confirm/auto branch → apply-
  or-skip → ready-to-send), using mocked `WailsApp` bindings — no real
  Wails runtime or CLI subprocess needed for these tests, matching how
  `Vault.test.js` tests pure logic without rendering against a live
  backend.
- No Go-side tests are needed for `app_applications.go` beyond what the
  existing `runVaultCLI`-style pattern already implies is untested at
  this layer in this codebase (checked: `app_vault.go` itself has no
  `_test.go` file — this thin-wrapper layer is conventionally left to
  manual/integration verification in this project, not unit-tested, and
  this phase follows that existing precedent rather than introducing a
  new testing expectation unilaterally).

## Out of Scope (this phase)

- Drag-and-drop Kanban board — a filterable table matches this
  codebase's own established list-page convention; revisit only if a
  real usability problem with the table surfaces later.
- Editing an application's job/tender fields after creation — Phase 1's
  CLI has no `application update` command either; out of scope for
  symmetry with the backend, not a GUI-specific omission.
- A dedicated UI for building the CV/cover-letter structured data that
  `apply`/`documents render` need — the Applications page's Apply flow
  picks up this data via a native file picker pointing at a JSON file
  (matching `Vault.jsx`'s `OpenVaultFilePicker` pattern; a new
  `App.OpenJSONFilePicker` method, or reusing a generic existing picker if
  one already exists — confirm which during planning), not a full
  CV-data-entry form. Building an in-app CV/cover-letter editor is a
  separate, sizeable piece of UI and is future work.
