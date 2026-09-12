package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyMigration040_AIChatForeignKeys_FreshDatabase is a regression test
// for docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md's
// "DeleteConversation isn't atomic against a concurrent StartChatTurn":
// neither ai_chat_turns nor ai_chat_events ever had a foreign key back to
// ai_chat_conversations, so nothing at the schema level stopped an orphaned
// turn/event row from surviving a deleted conversation. This applies every
// migration (including 040_ai_chat_conversation_foreign_keys.sql) to a
// brand-new, empty database -- the case where ApplyMigrations runs BEFORE
// ai.AIStore.initTables() ever gets a chance to create
// ai_chat_conversations/ai_chat_turns/ai_chat_events itself, since those
// three tables are otherwise Go-managed, not migration-managed. Migration
// 040's own bootstrap CREATE TABLE IF NOT EXISTS statements must make this
// safe.
func TestApplyMigration040_AIChatForeignKeys_FreshDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh-ai-chat-fk.db")
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()

	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations on a fresh database: %v", err)
	}

	for _, table := range []string{"ai_chat_conversations", "ai_chat_turns", "ai_chat_events"} {
		var name string
		if err := db.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("expected %s to exist after migration: %v", table, err)
		}
	}

	assertForeignKey(t, db.DB, "ai_chat_turns", "conversation_id", "ai_chat_conversations")
	assertForeignKey(t, db.DB, "ai_chat_events", "conversation_id", "ai_chat_conversations")
	assertForeignKey(t, db.DB, "ai_chat_events", "turn_id", "ai_chat_turns")

	// The concrete, sharp assertion the fix promises: a raw insert against a
	// conversation_id that does not exist must now fail.
	if _, err := db.DB.Exec(
		`INSERT INTO ai_chat_turns (id, conversation_id, profile_id, prompt, created_at, updated_at)
		 VALUES ('t-bogus', 'no-such-conversation', 'default', 'hi', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err == nil {
		t.Fatal("insert into ai_chat_turns with a bogus conversation_id unexpectedly succeeded -- foreign key not enforced")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY") {
		t.Errorf("insert failed as expected but not with a foreign key error: %v", err)
	}

	if _, err := db.DB.Exec(
		`INSERT INTO ai_chat_events (profile_id, conversation_id, turn_id, seq, created_at, version, type, payload)
		 VALUES ('default', 'no-such-conversation', 'no-such-turn', 1, '2026-01-01T00:00:00Z', 1, 'turn.started', '{}')`,
	); err == nil {
		t.Fatal("insert into ai_chat_events with a bogus conversation_id/turn_id unexpectedly succeeded -- foreign key not enforced")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY") {
		t.Errorf("insert failed as expected but not with a foreign key error: %v", err)
	}
}

// TestApplyMigration040_AIChatForeignKeys_PreservesExistingRows simulates a
// real, existing production database: it has been running the pre-migration
// shape (ai_chat_conversations/ai_chat_turns/ai_chat_events created by Go
// code -- internal/ai/chat_events.go's initChatEventTables -- with no
// foreign key at all) for a while, accumulating real conversations, turns
// and events, and ONLY THEN upgrades to a binary whose migrations include
// 040. No existing row may be lost or altered by the rebuild, and the new
// constraint must actually be enforced afterward on this exact upgraded
// database (not just a freshly-created one).
//
// The pre-040 CREATE TABLE statements below are duplicated from
// chat_events.go's initChatEventTables() rather than constructed by
// importing internal/ai (which would make internal/storage's own test
// binary depend on the higher-level ai package purely for schema strings --
// a layering inversion for no real benefit). This mirrors how
// internal/storage/reconcile.go's vaultKeysPerProfileDDL already duplicates
// 027_vault_keys_per_profile.sql's DDL with an explicit
// byte-compatibility comment; the same discipline applies here: keep this
// byte-compatible with chat_events.go's pre-foreign-key shape.
func TestApplyMigration040_AIChatForeignKeys_PreservesExistingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade-ai-chat-fk.db")
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()

	// Build the pre-040 schema: migrations 1-39 (which never touch these
	// tables at all) plus the exact pre-migration-040 ai_chat_* shape.
	applyMigrationsBelow(t, db, 40)
	exec := func(query string, args ...interface{}) {
		if _, err := db.DB.Exec(query, args...); err != nil {
			t.Fatalf("seeding %q: %v", query, err)
		}
	}
	exec(`CREATE TABLE ai_chat_conversations (
		id TEXT PRIMARY KEY,
		profile_id TEXT NOT NULL,
		backend TEXT NOT NULL,
		workflow_context TEXT NOT NULL DEFAULT '',
		runtime_id TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		history_key TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	exec(`CREATE TABLE ai_chat_turns (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		profile_id TEXT NOT NULL,
		owner_instance_id TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		reason TEXT NOT NULL DEFAULT '',
		last_committed_seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	exec(`CREATE TABLE ai_chat_events (
		profile_id TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		turn_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		version INTEGER NOT NULL,
		type TEXT NOT NULL,
		payload TEXT NOT NULL,
		PRIMARY KEY (profile_id, conversation_id, turn_id, seq)
	)`)

	// Populate as if the app had been running for a while: two independent
	// conversations, each with a turn and an event.
	exec(`INSERT INTO ai_chat_conversations (id, profile_id, backend, created_at, updated_at) VALUES ('conv-1', 'default', 'agent', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO ai_chat_conversations (id, profile_id, backend, created_at, updated_at) VALUES ('conv-2', 'default', 'agent', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO ai_chat_turns (id, conversation_id, profile_id, prompt, status, created_at, updated_at) VALUES ('turn-1', 'conv-1', 'default', 'hello', 'completed', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO ai_chat_turns (id, conversation_id, profile_id, prompt, status, created_at, updated_at) VALUES ('turn-2', 'conv-2', 'default', 'hi again', 'completed', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO ai_chat_events (profile_id, conversation_id, turn_id, seq, created_at, version, type, payload) VALUES ('default', 'conv-1', 'turn-1', 1, '2026-01-01T00:00:00Z', 1, 'turn.started', '{}')`)
	exec(`INSERT INTO ai_chat_events (profile_id, conversation_id, turn_id, seq, created_at, version, type, payload) VALUES ('default', 'conv-2', 'turn-2', 1, '2026-01-01T00:00:00Z', 1, 'turn.started', '{}')`)

	// Upgrade: run ApplyMigrations again, now discovering and applying 040.
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations (040 rebuild over existing data): %v", err)
	}

	count := func(query string) int {
		var n int
		if err := db.DB.QueryRow(query).Scan(&n); err != nil {
			t.Fatalf("counting %q: %v", query, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM ai_chat_conversations`); n != 2 {
		t.Errorf("ai_chat_conversations row count = %d, want 2 (no data lost)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ai_chat_turns`); n != 2 {
		t.Errorf("ai_chat_turns row count = %d, want 2 (no data lost)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ai_chat_events`); n != 2 {
		t.Errorf("ai_chat_events row count = %d, want 2 (no data lost)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ai_chat_turns WHERE id = 'turn-1' AND conversation_id = 'conv-1' AND prompt = 'hello' AND status = 'completed'`); n != 1 {
		t.Error("turn-1's own data was altered by the rebuild, want it preserved byte-for-byte")
	}
	if n := count(`SELECT COUNT(*) FROM ai_chat_events WHERE conversation_id = 'conv-2' AND turn_id = 'turn-2' AND seq = 1`); n != 1 {
		t.Error("conv-2's event was altered by the rebuild, want it preserved byte-for-byte")
	}

	assertForeignKey(t, db.DB, "ai_chat_turns", "conversation_id", "ai_chat_conversations")
	assertForeignKey(t, db.DB, "ai_chat_events", "conversation_id", "ai_chat_conversations")
	assertForeignKey(t, db.DB, "ai_chat_events", "turn_id", "ai_chat_turns")

	// The new constraint must actually be enforced now, on this exact
	// upgraded (not freshly-created) database -- not just conceptually
	// present in a fresh install.
	if _, err := db.DB.Exec(
		`INSERT INTO ai_chat_turns (id, conversation_id, profile_id, prompt, created_at, updated_at)
		 VALUES ('turn-bogus', 'no-such-conversation', 'default', 'hi', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err == nil {
		t.Fatal("insert into ai_chat_turns with a bogus conversation_id unexpectedly succeeded on the upgraded database")
	}
	if _, err := db.DB.Exec(
		`INSERT INTO ai_chat_events (profile_id, conversation_id, turn_id, seq, created_at, version, type, payload)
		 VALUES ('default', 'conv-1', 'no-such-turn', 2, '2026-01-01T00:00:00Z', 1, 'turn.started', '{}')`,
	); err == nil {
		t.Fatal("insert into ai_chat_events with a bogus turn_id unexpectedly succeeded on the upgraded database")
	}
}

// assertForeignKey fails the test unless table has a foreign key on column
// referencing refTable, via PRAGMA foreign_key_list. table/column/refTable
// are always caller-supplied literals (never external input), so building
// the PRAGMA statement by concatenation (PRAGMA does not accept bound
// parameters) is safe here -- the same approach database_test.go already
// uses for `PRAGMA table_info(crawler_sessions)`.
func assertForeignKey(t *testing.T, db *sql.DB, table, column, refTable string) {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%s)", table))
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_list(%s): %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("foreign_key_list(%s) columns: %v", table, err)
	}
	found := false
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan foreign_key_list(%s) row: %v", table, err)
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			row[c] = vals[i]
		}
		if fmt.Sprint(row["table"]) == refTable && fmt.Sprint(row["from"]) == column {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_list(%s): %v", table, err)
	}
	if !found {
		t.Errorf("%s.%s has no foreign key to %s (PRAGMA foreign_key_list(%s) found no matching entry)", table, column, refTable, table)
	}
}
