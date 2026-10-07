package main

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestResolveProfileIDMatchesNameCaseInsensitively(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE profiles (id TEXT PRIMARY KEY, name TEXT)`,
		`INSERT INTO profiles (id, name) VALUES ('p-work', 'Work')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []string{"p-work", "Work", "work", "WORK"} {
		if got, err := resolveProfileID(db, in); err != nil || got != "p-work" {
			t.Errorf("resolveProfileID(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := resolveProfileID(db, "nope"); err == nil {
		t.Error("unknown profile resolved")
	}
}
