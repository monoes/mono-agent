package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// queuedOrgTrigger calls the granted automation once (no wait) and returns
// the org object of the queued execution's trigger data.
func queuedOrgTrigger(t *testing.T, f *grantFixture, args string) map[string]interface{} {
	t.Helper()
	liveHeartbeat(t)
	if _, err := f.server.callGrantTool(context.Background(), "automation_publish", json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := f.db.DB.QueryRow(`SELECT trigger_data FROM workflow_executions ORDER BY rowid DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var td struct {
		Org map[string]interface{} `json:"org"`
	}
	if err := json.Unmarshal([]byte(raw), &td); err != nil {
		t.Fatal(err)
	}
	return td.Org
}

// C-46: a granted automation runs in the daemon, outside monomind's
// workdir confinement, so the grant handler hands the calling role's
// workdir to the run as org.workdir and the run's file nodes confine to it.
func TestGrantRunPassesTheRoleWorkdir(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: false})
	root := profiledir.Root(f.db.DB, "default")
	boss := "lead"
	doc := &orgdesign.Doc{Name: "growth", Status: "stopped", Schedule: json.RawMessage("null"),
		RunConfig: map[string]json.RawMessage{"workspace": json.RawMessage(`"worktree"`)},
		Roles: []orgdesign.Role{
			{ID: "lead", Title: "Lead", Type: "boss", Responsibilities: []string{"Lead."}},
			{ID: "writer", Title: "Writer", Type: "specialist", ReportsTo: &boss, Responsibilities: []string{"Write."}},
		}}
	if _, err := orgdesign.Save(root, doc); err != nil {
		t.Fatal(err)
	}
	org := queuedOrgTrigger(t, f, `{"path":"/etc/passwd","org":{"workdir":"/"}}`)
	want := filepath.Join(root, ".monomind", "orgs", "growth", "worktree")
	if org["workdir"] != want {
		t.Fatalf("org.workdir = %v, want %s", org["workdir"], want)
	}
}

// Without a readable org file the run is still confined — to the profile
// root, monomind's default workspace — never left unconfined.
func TestGrantRunWithoutOrgFileConfinesToTheProfileRoot(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: false})
	org := queuedOrgTrigger(t, f, `{}`)
	if want := filepath.Clean(profiledir.Root(f.db.DB, "default")); org["workdir"] != want {
		t.Fatalf("org.workdir = %v, want %s", org["workdir"], want)
	}
}
