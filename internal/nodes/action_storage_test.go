package nodes

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func newTargetsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "targets.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db.DB
}

// `node run` executes a browser node standalone under ExecutionID "cli",
// which has no workflow_executions row: saving extracted items must keep
// the targets unattached instead of failing the action on the FK.
func TestSaveExtractedDataWithoutWorkflowExecution(t *testing.T) {
	db := newTargetsDB(t)

	s := &workflowActionStorage{db: db, executionID: "cli", nodeID: "cli-node", platform: "hackernews"}
	items := []map[string]interface{}{{"url": "https://news.ycombinator.com/item?id=1"}}
	if err := s.SaveExtractedData("a", items); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}

	var execID sql.NullString
	if err := db.QueryRow(`SELECT execution_id FROM workflow_node_targets WHERE node_id = 'cli-node'`).Scan(&execID); err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if execID.Valid {
		t.Errorf("execution_id = %q, want NULL for a standalone run", execID.String)
	}
}

func TestSaveExtractedDataAttachesToWorkflowExecution(t *testing.T) {
	db := newTargetsDB(t)

	if _, err := db.Exec(`INSERT INTO workflows (id, name) VALUES ('w1', 'w')`); err != nil {
		t.Fatalf("seeding workflow: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workflow_executions (id, workflow_id) VALUES ('e1', 'w1')`); err != nil {
		t.Fatalf("seeding execution: %v", err)
	}

	s := &workflowActionStorage{db: db, executionID: "e1", nodeID: "n1", platform: "hackernews"}
	if err := s.SaveExtractedData("a", []map[string]interface{}{{"url": "https://news.ycombinator.com/item?id=1"}}); err != nil {
		t.Fatalf("SaveExtractedData: %v", err)
	}

	var execID sql.NullString
	if err := db.QueryRow(`SELECT execution_id FROM workflow_node_targets WHERE node_id = 'n1'`).Scan(&execID); err != nil {
		t.Fatalf("reading target: %v", err)
	}
	if execID.String != "e1" {
		t.Errorf("execution_id = %v, want e1", execID)
	}
}
