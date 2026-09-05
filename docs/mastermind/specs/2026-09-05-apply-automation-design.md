# Apply Automation — Design Spec

Date: 2026-09-05
Status: Approved (Phase 6 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

The final backend phase. Per the Phase 1 design gate (the user's own explicit
decision, re-confirmed at the very start of this feature): even in "AI
handles everything" auto mode, automation **stops one step short of the
actual submit** — it prepares everything, and a separate, always-manual
action performs the actual send. This phase builds that: assembling
everything needed for one application (CV, cover letter, contact info,
the job's URL open in a real browser) and a strict, code-level "never
submit programmatically" boundary — not a prompt instruction the AI is
merely asked to follow, an invariant no code path in this feature can
violate regardless of mode.

### A scoping decision made deliberately, not by default

Investigating how to actually fill an arbitrary, unknown ATS form
surfaced `monomind`'s tool-bridge (`ExecOptions.Tools`/`OnToolCall` in
`internal/monomind/exec.go`) — architecturally, this could let a
delegated local agent adaptively read a job page's DOM and fill whatever
fields it finds, avoiding the fragile per-ATS-selector maintenance
problem career-ops's research flagged (Ashby/Lever/Workable-specific
quirks). **This phase does not build that.** Two reasons:
1. **Zero production precedent.** Grepping the entire codebase, the
   tool-bridge is exercised only in `internal/monomind`'s own tests
   (`TestExecToolBridgeRoundTrip`) — no node, CLI command, or other
   feature uses it in production. Being the first production consumer of
   an intricate protocol, for a feature whose failure mode is "silently
   fills the wrong thing on a real job application," is a bad place to
   be first.
2. **No live-testable target.** This environment cannot reach real ATS
   sites (LinkedIn, Greenhouse, Lever, Workable, ...) to verify an
   adaptive filler actually works — the same limitation Phase 2 already
   hit for LinkedIn's scraping selectors, but with materially higher
   stakes here (a malformed real application vs. a failed scrape).

Instead, this phase builds the **browser-assisted, human-completes-the-
form** flow: one command opens the real job posting in a visible browser
window and surfaces everything the user needs (contact info, the
generated CV/cover-letter file paths) so filling the actual form by hand
takes seconds, not a document hunt. The adaptive-agent auto-fill is
recorded as explicit future work (Out of Scope), not silently dropped.

### Reused infrastructure (verified by reading the code)

- `internal/documents.GenerateDocument` (Phase 4) — generates and vault-
  saves the CV/cover letter for an application, on demand if not already
  done.
- `applications.Store.Get`/`SetStatus` (Phase 1) — fetch the job, and the
  existing `pending → applied` transition is exactly the bookkeeping step
  a manual "I submitted it" confirmation needs — no new status is required.
- The `launcher.New().Headless(...)./rod.New()` pattern (Phase 4, itself
  from `cmd/monoagentcli/crawl.go`) — this phase uses the same pattern
  with `Headless(false)` (a real, visible window, since a human needs to
  see and use it — the opposite of Phase 4's throwaway headless PDF
  render).

## Requirements

- One command assembles everything for an application (generating
  documents if needed) and opens the job posting in a real browser window.
- Two modes: **confirm** (ask before starting, per-application) and
  **auto** (start immediately) — both end at the same place: the browser
  is open, the form is not filled or submitted by mono-agent, and the
  application's status is unchanged until the user explicitly confirms
  they sent it.
- A structural guarantee, true in both modes: no code path in this
  feature ever performs a browser click on a submit-equivalent control,
  or otherwise completes a submission programmatically.
