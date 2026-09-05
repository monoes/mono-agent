# Chat-Powered Job Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (`mastermind-taskdev` is not installed in this project — the controlling session acts as the task dispatcher directly via the Agent tool, per the prior four phase plans' precedent.)

**Goal:** Score a pending job application's fit against the profile's ingested knowledge using a gated multi-dimension rubric, delegated to a local agent runtime via `monomind.Exec` (the sanctioned `agent.ask` pattern, not a deprecated direct-LLM node).

**Architecture:** A new `internal/matching` package (pure prompt-building/response-parsing logic + an `Evaluate` orchestration function with swappable dependency vars for testability) on top of Phase 1's `applications.Store` and Phase 3's `monomind.SearchKnowledge`, plus a CLI command and a workflow node.

**Tech Stack:** `internal/monomind.Exec`/`Ensure` (existing, the Agent Exec Protocol bridge). No new dependencies.

## Global Constraints

- Go toolchain at `~/.local/go/bin`, not on default PATH: `export PATH="$HOME/.local/go/bin:$PATH"` before any `go` command.
- Migrations: numbered SQL files in `data/migrations/`. Next number is 036.
- AI delegation MUST go through `monomind.Exec`/`monomind.Ensure` (mirroring `internal/nodes/agent/ask.go`'s exact usage), never a deprecated `ai.*` node and never a new direct LLM-provider integration.
- Every external-process dependency (`monomind.Exec`, `monomind.Ensure`, `monomind.SearchKnowledge`) is wrapped in a package-level swappable var (`matching.ExecFunc`, `matching.EnsureFunc`, `matching.SearchKnowledgeFunc`) initialized to the real function — the same pattern `internal/documents.RenderPDFFunc` already established in Phase 4 — so `Evaluate`'s orchestration logic is fully unit-testable without a real agent runtime or monomind binary.
- `application_evaluations` is append-only (like `application_status_log`) — re-evaluating inserts a new row, never updates one.
- This phase scores **job**-kind applications only; `Evaluate` rejects any other kind immediately, before any agent call.
- TDD: every behavior gets a failing test before its implementation.
- Commit after every task with a conventional-commits message ending with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

## File Structure

| File | Responsibility |
|---|---|
| `data/migrations/036_application_evaluations.sql` | New `application_evaluations` table. |
| `internal/matching/matching.go` | `FitVerdict`, `buildPrompt`, `parseVerdict`, `extractJSON`, `slugify`. |
| `internal/matching/matching_test.go` | Tests for the above (pure logic, no I/O). |
| `internal/matching/evaluate.go` | `Evaluate`, the swappable `ExecFunc`/`EnsureFunc`/`SearchKnowledgeFunc` vars. |
| `internal/matching/evaluate_test.go` | Tests using injected fakes. |
| `cmd/monoagentcli/application_evaluate.go` | `application evaluate`/`evaluate-pending` commands. |
| `cmd/monoagentcli/application_evaluate_test.go` | CLI integration tests. |
| `cmd/monoagentcli/application.go` | Modified: register the new subcommands. |
| `internal/nodes/matching/evaluate.go` / `evaluate_schema.go` | `applications.evaluate` workflow node. |
| `internal/tools/schemagen/manifest.go` | Modified: one new manifest entry. |
| `internal/noderegistry/registry.go` | Modified: register the new node package. |

---

### Task 0: `FitVerdict`, prompt building, response parsing

**Files:**
- Create: `data/migrations/036_application_evaluations.sql`
- Create: `internal/matching/matching.go`
- Create: `internal/matching/matching_test.go`

**Interfaces:**
- Consumes: `applications.{Application,JobDetails}` (Phase 1), `monomind.KnowledgeResult` (Phase 3) — only as parameter/field types, no calls to either package.
- Produces: `matching.FitVerdict` (with `json:"..."` tags), `matching.buildPrompt(app *applications.Application, excerpts []monomind.KnowledgeResult) string`, `matching.parseVerdict(responseText string) (*FitVerdict, error)`, `matching.slugify(s string) string`. Consumed by Task 1 (`Evaluate`).

- [ ] **Step 1: Write the migration**

```sql
-- data/migrations/036_application_evaluations.sql
-- Append-only fit-scoring history for job applications, mirroring
-- application_status_log's philosophy: re-evaluating never overwrites a
-- prior verdict, it adds a new row.

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

- [ ] **Step 2: Write the failing tests**

```go
// internal/matching/matching_test.go
package matching

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
)

