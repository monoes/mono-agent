package orgdesign

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func docWithWorkspace(ws string) *Doc {
	boss := "lead"
	d := &Doc{Name: "growth", Roles: []Role{{ID: "lead"}, {ID: "writer", ReportsTo: &boss}}}
	if ws != "" {
		raw, _ := json.Marshal(ws)
		d.RunConfig = map[string]json.RawMessage{"workspace": raw}
	}
	return d
}

// TestRoleWorkdir mirrors monomind's workspace resolution
// (orgrt/daemon.ts workspaceSetting/startOrgInner/buildRoleRuntime), which
// sets the directory a role's file tools are confined to (C-46).
func TestRoleWorkdir(t *testing.T) {
	root := filepath.FromSlash("/home/u/project")
	orgs := filepath.Join(root, ".monomind", "orgs", "growth")
	cases := []struct {
		ws, role, want string
	}{
		{"", "writer", root},
		{"repo", "writer", root},
		{"isolated", "writer", filepath.Join(orgs, "workspace")},
		{"worktree", "writer", filepath.Join(orgs, "worktree")},
		{"worktree-per-role", "writer", filepath.Join(orgs, "worktree-writer")},
		{"worktree-per-role", "lead", filepath.Join(root, "worktree-per-role")},
		{filepath.FromSlash("/srv/shared"), "writer", filepath.FromSlash("/srv/shared")},
		{"sub/dir", "writer", filepath.Join(root, "sub", "dir")},
		{"../escape", "writer", filepath.Join(root, "..", "escape")},
	}
	for _, c := range cases {
		if got := RoleWorkdir(root, docWithWorkspace(c.ws), c.role); got != c.want {
			t.Errorf("workspace %q role %s: got %q, want %q", c.ws, c.role, got, c.want)
		}
	}
}

func TestRoleWorkdir_NonStringWorkspaceFallsBackToRepo(t *testing.T) {
	d := docWithWorkspace("")
	d.RunConfig = map[string]json.RawMessage{"workspace": json.RawMessage(`42`)}
	if got := RoleWorkdir("/p", d, "writer"); got != filepath.Clean("/p") {
		t.Errorf("got %q, want the project root", got)
	}
}
