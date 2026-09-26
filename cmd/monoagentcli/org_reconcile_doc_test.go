package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
)

// reconcile-doc returns the reconciled document and stores a new org's
// starting autonomy, without writing the org file.
func TestOrgReconcileDocDoesNotSave(t *testing.T) {
	f := newOrgCLIFixture(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "orgs", "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d orgdesign.Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Name = "fresh"
	body, _ := json.Marshal(&d)

	cmd := newOrgCmd(f.cfg)
	cmd.SetArgs([]string{"reconcile-doc", "fresh", "--new", "--by", "gui"})
	cmd.SetIn(strings.NewReader(string(body)))
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var res struct {
		Org       orgdesign.Doc            `json:"org"`
		Reconcile []map[string]interface{} `json:"reconcile"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if res.Org.Name != "fresh" || len(res.Reconcile) == 0 {
		t.Fatalf("reconcile-doc = %+v", res)
	}
	lead, _ := res.Org.FindRole("lead")
	if lead == nil || len(lead.ToolProviders) != 0 {
		t.Fatalf("unbacked providers survived: %+v", lead)
	}
	if _, err := os.Stat(filepath.Join(orgdesign.OrgsDir(f.root), "fresh.json")); !os.IsNotExist(err) {
		t.Fatalf("reconcile-doc wrote the org file: %v", err)
	}
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := orgdecide.NewStore(db.DB).Get(context.Background(), "default", "fresh")
	if err != nil || !row.Stored || row.Level != orgdesign.LevelMid {
		t.Fatalf("starting autonomy = %+v, %v", row, err)
	}

	cmd = newOrgCmd(f.cfg)
	cmd.SetArgs([]string{"reconcile-doc", "other"})
	cmd.SetIn(strings.NewReader(string(body)))
	captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr == nil || exitCodeOf(t, runErr) != 3 {
		t.Fatalf("mismatched name: %v", runErr)
	}
}

// A new org from a document that asks for manual autonomy keeps its level
// and its decider policy text (the Org Designer's "new org" save).
func TestOrgReconcileDocNewOrgKeepsDocumentAutonomy(t *testing.T) {
	f := newOrgCLIFixture(t)
	d := orgdesign.NewOrg("careful", "grow", orgdesign.NewOrgOptions{})
	d.Autonomy = &orgdesign.Autonomy{Level: orgdesign.LevelManual, Policy: "never spend money without asking"}
	body, _ := json.Marshal(d)

	cmd := newOrgCmd(f.cfg)
	cmd.SetArgs([]string{"reconcile-doc", "careful", "--new", "--by", "gui"})
	cmd.SetIn(strings.NewReader(string(body)))
	var runErr error
	captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := orgdecide.NewStore(db.DB).Get(context.Background(), "default", "careful")
	if err != nil {
		t.Fatal(err)
	}
	if row.Level != orgdesign.LevelManual || row.Policy != "never spend money without asking" {
		t.Fatalf("starting autonomy = %+v, want manual with the document's policy", row)
	}
}