func testJob() *applications.Application {
	return &applications.Application{
		ID: "app-1", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", Description: "Build APIs in Go."},
	}
}

func TestBuildPromptIncludesJobAndExcerpts(t *testing.T) {
	excerpts := []monomind.KnowledgeResult{
		{Path: "/vault/resume.txt", Excerpt: "8 years of Go experience.", Score: 0.9},
	}
	prompt := buildPrompt(testJob(), excerpts)
	for _, want := range []string{"Backend Engineer", "Acme", "Build APIs in Go", "8 years of Go experience", "JSON"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildPromptNoExcerptsStillValid(t *testing.T) {
	prompt := buildPrompt(testJob(), nil)
	if !strings.Contains(prompt, "no excerpts") && !strings.Contains(prompt, "conservatively") {
		t.Errorf("expected prompt to instruct conservative scoring when no excerpts are available, got:\n%s", prompt)
	}
}

func TestParseVerdictPlainJSON(t *testing.T) {
	v, err := parseVerdict(`{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":80,"experience_score":70,"behavioral_score":60,"career_score":90,"overall_score":79.5,"verdict":"Good Fit","rationale":"Strong Go background."}`)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Verdict != "Good Fit" || v.OverallScore != 79.5 {
		t.Fatalf("unexpected verdict: %+v", v)
	}
}

func TestParseVerdictWrappedInMarkdownFence(t *testing.T) {
	resp := "Here is my assessment:\n```json\n{\"eligibility_pass\":true,\"language_pass\":true,\"location_pass\":true,\"technical_score\":50,\"experience_score\":50,\"behavioral_score\":50,\"career_score\":50,\"overall_score\":50,\"verdict\":\"Moderate Fit\",\"rationale\":\"Some overlap, e.g. {backend} skills.\"}\n```\nLet me know if you need more."
	v, err := parseVerdict(resp)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Verdict != "Moderate Fit" {
		t.Fatalf("unexpected verdict: %+v", v)
	}
	if !strings.Contains(v.Rationale, "{backend}") {
		t.Fatalf("expected rationale containing a brace character to survive extraction intact, got %q", v.Rationale)
	}
}

func TestParseVerdictNoJSONFound(t *testing.T) {
	if _, err := parseVerdict("I cannot evaluate this."); err == nil {
		t.Fatal("expected error when no JSON object is present, got nil")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Strong Fit":   "strong-fit",
		"Poor Fit":     "poor-fit",
		"Ineligible":   "ineligible",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/matching/... -v`
Expected: FAIL — package doesn't exist yet (no non-test Go files).

- [ ] **Step 4: Write matching.go**

```go
// internal/matching/matching.go

// Package matching scores a job application's fit against the profile's
// ingested knowledge, delegating the scoring decision to a local agent
// runtime via monomind.Exec (the same pattern internal/nodes/agent's
// agent.ask node uses) rather than a direct LLM-provider call. See
// docs/mastermind/specs/2026-09-05-chat-matching-design.md.
package matching

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
)

// FitVerdict is one scoring result for a job application.
type FitVerdict struct {
	EligibilityPass bool    `json:"eligibility_pass"`
	LanguagePass    bool    `json:"language_pass"`
	TechnicalScore  float64 `json:"technical_score"`
	ExperienceScore float64 `json:"experience_score"`
	BehavioralScore float64 `json:"behavioral_score"`
	CareerScore     float64 `json:"career_score"`
	LocationPass    bool    `json:"location_pass"`
	OverallScore    float64 `json:"overall_score"`
	Verdict         string  `json:"verdict"`
	Rationale       string  `json:"rationale"`
}

const rubricInstructions = `You are scoring a job application for fit against a candidate's profile.

Score in two phases:
1. HARD GATES (pass/fail, evaluated first):
   - Eligibility: does the candidate appear eligible to work in the job's location based on the profile information? (if unknown, assume pass)
   - Language: does the profile show proficiency in any language explicitly required by the job posting? (if the posting states no specific requirement, pass)
   - Location: is the job's location compatible with the candidate profile (remote, or a location the candidate could work from)? (if unclear, pass)
   If either eligibility or language fails, set overall_score to 0 and verdict to "Ineligible" and skip the dimension scores below (still include all fields, using 0 for unscored dimensions).

2. WEIGHTED DIMENSIONS (0-100 each, only if both eligibility and language gates pass):
   - technical_score (weight 30%): alignment of the candidate's technical skills/experience with the job's requirements.
   - experience_score (weight 25%): years and seniority level match.
   - behavioral_score (weight 15%): soft-skill/culture signals visible in the profile relative to what the posting implies.
   - career_score (weight 30%): whether this role is a sensible next step given the candidate's trajectory.
   overall_score = 0.30*technical_score + 0.25*experience_score + 0.15*behavioral_score + 0.30*career_score.
   verdict: "Strong Fit" (overall_score >= 80), "Good Fit" (>= 65), "Moderate Fit" (>= 50), "Weak Fit" (>= 30), "Poor Fit" (< 30).

Base every claim ONLY on the CANDIDATE PROFILE EXCERPTS section below — never invent experience, skills, or credentials not shown there.

Respond with ONLY a single JSON object, no markdown fencing, no other text, with exactly these fields:
{"eligibility_pass": bool, "language_pass": bool, "location_pass": bool, "technical_score": number, "experience_score": number, "behavioral_score": number, "career_score": number, "overall_score": number, "verdict": string, "rationale": string}`

// buildPrompt assembles the full evaluation prompt for job app, grounded
// in excerpts retrieved from the profile's knowledge base. If excerpts is
// empty, the prompt explicitly instructs conservative scoring rather than
// silently proceeding as if the profile were fully known.
func buildPrompt(app *applications.Application, excerpts []monomind.KnowledgeResult) string {
	var b strings.Builder
	b.WriteString(rubricInstructions)
	b.WriteString("\n\nJOB POSTING:\n")
	fmt.Fprintf(&b, "Title: %s\nCompany: %s\nLocation: %s\n\n%s\n",
		app.Job.Title, app.Job.Company, app.Job.Location, app.Job.Description)

	b.WriteString("\nCANDIDATE PROFILE EXCERPTS:\n")
	if len(excerpts) == 0 {
		b.WriteString("(no excerpts were found — no profile documents may be uploaded yet; score conservatively and say so explicitly in the rationale)\n")
	} else {
		for _, e := range excerpts {
			fmt.Fprintf(&b, "- (%s) %s\n", e.Path, e.Excerpt)
		}
	}
	return b.String()
}

// extractJSON returns the first balanced top-level {...} object found in
// s, correctly skipping over braces that appear inside quoted string
// values (e.g. rationale text containing "{" or "}"). Local agent CLIs
// sometimes wrap their JSON response in prose or a markdown code fence
// despite being instructed not to — this makes parseVerdict robust to
// that without loosening the rubric itself.
func extractJSON(s string) (string, error) {
	start := strings.Index(s, "{")
	if start == -1 {
		return "", fmt.Errorf("matching: no JSON object found in response")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("matching: no balanced JSON object found in response")
}

// parseVerdict extracts and decodes a FitVerdict from an agent's raw
// response text.
func parseVerdict(responseText string) (*FitVerdict, error) {
	jsonStr, err := extractJSON(responseText)
	if err != nil {
		return nil, fmt.Errorf("matching: parsing verdict: %w (response was: %.200s)", err, responseText)
	}
	var v FitVerdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, fmt.Errorf("matching: decoding verdict JSON: %w (extracted: %.200s)", err, jsonStr)
	}
	return &v, nil
}

// slugify lowercases s and replaces spaces with hyphens, for use as a tag
// suffix (e.g. "Strong Fit" -> "strong-fit", tagged as "fit:strong-fit").
func slugify(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/matching/... -v`
Expected: PASS (6 tests).

- [ ] **Step 6: Commit**

```bash
git add data/migrations/036_application_evaluations.sql internal/matching/matching.go internal/matching/matching_test.go
git commit -m "$(cat <<'EOF'
feat(matching): add FitVerdict, prompt building, and response parsing

Gated multi-dimension rubric (eligibility/language/location gates, then
weighted technical/experience/behavioral/career dimensions) prompted to
a local agent; JSON extraction is brace-depth-aware and string-literal-safe
so a rationale containing braces doesn't truncate parsing.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 1: `Evaluate` orchestration

**Files:**
- Create: `internal/matching/evaluate.go`
- Create: `internal/matching/evaluate_test.go`

**Interfaces:**
- Consumes: `matching.{FitVerdict,buildPrompt,parseVerdict,slugify}` (Task 0), `applications.{Store,KindJob,ErrNotFound}` (Phase 1), `monomind.{Exec,ExecOptions,Event,TurnResult,Ensure,VersionInfo,SearchKnowledge,KnowledgeResult}` (existing/Phase 3).
- Produces: `matching.ExecFunc`, `matching.EnsureFunc`, `matching.SearchKnowledgeFunc` (swappable vars), `matching.Evaluate(ctx context.Context, db *sql.DB, profileID, applicationID, runtime string) (*FitVerdict, error)`. Consumed by Task 2 (CLI), Task 3 (node).

- [ ] **Step 1: Write the failing tests**

```go
// internal/matching/evaluate_test.go
package matching

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "matching-test.db")
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

func fakeGoodResponse(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
	return &monomind.TurnResult{
		ResultText: `{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":85,"experience_score":80,"behavioral_score":70,"career_score":90,"overall_score":84.5,"verdict":"Strong Fit","rationale":"Great match."}`,
	}, nil
}

func setupEvaluateTest(t *testing.T) (db *sql.DB, applicationID string) {
	t.Helper()
	db = newTestDB(t)
	store := applications.NewStore(db)
	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1", Description: "Go backend role."},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	origEnsure := EnsureFunc
	EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { EnsureFunc = origEnsure })

	origSearch := SearchKnowledgeFunc
	SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return []monomind.KnowledgeResult{{Path: "/vault/resume.txt", Excerpt: "8 years of Go experience.", Score: 0.9}}, nil
	}
	t.Cleanup(func() { SearchKnowledgeFunc = origSearch })

	return db, app.ID
}

func TestEvaluateCreatesEvaluationAndTag(t *testing.T) {
	db, applicationID := setupEvaluateTest(t)
	origExec := ExecFunc
	ExecFunc = fakeGoodResponse
	t.Cleanup(func() { ExecFunc = origExec })

	verdict, err := Evaluate(context.Background(), db, "default", applicationID, "claude")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Verdict != "Strong Fit" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}

	store := applications.NewStore(db)
	app, err := store.Get(context.Background(), "default", applicationID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	found := false
	for _, tag := range app.Tags {
		if tag == "fit:strong-fit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tag fit:strong-fit, got tags %v", app.Tags)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_evaluations WHERE application_id = ?`, applicationID).Scan(&count); err != nil {
		t.Fatalf("counting evaluations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 evaluation row, got %d", count)
	}
}

func TestEvaluateRejectsNonJobApplications(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db)
	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindTender,
		Tender: &applications.TenderDetails{Title: "Road Tender", IssuingOrg: "Ministry", URL: "https://t.example/1", SubmissionDeadline: "2026-12-01"},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Evaluate(context.Background(), db, "default", app.ID, "claude"); err == nil {
		t.Fatal("expected error evaluating a tender application, got nil")
	}
}

func TestEvaluateReEvaluationAppendsNewRow(t *testing.T) {
	db, applicationID := setupEvaluateTest(t)
	origExec := ExecFunc
	ExecFunc = fakeGoodResponse
	t.Cleanup(func() { ExecFunc = origExec })

	if _, err := Evaluate(context.Background(), db, "default", applicationID, "claude"); err != nil {
		t.Fatalf("Evaluate (1st): %v", err)
	}
	if _, err := Evaluate(context.Background(), db, "default", applicationID, "claude"); err != nil {
		t.Fatalf("Evaluate (2nd): %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_evaluations WHERE application_id = ?`, applicationID).Scan(&count); err != nil {
		t.Fatalf("counting evaluations: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 evaluation rows after re-evaluating, got %d", count)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/matching/... -run TestEvaluate -v`
Expected: FAIL with "undefined: EnsureFunc" / "undefined: Evaluate" (compile errors).

- [ ] **Step 3: Write evaluate.go**

```go
// internal/matching/evaluate.go
package matching

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
)

// ExecFunc, EnsureFunc, and SearchKnowledgeFunc are Evaluate's external
// dependencies, exposed as swappable package-level variables (the same
// pattern internal/documents.RenderPDFFunc established in Phase 4) so
// tests can inject fakes without a real agent runtime or monomind binary.
var (
	ExecFunc            = monomind.Exec
	EnsureFunc          = monomind.Ensure
	SearchKnowledgeFunc = monomind.SearchKnowledge
)

// evaluateTimeout bounds one evaluation's agent turn. Shorter than
// agent.ask's 300s default since this is a single reasoning response, not
// a coding task with file edits or tool use.
const evaluateTimeout = 120 * time.Second

// Evaluate scores applicationID (must be kind=job) against profileID's
// ingested knowledge, via a local agent runtime delegated through
// ExecFunc. Persists the result as a new application_evaluations row and
// tags the application "fit:<verdict-slug>". See the design spec for the
// full rubric and rationale.
func Evaluate(ctx context.Context, db *sql.DB, profileID, applicationID, runtime string) (*FitVerdict, error) {
	store := applications.NewStore(db)
	app, err := store.Get(ctx, profileID, applicationID)
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: %w", err)
	}
	if app.Kind != applications.KindJob {
		return nil, fmt.Errorf("matching.Evaluate: only job-kind applications can be scored by this rubric, got kind %q", app.Kind)
	}

	bin, _, err := EnsureFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: %w", err)
	}

	query := app.Job.Title
	if len(app.Job.Description) > 0 {
		descLen := len(app.Job.Description)
		if descLen > 200 {
			descLen = 200
		}
		query += " " + app.Job.Description[:descLen]
	}
	excerpts, err := SearchKnowledgeFunc(ctx, db, profileID, query)
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: searching profile knowledge: %w", err)
	}

	prompt := buildPrompt(app, excerpts)
	res, err := ExecFunc(ctx, monomind.ExecOptions{
		Bin:     bin,
		Runtime: runtime,
		Prompt:  prompt,
		Timeout: evaluateTimeout,
	}, func(ev monomind.Event) {})
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: agent exec: %w", err)
	}
	if res.Err != nil {
		return nil, fmt.Errorf("matching.Evaluate: agent turn failed: %s", res.Err.Error())
	}

	verdict, err := parseVerdict(res.ResultText)
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO application_evaluations (id, application_id, runtime, eligibility_pass, language_pass,
		        technical_score, experience_score, behavioral_score, career_score, location_pass,
		        overall_score, verdict, rationale, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), applicationID, runtime, verdict.EligibilityPass, verdict.LanguagePass,
		verdict.TechnicalScore, verdict.ExperienceScore, verdict.BehavioralScore, verdict.CareerScore, verdict.LocationPass,
		verdict.OverallScore, verdict.Verdict, verdict.Rationale, now,
	)
	if err != nil {
		return nil, fmt.Errorf("matching.Evaluate: persisting evaluation: %w", err)
	}

	if err := store.AddTag(ctx, profileID, applicationID, "fit:"+slugify(verdict.Verdict)); err != nil {
		return nil, fmt.Errorf("matching.Evaluate: tagging application: %w", err)
	}

	return verdict, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/matching/... -v 2>&1 | tail -30`
Expected: PASS — all tests from Tasks 0-1 (none require a real agent runtime or monomind binary).

- [ ] **Step 5: Commit**

```bash
git add internal/matching/evaluate.go internal/matching/evaluate_test.go
git commit -m "$(cat <<'EOF'
feat(matching): add Evaluate orchestration

