package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// newOrgDesignApp returns a test App whose active profile's root is an empty
// temp directory, so org config files and the CLI's --project agree, and
// reconcileOrgDoc's profile/root guard passes.
func newOrgDesignApp(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	root := filepath.Join(t.TempDir(), "profile-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir profile root: %v", err)
	}
	if _, err := a.db.Exec("INSERT INTO profiles (id, name, root_dir) VALUES ('gui', 'GUI', ?)", root); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	a.setActiveProfileID("gui")
	return a, root
}

// fakeValidateCLI installs a stub monoagentcli on MONOAGENTCLI_BIN whose
// `org validate` answers with the given JSON payload (exit 0, as the real
// command does — it reports invalidity in the payload, not the exit code).
func fakeValidateCLI(t *testing.T, payload string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub CLI is a shell script (unix-only)")
	}
	bin := filepath.Join(t.TempDir(), "monoagentcli")
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"validate\" ]; then\n    printf '%s\\n' '" + payload + "'\n    exit 0\n  fi\ndone\nprintf '{\"ok\":true}\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub cli: %v", err)
	}
	t.Setenv("MONOAGENTCLI_BIN", bin)
}

func strPtr(s string) *string { return &s }

// twoRoleDoc is a lead + worker org where worker holds one automation grant's
// display copy.
func twoRoleDoc() *orgdesign.Doc {
	return &orgdesign.Doc{
		Name: "growth", Goal: "grow", Status: "stopped",
		Roles: []orgdesign.Role{
			{ID: "lead", Title: "Lead", Type: "boss", ReportsTo: nil, Responsibilities: []string{"lead"}},
			{
				ID: "worker", Title: "Worker", ReportsTo: strPtr("lead"), Responsibilities: []string{"work"},
				Automations: []orgdesign.GrantSpec{{Alias: "publish_post"}},
			},
		},
		Automations: []orgdesign.AutomationRef{{WorkflowID: "wf-1", Alias: "publish_post"}},
	}
}

// TestSaveOrgDoc_RejectedSaveLeavesGrantRowsIntact covers the ordering bug:
// reconcile revokes the grant rows a role no longer backs, so it may not run
// until `monoagentcli org validate` has accepted the document — the rollback
// can only restore the FILE, never a revoked row.
func TestSaveOrgDoc_RejectedSaveLeavesGrantRowsIntact(t *testing.T) {
	fakeValidateCLI(t, `{"v":1,"org":"growth","valid":false,"error":"monomind says no"}`)
	a, root := newOrgDesignApp(t)
	ctx := context.Background()

	if _, err := orgdesign.Save(root, twoRoleDoc()); err != nil {
		t.Fatalf("seed org file: %v", err)
	}
	store := orggrant.NewStore(a.db)
	if _, err := store.UpsertGrant(ctx, orggrant.GrantInput{
		ProfileID: "gui", OrgName: "growth", RoleID: "worker",
		Tool: orggrant.Tool{Alias: "publish_post", WorkflowID: "wf-1"},
	}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	// Drop the worker role — the change monomind's validate will reject.
	next := twoRoleDoc()
	next.Roles = next.Roles[:1]
	if _, err := a.saveOrgDoc(root, next); err == nil {
		t.Fatal("expected the save to be rejected by the CLI validate check")
	}

	grants, err := store.ListGrants(ctx, "gui", "growth", "")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("live grant rows after a rejected save = %d, want 1 (the row must survive)", len(grants))
	}

	// And the file must still be the pre-image, worker and all.
	back, err := orgdesign.Load(root, "growth")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(back.Roles) != 2 {
		t.Fatalf("rolled-back file has %d roles, want 2", len(back.Roles))
	}
}
