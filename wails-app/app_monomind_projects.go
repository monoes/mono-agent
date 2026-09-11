package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MonomindProjectInfo is one suggestion in the New Profile modal's "pick an
// existing monomind project" list.
type MonomindProjectInfo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// monomindProjectsFile is the shape of ~/.monomind-projects.json, written by
// monomind's own `init upgrade --all` (see registerClaudeCodeProject's doc
// comment in app_monomind_init.go for the sibling ~/.claude/projects/ list
// this is distinct from). Not owned by monoagent — read-only here.
type monomindProjectsFile struct {
	Projects []string `json:"projects"`
}

// readMonomindProjectsFile returns the raw project path list, or nil if the
// file doesn't exist or can't be parsed — this is a convenience suggestion,
// never worth failing the New Profile modal over.
func readMonomindProjectsFile() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	data, err := os.ReadFile(filepath.Join(home, ".monomind-projects.json"))
	if err != nil {
		return nil
	}
	var f monomindProjectsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Projects
}

// filterMonomindProjects turns a raw path list (as noisy as monomind's own
// init-upgrade bookkeeping — temp test directories from monomind's own test
// suite, one-off /tmp paths, monoagent's own profile folders — routinely
// makes it) into a small, meaningful suggestion list:
//   - a path that no longer exists, or was never (or is no longer) a real
//     monomind project (checked the same way IsMonomindInitialized does —
//     .monomind/config.yaml), is dropped — this one check alone eliminates
//     essentially all the ephemeral test-run noise
//   - a path already used as an existing profile's root_dir is dropped —
//     no point suggesting a project that already has one
//   - exact-duplicate paths collapse to their first occurrence
//
// Preserves the input's order — the source file carries no recency signal
// worth inventing one for.
func filterMonomindProjects(rawPaths []string, existingRootDirs map[string]bool) []MonomindProjectInfo {
	seen := make(map[string]bool, len(rawPaths))
	out := []MonomindProjectInfo{}
	for _, p := range rawPaths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if existingRootDirs[p] {
			continue
		}
		if !isMonomindInitializedAt(p) {
			continue
		}
		out = append(out, MonomindProjectInfo{Path: p, Name: filepath.Base(p)})
	}
	return out
}

// ListMonomindProjects suggests existing monomind projects for the New
// Profile modal to offer — picking one fills both the profile's name and
// its root directory. Never errors: an empty result just means the modal
// shows no suggestions.
func (a *App) ListMonomindProjects() []MonomindProjectInfo {
	raw := readMonomindProjectsFile()
	if len(raw) == 0 {
		return []MonomindProjectInfo{}
	}

	existingRootDirs := map[string]bool{}
	if a.db != nil {
		rows, err := a.db.Query(`SELECT root_dir FROM profiles WHERE root_dir != ''`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var rd string
				if rows.Scan(&rd) == nil {
					existingRootDirs[rd] = true
				}
			}
		}
	}

	return filterMonomindProjects(raw, existingRootDirs)
}
