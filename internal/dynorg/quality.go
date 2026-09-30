package dynorg

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// Quality records each worker result and the lead's rating of it as
// roster quality events (#230). One per turn; nil records nothing.
type Quality struct {
	Record func(agentroster.QualityEvent)

	mu   sync.Mutex
	last map[string]*result // agent id → its latest result
}

// result is a worker's latest result: what ran it, and whether the lead
// may still rate it.
type result struct {
	runtime, model, category string
	seq                      int  // results so far
	rateable                 bool // done or failed for a reason about the model
	rated                    bool
}

// Ratings the lead gives with org_rate.
const (
	RatingGood = "good"
	RatingBad  = "bad"
)

// recordResult counts w's finished run. Call it without c.mu held.
func (c *Conductor) recordResult(w *worker, outcome, errText string) {
	q := c.cfg.Quality
	if q == nil {
		return
	}
	c.mu.Lock()
	m, cat := w.model, w.staff.Category
	c.mu.Unlock()
	counts := countsAsQuality(outcome, errText)
	q.mu.Lock()
	if q.last == nil {
		q.last = map[string]*result{}
	}
	r := q.last[w.id]
	if r == nil {
		r = &result{}
		q.last[w.id] = r
	}
	*r = result{runtime: m.Runtime, model: m.Model, category: cat, seq: r.seq + 1, rateable: counts}
	q.mu.Unlock()
	if counts && q.Record != nil {
		q.Record(agentroster.QualityEvent{Runtime: m.Runtime, Model: m.Model, Category: cat,
			Kind: agentroster.KindOutcome, Success: outcome == chatevents.AgentDone, At: c.cfg.Now()})
	}
}

// countsAsQuality reports whether a finished run says something about how
// well its model fits the work: done, or failed on the worker's own error
// or timeout. A cancelled run, a budget refusal (#265: the org's cap, not
// the model) and a model that couldn't run at all (auth, quota, …: the
// roster's validation state covers those) don't count.
func countsAsQuality(outcome, errText string) bool {
	switch outcome {
	case chatevents.AgentDone:
		return true
	case chatevents.AgentFailed:
	default:
		return false
	}
	status, detail, ok := strings.Cut(errText, ": ")
	if !ok {
		return false // no model ran ("no model could run this worker")
	}
	if status == "budget" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(detail)), "budget") {
		return false
	}
	if unusable(status) || status == agentroster.StatusCancelled {
		return false
	}
	return true
}

// RateResult is org_rate's result.
type RateResult struct {
	AgentID  string `json:"agent_id"`
	Rating   string `json:"rating"`
	Model    string `json:"model"`
	Category string `json:"category"`
	Recorded bool   `json:"recorded"`
}

// Rate records the lead's rating of a worker's latest result, once per
// result. It feeds the model's success rate for the worker's category.
func (c *Conductor) Rate(agentID, rating string) (RateResult, error) {
	rating = strings.ToLower(strings.TrimSpace(rating))
	if rating != RatingGood && rating != RatingBad {
		return RateResult{}, fmt.Errorf("rating must be %q or %q", RatingGood, RatingBad)
	}
	c.mu.Lock()
	_, known := c.workers[agentID]
	c.mu.Unlock()
	if !known {
		return RateResult{}, fmt.Errorf("unknown worker %q", agentID)
	}
	q := c.cfg.Quality
	if q == nil {
		return RateResult{AgentID: agentID, Rating: rating}, nil
	}
	q.mu.Lock()
	r := q.last[agentID]
	switch {
	case r == nil:
		q.mu.Unlock()
		return RateResult{}, fmt.Errorf("%s has no result to rate yet; org_wait for it first", agentID)
	case !r.rateable:
		q.mu.Unlock()
		return RateResult{}, fmt.Errorf("%s's last run was cancelled, stopped by the budget, or its model couldn't run: there is nothing to rate", agentID)
	case r.rated:
		q.mu.Unlock()
		return RateResult{}, fmt.Errorf("%s's latest result is already rated", agentID)
	}
	r.rated = true
	ev := agentroster.QualityEvent{Runtime: r.runtime, Model: r.model, Category: r.category,
		Kind: agentroster.KindRating, Success: rating == RatingGood, At: c.cfg.Now()}
	q.mu.Unlock()
	if q.Record != nil {
		q.Record(ev)
	}
	return RateResult{AgentID: agentID, Rating: rating, Model: Model{Runtime: ev.Runtime, Model: ev.Model}.Key(),
		Category: agentroster.NormalizeCategory(ev.Category), Recorded: true}, nil
}

// trackRecord is a model's known rate for a category.
func (s *Staffer) trackRecord(m Model, category string) (agentroster.Rate, bool) {
	if s.Quality == nil {
		return agentroster.Rate{}, false
	}
	return s.Quality.Get(m.Runtime, m.Model, category)
}

// rankByQuality moves the models that are bad fits for the category (a
// known success rate under agentroster.BadFit) to the end, worst last; the
// others keep their order. It returns the ranked list and the bad fits.
func (s *Staffer) rankByQuality(eligible []Model, category string) (ranked, bad []Model) {
	rate := map[string]float64{}
	for _, m := range eligible {
		if r, ok := s.trackRecord(m, category); ok && r.BadFit() {
			bad = append(bad, m)
			rate[m.Key()] = r.Rate
		}
	}
	if len(bad) == 0 {
		return eligible, nil
	}
	sort.SliceStable(bad, func(i, j int) bool { return rate[bad[i].Key()] > rate[bad[j].Key()] })
	ranked = make([]Model, 0, len(eligible))
	for _, m := range eligible {
		if !slices.ContainsFunc(bad, func(b Model) bool { return b.Key() == m.Key() }) {
			ranked = append(ranked, m)
		}
	}
	return append(ranked, bad...), bad
}

// trackRecordState is Jev's view of the eligible models' known rates for
// the category, by model key.
func (s *Staffer) trackRecordState(eligible []Model, category string) map[string]any {
	out := map[string]any{}
	for _, m := range eligible {
		if r, ok := s.trackRecord(m, category); ok {
			out[m.Key()] = map[string]any{"success_rate": round2(r.Rate), "samples": r.Samples, "bad_fit": r.BadFit()}
		}
	}
	return out
}

func trackRecordText(r agentroster.Rate) string {
	return fmt.Sprintf("succeeded in %.0f%% of %d recent %s jobs", r.Rate*100, r.Samples, r.Category)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
