package workflow

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	_ "modernc.org/sqlite"
)

// TestCheckWorkflowProfile is a regression test: engine methods that load a
// workflow by bare ID (SaveWorkflow, ActivateWorkflow, DeactivateWorkflow,
// TriggerWorkflow, RetryExecution, GetWorkflow) must reject workflows that
// belong to a different profile than the engine's active one.
func TestCheckWorkflowProfile(t *testing.T) {
	e := &WorkflowEngine{profileID: "profile-a"}

	cases := []struct {
		name    string
		wf      *Workflow
		wantErr bool
	}{
		{"same profile", &Workflow{ID: "wf-1", ProfileID: "profile-a"}, false},
		{"different profile", &Workflow{ID: "wf-2", ProfileID: "profile-b"}, true},
		{"unset profile (legacy row)", &Workflow{ID: "wf-3", ProfileID: ""}, false},
	}

	for _, tc := range cases {
		err := e.checkWorkflowProfile(tc.wf)
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}

// newFullEngineStore opens a SQLiteWorkflowStore over a temp-file DB with
// every table the engine's execution path touches. A file (not :memory:)
// because queue workers run on other goroutines and each pooled :memory:
// connection would be its own database.
func newFullEngineStore(t *testing.T) *SQLiteWorkflowStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "engine.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE workflows (id TEXT PRIMARY KEY, name TEXT, description TEXT DEFAULT '', is_active INTEGER DEFAULT 0, version INTEGER DEFAULT 1, profile_id TEXT, created_at TIMESTAMP, updated_at TIMESTAMP)`,
		`CREATE TABLE workflow_nodes (id TEXT PRIMARY KEY, workflow_id TEXT, node_type TEXT, name TEXT, config TEXT DEFAULT '{}', position_x REAL DEFAULT 0, position_y REAL DEFAULT 0, disabled INTEGER DEFAULT 0, created_at TIMESTAMP, updated_at TIMESTAMP)`,
		`CREATE TABLE workflow_connections (id TEXT PRIMARY KEY, workflow_id TEXT, source_node_id TEXT, source_handle TEXT DEFAULT 'main', target_node_id TEXT, target_handle TEXT DEFAULT 'main', position INTEGER DEFAULT 0)`,
		`CREATE TABLE workflow_executions (id TEXT PRIMARY KEY, workflow_id TEXT, status TEXT, trigger_type TEXT, trigger_data TEXT DEFAULT '{}', started_at TIMESTAMP, finished_at TIMESTAMP, error_message TEXT DEFAULT '', created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, pid INTEGER, resume_state TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE workflow_execution_nodes (id TEXT PRIMARY KEY, execution_id TEXT, node_id TEXT, node_name TEXT, status TEXT DEFAULT 'PENDING', input_items TEXT DEFAULT '[]', output_items TEXT DEFAULT '[]', error_message TEXT, started_at TIMESTAMP, finished_at TIMESTAMP, retry_count INTEGER DEFAULT 0)`,
		`CREATE TABLE hil_pending (id TEXT PRIMARY KEY, execution_id TEXT, status TEXT DEFAULT 'pending')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return NewSQLiteWorkflowStore(db)
}

// newTestEngine wires an engine over store with only test-registered node
// types. The queue is NOT started — tests drive dispatch explicitly so no
// webhook port is bound and no background loops run.
func newTestEngine(t *testing.T, store *SQLiteWorkflowStore, registry *NodeTypeRegistry) *WorkflowEngine {
	t.Helper()
	eng := NewWorkflowEngineWithStore(store, store.RawDB(), nil, registry, EngineConfig{ProfileID: "default"}, zerolog.Nop())
	t.Cleanup(func() { _ = eng.Stop() })
	return eng
}

// createActiveManualWorkflow persists trigger.manual → single node workflow
// (active) and returns its ID.
func createActiveManualWorkflow(t *testing.T, ctx context.Context, eng *WorkflowEngine, nodeType string) string {
	t.Helper()
	wf := &Workflow{
		ID:        "wf-" + nodeType,
		Name:      "wf-" + nodeType,
		ProfileID: "default",
		Nodes: []WorkflowNode{
			{ID: "t", WorkflowID: "wf-" + nodeType, Type: "trigger.manual", Name: "T"},
			{ID: "n", WorkflowID: "wf-" + nodeType, Type: nodeType, Name: "N"},
		},
		Connections: []WorkflowConnection{
			{SourceNodeID: "t", SourceHandle: "main", TargetNodeID: "n", TargetHandle: "main"},
		},
	}
	if err := eng.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if err := eng.store.SetWorkflowActive(ctx, wf.ID, true); err != nil {
		t.Fatalf("activate workflow: %v", err)
	}
	return wf.ID
}

// waitForTerminalStatus polls the store until the execution leaves
// QUEUED/RUNNING/WAITING or the timeout hits; returns the terminal status.
func waitForTerminalStatus(t *testing.T, store *SQLiteWorkflowStore, execID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st, err := store.GetExecutionStatus(context.Background(), execID)
		if err != nil {
			t.Fatalf("poll status: %v", err)
		}
		switch st {
		case "QUEUED", "RUNNING", "WAITING":
			if time.Now().After(deadline) {
				t.Fatalf("execution still %s after %s", st, timeout)
			}
			time.Sleep(100 * time.Millisecond)
		default:
			return st
		}
	}
}

// slowNode sleeps for d (honouring cancellation) then passes items through.
type slowNode struct{ d time.Duration }

func (slowNode) Type() string { return "test.slow" }
func (s slowNode) Execute(ctx context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ErrExecutionCancelled
	case <-time.After(s.d):
	}
	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
}

// TestEngine_LongRunFinalStatusPersisted is the regression guard for the
// zombie-RUNNING bug (V3-F1): the persistence context used to be a 10s
// timeout created at dispatch, so an execution longer than 10s had its final
// SetExecutionFinished silently fail — the row stayed RUNNING forever, the
// CLI wait hung, and the next startup marked it FAILED. An 11s execution
// must end with SUCCESS durably persisted.
func TestEngine_LongRunFinalStatusPersisted(t *testing.T) {
	store := newFullEngineStore(t)
	reg := NewNodeTypeRegistry()
	reg.Register("test.slow", func() NodeExecutor { return slowNode{d: 11 * time.Second} })
	eng := newTestEngine(t, store, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.queue.Start(ctx)

	wfID := createActiveManualWorkflow(t, ctx, eng, "test.slow")
	execID, err := eng.TriggerWorkflow(ctx, wfID, nil)
	if err != nil {
		t.Fatalf("TriggerWorkflow: %v", err)
	}

	if st := waitForTerminalStatus(t, store, execID, 30*time.Second); st != "SUCCESS" {
		t.Fatalf("final status = %s, want SUCCESS (final-status persistence failed)", st)
	}
	exec, err := store.GetExecution(context.Background(), execID)
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if exec.FinishedAt == nil {
		t.Error("finished_at not recorded for the long run")
	}
}

// atomicCountNode counts its executions atomically — readable from the test
// goroutine while a queue worker executes it.
type atomicCountNode struct{ runs *int32 }

func (atomicCountNode) Type() string { return "test.count.atomic" }
func (c atomicCountNode) Execute(_ context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
	atomic.AddInt32(c.runs, 1)
	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
}

// TestEngine_AdoptsUnownedQueuedExecution is the --no-wait adoption test
// (V3-F2): a QUEUED execution with pid 0 persisted by a process that already
// exited must be claimed by a live engine and run to completion.
func TestEngine_AdoptsUnownedQueuedExecution(t *testing.T) {
	store := newFullEngineStore(t)
	var runs int32
	reg := NewNodeTypeRegistry()
	reg.Register("test.count.atomic", func() NodeExecutor { return atomicCountNode{runs: &runs} })
	eng := newTestEngine(t, store, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.queue.Start(ctx)

	wfID := createActiveManualWorkflow(t, ctx, eng, "test.count.atomic")

	// The --no-wait artifact: an unowned QUEUED row (pid 0, never dispatched).
	adopted := &WorkflowExecution{
		WorkflowID:  wfID,
		Status:      "QUEUED",
		TriggerType: "trigger.manual",
		TriggerData: map[string]interface{}{},
	}
	if err := store.CreateExecution(ctx, adopted); err != nil {
		t.Fatalf("create execution: %v", err)
	}

	eng.adoptQueuedExecutions(ctx)

	if st := waitForTerminalStatus(t, store, adopted.ID, 15*time.Second); st != "SUCCESS" {
		t.Fatalf("adopted execution final status = %s, want SUCCESS", st)
	}
	if n := atomic.LoadInt32(&runs); n != 1 {
		t.Errorf("adopted node ran %d times, want exactly 1", n)
	}
	var pid int
	if err := store.RawDB().QueryRow(`SELECT COALESCE(pid,0) FROM workflow_executions WHERE id=?`, adopted.ID).Scan(&pid); err != nil {
		t.Fatalf("read pid: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("adopted execution pid = %d, want %d (owner stamped on dispatch)", pid, os.Getpid())
	}
}

// TestEngine_AdoptionRunsExactlyOnce_TwoEngines guards the CAS: with two
// engines sharing the DB, an unowned QUEUED execution must be adopted (and
// run) exactly once.
func TestEngine_AdoptionRunsExactlyOnce_TwoEngines(t *testing.T) {
	store := newFullEngineStore(t)
	var runs int32
	reg := NewNodeTypeRegistry()
	reg.Register("test.count.atomic", func() NodeExecutor { return atomicCountNode{runs: &runs} })
	eng1 := newTestEngine(t, store, reg)
	eng2 := newTestEngine(t, store, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng1.queue.Start(ctx)
	eng2.queue.Start(ctx)

	wfID := createActiveManualWorkflow(t, ctx, eng1, "test.count.atomic")

	orphan := &WorkflowExecution{
		WorkflowID:  wfID,
		Status:      "QUEUED",
		TriggerType: "trigger.manual",
		TriggerData: map[string]interface{}{},
	}
	if err := store.CreateExecution(ctx, orphan); err != nil {
		t.Fatalf("create execution: %v", err)
	}

	// Both engines sweep; the pid-0→self CAS (and the pid filter in the list
	// query) must ensure exactly one of them runs it.
	eng1.adoptQueuedExecutions(ctx)
	eng2.adoptQueuedExecutions(ctx)

	if st := waitForTerminalStatus(t, store, orphan.ID, 15*time.Second); st != "SUCCESS" {
		t.Fatalf("orphan execution final status = %s, want SUCCESS", st)
	}
	if n := atomic.LoadInt32(&runs); n != 1 {
		t.Errorf("node ran %d times across two adopting engines, want exactly 1", n)
	}
}
