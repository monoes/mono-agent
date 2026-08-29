package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// insertExecAt inserts an execution with an explicit created_at so prune
// ordering is deterministic regardless of insert speed.
func insertExecAt(t *testing.T, s *SQLiteWorkflowStore, id, status string, createdAt time.Time) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO workflow_executions (id, workflow_id, status, created_at) VALUES (?,?,?,?)`,
		id, "wf", status, createdAt); err != nil {
		t.Fatalf("insert exec %s: %v", id, err)
	}
}

// TestPruneExecutions_ProtectsWaitingAndCleansHIL is the regression guard for
// the status-blind prune: a HIL-paused (WAITING) execution must never be
// pruned no matter how many newer executions exist, and hil_pending rows must
// not outlive their executions (orphans removed, rows of pruned executions
// removed, rows of surviving executions kept).
func TestPruneExecutions_ProtectsWaitingAndCleansHIL(t *testing.T) {
	s := newExecStore(t)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Oldest row: WAITING at a HIL pause point, with a pending HIL item.
	insertExecAt(t, s, "waiting-old", "WAITING", base)
	// An old finished row beyond the keep window, with its own HIL row.
	insertExecAt(t, s, "done-old", "SUCCESS", base.Add(time.Minute))
	// 500 newer finished rows fill the keep quota.
	for i := 0; i < 500; i++ {
		insertExecAt(t, s, execRowID(i), "SUCCESS", base.Add(time.Duration(i+2)*time.Minute))
	}

	insertHIL := func(id, execID string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO hil_pending (id, execution_id, status) VALUES (?,?,?)`,
			id, execID, "pending"); err != nil {
			t.Fatalf("insert hil %s: %v", id, err)
		}
	}
	insertHIL("hil-waiting", "waiting-old") // must survive
	insertHIL("hil-pruned", "done-old")     // must be removed with its execution
	insertHIL("hil-orphan", "no-such-exec") // orphaned row must be removed

	if err := s.PruneExecutions(ctx, "wf", 500); err != nil {
		t.Fatalf("PruneExecutions: %v", err)
	}

	if got := statusOf(t, s, "waiting-old"); got != "WAITING" {
		t.Errorf("oldest WAITING execution was pruned; status now %q", got)
	}
	if got := execExists(t, s, "done-old"); got {
		t.Error("old non-WAITING execution beyond keepCount survived; want pruned")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workflow_executions WHERE workflow_id='wf'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 501 { // 500 kept + the protected WAITING row
		t.Errorf("execution count after prune = %d, want 501", n)
	}

	hilIDs := map[string]bool{}
	rows, err := s.db.Query(`SELECT id FROM hil_pending`)
	if err != nil {
		t.Fatalf("select hil: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan hil: %v", err)
		}
		hilIDs[id] = true
	}
	if !hilIDs["hil-waiting"] {
		t.Error("hil_pending row of the surviving WAITING execution was removed")
	}
	if hilIDs["hil-pruned"] {
		t.Error("hil_pending row of a pruned execution survived")
	}
	if hilIDs["hil-orphan"] {
		t.Error("orphaned hil_pending row survived")
	}
}

func execRowID(i int) string {
	return fmt.Sprintf("exec-%03d", i)
}

