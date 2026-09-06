package storage

import (
	"path/filepath"
	"testing"
)

// newUpsertPersonTestDB returns a fully-migrated Database backed by a temp
// file, ready for UpsertPerson exercises.
func newUpsertPersonTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := NewDatabase(filepath.Join(t.TempDir(), "upsert-person.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db
}

// TestUpsertPersonPreservesEnrichmentOnPartialUpsert is a regression test
// for the data-loss bug where UpsertPerson's ON CONFLICT DO UPDATE SET
// clause wrote excluded.X straight over every enrichment column with no
// COALESCE fallback to the existing row. A caller that only ever populates
// a subset of fields on Person — exactly what SyncOutlookMessageNode does,
// setting only PlatformUsername/Platform/FullName/ProfileID — would
// silently wipe out enrichment previously written by another writer (e.g.
// people.save) back to NULL/0/false, with no error or log line.
func TestUpsertPersonPreservesEnrichmentOnPartialUpsert(t *testing.T) {
	db := newUpsertPersonTestDB(t)

	// First upsert: full enrichment, as if written by people.save from a
	// scraped profile.
	full := &Person{
		PlatformUsername: "alice@example.com",
		Platform:         "email",
		ProfileID:        "default",
		FullName:         "Alice Example",
		ImageURL:         "https://example.com/alice.png",
		ContactDetails:   "alice@example.com / +1-555-0100",
		Website:          "https://alice.example.com",
		ContentCount:     42,
		FollowerCount:    "1200",
		FollowingCount:   300,
		Introduction:     "Product lead at Example Corp.",
		IsVerified:       true,
		Category:         "Product",
		JobTitle:         "Head of Product",
	}
	if err := db.UpsertPerson(full); err != nil {
		t.Fatalf("first UpsertPerson: %v", err)
	}

	// Second upsert: mimics SyncOutlookMessageNode.Execute's call shape —
	// only PlatformUsername, Platform, FullName, and ProfileID are set;
	// every other field is left at its Go zero value.
	partial := &Person{
		PlatformUsername: "alice@example.com",
		Platform:         "email",
		ProfileID:        "default",
		FullName:         "Alice Example",
	}
	if err := db.UpsertPerson(partial); err != nil {
		t.Fatalf("second (partial) UpsertPerson: %v", err)
	}

	got, err := db.GetPersonByUsername("alice@example.com", "email", "default")
	if err != nil {
		t.Fatalf("GetPersonByUsername: %v", err)
	}
	if got == nil {
		t.Fatal("expected person to exist after upserts, got nil")
	}

	// The enrichment fields from the FIRST upsert must survive the second,
	// zero-value-heavy upsert untouched.
	if got.ImageURL != full.ImageURL {
		t.Errorf("ImageURL wiped: got %q, want %q", got.ImageURL, full.ImageURL)
	}
	if got.ContactDetails != full.ContactDetails {
		t.Errorf("ContactDetails wiped: got %q, want %q", got.ContactDetails, full.ContactDetails)
	}
	if got.Website != full.Website {
		t.Errorf("Website wiped: got %q, want %q", got.Website, full.Website)
	}
	if got.ContentCount != full.ContentCount {
		t.Errorf("ContentCount wiped: got %d, want %d", got.ContentCount, full.ContentCount)
	}
	if got.FollowerCount != full.FollowerCount {
		t.Errorf("FollowerCount wiped: got %q, want %q", got.FollowerCount, full.FollowerCount)
	}
	if got.FollowingCount != full.FollowingCount {
		t.Errorf("FollowingCount wiped: got %d, want %d", got.FollowingCount, full.FollowingCount)
	}
	if got.Introduction != full.Introduction {
		t.Errorf("Introduction wiped: got %q, want %q", got.Introduction, full.Introduction)
	}
	if got.IsVerified != full.IsVerified {
		t.Errorf("IsVerified wiped: got %v, want %v", got.IsVerified, full.IsVerified)
	}
	if got.Category != full.Category {
		t.Errorf("Category wiped: got %q, want %q", got.Category, full.Category)
	}
	if got.JobTitle != full.JobTitle {
		t.Errorf("JobTitle wiped: got %q, want %q", got.JobTitle, full.JobTitle)
	}
}

// TestUpsertPersonAppliesRealEnrichmentUpdate ensures COALESCE doesn't go
// too far the other way: a later upsert that legitimately provides new,
// non-zero enrichment values must still win over the stale stored ones.
func TestUpsertPersonAppliesRealEnrichmentUpdate(t *testing.T) {
	db := newUpsertPersonTestDB(t)

	first := &Person{
		PlatformUsername: "bob@example.com",
		Platform:         "email",
		ProfileID:        "default",
		FullName:         "Bob",
		JobTitle:         "Engineer",
		IsVerified:       false,
	}
	if err := db.UpsertPerson(first); err != nil {
		t.Fatalf("first UpsertPerson: %v", err)
	}

	second := &Person{
		PlatformUsername: "bob@example.com",
		Platform:         "email",
		ProfileID:        "default",
		FullName:         "Bob Roberts",
		JobTitle:         "Senior Engineer",
		IsVerified:       true,
	}
	if err := db.UpsertPerson(second); err != nil {
		t.Fatalf("second UpsertPerson: %v", err)
	}

	got, err := db.GetPersonByUsername("bob@example.com", "email", "default")
	if err != nil {
		t.Fatalf("GetPersonByUsername: %v", err)
	}
	if got == nil {
		t.Fatal("expected person to exist, got nil")
	}
	if got.FullName != "Bob Roberts" {
		t.Errorf("FullName not updated: got %q, want %q", got.FullName, "Bob Roberts")
	}
	if got.JobTitle != "Senior Engineer" {
		t.Errorf("JobTitle not updated: got %q, want %q", got.JobTitle, "Senior Engineer")
	}
	if !got.IsVerified {
		t.Errorf("IsVerified not updated: got %v, want true", got.IsVerified)
	}
}
