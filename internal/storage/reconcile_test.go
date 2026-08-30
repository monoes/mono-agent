package storage

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/data"
)

// hasColumn reports whether table has column, via PRAGMA table_info.
func hasColumn(t *testing.T, d *Database, table, column string) bool {
	t.Helper()
	ok, err := d.columnExists(context.Background(), table, column)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	return ok
}

// execMigrationFile applies an embedded migration file's statements and
// records it under the given (possibly aliased) version — used to reproduce
// historical branch states the runner can no longer produce on its own.
func execMigrationFile(t *testing.T, d *Database, filename string, version int) {
	t.Helper()
	content, err := data.MigrationsFS.ReadFile("migrations/" + filename)
	if err != nil {
		t.Fatalf("reading %s: %v", filename, err)
	}
	for _, stmt := range splitStatements(string(content)) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := d.DB.Exec(stmt); err != nil {
			t.Fatalf("applying %s: %v", filename, err)
		}
	}
	if _, err := d.DB.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		t.Fatalf("recording %s as version %d: %v", filename, version, err)
	}
}

// simulateOurBranchDB reproduces a database created by the pre-merge branch
// whose 023/024 were the execution-list and workflows-profile-default INDEX
// migrations: versions 23/24 are recorded against those files, the vault
// migrations (per-profile vault_keys, profiles.root_dir) were never applied,
// and vault_keys still has the old singleton shape from migration 017.
func simulateOurBranchDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "our-branch.db")
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()

	applyMigrationsBelow(t, db, 23) // 001..022, the shared prefix

	// Our branch's 023/024 — the same files that live at 025/026 today
	// (both CREATE INDEX IF NOT EXISTS, so re-applying them at their new
	// numbers stays a no-op).
	execMigrationFile(t, db, "025_execution_list_index.sql", 23)
	execMigrationFile(t, db, "026_workflows_profile_default_index.sql", 24)
	return dbPath
}

// TestApplyMigrations_HealsOurBranchDB reproduces FV1: a database whose
// versions 23/24 were recorded by the OTHER files at those numbers makes the
// merged binary skip the vault migrations, leaving vault_keys without
// profile_id ("no such column: profile_id" on every vault op). Reopening
// with the fixed runner must apply the renumbered 027/028 plus the shape
// reconcile and leave a fully usable per-profile vault schema.
func TestApplyMigrations_HealsOurBranchDB(t *testing.T) {
	dbPath := simulateOurBranchDB(t, t.TempDir())

	// Seed the old singleton vault_keys row (migration 017's shape) so the
	// heal can be verified to preserve it as vault_keys_legacy.
	seed, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen for seeding: %v", err)
	}
	if _, err := seed.DB.Exec(`INSERT INTO vault_keys (id, wrapped_dek, wrapped_nonce, created_at)
		VALUES (1, x'0102030405060708090a0b0c0d0e0f10', x'1112131415161718191a1b1c', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding singleton vault_keys: %v", err)
	}
	seed.DB.Close()

	// Reopen with the new code: apply + reconcile must heal in one step.
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations on our-branch DB: %v", err)
	}

	if !hasColumn(t, db, "vault_keys", "profile_id") {
		t.Fatal("vault_keys still lacks profile_id after heal")
	}
	if !hasColumn(t, db, "vault_keys_legacy", "id") {
		t.Fatal("vault_keys_legacy (old singleton shape) missing after heal")
	}
	if !hasColumn(t, db, "profiles", "root_dir") {
		t.Fatal("profiles still lacks root_dir after heal")
	}

	// The seeded singleton row must have been preserved into legacy.
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM vault_keys_legacy WHERE id = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("vault_keys_legacy row preserved: n=%d err=%v", n, err)
	}

	// The renumbered migrations must now be recorded, with filenames.
	var fn string
	if err := db.DB.QueryRow(`SELECT filename FROM schema_migrations WHERE version = 27`).Scan(&fn); err != nil || fn != "027_vault_keys_per_profile.sql" {
		t.Fatalf("version 27 filename: %q err=%v", fn, err)
	}

	// A vault query that used to fail with "no such column" now works.
	if _, err := db.DB.Exec(`UPDATE vault_keys SET created_at = created_at WHERE profile_id = 'default'`); err != nil {
		t.Fatalf("vault op on healed schema: %v", err)
	}

	// Second open is a clean no-op.
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("second ApplyMigrations: %v", err)
	}
}

// TestApplyMigrations_DelegationBranchDBIsNoOp reproduces the second
// population: databases that recorded 23/24 as the VAULT migrations already
// have the per-profile shape. The renumbered 027/028 must not raise
// duplicate-table/duplicate-column errors against them, and the reconcile
// must leave their data untouched.
func TestApplyMigrations_DelegationBranchDBIsNoOp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "delegation.db")
	db, err := NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()

	applyMigrationsBelow(t, db, 23)

	// The delegation branch's 023/024 — the original, unguarded vault pair.
	for _, stmt := range []string{
		`ALTER TABLE vault_keys RENAME TO vault_keys_legacy`,
		`CREATE TABLE vault_keys (
			profile_id    TEXT PRIMARY KEY,
			wrapped_dek   BLOB NOT NULL,
			wrapped_nonce BLOB NOT NULL,
			created_at    TEXT NOT NULL
		)`,
		`ALTER TABLE profiles ADD COLUMN root_dir TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.DB.Exec(stmt); err != nil {
			t.Fatalf("applying delegation migration: %v", err)
		}
	}
	for _, v := range []int{23, 24} {
		if _, err := db.DB.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, v); err != nil {
			t.Fatalf("recording delegation version %d: %v", v, err)
		}
	}
	// A per-profile key row that must survive the new runner's 027 (a
	// guarded no-op) and the reconcile (shape already correct).
	if _, err := db.DB.Exec(`INSERT INTO vault_keys (profile_id, wrapped_dek, wrapped_nonce, created_at)
		VALUES ('default', x'a1a2a3a4', x'b1b2b3b4', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding per-profile vault_keys: %v", err)
	}

	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations on delegation DB (must be a clean no-op): %v", err)
	}

	if !hasColumn(t, db, "vault_keys", "profile_id") {
		t.Fatal("vault_keys lost profile_id")
	}
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM vault_keys WHERE profile_id = 'default'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("per-profile key row lost: n=%d err=%v", n, err)
	}
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM vault_keys_legacy`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("vault_keys_legacy unexpectedly populated: n=%d err=%v", n, err)
	}
	for _, v := range []int{25, 26, 27, 28} {
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, v).Scan(&n); err != nil || n != 1 {
			t.Fatalf("version %d not recorded: n=%d err=%v", v, n, err)
		}
	}
}

