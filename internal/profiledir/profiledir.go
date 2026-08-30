// Package profiledir resolves the per-profile filesystem root each profile's
// vault files, encrypted keys, and monomind project (knowledge graph +
// memory) live under. Defaults to ~/.monoagent/profiles/<profileID>/, but a
// profile may override this with any folder the user picks (profiles.root_dir
// in the database) — e.g. an external drive or a synced folder.
package profiledir

import (
	"database/sql"
	"os"
	"path/filepath"
)

// defaultRoot is the fallback used when a profile has no root_dir override.
func defaultRoot(profileID string) string {
	return filepath.Join(os.Getenv("HOME"), ".monoagent", "profiles", profileID)
}

// Root returns the folder that owns everything belonging to one profile —
// either its chosen override (profiles.root_dir, if set), or the default
// ~/.monoagent/profiles/<profileID>/. db may be nil, in which case the
// default is always used (callers that genuinely have no DB handle yet,
// e.g. very early startup) — this never fails, it just can't see an
// override.
func Root(db *sql.DB, profileID string) string {
	if db != nil {
		var override string
		if err := db.QueryRow(`SELECT root_dir FROM profiles WHERE id = ?`, profileID).Scan(&override); err == nil && override != "" {
			return override
		}
	}
	return defaultRoot(profileID)
}

// VaultDir returns <profile root>/vault/, where this profile's registered
// files/images live.
func VaultDir(db *sql.DB, profileID string) string {
	return filepath.Join(Root(db, profileID), "vault")
}

// MonomindDir returns <profile root>/.monomind/, the project root monomind
// resolves its monograph/knowledge-graph databases against for this profile
// (via the MONOMIND_CWD env override — never the chat subprocess's own cwd).
func MonomindDir(db *sql.DB, profileID string) string {
	return filepath.Join(Root(db, profileID), ".monomind")
}

// EnsureLayout creates a profile's folder structure (root, vault, .monomind)
// if it doesn't already exist. Safe to call repeatedly.
func EnsureLayout(db *sql.DB, profileID string) error {
	for _, dir := range []string{Root(db, profileID), VaultDir(db, profileID), MonomindDir(db, profileID)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// Exists reports whether a profile's folder has already been created —
// used as the "already migrated" marker for existing profiles.
func Exists(db *sql.DB, profileID string) bool {
	info, err := os.Stat(Root(db, profileID))
	return err == nil && info.IsDir()
}