Ties buildPrompt/parseVerdict to a real (or, in tests, injected-fake)
monomind.Ensure/Exec call and monomind.SearchKnowledge, persisting an
append-only evaluation row and a fit:<verdict> tag on the application.
Rejects non-job kinds before any agent call is made.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 2: CLI commands

**Files:**
- Create: `cmd/monoagentcli/application_evaluate.go`
- Create: `cmd/monoagentcli/application_evaluate_test.go`
- Modify: `cmd/monoagentcli/application.go`

**Interfaces:**
- Consumes: `matching.{Evaluate,ExecFunc,EnsureFunc,SearchKnowledgeFunc,FitVerdict}` (Tasks 0-1), `applications.{Store,ListFilter,KindJob,StatusPending}` (Phase 1); `initDB`, `errNotFound`, `errInvalidInput` (existing CLI conventions).
- Produces: `newApplicationEvaluateCmd`, `newApplicationEvaluatePendingCmd`, added to `newApplicationCmd`'s subcommand list. No other task depends on this.

- [ ] **Step 1: Write the failing CLI tests**

```go
// cmd/monoagentcli/application_evaluate_test.go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/matching"
	"github.com/monoes/mono-agent/internal/monomind"
)

func runApplicationEvaluateCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func fakeEvaluateDeps(t *testing.T) {
	t.Helper()
	origEnsure := matching.EnsureFunc
	matching.EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { matching.EnsureFunc = origEnsure })

	origSearch := matching.SearchKnowledgeFunc
	matching.SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { matching.SearchKnowledgeFunc = origSearch })

	origExec := matching.ExecFunc
	matching.ExecFunc = func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		return &monomind.TurnResult{ResultText: `{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":80,"experience_score":80,"behavioral_score":80,"career_score":80,"overall_score":80,"verdict":"Strong Fit","rationale":"Good match."}`}, nil
	}
	t.Cleanup(func() { matching.ExecFunc = origExec })
}

