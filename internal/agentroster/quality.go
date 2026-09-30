package agentroster

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Roster quality from outcomes (monoes/mono-agent#230): every dynamic-org
// worker result (agent.finished's outcome) and every rating the lead gives
// one (org_rate) is an event for its runtime, model and role category. The
// events add up to a smoothed, decaying success rate per model per
// category, which staffing uses to push bad fits down the ranking.

// Event kinds.
const (
	KindOutcome = "outcome" // a worker finished done (success) or failed
	KindRating  = "rating"  // the lead rated a result good (success) or bad
)

// Quality tuning.
const (
	// QualityHalfLife: an event's weight halves every 30 days, so a model
	// that got better (or worse) shows it within a few weeks.
	QualityHalfLife = 30 * 24 * time.Hour
	// QualityMinSamples: below this many events a rate is not known and
	// has no effect, so one failure never punishes a model.
	QualityMinSamples = 3
	// QualityPriorRate and QualityPriorWeight are a Beta prior: every rate
	// starts as QualityPriorWeight pseudo-events at QualityPriorRate, and
	// real events pull it away from there.
	QualityPriorRate   = 0.75
	QualityPriorWeight = 4.0
	// RatingWeight: the lead's rating judges the result itself, so it
	// counts twice as much as the bare done/failed outcome.
	RatingWeight = 2.0
	// BadFit is the known rate under which staffing ranks a model last
	// for that category.
	BadFit = 0.5
	// qualityKeep bounds the table: past a year an event weighs < 0.02%.
	qualityKeep = 365 * 24 * time.Hour
)

// CategoryGeneral is the category of a worker whose role has none.
const CategoryGeneral = "general"

// QualityEvent is one worker result or one rating of it.
type QualityEvent struct {
	Runtime  string
	Model    string // "" = the runtime's default
	Category string // the worker's role category; "" = general
	Kind     string // KindOutcome or KindRating
	Success  bool
	At       time.Time
}

// RecordQuality stores e and drops events too old to matter.
func RecordQuality(ctx context.Context, db *sql.DB, e QualityEvent) error {
	if e.Kind != KindOutcome && e.Kind != KindRating {
		return fmt.Errorf("unknown quality event kind %q", e.Kind)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO agent_model_outcome_events (runtime, model, category, kind, success, at) VALUES (?,?,?,?,?,?)`,
		e.Runtime, modelKey(e.Model), NormalizeCategory(e.Category), e.Kind, boolInt(e.Success), e.At.UTC().Format(timeLayout)); err != nil {
		return fmt.Errorf("recording quality for %s/%s: %w", e.Runtime, e.Model, err)
	}
	_, err := db.ExecContext(ctx, `DELETE FROM agent_model_outcome_events WHERE at < ?`,
		e.At.Add(-qualityKeep).UTC().Format(timeLayout))
	return err
}

// ListQuality returns the events at or after since, oldest first.
func ListQuality(ctx context.Context, db *sql.DB, since time.Time) ([]QualityEvent, error) {
	rows, err := db.QueryContext(ctx, `
SELECT runtime, model, category, kind, success, at FROM agent_model_outcome_events
WHERE at >= ? ORDER BY at, id`, since.UTC().Format(timeLayout))
	if err != nil {
		return nil, fmt.Errorf("listing quality events: %w", err)
	}
	defer rows.Close()
	var out []QualityEvent
	for rows.Next() {
		var e QualityEvent
		var ok int
		var at string
		if err := rows.Scan(&e.Runtime, &e.Model, &e.Category, &e.Kind, &ok, &at); err != nil {
			return nil, fmt.Errorf("reading quality event: %w", err)
		}
		e.Success = ok != 0
		e.At, _ = time.Parse(timeLayout, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LoadQuality reads the events that still count and aggregates them.
func LoadQuality(ctx context.Context, db *sql.DB, now time.Time) (Quality, error) {
	events, err := ListQuality(ctx, db, now.Add(-qualityKeep))
	if err != nil {
		return nil, err
	}
	return Aggregate(events, now), nil
}

// Rate is one model's track record in one role category.
type Rate struct {
	Runtime   string  `json:"runtime"`
	Model     string  `json:"model"`
	Category  string  `json:"category"`
	Samples   int     `json:"samples"`   // events, undecayed
	Successes int     `json:"successes"` // successful events, undecayed
	Weight    float64 `json:"weight"`    // decayed, rating-weighted events
	Rate      float64 `json:"success_rate"`
	// Known: Samples reached QualityMinSamples; only a known rate affects
	// staffing.
	Known bool `json:"known"`
}

// BadFit reports whether r is a known rate under BadFit.
func (r Rate) BadFit() bool { return r.Known && r.Rate < BadFit }

// Quality is the aggregated track record, by runtime, model and category.
type Quality map[string]Rate

// Aggregate turns events into rates as of now. Each event weighs
// 0.5^(age/QualityHalfLife), times RatingWeight for a rating; the rate is
// (weighted successes + prior) / (weighted events + prior weight).
func Aggregate(events []QualityEvent, now time.Time) Quality {
	type acc struct {
		r    Rate
		wins float64
	}
	accs := map[string]*acc{}
	for _, e := range events {
		model, cat := modelKey(e.Model), NormalizeCategory(e.Category)
		k := qualityKey(e.Runtime, model, cat)
		a := accs[k]
		if a == nil {
			a = &acc{r: Rate{Runtime: e.Runtime, Model: model, Category: cat}}
			accs[k] = a
		}
		w := decay(now.Sub(e.At))
		if e.Kind == KindRating {
			w *= RatingWeight
		}
		a.r.Samples++
		a.r.Weight += w
		if e.Success {
			a.r.Successes++
			a.wins += w
		}
	}
	q := make(Quality, len(accs))
	for k, a := range accs {
		a.r.Rate = (a.wins + QualityPriorRate*QualityPriorWeight) / (a.r.Weight + QualityPriorWeight)
		a.r.Known = a.r.Samples >= QualityMinSamples
		q[k] = a.r
	}
	return q
}

// Get returns a model's rate in a category; ok is false when there is no
// known rate (no events, or fewer than QualityMinSamples).
func (q Quality) Get(runtime, model, category string) (Rate, bool) {
	r, ok := q[qualityKey(runtime, modelKey(model), NormalizeCategory(category))]
	if !ok || !r.Known {
		return Rate{}, false
	}
	return r, true
}

// ForModel returns a model's rates in every category it has events in,
// sorted by category.
func (q Quality) ForModel(runtime, model string) []Rate {
	model = modelKey(model)
	var out []Rate
	for _, r := range q {
		if r.Runtime == runtime && r.Model == model {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// AttachQuality sets each roster entry's track record.
func AttachQuality(roster []RuntimeRoster, q Quality) {
	for i := range roster {
		for j := range roster[i].Models {
			e := &roster[i].Models[j]
			e.TrackRecord = q.ForModel(roster[i].Runtime, e.Model)
		}
	}
}

// NormalizeCategory lowercases a role category; "" is CategoryGeneral.
func NormalizeCategory(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" {
		return CategoryGeneral
	}
	return c
}

func decay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(age)/float64(QualityHalfLife))
}

func modelKey(model string) string {
	if model == "" {
		return DefaultModel
	}
	return model
}

func qualityKey(runtime, model, category string) string {
	return runtime + "\x00" + model + "\x00" + category
}
