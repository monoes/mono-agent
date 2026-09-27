package storage

import (
	"path/filepath"
	"testing"
)

// Migration 054 adds people.profile_details, and keeps what people already
// had: a person saved before it keeps every column, and a draft in
// introduction stays a draft.
func TestApplyMigration054_PeopleProfileExtras(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "upgraded.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	applyMigrationsBelow(t, db, 54)

	if _, err := db.DB.Exec(`INSERT INTO people (id, platform_username, platform, full_name, introduction, about, profile_id)
		VALUES ('p', 'fake.ada', 'INSTAGRAM', 'Ada Fixture', 'Hi Ada, loved your work', 'Synthetic bio', 'p1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	var name, intro, about string
	if err := db.DB.QueryRow(`SELECT full_name, introduction, about FROM people WHERE id = 'p'`).Scan(&name, &intro, &about); err != nil {
		t.Fatal(err)
	}
	if name != "Ada Fixture" || intro != "Hi Ada, loved your work" || about != "Synthetic bio" {
		t.Errorf("person changed: %q / %q / %q", name, intro, about)
	}
	// The new column takes a JSON object and merges with json_patch.
	if _, err := db.DB.Exec(`UPDATE people SET profile_details = '{"pronouns":["she/her"],"likes_count":5}' WHERE id = 'p'`); err != nil {
		t.Fatalf("profile_details: %v", err)
	}
	if _, err := db.DB.Exec(`UPDATE people SET profile_details = json_patch(profile_details, '{"likes_count":7}') WHERE id = 'p'`); err != nil {
		t.Fatalf("json_patch: %v", err)
	}
	var details string
	if err := db.DB.QueryRow(`SELECT profile_details FROM people WHERE id = 'p'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if details != `{"pronouns":["she/her"],"likes_count":7}` {
		t.Errorf("profile_details = %s", details)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("second ApplyMigrations: %v", err)
	}
}
