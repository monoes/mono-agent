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
