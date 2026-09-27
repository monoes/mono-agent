package main

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// twoProfileDB is newSummaryCLITestDB plus a Work profile with its own
// workflow and runs in both profiles.
func twoProfileDB(t *testing.T) *globalConfig {
	t.Helper()
	cfg := newSummaryCLITestDB(t)
	db, err := storage.NewDatabase(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name) VALUES ('p-work', 'Work');
		INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w2','B',1,'p-work');
		INSERT INTO workflow_executions (id, workflow_id, status, profile_id, created_at) VALUES
		  ('e-old','w1','SUCCESS','default','2026-09-27 10:00:00'),
		  ('e-new','w2','FAILED','p-work','2026-09-27 11:00:00'),
		  ('e-mid','w1','SUCCESS','default','2026-09-27 10:30:00')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return cfg
}

func TestWorkflowListAllProfiles(t *testing.T) {
	cfg := twoProfileDB(t)
	var runErr error
	out := captureStdout(t, func() {
		cmd := newWorkflowListCmd(cfg)
		cmd.SetArgs([]string{"--all-profiles"})
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var rows []struct {
		ID          string `json:"id"`
		ProfileID   string `json:"profile_id"`
		ProfileName string `json:"profile_name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].ID != "w1" || rows[0].ProfileName != "Default" || rows[1].ID != "w2" || rows[1].ProfileID != "p-work" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRecentExecutionsAllProfiles(t *testing.T) {
	cfg := twoProfileDB(t)
	var runErr error
	out := captureStdout(t, func() {
		cmd := newWorkflowExecutionsCmd(cfg)
		cmd.SetArgs([]string{"--all", "--all-profiles", "--limit", "2"})
		runErr = cmd.Execute()
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var rows []struct {
		ID          string `json:"id"`
		ProfileName string `json:"profile_name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].ID != "e-new" || rows[0].ProfileName != "Work" || rows[1].ID != "e-mid" {
		t.Fatalf("rows = %+v, want e-new (Work) then e-mid", rows)
	}
}

func TestAllProfilesNeedsAll(t *testing.T) {
	cfg := twoProfileDB(t)
	cmd := newWorkflowExecutionsCmd(cfg)
	cmd.SetArgs([]string{"w1", "--all-profiles"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--all-profiles with a workflow id must be refused")
	}
}
