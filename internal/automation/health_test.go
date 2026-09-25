package automation

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
)

// healthTestDB opens a migrated database in a temp dir.
func healthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func healthRow(t *testing.T, db *sql.DB, id, key string) SelectorHealth {
	t.Helper()
	rows, err := LoadSelectorHealth(db, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no health row for %s/%s in %+v", id, key, rows)
	return SelectorHealth{}
}

func TestHealthStatusRules(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	cases := []struct {
		name     string
		h        SelectorHealth
		expected string
	}{
		{"no data", SelectorHealth{}, HealthOK},
		{"all first-candidate", SelectorHealth{OK: 10, Recent: "oooooooooo", LastOK: &t1}, HealthOK},
		{"occasional heal under 30%", SelectorHealth{OK: 10, Healed: 2, Recent: "oohoooohoo"}, HealthOK},
		{"heals over 30%", SelectorHealth{OK: 10, Healed: 4, Recent: "ohohohohoo"}, HealthDecaying},
		{"one recent fail", SelectorHealth{OK: 9, Fail: 1, Recent: "oooofooooo", LastOK: &t1, LastFail: &t0}, HealthDecaying},
		{"two trailing fails", SelectorHealth{OK: 8, Fail: 2, Recent: "ooooooooff", LastOK: &t0, LastFail: &t1}, HealthDecaying},
		{"three trailing fails after ok", SelectorHealth{OK: 7, Fail: 3, Recent: "oooooooofff", LastOK: &t0, LastFail: &t1}, HealthBroken},
		{"three trailing fails, never ok", SelectorHealth{Fail: 3, Recent: "fff", LastFail: &t1}, HealthBroken},
		{"five trailing fails", SelectorHealth{OK: 5, Fail: 5, Recent: "ooooofffff", LastOK: &t0, LastFail: &t1}, HealthBroken},
		{"recovered after fails", SelectorHealth{OK: 6, Fail: 5, Recent: "ffffffoo", LastOK: &t1, LastFail: &t0}, HealthDecaying},
	}
	for _, c := range cases {
		if got := SelectorStatus(c.h); got != c.expected {
			t.Errorf("%s: SelectorStatus(%q) = %s, want %s", c.name, c.h.Recent, got, c.expected)
		}
	}
}

func TestHealthRecentRing(t *testing.T) {
	if got := appendRecent("ooooooooo", "hf"); got != "oooooooohf" {
		t.Fatalf("appendRecent = %q", got)
	}
	if got := appendRecent("", "ffffffffffffoo"); len(got) != recentRingSize || got != "ffffffffoo" {
		t.Fatalf("appendRecent trims to the newest %d: %q", recentRingSize, got)
	}
}

// fakePromoter records promotions.
type fakePromoter struct {
	mu    sync.Mutex
	calls []string
}

func (p *fakePromoter) PromoteSelector(id, key string, idx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf("%s/%s#%d", id, key, idx))
	return nil
}

