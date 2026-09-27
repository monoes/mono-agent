package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 053 on a database holding the "people" that saving LinkedIn
// posts created (urn:li:activity:… usernames) next to a real person that
// shares their history: the bogus rows and what only exists for them go,
// history rows are kept but unlinked, the real person is untouched, and the
// new profile columns exist.
func TestApplyMigration053_PeopleProfileDetails(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "upgraded.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	applyMigrationsBelow(t, db, 53)

	for _, stmt := range []string{
		`INSERT INTO people (id, platform_username, platform, full_name, profile_id) VALUES
			('real', 'ada-first-test', 'LINKEDIN', 'Ada First', 'p1'),
			('urn1', 'urn:li:activity:7509522219027554305', 'LINKEDIN', NULL, 'p1'),
			('urn2', 'urn:li:activity:7508827577256460288', 'linkedin', NULL, 'default'),
			('hn', 'item', 'HACKERNEWS', NULL, 'p1'),
			('hnreal', 'item', 'HACKERNEWS', 'Item Person', 'p2')`,
		`INSERT INTO workflow_node_targets (id, node_id, person_id, platform, link) VALUES
			('t1', 'cli-node', 'urn1', 'LINKEDIN', 'https://www.linkedin.com/feed/update/urn:li:activity:7509522219027554305/'),
			('t2', 'cli-node', 'real', 'LINKEDIN', 'https://www.linkedin.com/in/ada-first-test/')`,
		`INSERT INTO posts (id, person_id, platform, shortcode, url, scraped_at) VALUES
			('post1', 'urn2', 'LINKEDIN', '7508827577256460288', 'https://www.linkedin.com/feed/update/urn:li:activity:7508827577256460288/', '2026-09-26')`,
		`INSERT INTO tags (id, name, profile_id) VALUES ('tag1', 'lead', 'p1')`,
		`INSERT INTO people_tags (person_id, tag_id) VALUES ('urn1', 'tag1'), ('real', 'tag1')`,
		`INSERT INTO person_links (id, profile_id, person_a, person_b, relation, status) VALUES ('l1', 'p1', 'real', 'urn1', 'same', 'suggested')`,
		`INSERT INTO person_messages (id, person_id, source, body) VALUES ('m1', 'urn1', 'manual', 'x'), ('m2', 'real', 'manual', 'y')`,
		`INSERT INTO person_status_updates (id, person_id, text) VALUES ('s1', 'urn1', 'contacted')`,
	} {
		if _, err := db.DB.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	count := func(q string, args ...interface{}) int {
		t.Helper()
		var n int
		if err := db.DB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM people WHERE id IN ('urn1','urn2','hn')`); n != 0 {
		t.Errorf("%d bogus people left", n)
	}
	if n := count(`SELECT COUNT(*) FROM people WHERE id IN ('real','hnreal')`); n != 2 {
		t.Errorf("real people left = %d, want 2", n)
	}
	var pid sql.NullString
	if err := db.DB.QueryRow(`SELECT person_id FROM workflow_node_targets WHERE id = 't1'`).Scan(&pid); err != nil || pid.Valid {
		t.Errorf("t1 person_id = %v (%v), want the target kept and unlinked", pid, err)
	}
	if err := db.DB.QueryRow(`SELECT person_id FROM workflow_node_targets WHERE id = 't2'`).Scan(&pid); err != nil || pid.String != "real" {
		t.Errorf("t2 person_id = %v (%v), want real", pid, err)
	}
	if err := db.DB.QueryRow(`SELECT person_id FROM posts WHERE id = 'post1'`).Scan(&pid); err != nil || pid.Valid {
		t.Errorf("post person_id = %v (%v), want the post kept and unlinked", pid, err)
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM people_tags`:           1,
		`SELECT COUNT(*) FROM person_links`:          0,
		`SELECT COUNT(*) FROM person_messages`:       1,
		`SELECT COUNT(*) FROM person_status_updates`: 0,
	} {
		if n := count(q); n != want {
			t.Errorf("%s = %d, want %d", q, n, want)
		}
	}

	// The new columns take values.
	if _, err := db.DB.Exec(`UPDATE people SET headline = 'h', location = 'l', about = 'a', experience = '[]', education = '[]' WHERE id = 'real'`); err != nil {
		t.Fatalf("new columns: %v", err)
	}
	// Foreign keys still hold after the migration.
	rows, err := db.DB.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check reports violations")
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("second ApplyMigrations: %v", err)
	}
}
