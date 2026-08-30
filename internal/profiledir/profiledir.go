// Package profiledir resolves the per-profile filesystem root each profile's
// vault files, encrypted keys, and monomind project (knowledge graph +
// memory) live under. Defaults to ~/.monoagent/profiles/<profileID>/, but a
// profile may override this with any folder the user picks (profiles.root_dir
// in the database) — e.g. an external drive or a synced folder.
package profiledir

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidProfileID reports whether profileID is safe to embed in a filesystem
// path: non-empty, and free of path separators and parent-directory
// components, so it can never escape ~/.monoagent/profiles/ via a join.
func ValidProfileID(profileID string) bool {
	if profileID == "" || strings.ContainsAny(profileID, `/\`) {
		return false
	}
	if strings.Contains(profileID, "..") {
		return false
	}
	return true
}

// defaultRoot is the fallback used when a profile has no root_dir override.
// os.UserHomeDir covers Unix ($HOME) and Windows (%USERPROFILE%); $HOME is
// the last-resort fallback for environments where the lookup fails but the
// variable is still set.
func defaultRoot(profileID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".monoagent", "profiles", profileID)
}

// Root returns the folder that owns everything belonging to one profile —
// either its chosen override (profiles.root_dir, if set), or the default
// ~/.monoagent/profiles/<profileID>/. db may be nil, in which case the
// default is always used (callers that genuinely have no DB handle yet,
// e.g. very early startup) — this never fails, it just can't see an
// override. An invalid profileID (see ValidProfileID) yields "" — a dead
// path — instead of joining untrusted components into a real one;
// EnsureLayout, the only creator of these directories, rejects such IDs
// outright.
func Root(db *sql.DB, profileID string) string {
	if !ValidProfileID(profileID) {
		return ""
	}
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
// if it doesn't already exist. Safe to call repeatedly. Rejects invalid
// profile IDs before any path is joined or created.
func EnsureLayout(db *sql.DB, profileID string) error {
	if !ValidProfileID(profileID) {
		return fmt.Errorf("invalid profile id %q: must be non-empty and contain no '/', '\\', or '..'", profileID)
	}
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
