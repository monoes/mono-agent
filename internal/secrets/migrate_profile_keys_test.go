package secrets

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/storage"

	"github.com/zalando/go-keyring"
)

// seedLegacySecret inserts a vault_secrets row encrypted under a fresh
// legacy-scheme DEK/KEK (the fixed pre-per-profile "kek" keychain account +
// a vault_keys_legacy row), reproducing exactly what a secret written before
// the per-profile redesign looks like on disk. Reuses the same legacy key
// across calls within one test (a real legacy install had exactly one).
func seedLegacySecret(t *testing.T, db *storage.Database, profileID, id, name string, fields map[string]string, notes string) {
	t.Helper()
	ctx := context.Background()

	var legacyDEK []byte
	var count int
	if err := db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_keys_legacy WHERE id = 1`).Scan(&count); err != nil {
		t.Fatalf("checking vault_keys_legacy: %v", err)
	}
	if count == 0 {
		kek := make([]byte, 32)
		for i := range kek {
			kek[i] = byte(i + 1)
		}
		if err := keyring.Set(keyringService, legacyKeyringAccount, hex.EncodeToString(kek)); err != nil {
			t.Fatalf("seeding legacy KEK: %v", err)
		}
		legacyDEK = make([]byte, 32)
		for i := range legacyDEK {
			legacyDEK[i] = byte(255 - i)
		}
		wrappedDEK, wrappedNonce, err := Encrypt(kek, legacyDEK)
		if err != nil {
			t.Fatalf("wrapping legacy DEK: %v", err)
		}
		if _, err := db.DB.ExecContext(ctx,
			`INSERT INTO vault_keys_legacy (id, wrapped_dek, wrapped_nonce, created_at) VALUES (1, ?, ?, ?)`,
			wrappedDEK, wrappedNonce, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			t.Fatalf("seeding vault_keys_legacy: %v", err)
		}
	} else {
		kek, found, err := fetchLegacyKEK()
		if err != nil || !found {
			t.Fatalf("re-reading legacy KEK: found=%v err=%v", found, err)
		}
		dek, found, err := fetchLegacyDEK(ctx, db.DB)
		if err != nil || !found {
			t.Fatalf("re-reading legacy DEK: found=%v err=%v", found, err)
		}
		_ = kek
		legacyDEK = dek
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshaling fields: %v", err)
	}
	ciphertext, nonce, err := Encrypt(legacyDEK, fieldsJSON)
	if err != nil {
		t.Fatalf("encrypting fields: %v", err)
	}
	var notesCiphertext, notesNonce []byte
	if notes != "" {
		notesCiphertext, notesNonce, err = Encrypt(legacyDEK, []byte(notes))
		if err != nil {
			t.Fatalf("encrypting notes: %v", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO vault_secrets (id, seq, profile_id, kind, name, ciphertext, nonce, notes_ciphertext, notes_nonce, created_at, updated_at, kv, field_count)
		VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM vault_secrets), ?, 'secret', ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, profileID, name, ciphertext, nonce, notesCiphertext, notesNonce, now, now, len(fields),
	); err != nil {
		t.Fatalf("inserting legacy vault_secrets row: %v", err)
	}
}

func TestMigrateProfileVaultKeys_ReencryptsUnderNewKeyAndIsolatesProfiles(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	seedLegacySecret(t, db, "profile-a", "sec-901", "svc-a", map[string]string{"secret": "a-value"}, "a notes")
	seedLegacySecret(t, db, "profile-b", "sec-902", "svc-b", map[string]string{"secret": "b-value"}, "")

	migratedA, errsA := MigrateProfileVaultKeys(ctx, db.DB, "profile-a")
	if len(errsA) != 0 {
		t.Fatalf("migrating profile-a: %v", errsA)
	}
	if migratedA != 1 {
		t.Fatalf("expected 1 row migrated for profile-a, got %d", migratedA)
	}

	migratedB, errsB := MigrateProfileVaultKeys(ctx, db.DB, "profile-b")
	if len(errsB) != 0 {
		t.Fatalf("migrating profile-b: %v", errsB)
	}
	if migratedB != 1 {
		t.Fatalf("expected 1 row migrated for profile-b, got %d", migratedB)
	}

	// Each profile's secret still decrypts correctly under its own new key.
	fieldsA, notesA, err := DecryptFields(ctx, db.DB, "profile-a", "sec-901")
	if err != nil {
		t.Fatalf("DecryptFields profile-a after migration: %v", err)
	}
	if fieldsA["secret"] != "a-value" || notesA != "a notes" {
		t.Fatalf("profile-a data mismatch after migration: fields=%v notes=%q", fieldsA, notesA)
	}
	fieldsB, _, err := DecryptFields(ctx, db.DB, "profile-b", "sec-902")
	if err != nil {
		t.Fatalf("DecryptFields profile-b after migration: %v", err)
	}
	if fieldsB["secret"] != "b-value" {
		t.Fatalf("profile-b data mismatch after migration: fields=%v", fieldsB)
	}

	// Isolation: profile-b's key must not be able to decrypt profile-a's
	// (now re-encrypted) row, proving the two profiles are on genuinely
	// different keys, not just filtered by profile_id.
	dekB, err := getOrCreateDEK(ctx, db.DB, "profile-b")
	if err != nil {
		t.Fatalf("getOrCreateDEK profile-b: %v", err)
	}
	var ciphertextA, nonceA []byte
	if err := db.DB.QueryRow(`SELECT ciphertext, nonce FROM vault_secrets WHERE id = 'sec-901'`).Scan(&ciphertextA, &nonceA); err != nil {
		t.Fatalf("reading profile-a's re-encrypted row: %v", err)
	}
	if _, err := Decrypt(dekB, ciphertextA, nonceA); err == nil {
		t.Fatal("expected profile-b's key to fail decrypting profile-a's secret, but it succeeded")
	}

	// Idempotency: a second run for an already-migrated profile is a no-op.
	migratedAgain, errsAgain := MigrateProfileVaultKeys(ctx, db.DB, "profile-a")
	if len(errsAgain) != 0 {
		t.Fatalf("second migration run for profile-a: %v", errsAgain)
	}
	if migratedAgain != 0 {
		t.Fatalf("expected second run to be a no-op, got migrated=%d", migratedAgain)
	}
}

func TestMigrateProfileVaultKeys_NoOpWithNoLegacyKey(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	migrated, errs := MigrateProfileVaultKeys(ctx, db.DB, "fresh-profile")
	if len(errs) != 0 {
		t.Fatalf("expected no errors on a fresh install with no legacy key, got %v", errs)
	}
	if migrated != 0 {
		t.Fatalf("expected 0 migrated with no legacy key, got %d", migrated)
	}
}

// applyEmbeddedBelow applies every embedded migration with version < maxVer
// (recording each), reproducing a database state from before a given
// migration existed. Mirrors storage's internal test helper; duplicated here
// because it is unexported there.
func applyEmbeddedBelow(t *testing.T, db *storage.Database, maxVer int) {
	t.Helper()

	if _, err := db.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("creating schema_migrations: %v", err)
	}

	entries, err := data.MigrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("reading migrations dir: %v", err)
	}
	type mig struct {
		version  int
		filename string
	}
	var migs []mig
	for _, e := range entries {
		nm := e.Name()
		if strings.HasPrefix(nm, "._") || e.IsDir() || !strings.HasSuffix(nm, ".sql") {
			continue
		}
		parts := strings.SplitN(nm, "_", 2)
		ver, err := strconv.Atoi(parts[0])
		if err != nil || ver >= maxVer {
			continue
		}
		migs = append(migs, mig{version: ver, filename: nm})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		content, err := data.MigrationsFS.ReadFile("migrations/" + m.filename)
		if err != nil {
			t.Fatalf("reading %s: %v", m.filename, err)
		}
		for _, stmt := range splitSQLStatements(string(content)) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.DB.Exec(stmt); err != nil {
				t.Fatalf("applying %s: %v", m.filename, err)
			}
		}
		if _, err := db.DB.Exec("INSERT INTO schema_migrations (version) VALUES (?)", m.version); err != nil {
			t.Fatalf("recording %s: %v", m.filename, err)
		}
	}
}

// splitSQLStatements splits a SQL script on semicolons, skipping line
// comments and respecting single-quoted strings — the same rules storage's
// migration runner applies (a plain strings.Split breaks on migrations whose
// comments contain semicolons, e.g. 011_profiles.sql).
func splitSQLStatements(script string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inLineComment := false
	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if !inSingleQuote && ch == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			inLineComment = true
			continue
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if ch == '\'' {
			inSingleQuote = !inSingleQuote
		}
		if ch == ';' && !inSingleQuote {
			if stmt := strings.TrimSpace(current.String()); stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		statements = append(statements, stmt)
	}
	return statements
}

// TestMigrateProfileVaultKeys_HealsOurBranchDatabase is the end-to-end FV1
// proof: a database whose recorded 23/24 were the other branch's index
// migrations (so the vault migrations were skipped — vault_keys still the
// migration-017 singleton, no profiles.root_dir), carrying a real legacy
// KEK/DEK and a legacy-encrypted secret. Opening it with the fixed code
// (ApplyMigrations + reconcile) and running the profile migration must
// leave the secret decryptable under the profile's own key.
func TestMigrateProfileVaultKeys_HealsOurBranchDatabase(t *testing.T) {
	keyring.MockInit()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "our-branch-crypto.db")

	// Build the pre-merge our-branch state: 001..022 plus our old 023/024
	// (today's 025/026 index files) recorded at 23/24.
	db1, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	applyEmbeddedBelow(t, db1, 23)
	for _, mig := range []struct {
		filename string
		version  int
	}{{"025_execution_list_index.sql", 23}, {"026_workflows_profile_default_index.sql", 24}} {
		content, err := data.MigrationsFS.ReadFile("migrations/" + mig.filename)
		if err != nil {
			t.Fatalf("reading %s: %v", mig.filename, err)
		}
		if _, err := db1.DB.Exec(string(content)); err != nil {
			t.Fatalf("applying %s as version %d: %v", mig.filename, mig.version, err)
		}
		if _, err := db1.DB.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, mig.version); err != nil {
			t.Fatalf("recording version %d: %v", mig.version, err)
		}
	}

	// Seed real legacy crypto against the OLD singleton vault_keys (id=1):
	// keychain "kek" entry + wrapped legacy DEK + one legacy-encrypted row.
	legacyKEK := make([]byte, 32)
	for i := range legacyKEK {
		legacyKEK[i] = byte(i + 7)
	}
	if err := keyring.Set(keyringService, legacyKeyringAccount, hex.EncodeToString(legacyKEK)); err != nil {
		t.Fatalf("seeding legacy KEK: %v", err)
	}
	legacyDEK := make([]byte, 32)
	for i := range legacyDEK {
		legacyDEK[i] = byte(200 - i)
	}
	wrappedDEK, wrappedNonce, err := Encrypt(legacyKEK, legacyDEK)
	if err != nil {
		t.Fatalf("wrapping legacy DEK: %v", err)
	}
	if _, err := db1.DB.ExecContext(ctx,
		`INSERT INTO vault_keys (id, wrapped_dek, wrapped_nonce, created_at) VALUES (1, ?, ?, ?)`,
		wrappedDEK, wrappedNonce, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seeding singleton vault_keys: %v", err)
	}
	fieldsJSON, _ := json.Marshal(map[string]string{"secret": "pre-merge-value"})
	ct, n, err := Encrypt(legacyDEK, fieldsJSON)
	if err != nil {
		t.Fatalf("encrypting legacy secret: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db1.DB.ExecContext(ctx, `
		INSERT INTO vault_secrets (id, seq, profile_id, kind, name, ciphertext, nonce, notes_ciphertext, notes_nonce, created_at, updated_at, kv, field_count)
		VALUES ('sec-heal', (SELECT COALESCE(MAX(seq),0)+1 FROM vault_secrets), 'default', 'secret', 'svc-heal', ?, ?, NULL, NULL, ?, ?, 1, 1)`,
		ct, n, now, now); err != nil {
		t.Fatalf("seeding legacy secret row: %v", err)
	}
	db1.DB.Close()

	// Open with the fixed code. Pre-merge binary this is where every vault
	// op died with "no such column: profile_id".
	db2, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.DB.Close()
	if err := db2.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations (heal): %v", err)
	}

	migrated, errs := MigrateProfileVaultKeys(ctx, db2.DB, "default")
	if len(errs) != 0 {
		t.Fatalf("migrating healed DB: %v", errs)
	}
	if migrated != 1 {
		t.Fatalf("expected 1 row migrated on healed DB, got %d", migrated)
	}

	fields, _, err := DecryptFields(ctx, db2.DB, "default", "sec-heal")
	if err != nil {
		t.Fatalf("DecryptFields after heal+migrate: %v", err)
	}
	if fields["secret"] != "pre-merge-value" {
		t.Fatalf("legacy value corrupted: %+v", fields)
	}

	// The re-encrypted row must no longer decrypt under the legacy DEK.
	var ct2, n2 []byte
	if err := db2.DB.QueryRow(`SELECT ciphertext, nonce FROM vault_secrets WHERE id = 'sec-heal'`).Scan(&ct2, &n2); err != nil {
		t.Fatalf("reading re-encrypted row: %v", err)
	}
	if _, err := Decrypt(legacyDEK, ct2, n2); err == nil {
		t.Fatal("legacy DEK still decrypts the migrated row — re-encryption did not happen")
	}

	// Second run settles into the cheap no-op fast path.
	migratedAgain, errsAgain := MigrateProfileVaultKeys(ctx, db2.DB, "default")
	if len(errsAgain) != 0 || migratedAgain != 0 {
		t.Fatalf("second run: migrated=%d errs=%v", migratedAgain, errsAgain)
	}
}

// TestMigrateProfileVaultKeys_RetriesPerRowAfterFailure covers per-row
// idempotency: a corrupt row errors (and is left untouched) while healthy
// rows still migrate; once the corrupt row is repaired, a retry migrates it
// without touching the already-migrated one.
func TestMigrateProfileVaultKeys_RetriesPerRowAfterFailure(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	seedLegacySecret(t, db, "default", "sec-good", "svc-good", map[string]string{"secret": "good-value"}, "")

	// A row that decrypts under neither key (corrupt ciphertext).
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO vault_secrets (id, seq, profile_id, kind, name, ciphertext, nonce, notes_ciphertext, notes_nonce, created_at, updated_at, kv, field_count)
		VALUES ('sec-bad', (SELECT COALESCE(MAX(seq),0)+1 FROM vault_secrets), 'default', 'secret', 'svc-bad', x'deadbeefdeadbeef', x'000000000000000000000000', NULL, NULL, ?, ?, 1, 1)`,
		now, now); err != nil {
		t.Fatalf("seeding corrupt row: %v", err)
	}

	migrated, errs := MigrateProfileVaultKeys(ctx, db.DB, "default")
	if migrated != 1 {
		t.Fatalf("expected healthy row to migrate despite corrupt one, got %d", migrated)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "sec-bad") {
		t.Fatalf("expected exactly one per-row error naming sec-bad, got %v", errs)
	}

	// Repair the bad row by encrypting a real value under the legacy DEK.
	legacyDEK, found, err := fetchLegacyDEK(ctx, db.DB)
	if err != nil || !found {
		t.Fatalf("fetchLegacyDEK: found=%v err=%v", found, err)
	}
	fieldsJSON, _ := json.Marshal(map[string]string{"secret": "fixed-value"})
	ct, n, err := Encrypt(legacyDEK, fieldsJSON)
	if err != nil {
		t.Fatalf("re-encrypting repaired row: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx,
		`UPDATE vault_secrets SET ciphertext = ?, nonce = ? WHERE id = 'sec-bad'`, ct, n); err != nil {
		t.Fatalf("repairing sec-bad: %v", err)
	}

	migrated, errs = MigrateProfileVaultKeys(ctx, db.DB, "default")
	if len(errs) != 0 {
		t.Fatalf("retry after repair: %v", errs)
	}
	if migrated != 1 {
		t.Fatalf("expected repaired row to migrate on retry (and sec-good to be skipped), got %d", migrated)
	}
	if fields, _, err := DecryptFields(ctx, db.DB, "default", "sec-bad"); err != nil || fields["secret"] != "fixed-value" {
		t.Fatalf("repaired row after retry: fields=%v err=%v", fields, err)
	}
}

