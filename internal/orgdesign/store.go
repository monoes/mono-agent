package orgdesign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// orgDir is the directory name org configs live under, relative to a
// project root — matches monomind's own ORG_DIR constant
// (packages/@monomind/cli/src/orgrt/types.ts).
const orgDir = ".monomind/orgs"

// orgNameRe mirrors monomind's own ORG_NAME_RE
// (packages/@monomind/cli/src/commands/org.ts) — anything else is rejected
// to prevent path traversal via a crafted org name.
var orgNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// orgArtifactSuffixes mirrors monomind's ORG_ARTIFACT_SUFFIXES
// (commands/org.ts) verbatim — org-internal state files
// (<name>-state.json, <name>-goals.json, ...) share the <name>.json naming
// pattern with a real org config and must be excluded from discovery, or
// the watcher fires on every write a running daemon performs and role
// listings show phantom "orgs". Matched with strings.HasSuffix, never
// strings.Contains — see the comment on IsOrgConfigFile.
var orgArtifactSuffixes = []string{
	"-state", "-goals", "-threads", "-activity", "-approvals", "-members",
	"-secrets", "-budgets", "-routines", "-issues", "-projects",
	"-workspaces", "-worktrees", "-environments", "-plugins", "-adapters",
	"-join-requests", "-bootstrap", "-project-workspaces",
	"-approval-comments", "-skills", "-runstate",
}

// ValidOrgName reports whether name is safe to use in a path.
func ValidOrgName(name string) bool {
	return name != "" && orgNameRe.MatchString(name)
}

// OrgsDir returns <profileRoot>/.monomind/orgs.
func OrgsDir(profileRoot string) string {
	return filepath.Join(profileRoot, orgDir)
}

// ConfigPath returns the config file path for name under profileRoot,
// validating name first (path-traversal defense — mirrors monomind's own
// validateOrgName).
func ConfigPath(profileRoot, name string) (string, error) {
	if !ValidOrgName(name) {
		return "", fmt.Errorf("invalid org name: %q", name)
	}
	return filepath.Join(OrgsDir(profileRoot), name+".json"), nil
}

// IsOrgConfigFile reports whether filename (a base name, no directory
// component) is a real org config file — a .json file that is not an
// AppleDouble sidecar (._*), not a legacy v1 backup (*.v1.json), and does
// not end with any of monomind's own artifact suffixes. Mirrors
// listOrgConfigFiles (commands/org.ts) exactly, including its own note that
// this must be endsWith/HasSuffix, not a substring check — an org
// legitimately named "state-machine" would otherwise be hidden.
func IsOrgConfigFile(filename string) bool {
	if !strings.HasSuffix(filename, ".json") {
		return false
	}
	if strings.HasPrefix(filename, "._") {
		return false
	}
	if strings.HasSuffix(filename, ".v1.json") {
		return false
	}
	for _, suf := range orgArtifactSuffixes {
		if strings.HasSuffix(filename, suf+".json") {
			return false
		}
	}
	return true
}

// ListOrgNames returns every real org config's name (file stem) under
// profileRoot's orgs directory, sorted. Returns an empty slice (not an
// error) if the directory doesn't exist yet.
func ListOrgNames(profileRoot string) ([]string, error) {
	dir := OrgsDir(profileRoot)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !IsOrgConfigFile(e.Name()) {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names, nil
}

// LoadPath reads and parses an org config file at an exact path.
func LoadPath(path string) (*Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Doc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &d, nil
}

// Load reads and parses the named org's config file under profileRoot.
func Load(profileRoot, name string) (*Doc, error) {
	path, err := ConfigPath(profileRoot, name)
	if err != nil {
		return nil, err
	}
	return LoadPath(path)
}

// Save validates d, then writes it to <profileRoot>/.monomind/orgs/<d.Name>.json
// atomically (write to a .tmp sibling, then rename — the same pattern as
// internal/workflow/file_store.go's SaveWorkflow and monomind's own
// migrateOrgFile). Returns the sha256 of the exact bytes written, which
// callers use to register a watcher self-write suppression before emitting
// their own live-update event.
//
// Save does NOT run monomind's own `org validate` — that is the caller's
// responsibility (see wails-app/app_orgs_design.go), since it requires a
// resolved monomind binary and this package intentionally has no such
// dependency (it must remain usable, and testable, standalone).
func Save(profileRoot string, d *Doc) (sha string, err error) {
	if err := Validate(d); err != nil {
		return "", err
	}
	path, err := ConfigPath(profileRoot, d.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating orgs directory: %w", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding org config: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("renaming %s to %s: %w", tmp, path, err)
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Delete removes an org's config file only — never its run-data
// subdirectory (<orgsDir>/<name>/, containing bus.jsonl, checkpoints, and
// sentinel files), which callers who want a full teardown should remove via
// `monomind org delete`, not this package.
func Delete(profileRoot, name string) error {
	path, err := ConfigPath(profileRoot, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