func (p *fakePromoter) list() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func TestHealthRecorderBatchesAndFlushes(t *testing.T) {
	db := healthTestDB(t)
	// A long interval: only Flush writes.
	r := NewHealthRecorder(db, HealthOptions{Interval: time.Hour})
	defer r.Close()
	for i := 0; i < 3; i++ {
		r.ObserveSelector("hn", "story.title", 0, true, false)
	}
	r.ObserveSelector("hn", "story.title", 2, true, true)
	r.ObserveSelector("hn", "story.title", -1, false, false)
	r.ObserveSelector("hn", "", 0, true, false) // ignored: no key
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	h := healthRow(t, db, "hn", "story.title")
	if h.OK != 4 || h.Fail != 1 || h.Healed != 1 || h.Recent != "ooohf" || h.LastCandidateIndex != -1 {
		t.Fatalf("row after first flush = %+v", h)
	}
	if h.LastOK == nil || h.LastFail == nil {
		t.Fatalf("timestamps not set: %+v", h)
	}
	// A second batch accumulates, and the ring keeps the newest 10.
	for i := 0; i < 8; i++ {
		r.ObserveSelector("hn", "story.title", 0, true, false)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	h = healthRow(t, db, "hn", "story.title")
	if h.OK != 12 || h.Fail != 1 || h.Recent != "hfoooooooo" || h.LastCandidateIndex != 0 {
		t.Fatalf("row after second flush = %+v", h)
	}
	if h.Status != HealthDecaying {
		t.Fatalf("status = %s, want decaying (a fail in the ring)", h.Status)
	}
}

func TestHealthRecorderFlushesOnInterval(t *testing.T) {
	db := healthTestDB(t)
	r := NewHealthRecorder(db, HealthOptions{Interval: 20 * time.Millisecond})
	defer r.Close()
	r.ObserveSelector("hn", "k", 0, true, false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rows, _ := LoadSelectorHealth(db, "hn"); len(rows) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("interval flush never wrote the observation")
}

func TestHealthRecorderCloseFlushesAndDropsAfter(t *testing.T) {
	db := healthTestDB(t)
	r := NewHealthRecorder(db, HealthOptions{Interval: time.Hour})
	r.ObserveSelector("hn", "k", 0, true, false)
	r.ObserveSelector("hn", "k", 0, true, false)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if h := healthRow(t, db, "hn", "k"); h.OK != 2 {
		t.Fatalf("Close did not write the pending batch: %+v", h)
	}
	r.ObserveSelector("hn", "k", 0, true, false) // must not panic or block
	if r.Dropped() != 1 {
		t.Fatalf("Dropped = %d after observing on a closed recorder, want 1", r.Dropped())
	}
	if err := r.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := r.Flush(); err == nil {
		t.Fatal("Flush after Close should report the recorder is closed")
	}
}

func TestHealthRecorderOverflowDropsWithoutBlocking(t *testing.T) {
	db := healthTestDB(t)
	r := NewHealthRecorder(db, HealthOptions{Buffer: 4, Interval: time.Hour, MaxBatch: 1})
	defer r.Close()
	// Stall the writer: the loop takes one observation, blocks in its
	// flush, the 4-slot queue fills and the rest must be dropped at once.
	release := make(chan struct{})
	r.testHookWrite = func() { <-release }
	start := time.Now()
	const total = 5000
	for i := 0; i < total; i++ {
		r.ObserveSelector("hn", "k", 0, true, false)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("ObserveSelector blocked: %d calls took %v", total, el)
	}
	close(release)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	h := healthRow(t, db, "hn", "k")
	if int64(h.OK)+r.Dropped() != total {
		t.Fatalf("written %d + dropped %d != %d observed", h.OK, r.Dropped(), total)
	}
	if r.Dropped() == 0 {
		t.Fatal("expected drops with a 4-slot queue")
	}
}

func TestHealthRecorderPromotesOffHotPathOnce(t *testing.T) {
	db := healthTestDB(t)
	p := &fakePromoter{}
	r := NewHealthRecorder(db, HealthOptions{Interval: time.Hour, Promoter: p})
	defer r.Close()
	r.ObserveSelector("hn", "a", 1, true, true)
	r.ObserveSelector("hn", "a", 2, true, true) // latest heal wins
	r.ObserveSelector("hn", "b", 1, true, true)
	r.ObserveSelector("hn", "b", 0, true, false) // first candidate works again: no promotion
	r.ObserveSelector("hn", "c", -1, true, true) // Jev fallback: nothing to promote
	r.ObserveSelector("hn", "d", 1, false, false)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := p.list(); len(got) != 1 || got[0] != "hn/a#2" {
		t.Fatalf("promotions = %v, want [hn/a#2]", got)
	}
	// A run that started before the promotion reports the old index again:
	// within the cooldown it must not flip the order back.
	r.ObserveSelector("hn", "a", 2, true, true)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := p.list(); len(got) != 1 {
		t.Fatalf("promotion repeated within cooldown: %v", got)
	}
}