func TestApplicationEvaluate(t *testing.T) {
	fakeEvaluateDeps(t)
	dbPath := newApplicationCLITestDB(t)

	addOut, err := runApplicationEvaluateCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "https://acme.example/1")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}
	if id == "" {
		t.Fatalf("could not extract id from add output: %s", addOut)
	}

	evalOut, err := runApplicationEvaluateCmd(t, dbPath, "evaluate", id)
	if err != nil {
		t.Fatalf("application evaluate: %v (%s)", err, evalOut)
	}
	if !strings.Contains(evalOut, "Strong Fit") {
		t.Fatalf("expected verdict in output, got: %s", evalOut)
	}
}

func TestApplicationEvaluateRejectsNotFound(t *testing.T) {
	fakeEvaluateDeps(t)
	dbPath := newApplicationCLITestDB(t)
	if _, err := runApplicationEvaluateCmd(t, dbPath, "evaluate", "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestApplicationEvaluate -v`
Expected: FAIL — `application add` doesn't have a `--title` flag issue is already resolved (Phase 2 added it), so this fails specifically on the unknown "evaluate" subcommand.

- [ ] **Step 3: Write the commands**

```go
// cmd/monoagentcli/application_evaluate.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/matching"

	"github.com/spf13/cobra"
)

func newApplicationEvaluateCmd(cfg *globalConfig) *cobra.Command {
	var runtime string
	cmd := &cobra.Command{
		Use:     "evaluate <id>",
		Short:   "Score a job application's fit against your profile using a local AI agent",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli application evaluate 1c2e... --runtime claude`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			verdict, err := matching.Evaluate(cmd.Context(), db.DB, cfg.ProfileID, args[0], runtime)
			if err != nil {
				return fmt.Errorf("evaluating application: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(verdict)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (overall %.1f) — %s\n", verdict.Verdict, verdict.OverallScore, verdict.Rationale)
			return nil
		},
	}
	cmd.Flags().StringVar(&runtime, "runtime", "claude", "Local agent runtime to use (see `monoagentcli agent scan --installed`)")
	return cmd
}

