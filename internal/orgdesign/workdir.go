package orgdesign

import (
	"encoding/json"
	"path/filepath"
)

// Workspace settings monomind accepts in run_config.workspace besides a
// path (orgrt/types.ts).
const (
	WorkspaceRepo            = "repo"
	WorkspaceIsolated        = "isolated"
	WorkspaceWorktree        = "worktree"
	WorkspaceWorktreePerRole = "worktree-per-role"
)

// RoleWorkdir returns the directory monomind confines roleID's file tools
// to, for the org d under profileRoot. It mirrors orgrt/daemon.ts
// (workspaceSetting, startOrgInner, buildRoleRuntime):
//
//   - "repo" (default): the project root
//   - "isolated": <root>/.monomind/orgs/<org>/workspace
//   - "worktree": <root>/.monomind/orgs/<org>/worktree
//   - "worktree-per-role": <root>/.monomind/orgs/<org>/worktree-<role> for
//     every role but the boss; the boss gets the setting itself as a
//     relative path, which the daemon resolves against its working
//     directory, the project root
//   - an absolute path: itself; a relative path: joined to the root
//
// The grant handler passes the result as org.workdir so a granted
// automation's file nodes are held to the same directory (C-46). The
// setting is read from the org file; a role that can already write that
// file can change its own workdir for the next org start as well, so this
// adds no new way to widen it.
func RoleWorkdir(profileRoot string, d *Doc, roleID string) string {
	root := filepath.Clean(profileRoot)
	ws := WorkspaceRepo
	if raw, ok := d.RunConfig["workspace"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			ws = s
		}
	}
	orgDirPath := filepath.Join(OrgsDir(root), d.Name)
	switch ws {
	case WorkspaceRepo:
		return root
	case WorkspaceIsolated:
		return filepath.Join(orgDirPath, "workspace")
	case WorkspaceWorktree:
		return filepath.Join(orgDirPath, "worktree")
	case WorkspaceWorktreePerRole:
		if roleID == bossRoleID(d) {
			return filepath.Join(root, ws)
		}
		return filepath.Join(orgDirPath, "worktree-"+roleID)
	}
	if filepath.IsAbs(ws) {
		return filepath.Clean(ws)
	}
	return filepath.Join(root, ws)
}

// bossRoleID applies monomind's boss rule: the first agent role typed boss
// or with no reports_to, else the first agent role.
func bossRoleID(d *Doc) string {
	first := ""
	for i := range d.Roles {
		r := &d.Roles[i]
		if r.IsEndpoint() {
			continue
		}
		if first == "" {
			first = r.ID
		}
		if r.Type == "boss" || r.ReportsTo == nil {
			return r.ID
		}
	}
	return first
}
