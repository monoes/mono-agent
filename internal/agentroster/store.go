package agentroster

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Result is one runtime × model test outcome, as stored.
type Result struct {
	Runtime        string    `json:"runtime"`
	Model          string    `json:"model"`
	Label          string    `json:"label,omitempty"`
	EffortLevels   []string  `json:"effort_levels,omitempty"`
	Status         string    `json:"status"`
	Detail         string    `json:"detail,omitempty"`
	Reply          string    `json:"reply,omitempty"`
	LatencyFirstMs int64     `json:"latency_first_ms"`
	LatencyMs      int64     `json:"latency_ms"`
	TokensIn       int64     `json:"tokens_in"`
	TokensOut      int64     `json:"tokens_out"`
	CostUSD        float64   `json:"cost_usd"`
	HasCost        bool      `json:"has_cost"`
	RuntimeVersion string    `json:"runtime_version,omitempty"`
	Source         string    `json:"source"`
	RunID          string    `json:"run_id,omitempty"`
	ValidatedAt    time.Time `json:"validated_at"`
}

// Sources of a roster row.
const (
	SourceListed = "listed"
	SourceManual = "manual"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// Save stores r as the latest result for its runtime and model. A manual
// row stays manual when a later run lists the same model.
func Save(ctx context.Context, db *sql.DB, r Result) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO agent_model_validations
  (runtime, model, label, effort_levels, status, detail, reply, latency_first_ms, latency_ms,
   tokens_in, tokens_out, cost_usd, has_cost, runtime_version, source, run_id, validated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(runtime, model) DO UPDATE SET
  label=excluded.label, effort_levels=excluded.effort_levels, status=excluded.status, detail=excluded.detail, reply=excluded.reply,
  latency_first_ms=excluded.latency_first_ms, latency_ms=excluded.latency_ms,
  tokens_in=excluded.tokens_in, tokens_out=excluded.tokens_out,
  cost_usd=excluded.cost_usd, has_cost=excluded.has_cost,
  runtime_version=excluded.runtime_version,
  source=CASE WHEN agent_model_validations.source='manual' THEN 'manual' ELSE excluded.source END,
  run_id=excluded.run_id, validated_at=excluded.validated_at`,
		r.Runtime, r.Model, r.Label, effortJSON(r.EffortLevels), r.Status, r.Detail, r.Reply, r.LatencyFirstMs, r.LatencyMs,
		r.TokensIn, r.TokensOut, r.CostUSD, boolInt(r.HasCost), r.RuntimeVersion, orDefault(r.Source, SourceListed),
		r.RunID, r.ValidatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("saving validation for %s/%s: %w", r.Runtime, r.Model, err)
	}
	return nil
}

// List returns every stored result, ordered by runtime and model.
func List(ctx context.Context, db *sql.DB) ([]Result, error) {
	rows, err := db.QueryContext(ctx, `
SELECT runtime, model, label, effort_levels, status, detail, reply, latency_first_ms, latency_ms,
       tokens_in, tokens_out, cost_usd, has_cost, runtime_version, source, run_id, validated_at
FROM agent_model_validations ORDER BY runtime, model`)
	if err != nil {
		return nil, fmt.Errorf("listing validations: %w", err)
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		var hasCost int
		var at, efforts string
		if err := rows.Scan(&r.Runtime, &r.Model, &r.Label, &efforts, &r.Status, &r.Detail, &r.Reply,
			&r.LatencyFirstMs, &r.LatencyMs, &r.TokensIn, &r.TokensOut, &r.CostUSD, &hasCost,
			&r.RuntimeVersion, &r.Source, &r.RunID, &at); err != nil {
			return nil, fmt.Errorf("reading validation: %w", err)
		}
		r.HasCost = hasCost != 0
		_ = json.Unmarshal([]byte(efforts), &r.EffortLevels)
		r.ValidatedAt, _ = time.Parse(timeLayout, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddManual records a model id the user typed for a runtime, as untested.
// It is validated with the next run and kept even when the runtime does not
// list it.
func AddManual(ctx context.Context, db *sql.DB, runtime, model string) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO agent_model_validations (runtime, model, label, status, source, validated_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(runtime, model) DO UPDATE SET source='manual'`,
		runtime, model, model, StatusUntested, SourceManual, time.Time{}.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("adding %s/%s: %w", runtime, model, err)
	}
	return nil
}

// Remove deletes a runtime's model from the roster.
func Remove(ctx context.Context, db *sql.DB, runtime, model string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM agent_model_validations WHERE runtime=? AND model=?`, runtime, model)
	return err
}

// StartRun records the start of a validate run.
func StartRun(ctx context.Context, db *sql.DB, id string, planned int, at time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO agent_model_validation_runs (id, started_at, planned) VALUES (?,?,?)`,
		id, at.UTC().Format(timeLayout), planned)
	return err
}

// FinishRun records a validate run's counts.
func FinishRun(ctx context.Context, db *sql.DB, id string, s Summary, at time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE agent_model_validation_runs SET finished_at=?, ok=?, failed=?, cancelled=? WHERE id=?`,
		at.UTC().Format(timeLayout), s.OK, s.Failed, s.Cancelled, id)
	return err
}

// RecordOutcome counts a real turn's result against a runtime and model.
// A failure that means the model can't be used (auth, quota,
// model_unavailable) also marks the roster row failed.
func RecordOutcome(ctx context.Context, db *sql.DB, runtime, model, status, detail string, at time.Time) error {
	ok := Works(status)
	stamp := at.UTC().Format(timeLayout)
	if _, err := db.ExecContext(ctx, `
INSERT INTO agent_model_outcomes (runtime, model, successes, failures, last_status, last_detail, updated_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(runtime, model) DO UPDATE SET
  successes=successes+excluded.successes, failures=failures+excluded.failures,
  last_status=excluded.last_status, last_detail=excluded.last_detail, updated_at=excluded.updated_at`,
		runtime, model, boolInt(ok), boolInt(!ok), status, clip(detail), stamp); err != nil {
		return fmt.Errorf("recording outcome for %s/%s: %w", runtime, model, err)
	}
	switch status {
	case StatusAuth, StatusQuota, StatusModelUnavailable:
		_, err := db.ExecContext(ctx, `UPDATE agent_model_validations SET status=?, detail=?, validated_at=? WHERE runtime=? AND model=?`,
			status, clip(detail), stamp, runtime, model)
		return err
	}
	return nil
}

func effortJSON(levels []string) string {
	if len(levels) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(levels)
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