// TestApplyMigrations_FreshDatabaseHasPerProfileVaultShape pins the third
// population: a brand-new database must come up with the healed shape (the
// rename half of 027 lives in the reconcile, so it must run on fresh DBs
// too, not just drifted ones).
func TestApplyMigrations_FreshDatabaseHasPerProfileVaultShape(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	if !hasColumn(t, db, "vault_keys", "profile_id") {
		t.Fatal("fresh vault_keys lacks profile_id")
	}
	if !hasColumn(t, db, "vault_keys_legacy", "id") {
		t.Fatal("fresh vault_keys_legacy missing (empty pre-per-profile table should be preserved by the rename)")
	}
	if !hasColumn(t, db, "profiles", "root_dir") {
		t.Fatal("fresh profiles lacks root_dir")
	}
}

// TestReconcileSchema_BackfillsExecutionProfiles covers the execution
// profile backfill: executions stamped 'default' whose workflow belongs to
// another profile get that profile; orphaned executions and genuinely
// default-profile executions are left alone; the whole pass is idempotent.
func TestReconcileSchema_BackfillsExecutionProfiles(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	exec := func(query string, args ...interface{}) {
		t.Helper()
		if _, err := db.DB.Exec(query, args...); err != nil {
			t.Fatalf("seeding %q: %v", query, err)
		}
	}
	exec(`INSERT INTO workflows (id, name, profile_id) VALUES ('w-work', 'workflows of work profile', 'work')`)
	exec(`INSERT INTO workflows (id, name, profile_id) VALUES ('w-default', 'default profile workflows', 'default')`)
	exec(`INSERT INTO workflow_executions (id, workflow_id, profile_id) VALUES ('e-adopt', 'w-work', 'default')`)
	exec(`INSERT INTO workflow_executions (id, workflow_id, profile_id) VALUES ('e-stay', 'w-default', 'default')`)

	// Orphaned execution (its workflow is gone): insert with FK enforcement
	// off — the same dedicated-connection trick the migration runner uses —
	// because the schema's ON DELETE CASCADE normally prevents orphans from
	// existing at all; the backfill still must not touch one if it does.
	ctx := context.Background()
	conn, err := db.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("orphan-seed conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disabling FKs for orphan seed: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO workflow_executions (id, workflow_id, profile_id) VALUES ('e-orphan', 'w-gone', 'default')`); err != nil {
		t.Fatalf("seeding orphaned execution: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("restoring FKs: %v", err)
	}
	conn.Close()

	if err := db.ReconcileSchema(context.Background()); err != nil {
		t.Fatalf("ReconcileSchema: %v", err)
	}

	profile := func(id string) string {
		t.Helper()
		var p string
		if err := db.DB.QueryRow(`SELECT profile_id FROM workflow_executions WHERE id = ?`, id).Scan(&p); err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}
		return p
	}
	if got := profile("e-adopt"); got != "work" {
		t.Fatalf("e-adopt profile = %q, want %q", got, "work")
	}
	if got := profile("e-orphan"); got != "default" {
		t.Fatalf("orphaned e-orphan profile = %q, want left at %q", got, "default")
	}
	if got := profile("e-stay"); got != "default" {
		t.Fatalf("e-stay profile = %q, want %q", got, "default")
	}

	// Idempotent: nothing left to backfill, no error.
	if err := db.ReconcileSchema(context.Background()); err != nil {
		t.Fatalf("second ReconcileSchema: %v", err)
	}
	if got := profile("e-stay"); got != "default" {
		t.Fatalf("e-stay profile changed on second pass: %q", got)
	}
}

// TestApplyMigrations_WarnsOnFilenameAliasing covers the runner hardening:
// a recorded version whose filename no longer matches the current file at
// that version must produce a WARN instead of silently trusting the number.
func TestApplyMigrations_WarnsOnFilenameAliasing(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "alias.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	defer db.DB.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	// Forge an aliased record: version 27 recorded under a filename that is
	// not the current 027_vault_keys_per_profile.sql.
	if _, err := db.DB.Exec(`UPDATE schema_migrations SET filename = '027_something_else.sql' WHERE version = 27`); err != nil {
		t.Fatalf("forging aliased record: %v", err)
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations with aliased record: %v", err)
	}
	if !strings.Contains(buf.String(), "version aliasing suspected") {
		t.Fatalf("expected aliasing WARN, got log output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), `"027_something_else.sql"`) {
		t.Fatalf("WARN should name the recorded filename, got:\n%s", buf.String())
	}
}
