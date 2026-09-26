package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func TestWorkflowExecutionsAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "e.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO workflows (id, name, is_active, profile_id) VALUES ('w1','A',1,'default'),('w2','B',1,'default'),('w3','C',1,'other');
		INSERT INTO workflow_executions (id, workflow_id, status, profile_id, created_at) VALUES
		('e1','w1','SUCCESS','default','2026-09-26 10:00:00'),('e2','w2','FAILED','default','2026-09-26 11:00:00'),
		('e3','w3','SUCCESS','other','2026-09-26 12:00:00')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}
	run := func(args ...string) (string, error) {
		var err error
		out := captureStdout(t, func() {
			c := newWorkflowCmd(cfg)
			c.SetArgs(args)
			err = c.Execute()
		})
		return out, err
	}
	out, err := run("executions", "--all", "--limit", "10")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID           string `json:"id"`
		WorkflowName string `json:"workflow_name"`
		CreatedAt    string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].ID != "e2" || rows[0].WorkflowName != "B" || rows[1].CreatedAt != "2026-09-26T10:00:00Z" {
		t.Fatalf("rows = %+v\n%s", rows, out)
	}
	if _, err := run("executions", "--all", "w1"); exitCode(err) != 3 {
		t.Fatalf("--all with an id: exit %d, want 3", exitCode(err))
	}
	if _, err := run("executions"); exitCode(err) != 3 {
		t.Fatalf("neither id nor --all: exit %d, want 3", exitCode(err))
	}
}
