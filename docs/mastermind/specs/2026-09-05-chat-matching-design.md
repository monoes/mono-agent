# Chat-Powered Job Matching — Design Spec

Date: 2026-09-05
Status: Approved (Phase 5 of the "ultimate job applier" feature)
Branch: `worktree-feature+job-tender-applications`

## Context

Phase 5 of the multi-phase feature. Per the original phase breakdown:
"chat-powered matching (gated fit-scoring against the profile knowledge
graph)." Given Phases 1-4 (application pipeline, job discovery, profile
knowledge ingestion, document generation), this phase adds the qualitative
layer: for a pending job application, decide how good a fit it is against
the user's profile, using the research-approved gated multi-dimension
rubric (ai-job-search: hard eligibility/language veto gates, then weighted
dimensions, then a verdict band).

**Scope boundary**: this phase scores **job** applications only, using a
job-specific rubric researched in Phase 1. Tender fit-scoring would need
its own rubric (different dimensions — certifications, financial capacity,
technical capability) and is out of scope here, matching this project's
established pattern of not silently expanding scope to a kind that wasn't
concretely specified for the feature at hand.

### Critical finding that reshapes this phase's architecture

Investigating how to call an LLM from mono-agent surfaced that **the old
direct-AI-provider node types are deprecated**: `internal/ai/nodes/deprecated.go`
registers `ai.chat`, `ai.classify`, `ai.extract`, `ai.embed`, `ai.transform`
etc. as fail-fast stubs (see `internal/workflow/schemas/README.md`'s
explanation: "the local-agent transition"). The current, non-deprecated
pattern is `agent.ask` (`internal/nodes/agent/ask.go`): delegate to a
**locally-installed coding agent runtime** (`claude`, `codex`, `kimi`, ...)
via `monomind.Exec` (the same Agent Exec Protocol subprocess bridge already
used elsewhere in this codebase, e.g. `internal/monomind/exec.go`), not a
direct API call to an LLM provider. **This phase must follow that pattern**
— it calls `monomind.Exec` directly (the same function `agent.ask` itself
calls), not a deprecated `ai.*` node, and not a new direct-provider
integration.

### Reused infrastructure (verified by reading the code)

- `internal/monomind.Exec(ctx, ExecOptions{Bin, Runtime, Model, Prompt,
  SystemPrompt, Timeout}, onEvent) (*TurnResult, error)` and
  `monomind.Ensure(ctx) (bin string, info *VersionInfo, err error)` —
  exactly as `agent.ask` already uses them.
- `applications.Store.Get`/`AddTag` (Phase 1) — fetch the job to score,
  tag it with the resulting verdict.
- `monomind.SearchKnowledge` (Phase 3) — retrieve relevant profile
  excerpts (skills, experience) to ground the scoring prompt.
- ai-job-search's researched rubric (already approved in Phase 1's
  research phase): hard eligibility/language veto gates first, then
  weighted dimensions (Technical 30%, Experience 25%, Behavioral 15%,
  Career 30%, Location pass/fail), verdict bands (Strong/Good/Moderate/
  Weak/Poor Fit).
- ai-job-search's anti-fabrication grounding principle — the prompt
  instructs the agent to base claims only on the retrieved profile
  excerpts actually provided, not to invent experience.

## Requirements

- Given a job application, retrieve relevant profile context and produce a
  structured fit verdict (eligibility/language gates, per-dimension
  scores, overall score, verdict band, rationale).
- Store the verdict (for history — re-evaluating should not destroy the
  prior verdict) and tag the application for easy filtering.
- CLI command for one application and for batch-evaluating all
  not-yet-evaluated pending job applications.
- A workflow node, so a future automation phase can trigger evaluation
  programmatically.

## Architecture

### `application_evaluations` table (new, append-only like the status log)

```sql
-- data/migrations/036_application_evaluations.sql
CREATE TABLE IF NOT EXISTS application_evaluations (
    id                TEXT PRIMARY KEY,
    application_id    TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    runtime           TEXT NOT NULL,
    eligibility_pass  BOOLEAN NOT NULL,
    language_pass     BOOLEAN NOT NULL,
    technical_score   REAL NOT NULL DEFAULT 0,
    experience_score  REAL NOT NULL DEFAULT 0,
    behavioral_score  REAL NOT NULL DEFAULT 0,
    career_score      REAL NOT NULL DEFAULT 0,
    location_pass     BOOLEAN NOT NULL,
    overall_score     REAL NOT NULL DEFAULT 0,
    verdict           TEXT NOT NULL,
    rationale         TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_application_evaluations_app ON application_evaluations(application_id);
```

Re-evaluating an application appends a new row rather than updating one —
the same append-only-ledger philosophy Phase 1's `application_status_log`
already established, so a fit verdict's history is inspectable (e.g. a
verdict changing after the profile is updated with new skills).

### Data types and evaluation (new package `internal/matching`)

```go
package matching

// FitVerdict is one scoring result for a job application.
type FitVerdict struct {
	EligibilityPass bool
	LanguagePass    bool
	TechnicalScore  float64 // 0-100, weight 30%
	ExperienceScore float64 // 0-100, weight 25%
	BehavioralScore float64 // 0-100, weight 15%
	CareerScore     float64 // 0-100, weight 30%
	LocationPass    bool
	OverallScore    float64 // weighted sum, 0 if either gate fails
	Verdict         string  // "Strong Fit"|"Good Fit"|"Moderate Fit"|"Weak Fit"|"Poor Fit"|"Ineligible"
	Rationale       string
}

// Evaluate scores applicationID (must be kind=job) against profileID's
// ingested knowledge, via a local agent runtime (delegated through
// monomind.Exec — the same mechanism agent.ask uses, not a direct LLM
// provider call). Persists the result as a new application_evaluations
// row and tags the application "fit:<verdict-slug>". Returns the verdict.
func Evaluate(ctx context.Context, db *sql.DB, profileID, applicationID, runtime string) (*FitVerdict, error)
```

