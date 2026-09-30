package agentroster

import (
	"context"
	"math"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func ev(model, cat, kind string, ok bool, at time.Time) QualityEvent {
	return QualityEvent{Runtime: "claude", Model: model, Category: cat, Kind: kind, Success: ok, At: at}
}

func TestAggregateMath(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	q := Aggregate([]QualityEvent{
		// opus/engineering: three fresh failures.
		ev("opus", "engineering", KindOutcome, false, now),
		ev("opus", "engineering", KindOutcome, false, now),
		ev("opus", "engineering", KindOutcome, false, now),
		// opus/testing: a success a half-life ago, two fresh ones, and
		// fresh ratings that count RatingWeight times each.
		ev("opus", "Testing", KindOutcome, true, now.Add(-QualityHalfLife)),
		ev("opus", "testing", KindOutcome, true, now),
		ev("opus", "testing", KindOutcome, true, now),
		ev("opus", "testing", KindRating, true, now),
		ev("opus", "testing", KindRating, false, now),
		// default model, no category.
		ev("", "", KindOutcome, true, now),
	}, now)

	r, ok := q.Get("claude", "opus", "engineering")
	// (0 + 0.75*4) / (3 + 4)
	if !ok || r.Results != 3 || r.Succeeded != 0 || r.Ratings != 0 || !near(r.Rate, 3.0/7) || !r.BadFit() {
		t.Errorf("engineering = %+v, %v; want rate 3/7, a bad fit", r, ok)
	}
	r, ok = q.Get("claude", "opus", "TESTING ")
	// weight 0.5 + 1 + 1 + 2 + 2 = 6.5, wins 0.5 + 1 + 1 + 2 = 4.5
	// → (4.5 + 3) / (6.5 + 4)
	if !ok || r.Results != 3 || r.Succeeded != 3 || r.Ratings != 2 || r.RatedGood != 1 ||
		!near(r.Weight, 6.5) || !near(r.Rate, 7.5/10.5) || r.BadFit() {
		t.Errorf("testing = %+v, %v; want weight 6.5, rate 7.5/10.5", r, ok)
	}
	if _, ok := q.Get("claude", "", ""); ok {
		t.Error("one event is below the minimum sample: no known rate")
	}
	if all := q.ForModel("claude", ""); len(all) != 1 || all[0].Model != DefaultModel || all[0].Category != CategoryGeneral || all[0].Known {
		t.Errorf("default model's record = %+v", all)
	}
	if got := q.ForModel("claude", "opus"); len(got) != 2 || got[0].Category != "engineering" || got[1].Category != "testing" {
		t.Errorf("ForModel = %+v", got)
	}
}

func TestAggregateMinimumSampleAndDecay(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	two := Aggregate([]QualityEvent{
		ev("haiku", "research", KindOutcome, false, now),
		ev("haiku", "research", KindRating, false, now),
		ev("haiku", "research", KindOutcome, false, now),
	}, now)
	// Three events, but only two results: a rating is not a result.
	if _, ok := two.Get("claude", "haiku", "research"); ok {
		t.Error("two bad results (one rated bad) must not count yet")
	}
	// Three failures long ago barely move the rate from the prior, and
	// three fresh successes outweigh them.
	old := now.Add(-6 * QualityHalfLife)
	q := Aggregate([]QualityEvent{
		ev("haiku", "research", KindOutcome, false, old),
		ev("haiku", "research", KindOutcome, false, old),
		ev("haiku", "research", KindOutcome, false, old),
	}, now)
	r, _ := q.Get("claude", "haiku", "research")
	if r.BadFit() || r.Rate < 0.7 {
		t.Errorf("old failures = %+v; want close to the prior", r)
	}
}

func TestQualityStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	// A year-old event is pruned by the next write.
	if err := RecordQuality(ctx, db, ev("opus", "engineering", KindOutcome, true, now.Add(-400*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	for _, e := range []QualityEvent{
		ev("opus", "engineering", KindOutcome, false, now),
		ev("opus", "engineering", KindRating, false, now),
		ev("opus", "engineering", KindOutcome, true, now),
		ev("opus", "engineering", KindOutcome, true, now),
		ev("", "", KindOutcome, true, now),
	} {
		if err := RecordQuality(ctx, db, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordQuality(ctx, db, ev("opus", "x", "vibes", true, now)); err == nil {
		t.Error("unknown kind must fail")
	}
	all, err := ListQuality(ctx, db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 || all[4].Model != DefaultModel || all[4].Category != CategoryGeneral || !all[4].At.Equal(now) {
		t.Fatalf("events = %+v", all)
	}
	q, err := LoadQuality(ctx, db, now)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := q.Get("claude", "opus", "engineering")
	// weight 1 + 2 + 1 + 1 = 5, wins 2 → (2 + 3) / 9
	if !ok || !near(r.Rate, 5.0/9) || r.BadFit() {
		t.Errorf("rate = %+v, %v", r, ok)
	}
	roster := []RuntimeRoster{{Runtime: "claude", Models: []Entry{{Result: Result{Model: "opus"}}, {Result: Result{Model: "haiku"}}}}}
	AttachQuality(roster, q)
	if len(roster[0].Models[0].TrackRecord) != 1 || roster[0].Models[1].TrackRecord != nil {
		t.Errorf("attached = %+v", roster[0].Models)
	}
}