func execExists(t *testing.T, s *SQLiteWorkflowStore, id string) bool {
	t.Helper()
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM workflow_executions WHERE id=?`, id).Scan(&one)
	if err == nil {
		return true
	}
	return false
}

// deadPID returns a pid that is no longer running: it spawns and reaps a
// short-lived child until Kill(pid, 0) reports the pid as gone.
func deadPID(t *testing.T) int {
	t.Helper()
	for i := 0; i < 10; i++ {
		cmd := exec.Command("sleep", "0.2")
		if err := cmd.Start(); err != nil {
			t.Fatalf("spawn probe process: %v", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Wait() // reap so the pid is genuinely free
		if !processAlive(pid) {
			return pid
		}
	}
	t.Skip("could not obtain a dead pid (all probes were reused)")
	return 0
}

// TestRecoverStaleExecutions_ResumableQueued is the regression guard for the
// crash-between-resume-CAS-and-enqueue window: a QUEUED row with a persisted
// resume_state and no pid is an approved HIL run that must be flipped back to
// WAITING (so the resume loop re-enqueues it), not marked FAILED.
func TestRecoverStaleExecutions_ResumableQueued(t *testing.T) {
	s := newExecStore(t)
	ctx := context.Background()

	// Crash residue: resume CAS done (WAITING→QUEUED), enqueue never happened.
	if _, err := s.db.Exec(
		`INSERT INTO workflow_executions (id, workflow_id, status, resume_state, pid) VALUES (?,?, 'QUEUED', ?, NULL)`,
		"resumable", "wf", `{"completedNodes":{}}`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// A plain QUEUED row without resume_state is an unowned --no-wait row
	// awaiting adoption by a live engine.
	if _, err := s.db.Exec(
		`INSERT INTO workflow_executions (id, workflow_id, status, resume_state, pid) VALUES (?,?, 'QUEUED', '', NULL)`,
		"plain-queued", "wf"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.RecoverStaleExecutions(ctx); err != nil {
		t.Fatalf("RecoverStaleExecutions: %v", err)
	}

	if got := statusOf(t, s, "resumable"); got != "WAITING" {
		t.Errorf("resumable QUEUED execution recovered to %q, want WAITING", got)
	}
	var rs string
	if err := s.db.QueryRow(`SELECT resume_state FROM workflow_executions WHERE id='resumable'`).Scan(&rs); err != nil {
		t.Fatalf("read resume_state: %v", err)
	}
	if rs == "" {
		t.Error("resume_state of the recovered WAITING execution was cleared; want preserved")
	}
	if got := statusOf(t, s, "plain-queued"); got != "QUEUED" {
		t.Errorf("unowned plain QUEUED execution recovered to %q, want QUEUED (adoption candidate)", got)
	}
}

// TestRecoverStaleExecutions_ClearsResumeStateOnTerminal verifies the FAILED
// recovery write clears resume_state — a failed execution must never look
// resumable again.
func TestRecoverStaleExecutions_ClearsResumeStateOnTerminal(t *testing.T) {
	s := newExecStore(t)
	ctx := context.Background()
	dead := deadPID(t)

	if _, err := s.db.Exec(
		`INSERT INTO workflow_executions (id, workflow_id, status, resume_state, pid) VALUES (?,?, 'RUNNING', ?, ?)`,
		"stale-running", "wf", `{"completedNodes":{}}`, dead); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.RecoverStaleExecutions(ctx); err != nil {
		t.Fatalf("RecoverStaleExecutions: %v", err)
	}

	if got := statusOf(t, s, "stale-running"); got != "FAILED" {
		t.Fatalf("stale RUNNING execution recovered to %q, want FAILED", got)
	}
	var rs string
	if err := s.db.QueryRow(`SELECT resume_state FROM workflow_executions WHERE id='stale-running'`).Scan(&rs); err != nil {
		t.Fatalf("read resume_state: %v", err)
	}
	if rs != "" {
		t.Errorf("resume_state after FAILED recovery = %q, want cleared", rs)
	}
}

// TestReapStaleRunningExecutions verifies the periodic sweep: only RUNNING
// rows older than the cutoff whose owner is dead (or this process) are
// failed; fresh rows and rows owned by a live foreign process survive.
func TestReapStaleRunningExecutions(t *testing.T) {
	s := newExecStore(t)
	ctx := context.Background()
	dead := deadPID(t)
	live := os.Getpid()

	old := time.Now().UTC().Add(-25 * time.Hour)
	fresh := time.Now().UTC().Add(-time.Minute)

	insertRunning := func(id string, pid int, startedAt time.Time) {
		t.Helper()
		if _, err := s.db.Exec(
			`INSERT INTO workflow_executions (id, workflow_id, status, started_at, pid) VALUES (?,?, 'RUNNING', ?, ?)`,
			id, "wf", startedAt, pid); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insertRunning("old-dead", dead, old)     // reaped
	insertRunning("old-self", live, old)     // reaped (self-owned that long is stale)
	insertRunning("fresh-dead", dead, fresh) // kept: not old enough
	insertRunning("no-pid-old", 0, old)      // kept: unknown owner, conservative

	if err := s.ReapStaleRunningExecutions(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatalf("ReapStaleRunningExecutions: %v", err)
	}

	if got := statusOf(t, s, "old-dead"); got != "FAILED" {
		t.Errorf("old RUNNING row with dead pid = %s, want FAILED", got)
	}
	if got := statusOf(t, s, "old-self"); got != "FAILED" {
		t.Errorf("old RUNNING row owned by self = %s, want FAILED", got)
	}
	if got := statusOf(t, s, "fresh-dead"); got != "RUNNING" {
		t.Errorf("fresh RUNNING row with dead pid = %s, want RUNNING", got)
	}
	if got := statusOf(t, s, "no-pid-old"); got != "RUNNING" {
		t.Errorf("old RUNNING row with unknown pid = %s, want RUNNING (conservative)", got)
	}
	var errMsg string
	if err := s.db.QueryRow(`SELECT error_message FROM workflow_executions WHERE id='old-dead'`).Scan(&errMsg); err != nil {
		t.Fatalf("read error_message: %v", err)
	}
	if errMsg == "" {
		t.Error("reaped row should carry a 'stale' error message")
	}
}