### Prompt construction

1. `applications.Store.Get(ctx, profileID, applicationID)` → the job's
   title, company, description.
2. `monomind.SearchKnowledge(ctx, db, profileID, job.Title + " " + job.Description[:N])`
   (a bounded excerpt of the description, not the whole thing, to keep the
   search query reasonable) → up to a handful of relevant profile excerpts.
3. Build a single prompt combining: the rubric (hard gates then weighted
   dimensions, spelled out explicitly, not left to the agent's own
   judgment of what "fit" means), the job's title/company/description, the
   retrieved profile excerpts (explicitly labeled as "the only information
   you have about this candidate — do not invent experience not shown
   here", per ai-job-search's anti-fabrication principle), and an explicit
   instruction to respond with **only** a JSON object matching
   `FitVerdict`'s field names (snake_case, since that's this codebase's
   JSON convention elsewhere) — no markdown fencing, no prose outside the
   JSON.
4. Call `monomind.Exec` with that prompt, `runtime` (default `"claude"`,
   overridable), a reasonable timeout (this is a single reasoning turn,
   not a coding task — 120s default, versus `agent.ask`'s 300s default,
   since there's no file editing or tool use expected here beyond the one
   text response).
5. Parse the response text as JSON into `FitVerdict`. Local agent CLIs
   sometimes wrap JSON in prose or a markdown code fence despite
   instructions not to — extract the first balanced `{...}` block from the
   response before parsing, rather than requiring an exact-JSON-only
   response (a pragmatic robustness measure, not a rubric change).

### Kind guard

`Evaluate` returns an error immediately (no agent call made) if
`application.Kind != KindJob` — this phase's rubric is job-specific;
calling it on a tender would silently produce a meaningless score against
the wrong rubric, which is worse than a clear early error.

### CLI

```
monoagentcli application evaluate <id> [--runtime claude]
monoagentcli application evaluate-pending [--runtime claude] [--limit N]
```

`evaluate-pending` lists `pending`, `job`-kind applications with no prior
evaluation (a simple `NOT EXISTS` check against `application_evaluations`)
and evaluates each **sequentially** (not concurrent local-agent
subprocesses — each is a real CLI process invocation with its own
resource footprint; concurrency here is unwarranted complexity for this
phase's scope, unlike Phase 2's HTTP-based discovery batch, which had no
such per-call resource cost). Reports a summary: evaluated count, verdict
distribution.

### Workflow node

`applications.evaluate` (`internal/nodes/matching`), config: `application_id`
(required), `runtime` (default `claude`), `profile_id` (default `default`).
Wraps `matching.Evaluate` directly.

## Data Flow

1. CLI/node provides an `application_id` (+ optional `runtime`).
2. `Evaluate` loads the application, rejects non-job kinds immediately.
3. Retrieves profile context via `SearchKnowledge`.
4. Builds and sends the rubric+job+context prompt via `monomind.Exec`.
5. Parses the JSON verdict out of the response.
6. Persists a new `application_evaluations` row and tags the application
   `fit:<verdict-slug>` (e.g. `fit:strong-fit`) via `applications.Store.AddTag`
   — enabling `monoagentcli application list --tag fit:strong-fit` to
   surface the best matches using Phase 1's existing filter, no new query
   surface needed.
7. Returns the verdict to the caller.

## Error Handling

- Non-job application → immediate error, no agent call made (see Kind
  guard above).
- `monomind.Ensure` failing (no compatible local agent runtime installed)
  → surfaced verbatim, matching `agent.ask`'s own error handling for the
  same failure.
- Agent response isn't parseable as the expected JSON shape (even after
  the balanced-brace extraction) → a clear error naming what was expected
  vs. received (truncated), not a silent zero-value verdict — a
  fit-scoring feature that silently produces meaningless zeros is worse
  than one that visibly fails.
- `SearchKnowledge` returning zero results (e.g. no profile documents
  uploaded yet) is not an error — the prompt is built with an explicit
  "no profile information was found; score conservatively and note this
  in the rationale" instruction rather than failing outright, since a
  user should be able to see *some* result and be told why it's
  low-confidence, rather than being blocked entirely.

## Testing

- `internal/matching/matching_test.go` — prompt construction (given a job
  + fake excerpts, assert the prompt contains all required pieces:
  rubric text, job description, excerpts, JSON-only instruction),
  response parsing (valid JSON, JSON wrapped in markdown fencing, JSON
  wrapped in prose, malformed JSON → clear error), the kind-guard
  rejection, and `Evaluate`'s end-to-end flow against an injected fake
  `monomind.Exec` call (via a swappable package-level function variable,
  the same pattern Phase 4's `RenderPDFFunc` already established for
  exactly this kind of external-process dependency) plus a fake
  `SearchKnowledge` (also swappable) — no real agent runtime or monomind
  binary needed for these tests.
- `cmd/monoagentcli/application_evaluate_test.go` — CLI tests using the
  same injected fakes.

## Out of Scope (this phase)

- Tender-kind fit scoring (a different rubric — future work).
- Concurrent/batched agent calls for `evaluate-pending` — sequential is
  sufficient at current expected volumes.
- Automatically re-evaluating when the profile changes — evaluation is
  always explicit (a CLI/node call), never triggered implicitly by a
  document upload.
- Using the verdict to drive anything automatically (e.g. auto-rejecting
  Poor Fit applications) — that's Phase 6's job (apply automation), which
  will read these tags/verdicts as an input, not this phase's job to act
  on them.