// TestMigrateProfileVaultKeys_ResumesAfterCrashBetweenMarkerAndData pins
// the marker-before-data repair: a profile whose vault_keys row (the
// marker) was created but whose rows never got re-encrypted — the exact
// state a crash between the old code's COUNT check and its UPDATE loop
// stranded forever — must still migrate.
func TestMigrateProfileVaultKeys_ResumesAfterCrashBetweenMarkerAndData(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	seedLegacySecret(t, db, "default", "sec-strand", "svc-strand", map[string]string{"secret": "stranded"}, "")

	// Simulate the crash: the marker exists, the data pass never ran.
	if _, err := getOrCreateDEK(ctx, db.DB, "default"); err != nil {
		t.Fatalf("pre-creating marker key: %v", err)
	}

	migrated, errs := MigrateProfileVaultKeys(ctx, db.DB, "default")
	if len(errs) != 0 {
		t.Fatalf("resuming after crash: %v", errs)
	}
	if migrated != 1 {
		t.Fatalf("expected stranded row to migrate despite existing marker, got %d", migrated)
	}
	if fields, _, err := DecryptFields(ctx, db.DB, "default", "sec-strand"); err != nil || fields["secret"] != "stranded" {
		t.Fatalf("stranded row after resume: fields=%v err=%v", fields, err)
	}
}

