package automation

import (
	"testing"
	"time"
)

func TestHealthResetAfterRerecord(t *testing.T) {
	db := healthTestDB(t)
	r := NewHealthRecorder(db, HealthOptions{Interval: time.Hour})
	defer r.Close()
	for i := 0; i < 5; i++ {
		r.ObserveSelector("hn", "k", -1, false, false)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if h := healthRow(t, db, "hn", "k"); h.Status != HealthBroken {
		t.Fatalf("before reset: %+v", h)
	}
	if err := ResetSelectorHealth(db, "hn", "k"); err != nil {
		t.Fatal(err)
	}
	h := healthRow(t, db, "hn", "k")
	if h.Status != HealthOK || h.OK != 0 || h.Fail != 0 || h.Healed != 0 || h.Recent != "" ||
		h.LastOK != nil || h.LastFail != nil || h.LastCandidateIndex != -1 || h.RerecordedAt == nil {
		t.Fatalf("after reset: %+v", h)
	}
	// New outcomes accumulate from zero; rerecordedAt survives them.
	r.ObserveSelector("hn", "k", 0, true, false)
	r.ObserveSelector("hn", "k", -1, false, false)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	h = healthRow(t, db, "hn", "k")
	if h.OK != 1 || h.Fail != 1 || h.Recent != "of" || h.Status != HealthDecaying || h.RerecordedAt == nil {
		t.Fatalf("after new runs: %+v", h)
	}
	// Resetting a key with no row creates one.
	if err := ResetSelectorHealth(db, "hn", "fresh"); err != nil {
		t.Fatal(err)
	}
	if h := healthRow(t, db, "hn", "fresh"); h.Status != HealthOK || h.RerecordedAt == nil {
		t.Fatalf("fresh reset row: %+v", h)
	}
}
