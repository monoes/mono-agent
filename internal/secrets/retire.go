package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// retiredAIProviderKind is the vault kind the removed in-app AI provider
// stack stored its API keys under. Nothing creates it any more: AI runs
// through local agents via monomind.
const retiredAIProviderKind = "ai_provider"

// RetiredAIProviderBackupName is the name of the plain secret that holds a
// RetireAIProviderEntries backup's passphrase (disambiguated with " (2)"
// etc. if the name is taken).
const RetiredAIProviderBackupName = "Retired AI provider keys backup"

// RetireAIProviderEntries exports, then deletes, every vault entry of the
// retired "ai_provider" kind. For each profile that has any, it writes
// backupDir/retired-ai-providers-<profile>-<time>.json (an ordinary vault
// export, so `monoagentcli secret import` restores the keys as plain
// secrets), saves that file's generated passphrase as a plain secret named
// RetiredAIProviderBackupName, and only then deletes the entries. A profile
// whose entries cannot all be decrypted (the keyring may be locked right
// now) is left untouched and retried on the next start. Idempotent and a
// single query once nothing is left, like the other vault migrations.
func RetireAIProviderEntries(ctx context.Context, db *sql.DB, backupDir string) (retired int, err error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT profile_id FROM vault_secrets WHERE kind = ?`, retiredAIProviderKind)
	if err != nil {
		return 0, fmt.Errorf("secrets.RetireAIProviderEntries: listing profiles: %w", err)
	}
	var profiles []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, fmt.Errorf("secrets.RetireAIProviderEntries: %w", err)
		}
		profiles = append(profiles, p)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("secrets.RetireAIProviderEntries: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("secrets.RetireAIProviderEntries: %w", err)
	}

	var failed []string
	for _, profileID := range profiles {
		n, err := retireProfileAIProviders(ctx, db, profileID, backupDir, time.Now().UTC())
		retired += n
		if err != nil {
			failed = append(failed, fmt.Sprintf("profile %s: %v", profileID, err))
		}
	}
	if len(failed) > 0 {
		return retired, fmt.Errorf("secrets.RetireAIProviderEntries: %s", strings.Join(failed, "; "))
	}
	return retired, nil
}

func retireProfileAIProviders(ctx context.Context, db *sql.DB, profileID, backupDir string, now time.Time) (int, error) {
	entries, err := List(ctx, db, profileID)
	if err != nil {
		return 0, fmt.Errorf("listing entries: %w", err)
	}
	payload := exportPayload{ExportedAt: now.Format(time.RFC3339), ProfileID: profileID}
	var ids []string
	for _, e := range entries {
		if e.Kind != retiredAIProviderKind {
			continue
		}
		fields, notes, err := DecryptFields(ctx, db, profileID, e.ID)
		if err != nil {
			return 0, fmt.Errorf("decrypting %q (left in place): %w", e.Name, err)
		}
		payload.Entries = append(payload.Entries, exportEntry{
			Kind: e.Kind, Name: e.Name, Username: e.Username, URL: e.URL,
			Notes: notes, Fields: fields, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		})
		ids = append(ids, e.ID)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	passphrase, err := GenerateExportPassword()
	if err != nil {
		return 0, err
	}
	data, err := sealExport(payload, passphrase)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(backupDir, fmt.Sprintf("retired-ai-providers-%s-%s.json", fileSafe(profileID), now.Format("20060102T150405Z")))
	if err := writeFileAtomic(path, data); err != nil {
		return 0, fmt.Errorf("writing backup: %w", err)
	}

	name, err := disambiguateName(ctx, db, profileID, RetiredAIProviderBackupName)
	if err != nil {
		os.Remove(path)
		return 0, err
	}
	notes := fmt.Sprintf("Passphrase for %s, a backup of the %d AI provider key(s) removed on %s "+
		"(mono-agent no longer has AI providers; AI runs through local agents via monomind). "+
		"Restore them as plain secrets with: monoagentcli secret import %s. "+
		"Delete this entry and the file once you no longer need them.",
		path, len(ids), now.Format("2006-01-02"), path)
	if _, err := addEntry(ctx, db, profileID, "secret", name, map[string]string{"passphrase": passphrase}, "", "", notes); err != nil {
		os.Remove(path)
		return 0, fmt.Errorf("saving the backup passphrase: %w", err)
	}

	retired := 0
	for _, id := range ids {
		if err := Delete(ctx, db, profileID, id); err != nil {
			return retired, fmt.Errorf("deleting %s (backed up in %s): %w", id, path, err)
		}
		retired++
	}
	return retired, nil
}

// fileSafe keeps letters, digits, '-' and '_' so a profile id can go in a
// file name.
func fileSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

// writeFileAtomic writes data to path (mode 0600, parent made 0700) via a
// temporary file and a rename, so a crash never leaves a partial backup.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".retire-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
