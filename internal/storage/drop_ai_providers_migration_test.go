package storage

import (
	"path/filepath"
	"testing"
)

func tableExistsForTest(t *testing.T, d *Database, name string) bool {
	t.Helper()
	var n int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	return n > 0
}

// Migration 051 on a fresh database: there is no ai_providers table to drop,
// and the migration must not fail over it.
func TestApplyMigration051_DropAIProviders_FreshDatabase(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if tableExistsForTest(t, db, "ai_providers") {
		t.Error("ai_providers exists on a fresh database")
	}
}

// Migration 051 on an upgraded database that has the provider table with a
// row and chat history next to it: the provider table goes, the history
// stays, and applying again is a no-op.
func TestApplyMigration051_DropAIProviders_PopulatedDatabase(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "upgraded.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	applyMigrationsBelow(t, db, 51)

	// The shape internal/ai used to create at startup.
	for _, stmt := range []string{
		`CREATE TABLE ai_providers (id TEXT PRIMARY KEY, name TEXT NOT NULL, provider_id TEXT NOT NULL, tier TEXT NOT NULL,
			api_key TEXT NOT NULL, base_url TEXT NOT NULL DEFAULT '', default_model TEXT NOT NULL DEFAULT '',
			extra_headers TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'untested', last_tested TEXT NOT NULL DEFAULT '',
			profile_id TEXT NOT NULL DEFAULT 'default', vault_ref TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
		`INSERT INTO ai_providers (id, name, provider_id, tier, api_key, vault_ref, created_at)
			VALUES ('p1', 'My OpenAI', 'openai', 'known', '', 'vault-1', '2026-01-01T00:00:00Z')`,
		`CREATE TABLE IF NOT EXISTS ai_chat_messages (id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, role TEXT NOT NULL,
			content TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`INSERT INTO ai_chat_messages (id, workflow_id, role, content, created_at) VALUES ('m1', 'wf', 'user', 'hi', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := db.DB.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if tableExistsForTest(t, db, "ai_providers") {
		t.Error("ai_providers survived migration 051")
	}
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM ai_chat_messages`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ai_chat_messages rows = %d (%v), want the history kept", n, err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("second ApplyMigrations: %v", err)
	}
}
