package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/storage"

	"github.com/zalando/go-keyring"
)

func newLoginTestDB(t *testing.T) *storage.Database {
	t.Helper()
	keyring.MockInit()
	db, err := storage.NewDatabase(t.TempDir() + "/login-test.db")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func TestUpsertSessionRow_CreatesThenUpdatesInPlace(t *testing.T) {
	db := newLoginTestDB(t)
	ctx := context.Background()

	firstCookies := []byte(`[{"name":"sid","value":"abc"}]`)
	if err := upsertSessionRow(ctx, db.DB, "default", "instagram", "alice", firstCookies); err != nil {
		t.Fatalf("first upsertSessionRow: %v", err)
	}

	var vaultRef string
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*), vault_ref FROM crawler_sessions WHERE platform = 'instagram' AND username = 'alice'`).Scan(&count, &vaultRef); err != nil {
		t.Fatalf("reading session row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one session row, got %d", count)
	}
	if vaultRef == "" {
		t.Fatal("expected vault_ref to be populated")
	}

	resolved, _, err := secrets.DecryptFields(ctx, db.DB, "default", vaultRef)
	if err != nil {
		t.Fatalf("DecryptFields: %v", err)
	}
	if resolved["cookies"] != string(firstCookies) {
		t.Fatalf("unexpected cookies field: %q", resolved["cookies"])
	}

	// Second call for the same platform+username must update in place, not
	// create a second row or a second vault entry.
	secondCookies := []byte(`[{"name":"sid","value":"xyz"}]`)
	if err := upsertSessionRow(ctx, db.DB, "default", "instagram", "alice", secondCookies); err != nil {
		t.Fatalf("second upsertSessionRow: %v", err)
	}
	var vaultRef2 string
	if err := db.DB.QueryRow(`SELECT COUNT(*), vault_ref FROM crawler_sessions WHERE platform = 'instagram' AND username = 'alice'`).Scan(&count, &vaultRef2); err != nil {
		t.Fatalf("reading session row after update: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected still exactly one session row, got %d", count)
	}
	if vaultRef2 != vaultRef {
		t.Fatalf("expected the same vault entry to be reused, got %q want %q", vaultRef2, vaultRef)
	}
	resolved, _, err = secrets.DecryptFields(ctx, db.DB, "default", vaultRef2)
	if err != nil {
		t.Fatalf("DecryptFields after update: %v", err)
	}
	if resolved["cookies"] != string(secondCookies) {
		t.Fatalf("expected updated cookies, got %q", resolved["cookies"])
	}
}

// TestLoginStatusTableRightAlignsNumericIDColumn guards against a
// tablewriter v1 migration regression: v0.0.5 auto-right-aligned any cell
// whose content was purely numeric (via regex), with no explicit
// SetColumnAlignment call needed; v1's shared newPlainTable helper has no
// such content-sniffing default, so a numeric ID column that never
// explicitly requested alignment silently went from right- to
// left-aligned. Seeds two sessions with deliberately different ID widths
// (1 vs 100, inserted directly so the numeric IDs are controlled rather
// than relying on autoincrement) and asserts the shorter ID is
// left-padded to align with the wider one, not left-flush against it.
func TestLoginStatusTableRightAlignsNumericIDColumn(t *testing.T) {
	dbPath := t.TempDir() + "/login-status-test.db"
	keyring.MockInit()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	future := time.Now().Add(24 * time.Hour)

	insert := func(id int, username string) {
		t.Helper()
		if _, err := db.DB.Exec(
			`INSERT INTO crawler_sessions (id, username, platform, cookies_json, expiry, when_added, profile_id, vault_ref) VALUES (?, ?, ?, '{}', ?, ?, 'default', '')`,
			id, username, "instagram", future, time.Now(),
		); err != nil {
			t.Fatalf("seeding session id=%d: %v", id, err)
		}
	}
	insert(1, "shortid")
	insert(100, "longid")

	cfg := &globalConfig{DBPath: dbPath, JSONOutput: false}
	cmd := newLoginCmd(cfg)
	cmd.SetArgs([]string{"status"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("login status: %v", err)
	}

	lines := strings.Split(out.String(), "\n")
	var line1, line100 string
	for _, l := range lines {
		if strings.Contains(l, "shortid") {
			line1 = l
		}
		if strings.Contains(l, "longid") {
			line100 = l
		}
	}
	if line1 == "" || line100 == "" {
		t.Fatalf("expected both session rows in output, got:\n%s", out.String())
	}
	pos1 := strings.Index(line1, "1")
	pos100 := strings.Index(line100, "100")
	if pos1 <= pos100 {
		t.Fatalf("expected the single-digit ID to start further right than the 3-digit ID (right-alignment), got ID \"1\" at column %d and \"100\" at column %d\nrow1:   %q\nrow100: %q", pos1, pos100, line1, line100)
	}
}
