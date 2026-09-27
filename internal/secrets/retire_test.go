package secrets

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func countKind(t *testing.T, db *sql.DB, kind string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vault_secrets WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", kind, err)
	}
	return n
}

// An upgraded install still has the removed AI provider stack's keys in the
// vault. They are backed up to a file whose passphrase is kept as a plain
// secret, then deleted; importing that file restores them as plain secrets.
func TestRetireAIProviderEntries_BacksUpThenPurges(t *testing.T) {
	db := newExportTestDB(t)
	ctx := context.Background()
	backupDir := filepath.Join(t.TempDir(), "backups")

	for _, e := range []struct{ profile, name, key string }{
		{"default", "My OpenAI", "sk-openai-111"},
		{"default", "Claude key", "sk-ant-222"},
		{"p2", "Work Gemini", "gm-333"},
	} {
		if _, err := addEntry(ctx, db.DB, e.profile, retiredAIProviderKind, e.name, map[string]string{"api_key": e.key}, "", "", ""); err != nil {
			t.Fatalf("seed %s: %v", e.name, err)
		}
	}
	if _, err := Add(ctx, db.DB, "default", "secret", "unrelated", map[string]string{"v": "keep-me"}, "", "", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	retired, err := RetireAIProviderEntries(ctx, db.DB, backupDir)
	if err != nil {
		t.Fatalf("RetireAIProviderEntries: %v", err)
	}
	if retired != 3 {
		t.Fatalf("retired = %d, want 3", retired)
	}
	if n := countKind(t, db.DB, retiredAIProviderKind); n != 0 {
		t.Fatalf("%d ai_provider entries left", n)
	}
	files, _ := filepath.Glob(filepath.Join(backupDir, "retired-ai-providers-*.json"))
	if len(files) != 2 {
		t.Fatalf("backup files = %v, want one per profile", files)
	}
	if runtime.GOOS != "windows" {
		for _, f := range files {
			if st, err := os.Stat(f); err != nil || st.Mode().Perm() != 0o600 {
				t.Errorf("%s mode = %v (%v), want 0600", f, st.Mode().Perm(), err)
			}
		}
	}

	entries, err := List(ctx, db.DB, "default")
	if err != nil {
		t.Fatal(err)
	}
	if findEntryID(entries, "unrelated") == "" {
		t.Error("an unrelated secret was deleted")
	}
	pwID := findEntryID(entries, RetiredAIProviderBackupName)
	if pwID == "" {
		t.Fatalf("no %q entry in %+v", RetiredAIProviderBackupName, entries)
	}
	pwFields, notes, err := DecryptFields(ctx, db.DB, "default", pwID)
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "retired-ai-providers-default-") {
			backup = f
		}
	}
	if backup == "" || !strings.Contains(notes, backup) {
		t.Fatalf("passphrase notes %q do not name the default profile's backup (%v)", notes, files)
	}

	// Restore on a fresh machine: the keys come back as plain secrets.
	dst := newExportTestDB(t)
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := Import(ctx, dst.DB, "default", pwFields["passphrase"], data, nil, nil)
	if err != nil {
		t.Fatalf("Import(backup): %v", err)
	}
	if imported != 2 {
		t.Fatalf("imported = %d, want 2", imported)
	}
	if n := countKind(t, dst.DB, retiredAIProviderKind); n != 0 {
		t.Errorf("import recreated %d ai_provider entries", n)
	}
	restored, _ := List(ctx, dst.DB, "default")
	f, _, err := DecryptFields(ctx, dst.DB, "default", findEntryID(restored, "Claude key"))
	if err != nil || f["api_key"] != "sk-ant-222" {
		t.Fatalf("restored Claude key = %v (%v)", f, err)
	}
	for _, e := range restored {
		if e.Kind != "secret" {
			t.Errorf("restored %q has kind %q, want secret", e.Name, e.Kind)
		}
	}

	// Nothing left to retire: no new backup, no second passphrase entry.
	again, err := RetireAIProviderEntries(ctx, db.DB, backupDir)
	if err != nil || again != 0 {
		t.Fatalf("second run = %d, %v; want 0, nil", again, err)
	}
	if files2, _ := filepath.Glob(filepath.Join(backupDir, "*.json")); len(files2) != 2 {
		t.Errorf("second run wrote a backup: %v", files2)
	}
}

// Keys that cannot be decrypted right now (e.g. a locked keyring) must not
// be deleted without a backup: the profile is skipped and retried later.
func TestRetireAIProviderEntries_LeavesUndecryptableEntries(t *testing.T) {
	db := newExportTestDB(t)
	ctx := context.Background()
	backupDir := filepath.Join(t.TempDir(), "backups")

	id, err := addEntry(ctx, db.DB, "default", retiredAIProviderKind, "Broken", map[string]string{"api_key": "sk-x"}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`UPDATE vault_secrets SET ciphertext = X'00' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	if _, err := RetireAIProviderEntries(ctx, db.DB, backupDir); err == nil {
		t.Fatal("RetireAIProviderEntries succeeded on an undecryptable entry")
	}
	if n := countKind(t, db.DB, retiredAIProviderKind); n != 1 {
		t.Errorf("ai_provider entries = %d, want the entry left in place", n)
	}
	if files, _ := filepath.Glob(filepath.Join(backupDir, "*")); len(files) != 0 {
		t.Errorf("backup written for a failed profile: %v", files)
	}
	entries, _ := List(ctx, db.DB, "default")
	if findEntryID(entries, RetiredAIProviderBackupName) != "" {
		t.Error("passphrase entry saved for a failed profile")
	}
}