func newApplicationEvaluatePendingCmd(cfg *globalConfig) *cobra.Command {
	var runtime string
	var limit int
	cmd := &cobra.Command{
		Use:   "evaluate-pending",
		Short: "Evaluate every pending job application with no evaluation yet",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			store := applications.NewStore(db.DB)
			apps, err := store.List(cmd.Context(), cfg.ProfileID, applications.ListFilter{
				Kind: applications.KindJob, Status: applications.StatusPending,
			})
			if err != nil {
				return fmt.Errorf("listing pending applications: %w", err)
			}

			verdictCounts := map[string]int{}
			evaluated := 0
			for _, app := range apps {
				if limit > 0 && evaluated >= limit {
					break
				}
				var already int
				if err := db.DB.QueryRowContext(cmd.Context(),
					`SELECT COUNT(*) FROM application_evaluations WHERE application_id = ?`, app.ID,
				).Scan(&already); err != nil {
					return fmt.Errorf("checking existing evaluations: %w", err)
				}
				if already > 0 {
					continue
				}
				verdict, err := matching.Evaluate(cmd.Context(), db.DB, cfg.ProfileID, app.ID, runtime)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: evaluating %s failed: %v\n", app.ID, err)
					continue
				}
				verdictCounts[verdict.Verdict]++
				evaluated++
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]interface{}{"evaluated": evaluated, "verdicts": verdictCounts})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Evaluated %d application(s): %v\n", evaluated, verdictCounts)
			return nil
		},
	}
	cmd.Flags().StringVar(&runtime, "runtime", "claude", "Local agent runtime to use")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum applications to evaluate (0 = no limit)")
	return cmd
}
```

- [ ] **Step 4: Register the subcommands**

In `cmd/monoagentcli/application.go`, add to `newApplicationCmd`'s `cmd.AddCommand(...)` list:

```go
	cmd.AddCommand(
		newApplicationAddCmd(cfg),
		newApplicationListCmd(cfg),
		newApplicationGetCmd(cfg),
		newApplicationStatusCmd(cfg),
		newApplicationTagCmd(cfg),
		newApplicationDiscoverCmd(cfg),
		newApplicationEvaluateCmd(cfg),
		newApplicationEvaluatePendingCmd(cfg),
	)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./cmd/monoagentcli/... -run TestApplicationEvaluate -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing.