// TestMigrateProfileVaultKeys_ConcurrentRunsSerialize runs two migration
// passes for the same profile concurrently: the BEGIN IMMEDIATE per-profile
// lock must serialize them so every row is re-encrypted exactly once, with
// no errors and no rows left readable under the legacy key.
func TestMigrateProfileVaultKeys_ConcurrentRunsSerialize(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	const rows = 5
	for i := 0; i < rows; i++ {
		seedLegacySecret(t, db, "default", fmt.Sprintf("sec-conc-%d", i), fmt.Sprintf("svc-conc-%d", i),
			map[string]string{"secret": fmt.Sprintf("value-%d", i)}, "")
	}

	type result struct {
		migrated int
		errs     []error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, e := MigrateProfileVaultKeys(ctx, db.DB, "default")
			results <- result{migrated: m, errs: e}
		}()
	}
	wg.Wait()
	close(results)

	total := 0
	for r := range results {
		if len(r.errs) != 0 {
			t.Fatalf("concurrent migration errors: %v", r.errs)
		}
		total += r.migrated
	}
	if total != rows {
		t.Fatalf("expected exactly %d rows migrated across both passes, got %d", rows, total)
	}

	newDEK, err := getOrCreateDEK(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("getOrCreateDEK after concurrent migration: %v", err)
	}
	legacyDEK, found, err := fetchLegacyDEK(ctx, db.DB)
	if err != nil || !found {
		t.Fatalf("fetchLegacyDEK: found=%v err=%v", found, err)
	}
	ids, err := db.DB.QueryContext(ctx, `SELECT id, ciphertext, nonce FROM vault_secrets WHERE profile_id = 'default'`)
	if err != nil {
		t.Fatalf("listing rows: %v", err)
	}
	count := 0
	for ids.Next() {
		var id string
		var ct, n []byte
		if err := ids.Scan(&id, &ct, &n); err != nil {
			t.Fatalf("scanning %s: %v", id, err)
		}
		if _, err := Decrypt(legacyDEK, ct, n); err == nil {
			t.Fatalf("%s still decrypts under the legacy key after concurrent migration", id)
		}
		if plain, err := Decrypt(newDEK, ct, n); err != nil {
			t.Fatalf("%s does not decrypt under the profile key: %v", id, err)
		} else {
			var fields map[string]string
			if err := json.Unmarshal(plain, &fields); err != nil {
				t.Fatalf("%s plaintext is not the fields JSON: %v", id, err)
			}
		}
		count++
	}
	ids.Close()
	if count != rows {
		t.Fatalf("expected %d rows, found %d", rows, count)
	}
}
