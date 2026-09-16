package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

type grantFixture struct {
	server *Server
	db     *storage.Database
	grant  *orggrant.Grant
	store  *orggrant.Store
}

func newGrantFixture(t *testing.T, tool orggrant.Tool) *grantFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(home, "hb.json"))
	t.Setenv("MONOMIND_ORG_NAME", "")
	t.Setenv("MONOMIND_ORG_ROLE", "")
	t.Setenv("MONOMIND_ORG_RUN", "")
	dbPath := filepath.Join(t.TempDir(), "grant.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wfStore := workflow.NewSQLiteWorkflowStore(db.DB)
	if err := wfStore.CreateWorkflow(ctx, &workflow.Workflow{ID: "wf-pub", Name: "Publish", ProfileID: "default"}); err != nil {
		t.Fatal(err)
	}
	store := orggrant.NewStore(db.DB)
	tool.Alias, tool.WorkflowID = "publish", "wf-pub"
	g, err := store.UpsertGrant(ctx, orggrant.GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "writer", Tool: tool})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(Options{DBPath: dbPath, Profile: "default", WorkflowsDir: filepath.Join(t.TempDir(), "wf"), Grant: g.ID})
	t.Cleanup(func() { s.closeRuntime() })
	oldPoll := grantPollInterval
	grantPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { grantPollInterval = oldPoll })
	return &grantFixture{server: s, db: db, grant: g, store: store}
}

func liveHeartbeat(t *testing.T) {
	t.Helper()
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// fakeDaemon completes the first QUEUED execution with one output item.
func fakeDaemon(t *testing.T, db *storage.Database, output map[string]interface{}) {
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var id string
			if err := db.DB.QueryRow(`SELECT id FROM workflow_executions WHERE status = 'QUEUED' LIMIT 1`).Scan(&id); err == nil {
				now := time.Now().UTC()
				ws := workflow.NewSQLiteWorkflowStore(db.DB)
				_ = ws.CreateExecutionNode(context.Background(), &workflow.WorkflowExecutionNode{
					ExecutionID: id, NodeID: "n1", NodeName: "post", Status: "SUCCESS",
					OutputItems: []workflow.Item{{JSON: output}}, StartedAt: &now, FinishedAt: &now})
				_, _ = db.DB.Exec(`UPDATE workflow_executions SET status = 'SUCCESS' WHERE id = ?`, id)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

func TestGrantModeListsOnlyGrantedTools(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: true})
	resps := serveLines(t, f.server, request(1, "tools/list", map[string]interface{}{}))
	var res struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resps[0]["result"], &res); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "automation_output,automation_publish,automation_status" {
		t.Fatalf("grant-mode tools = %v", names)
	}
}

func TestGrantModeRunWaitsTracesAndRedacts(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: true, Timeout: 5})
	ctx := context.Background()

	// No daemon: refuse fast.
	start := time.Now()
	if _, err := f.server.callGrantTool(ctx, "automation_publish", json.RawMessage(`{"text":"hi"}`)); err == nil || !strings.HasPrefix(err.Error(), codeDaemonRequired) {
		t.Fatalf("without daemon: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("daemon_required took over a second")
	}

	liveHeartbeat(t)
	fakeDaemon(t, f.db, map[string]interface{}{"url": "https://example.test/1", "password": "hunter2"})
	meta := json.RawMessage(`{"trace":{"org":"growth","run":"run-7","role":"writer","chain_id":"chn_role1","hop":2}}`)
	out, err := f.server.callGrantTool(withMeta(ctx, meta), "automation_publish", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status": "success"`) || !strings.Contains(out, "example.test/1") || strings.Contains(out, "hunter2") {
		t.Fatalf("run output = %s", out)
	}
	var trigger, data string
	if err := f.db.DB.QueryRow(`SELECT trigger_type, trigger_data FROM workflow_executions`).Scan(&trigger, &data); err != nil {
		t.Fatal(err)
	}
	if trigger != workflow.TriggerTypeOrgTool || !strings.Contains(data, `"text":"hi"`) || !strings.Contains(data, `"run":"run-7"`) {
		t.Fatalf("execution = %s %s", trigger, data)
	}
	var chain, grantID, run string
	var hop int
	if err := f.db.DB.QueryRow(`SELECT chain_id, hop, grant_id, run_id FROM org_bridge_calls WHERE direction = 'role_tool'`).Scan(&chain, &hop, &grantID, &run); err != nil {
		t.Fatal(err)
	}
	if chain != "chn_role1" || hop != 3 || grantID != f.grant.ID || run != "run-7" {
		t.Fatalf("ledger row = %s %d %s %s", chain, hop, grantID, run)
	}

	// The role cannot read runs its grants did not start.
	if _, err := f.server.callGrantTool(ctx, "automation_status", json.RawMessage(`{"execution_id":"someone-else"}`)); err == nil {
		t.Fatal("read another execution")
	}

	// Revocation takes effect on the next call (C-29).
	if err := f.store.RevokeGrant(ctx, "default", "growth", "writer", "publish"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.callGrantTool(ctx, "automation_publish", nil); err == nil || !strings.HasPrefix(err.Error(), codeRefusedGrant) {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestGrantModeScopeAndCaps(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: false, MaxCallsPerRun: 1})
	ctx := context.Background()
	liveHeartbeat(t)

	t.Setenv("MONOMIND_ORG_ROLE", "lead")
	if _, err := f.server.callGrantTool(ctx, "automation_publish", nil); err == nil || !strings.Contains(err.Error(), `is for role "writer"`) {
		t.Fatalf("grant used by another role: %v", err)
	}
	t.Setenv("MONOMIND_ORG_ROLE", "writer")
	t.Setenv("MONOMIND_ORG_RUN", "run-1")
	out, err := f.server.callGrantTool(ctx, "automation_publish", nil)
	if err != nil || !strings.Contains(out, `"status": "queued"`) {
		t.Fatalf("first call: %s %v", out, err)
	}
	if _, err := f.server.callGrantTool(ctx, "automation_publish", nil); err == nil || !strings.HasPrefix(err.Error(), codeRefusedCap) {
		t.Fatalf("second call in the run: %v", err)
	}
	if _, err := f.server.callGrantTool(ctx, "workflow_run", nil); err == nil || !strings.HasPrefix(err.Error(), codeRefusedGrant) {
		t.Fatalf("ungranted tool: %v", err)
	}
}

func TestGrantModeReturnsHILImmediately(t *testing.T) {
	f := newGrantFixture(t, orggrant.Tool{Wait: true, Timeout: 30})
	ctx := context.Background()
	liveHeartbeat(t)
	go func() {
		for i := 0; i < 500; i++ {
			var id string
			if f.db.DB.QueryRow(`SELECT id FROM workflow_executions WHERE status = 'QUEUED' LIMIT 1`).Scan(&id) == nil {
				_, _ = f.db.DB.Exec(`UPDATE workflow_executions SET status = 'WAITING' WHERE id = ?`, id)
				_, _ = f.db.DB.Exec(`INSERT INTO hil_pending (id, execution_id, workflow_id, node_id, node_name, status, profile_id) VALUES ('hil-1', ?, 'wf-pub', 'n', 'Review post', 'pending', 'default')`, id)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	start := time.Now()
	out, err := f.server.callGrantTool(ctx, "automation_publish", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"hil"`) || !strings.Contains(out, "hil-1") || time.Since(start) > 10*time.Second {
		t.Fatalf("HIL result = %s", out)
	}
}