- [ ] **Step 7: Commit**

```bash
git add cmd/monoagentcli/application_evaluate.go cmd/monoagentcli/application_evaluate_test.go cmd/monoagentcli/application.go
git commit -m "$(cat <<'EOF'
feat(cli): add `monoagentcli application evaluate`/`evaluate-pending`

Scores one or all not-yet-evaluated pending job applications via
matching.Evaluate, delegated to a local AI agent runtime.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task 3: Workflow node `applications.evaluate`

**Files:**
- Create: `internal/nodes/matching/evaluate.go`
- Create: `internal/nodes/matching/evaluate_schema.go`
- Create: `internal/nodes/matching/evaluate_test.go`
- Modify: `internal/tools/schemagen/manifest.go`
- Modify: `internal/noderegistry/registry.go`
- Create (generated, not hand-edited): `internal/workflow/schemas/applications.evaluate.json`

**Interfaces:**
- Consumes: `matching.{Evaluate,ExecFunc,EnsureFunc,SearchKnowledgeFunc}` (Tasks 0-1); `workflow.{NodeExecutor,NodeInput,NodeOutput,Item,NewItem,NodeTypeRegistry}` (existing).
- Produces: node type `applications.evaluate` registered in the global registry. No other task in this plan depends on it (a future apply-automation phase will).

- [ ] **Step 1: Write the failing test**

```go
// internal/nodes/matching/evaluate_test.go
package matchingnodes_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/matching"
	matchingnodes "github.com/monoes/mono-agent/internal/nodes/matching"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "matching-node-test.db")
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

