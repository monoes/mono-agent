package storage

import (
	"path/filepath"
	"testing"
)

func newCountersTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := NewDatabase(filepath.Join(t.TempDir(), "counters.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db
}

// TestGetDailyActionCount_NoRowsYet verifies a fresh (profile, action type)
// pair with no recorded activity today reports a count of 0, not an error.
func TestGetDailyActionCount_NoRowsYet(t *testing.T) {
	db := newCountersTestDB(t)

	count, err := db.GetDailyActionCount("default", "follow_users")
	if err != nil {
		t.Fatalf("GetDailyActionCount: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected count 0 for unrecorded pair, got %d", count)
	}
}

// TestIncrementDailyActionCount_CreatesAndIncrements verifies the counter
// starts at 1 on first increment and accumulates correctly afterward.
func TestIncrementDailyActionCount_CreatesAndIncrements(t *testing.T) {
	db := newCountersTestDB(t)

	count, err := db.IncrementDailyActionCount("default", "follow_users")
	if err != nil {
		t.Fatalf("IncrementDailyActionCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1 after first increment, got %d", count)
	}

	for i := 0; i < 4; i++ {
		if _, err := db.IncrementDailyActionCount("default", "follow_users"); err != nil {
			t.Fatalf("IncrementDailyActionCount: %v", err)
		}
	}

	got, err := db.GetDailyActionCount("default", "follow_users")
	if err != nil {
		t.Fatalf("GetDailyActionCount: %v", err)
	}
	if got != 5 {
		t.Fatalf("expected count 5 after 5 increments, got %d", got)
	}
}

// TestDailyActionCount_ScopedByProfileAndActionType verifies the counter is
// independent per (profile, action type) pair — the (profile_key,
// action_type, day) primary key must not let unrelated actions or profiles
// bleed into each other's counts.
func TestDailyActionCount_ScopedByProfileAndActionType(t *testing.T) {
	db := newCountersTestDB(t)

	if _, err := db.IncrementDailyActionCount("default", "follow_users"); err != nil {
		t.Fatalf("IncrementDailyActionCount: %v", err)
	}
	if _, err := db.IncrementDailyActionCount("default", "unfollow_users"); err != nil {
		t.Fatalf("IncrementDailyActionCount: %v", err)
	}
	if _, err := db.IncrementDailyActionCount("work", "follow_users"); err != nil {
		t.Fatalf("IncrementDailyActionCount: %v", err)
	}

	if got, err := db.GetDailyActionCount("default", "follow_users"); err != nil || got != 1 {
		t.Fatalf("default/follow_users: got (%d, %v), want (1, nil)", got, err)
	}
	if got, err := db.GetDailyActionCount("default", "unfollow_users"); err != nil || got != 1 {
		t.Fatalf("default/unfollow_users: got (%d, %v), want (1, nil)", got, err)
	}
	if got, err := db.GetDailyActionCount("work", "follow_users"); err != nil || got != 1 {
		t.Fatalf("work/follow_users: got (%d, %v), want (1, nil)", got, err)
	}
	if got, err := db.GetDailyActionCount("work", "unfollow_users"); err != nil || got != 0 {
		t.Fatalf("work/unfollow_users: got (%d, %v), want (0, nil) — unrecorded pair", got, err)
	}
}

// TestDailyActionCount_EmptyProfileDefaultsToDefault verifies an empty
// profile key is treated as "default", matching UpdateActionState's own
// "default" fallback for profile-scoped operations elsewhere in this file.
func TestDailyActionCount_EmptyProfileDefaultsToDefault(t *testing.T) {
	db := newCountersTestDB(t)

	if _, err := db.IncrementDailyActionCount("", "follow_users"); err != nil {
		t.Fatalf("IncrementDailyActionCount: %v", err)
	}
	got, err := db.GetDailyActionCount("default", "follow_users")
	if err != nil {
		t.Fatalf("GetDailyActionCount: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected empty profile key to be recorded under \"default\", got count %d", got)
	}
}