- The explicit "I sent it" confirmation transitions the application to
  `applied` (Phase 1's existing status machine — no schema change needed).

## Architecture

### `Prepare` (new function, `internal/apply/apply.go`)

```go
// Prepare ensures applicationID (a job) has a generated CV and cover
// letter in the vault, generating them now (via documents.GenerateDocument)
// if none exist yet. cvData/coverLetterData are the structured content to
// render — this phase does not generate that content itself (that would
// require the same kind of LLM delegation Phase 5 uses for scoring; a
// natural next step, but a distinct decision this phase doesn't make
// silently — see Out of Scope). Returns the vault IDs of the (possibly
// pre-existing) HTML/PDF documents for each.
func Prepare(ctx context.Context, db *sql.DB, profileID, applicationID string, cvData documents.CVData, coverLetterData documents.CoverLetterData) (cvHTMLID, cvPDFID, letterHTMLID, letterPDFID string, err error)
```

If documents already exist for this `application_id` (checked via
`vault.ListDocuments` filtered by `ApplicationID`), `Prepare` reuses them
rather than regenerating — calling `apply` twice on the same application
doesn't create duplicate CVs.

### `OpenForApplication` (new function, `internal/apply/browser.go`)

```go
// OpenForApplication launches a real, VISIBLE (not headless) browser
// window at app's job URL, for the user to complete the application by
// hand. This function contains no click/fill/submit logic whatsoever —
// it only navigates. That is a deliberate, structural property: the
// absence of any "click" call anywhere in this file is what makes "never
// submits programmatically" true by construction, not by convention.
func OpenForApplication(ctx context.Context, jobURL string) error
```

Uses the `launcher.New().Headless(false).Launch()` → `rod.New()` →
`browser.Page(...)` pattern, but **does not call `browser.Close()`** —
the whole point is the window stays open after this function returns, for
the human to use. (Unlike Phase 4's `RenderPDF`, which closes its
throwaway browser immediately after one automated task.)

### CLI

```
monoagentcli application apply <id> [--mode confirm|auto] [--cv-data-file <path>] [--cover-letter-data-file <path>]
monoagentcli application send <id> [--note "..."]
```

- `apply`: mode defaults to `confirm` — prompts "About to prepare and open
  <company> — <title> (<url>). Continue? [y/N]" before doing anything, in
  either terminal or `--json` mode (in `--json` mode, the prompt is
  skipped and `auto` behavior is used — a non-interactive JSON caller
  can't answer a y/N prompt, so requiring `--mode auto` explicitly for
  scripted/JSON use is the safer default; text-mode with no `--mode` flag
  always confirms). Runs `Prepare` (generating documents from the given
  data files, or reusing existing ones if the flags are omitted and
  documents already exist for this application), then `OpenForApplication`.
  Prints the generated document paths so the user can attach them by hand.
  It does not re-derive or print contact info from Phase 3's profile
  documents — that remains their own source of truth, queried via
  `monoagentcli profile search-knowledge` if the user needs a reminder;
  duplicating contact-info extraction here would be a second, divergent
  source of the same data.
- `send`: transitions the application from `pending` to `applied` via
  `applications.Store.SetStatus` (Phase 1's existing, already-validated
  transition graph — `send` on an application not in `pending` fails with
  the same clear error Phase 1's `application status` command already
  gives). This is the **only** code path in this entire feature that
  changes an application's status to `applied` — there is no other way,
  automated or otherwise, for `mono-agent` to mark something as applied.

## Error Handling

- `apply` on a non-job or already-non-pending application → `errInvalidInput`,
  matching Phase 1's existing status-transition error surfacing.
- Document generation failing inside `Prepare` → surfaced verbatim (same
  as Phase 4's own error handling — an HTML-only partial success is still
  reported, not swallowed).
- Browser launch failing (no Chrome/Chromium reachable) → surfaced
  verbatim, same as Phase 4's `RenderPDF`.
- `send` on an application not in `pending` → the existing
  `applications.ErrInvalidTransition` error, unchanged from Phase 1.

## Testing

- `internal/apply/apply_test.go` — `Prepare` reuses existing documents on
  a second call rather than regenerating (asserted via vault document
  count staying at 2, not becoming 4).
- `internal/apply/browser_test.go` — `OpenForApplication` is inherently
  hard to assert on without visually inspecting a real window; the test
  verifies it returns no error against a real launched browser (same
  environmental note as Phase 4's `pdf_test.go` — requires a reachable
  Chrome/Chromium binary) and, more importantly, a **static code-level
  check**: a test that greps `browser.go`'s source for the strings
  `"Click"`, `"MustClick"`, `".Submit"` and fails if any are found — a
  literal, mechanical enforcement that the "never submits" guarantee holds
  by construction, not just by review. (This is an unusual test — testing
  the absence of a capability in the source itself — justified because the
  property being guaranteed is exactly "this code cannot click anything,"
  which a behavioral test alone can't fully prove the way a source-grep
  can for a file that's supposed to contain zero click calls.)
- `cmd/monoagentcli/application_apply_test.go` — CLI tests for `apply`'s
  confirm/auto mode branching and `send`'s status transition, using a
  swappable "prompt" function (mirroring Phase 4/5's swappable-dependency
  pattern) so the confirm-mode y/N prompt is testable without real stdin.

## Out of Scope (this phase)

- **Adaptive LLM-driven form auto-fill** via `monomind`'s tool-bridge — a
  real, valuable future capability, deliberately not built now (see the
  scoping decision above). If pursued later, it should get its own
  design spec, a local static-HTML test fixture standing in for a generic
  ATS form (the same honest-testing approach Phase 2 used for LinkedIn's
  markup), and should preserve this phase's absolute "no programmatic
  submit" invariant as a code-level constraint on whatever tools the
  agent is given (e.g. never exposing a "submit"-capable tool to it at
  all, not just prompting it not to use one).
- **Tender document-package staging** — the original 6-phase plan
  mentioned this; given jobs are this phase's fully-scoped, testable
  target and tenders would need their own submission-channel research
  (portal upload vs. email vs. physical), tender apply-automation is
  future work, not silently folded into this phase's job-only testing.
- **Generating CV/cover-letter content from the job posting automatically**
  — `Prepare` takes already-assembled structured data as input (matching
  Phase 4's own scope boundary); wiring Phase 5's scoring output or a new
  content-generation step into an automatic data-file producer is a
  distinct, not-yet-designed decision.
- **A Wails GUI Applications board** — the original phase list included
  this. Given it's a genuinely separate engineering domain (React
  frontend, Wails IPC bindings) that hasn't been researched at all in this
  session (every phase so far has been Go backend + CLI), it gets its own
  design pass as an explicit follow-up after this backend phase, rather
  than a rushed, under-researched implementation appended here.