func TestEvaluateNodeScoresApplication(t *testing.T) {
	origEnsure := matching.EnsureFunc
	matching.EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { matching.EnsureFunc = origEnsure })
	origSearch := matching.SearchKnowledgeFunc
	matching.SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { matching.SearchKnowledgeFunc = origSearch })
	origExec := matching.ExecFunc
	matching.ExecFunc = func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		return &monomind.TurnResult{ResultText: `{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":80,"experience_score":80,"behavioral_score":80,"career_score":80,"overall_score":80,"verdict":"Strong Fit","rationale":"Good."}`}, nil
	}
	t.Cleanup(func() { matching.ExecFunc = origExec })

	db := newTestDB(t)
	matchingnodes.SetGlobalDB(db.DB)
	store := applications.NewStore(db.DB)
	app := &applications.Application{
		Kind: applications.KindJob,
		Job:  &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &matchingnodes.EvaluateNode{}
	if node.Type() != "applications.evaluate" {
		t.Fatalf("expected type applications.evaluate, got %q", node.Type())
	}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{"application_id": app.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outputs[0].Items[0].JSON["verdict"] != "Strong Fit" {
		t.Fatalf("unexpected output: %+v", outputs[0].Items[0].JSON)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/matching/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the node**

```go
// internal/nodes/matching/evaluate.go

// Package matchingnodes exposes internal/matching as a workflow node
// type: applications.evaluate.
package matchingnodes

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/matching"
	"github.com/monoes/mono-agent/internal/workflow"
)

var globalDB *sql.DB

// SetGlobalDB wires the shared SQLite connection into this package's node(s).
func SetGlobalDB(db *sql.DB) {
	globalDB = db
}

// RegisterAll registers applications.evaluate into the registry.
func RegisterAll(r *workflow.NodeTypeRegistry, db *sql.DB) {
	SetGlobalDB(db)
	r.Register("applications.evaluate", func() workflow.NodeExecutor { return &EvaluateNode{} })
}

// EvaluateNode scores one job application's fit via matching.Evaluate.
// Type: "applications.evaluate"
type EvaluateNode struct{}

func (n *EvaluateNode) Type() string { return "applications.evaluate" }

func (n *EvaluateNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	if globalDB == nil {
		return nil, fmt.Errorf("applications.evaluate: database not available (call SetGlobalDB at startup)")
	}
	applicationID, _ := config["application_id"].(string)
	if applicationID == "" {
		return nil, fmt.Errorf("applications.evaluate: config \"application_id\" is required")
	}
	runtime, _ := config["runtime"].(string)
	if runtime == "" {
		runtime = "claude"
	}
	profileID, _ := config["profile_id"].(string)
	if profileID == "" {
		profileID = "default"
	}

	verdict, err := matching.Evaluate(ctx, globalDB, profileID, applicationID, runtime)
	if err != nil {
		return nil, fmt.Errorf("applications.evaluate: %w", err)
	}

	out := map[string]interface{}{
		"application_id": applicationID, "verdict": verdict.Verdict, "overall_score": verdict.OverallScore,
		"rationale": verdict.Rationale,
	}
	return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{workflow.NewItem(out)}}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go test ./internal/nodes/matching/... -v`
Expected: PASS.

- [ ] **Step 5: Write the schema struct**

```go
// internal/nodes/matching/evaluate_schema.go
package matchingnodes

// EvaluateNodeSchema documents the config keys EvaluateNode.Execute reads
// out of its map[string]interface{} config.
type EvaluateNodeSchema struct {
	ApplicationID string `json:"application_id" schema:"label=Application ID,type=text,required,help=The job application to score."`
	Runtime       string `json:"runtime" schema:"label=Runtime,type=text,default=claude,help=Local agent runtime to use."`
	ProfileID     string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
```

- [ ] **Step 6: Register in the schemagen manifest**

In `internal/tools/schemagen/manifest.go`, add a new section after `documents.*`:

```go
	// --- applications.evaluate (matching) ---
	{NodeType: "applications.evaluate", GoFile: "internal/nodes/matching/evaluate_schema.go", StructName: "EvaluateNodeSchema"},
```

- [ ] **Step 7: Generate the schema and register the node package**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go run ./cmd/schemagen`
Expected: writes `internal/workflow/schemas/applications.evaluate.json`. **Before running**, double-check every `help=` string above for a literal comma — none are present here, so no full-width-comma escaping is needed this time.

In `internal/noderegistry/registry.go`, add the import (alphabetically) and register call:

```go
	matchingnodes "github.com/monoes/mono-agent/internal/nodes/matching"
```

```go
	matchingnodes.RegisterAll(registry, db)
```

- [ ] **Step 8: Run the full build and test suite**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"`
Expected: build succeeds; grep shows nothing. Completes Phase 5.

- [ ] **Step 9: Commit**

```bash
git add internal/nodes/matching/ internal/tools/schemagen/manifest.go internal/noderegistry/registry.go internal/workflow/schemas/applications.evaluate.json
git commit -m "$(cat <<'EOF'
feat(matching): add applications.evaluate workflow node

Wraps matching.Evaluate for use in workflows; registered in
noderegistry.Build alongside the Phase 1-4 node packages. Completes
Phase 5 (Chat-Powered Matching).

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

## Self-Review

**1. Spec coverage:**
- Gated multi-dimension rubric → Task 0's `rubricInstructions`. ✅
- `monomind.Exec` delegation (not a deprecated `ai.*` node) → Task 1's `ExecFunc`/`EnsureFunc` wired to the real `monomind` package. ✅
- Profile-knowledge grounding via `SearchKnowledge` → Task 1. ✅
- Append-only evaluation history → Task 0's migration + Task 1's `INSERT`-only usage (no `UPDATE` anywhere). ✅
- `fit:<verdict>` tagging for filtering via existing `ListFilter` → Task 1. ✅
- Kind guard (job-only) → Task 1, tested explicitly. ✅
- CLI surface (single + batch) → Task 2. ✅
- Workflow node → Task 3. ✅
- Anti-fabrication grounding instruction → Task 0's prompt text ("never invent experience... not shown there"). ✅

**2. Placeholder scan:** No "TBD"/"TODO". Every step has complete code.

**3. Type consistency:** `matching.FitVerdict`'s fields (Task 0) are used identically in Task 1's persistence SQL (column order matches struct field order for readability, though SQL binds by `?` position, not name, so this is a readability choice, not a correctness dependency — verified the `INSERT` statement's column list and value list are in the same relative order), Task 2's CLI JSON output, and Task 3's node output. `Evaluate`'s signature (`ctx, db, profileID, applicationID, runtime string) (*FitVerdict, error)`, introduced in Task 1, is called identically in Task 2 and Task 3. The three swappable vars (`ExecFunc`, `EnsureFunc`, `SearchKnowledgeFunc`) are referenced by the same names across Task 1's own tests, Task 2's CLI tests, and Task 3's node test — no drift.
