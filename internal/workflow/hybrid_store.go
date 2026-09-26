package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HybridWorkflowStore delegates workflow-definition operations to a
// WorkflowFileStore (JSON files) and all execution/credential operations to a
// SQLiteWorkflowStore.
//
// If the file store is nil, all workflow CRUD falls back to SQLite only.
// This is the canonical store — used by both the CLI and the Wails GUI.
type HybridWorkflowStore struct {
	files *WorkflowFileStore
	sql   *SQLiteWorkflowStore
}

// NewHybridWorkflowStore creates a HybridWorkflowStore.
// files may be nil, in which case all workflow CRUD uses SQLite only.
func NewHybridWorkflowStore(files *WorkflowFileStore, sql *SQLiteWorkflowStore) *HybridWorkflowStore {
	return &HybridWorkflowStore{files: files, sql: sql}
}

// ---------------------------------------------------------------------------
// Workflow CRUD — file store preferred, SQLite fallback
// ---------------------------------------------------------------------------

func (h *HybridWorkflowStore) CreateWorkflow(ctx context.Context, w *Workflow) error {
	if h.files != nil {
		if err := h.files.SaveWorkflow(ctx, w); err != nil {
			return err
		}
		return h.mirrorToSQL(ctx, w)
	}
	return h.sql.CreateWorkflow(ctx, w)
}

func (h *HybridWorkflowStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	// Try file store first (Wails-created workflows live here).
	if h.files != nil {
		wf, err := h.files.GetWorkflow(ctx, id)
		if err != nil {
			return nil, err
		}
		if wf != nil {
			return wf, nil
		}
	}
	// Fall back to SQLite (imported/legacy workflows).
	return h.sql.GetWorkflow(ctx, id)
}

func (h *HybridWorkflowStore) ListWorkflows(ctx context.Context, profileID string) ([]Workflow, error) {
	seen := make(map[string]bool)
	var result []Workflow

	// Collect file-store workflows first (file store has no profile concept).
	if h.files != nil {
		filePtrs, err := h.files.ListWorkflows(ctx)
		if err != nil {
			return nil, err
		}
		for _, wf := range filePtrs {
			seen[wf.ID] = true
			result = append(result, *wf)
		}
	}

	// Append SQLite workflows not already present in the file store.
	sqlWFs, err := h.sql.ListWorkflows(ctx, profileID)
	if err != nil {
		return nil, err
	}
	for _, wf := range sqlWFs {
		if !seen[wf.ID] {
			result = append(result, wf)
		}
	}
	return result, nil
}

// NodeCounts returns each workflow's node count, keyed by workflow id,
// following the same precedence as ListWorkflows: a workflow present in the
// file store is the file store's, and SQLite supplies the rest. File-store
// workflows are parsed whole, so their count is already in hand; only the
// SQLite half needs a query.
func (h *HybridWorkflowStore) NodeCounts(ctx context.Context, profileID string) (map[string]int, error) {
	counts := make(map[string]int)
	if h.files != nil {
		filePtrs, err := h.files.ListWorkflows(ctx)
		if err != nil {
			return nil, err
		}
		for _, wf := range filePtrs {
			counts[wf.ID] = len(wf.Nodes)
		}
	}
	sqlCounts, err := h.sql.NodeCounts(ctx, profileID)
	if err != nil {
		return nil, err
	}
	for id, n := range sqlCounts {
		if _, fromFile := counts[id]; !fromFile {
			counts[id] = n
		}
	}
	return counts, nil
}

func (h *HybridWorkflowStore) UpdateWorkflow(ctx context.Context, w *Workflow) error {
	if h.files != nil {
		if err := h.files.SaveWorkflow(ctx, w); err != nil {
			return err
		}
		return h.mirrorToSQL(ctx, w)
	}
	return h.sql.UpdateWorkflow(ctx, w)
}

// SaveWorkflow writes a workflow to the file store (create or update) and
// mirrors it — metadata, nodes, connections — to SQLite, which executions,
// profile scoping and `workflow run --json` read.
func (h *HybridWorkflowStore) SaveWorkflow(ctx context.Context, w *Workflow) error {
	if h.files != nil {
		if err := h.files.SaveWorkflow(ctx, w); err != nil {
			return err
		}
		return h.mirrorToSQL(ctx, w)
	}
	return h.sql.UpdateWorkflow(ctx, w)
}

// ErrSharedIDs reports a workflow whose node or connection ids belong to
// another workflow in SQLite; such a workflow is not mirrored.
var ErrSharedIDs = errors.New("workflow shares node or connection ids with another workflow")

// mirrorToSQL makes SQLite's copy of w match the file just written: the
// workflow row (created, or its name/description/active flag updated; the
// profile and version are left alone), then nodes and connections. The
// file stays canonical. A workflow whose node/connection ids are owned by
// another workflow is skipped (SaveWorkflowNodes' upsert would move them);
// the save itself still succeeds. The caller's w is not modified.
func (h *HybridWorkflowStore) mirrorToSQL(ctx context.Context, w *Workflow) error {
	if h.sql == nil || w == nil || w.ID == "" {
		return nil
	}
	if err := h.sql.checkOwnIDs(ctx, w); err != nil {
		if errors.Is(err, ErrSharedIDs) {
			return nil
		}
		return err
	}
	var n int
	if err := h.sql.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflows WHERE id = ?`, w.ID).Scan(&n); err != nil {
		return fmt.Errorf("mirror workflow %s: %w", w.ID, err)
	}
	if n == 0 {
		stub := *w
		stub.Nodes, stub.Connections = nil, nil
		if err := h.sql.CreateWorkflow(ctx, &stub); err != nil {
			return fmt.Errorf("mirror workflow %s: %w", w.ID, err)
		}
	} else if _, err := h.sql.db.ExecContext(ctx,
		`UPDATE workflows SET name = ?, description = ?, is_active = ?, updated_at = ? WHERE id = ?`,
		w.Name, w.Description, boolToInt(w.IsActive), time.Now().UTC(), w.ID); err != nil {
		return fmt.Errorf("mirror workflow %s: %w", w.ID, err)
	}
	nodes := append([]WorkflowNode(nil), w.Nodes...)
	if err := h.sql.SaveWorkflowNodes(ctx, w.ID, nodes); err != nil {
		return fmt.Errorf("mirror workflow %s nodes: %w", w.ID, err)
	}
	conns := append([]WorkflowConnection(nil), w.Connections...)
	if err := h.sql.SaveWorkflowConnections(ctx, w.ID, conns); err != nil {
		return fmt.Errorf("mirror workflow %s connections: %w", w.ID, err)
	}
	return nil
}

// checkOwnIDs returns ErrSharedIDs when any of w's node or connection ids
// belongs to a different workflow in SQLite.
func (s *SQLiteWorkflowStore) checkOwnIDs(ctx context.Context, w *Workflow) error {
	var nodeIDs, connIDs []any
	for _, n := range w.Nodes {
		if n.ID != "" {
			nodeIDs = append(nodeIDs, n.ID)
		}
	}
	for _, c := range w.Connections {
		if c.ID != "" {
			connIDs = append(connIDs, c.ID)
		}
	}
	for _, q := range []struct {
		table string
		ids   []any
	}{{"workflow_nodes", nodeIDs}, {"workflow_connections", connIDs}} {
		if len(q.ids) == 0 {
			continue
		}
		stmt := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE workflow_id != ? AND id IN (%s)`, q.table,
			strings.TrimSuffix(strings.Repeat("?,", len(q.ids)), ","))
		var n int
		if err := s.db.QueryRowContext(ctx, stmt, append([]any{w.ID}, q.ids...)...).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s (%d in %s)", ErrSharedIDs, w.ID, n, q.table)
		}
	}
	return nil
}

// DeleteWorkflow removes a workflow from both stores. "Already gone" from
// either side is treated as success (the caller wanted it gone; it is) —
// WorkflowFileStore.DeleteWorkflow already returns nil for a missing file,
// and SQLiteWorkflowStore.DeleteWorkflow wraps ErrWorkflowNotFound for zero
// rows affected. Any OTHER error (permission denied, disk full, a genuine
// DB error) is propagated, not swallowed: this method previously discarded
// every error unconditionally, which made it unsafe to use as a
// compensating cleanup action — a caller relying on its return value to
// know whether cleanup actually happened would be told "success" even when
// the workflow (or its rows) are still sitting there.
func (h *HybridWorkflowStore) DeleteWorkflow(ctx context.Context, id string) error {
	var errs []error
	if h.files != nil {
		if err := h.files.DeleteWorkflow(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("file store: %w", err))
		}
	}
	if err := h.sql.DeleteWorkflow(ctx, id); err != nil && !errors.Is(err, ErrWorkflowNotFound) {
		errs = append(errs, fmt.Errorf("sql store: %w", err))
	}
	return errors.Join(errs...)
}

func (h *HybridWorkflowStore) SetWorkflowActive(ctx context.Context, id string, active bool) error {
	// Try to update in file store.
	if h.files != nil {
		wf, err := h.files.GetWorkflow(ctx, id)
		if err == nil && wf != nil {
			wf.IsActive = active
			if err := h.files.SaveWorkflow(ctx, wf); err != nil {
				return err
			}
			return h.mirrorToSQL(ctx, wf)
		}
	}
	return h.sql.SetWorkflowActive(ctx, id, active)
}

// ---------------------------------------------------------------------------
// Node / Connection save — SQLite only (executions need them there)
// ---------------------------------------------------------------------------

// ensureSQLWorkflow guarantees a minimal metadata row exists in SQLite for
// workflowID, mirroring it from the file store if necessary. Nodes,
// connections, and executions all have hard FK references to workflows(id)
// in SQLite even when the workflow's canonical definition lives only in the
// file store (e.g. created via the CLI's `workflow create`).
func (h *HybridWorkflowStore) ensureSQLWorkflow(ctx context.Context, workflowID string) {
	existing, err := h.sql.GetWorkflow(ctx, workflowID)
	if err != nil || existing != nil {
		return
	}
	if h.files == nil {
		return
	}
	wf, ferr := h.files.GetWorkflow(ctx, workflowID)
	if ferr != nil || wf == nil {
		return
	}
	// Insert a minimal stub row — only metadata, no nodes/connections.
	stub := *wf
	stub.Nodes = nil
	stub.Connections = nil
	_ = h.sql.CreateWorkflow(ctx, &stub)
}

// syncFileNodes mirrors the current SQL-stored nodes/connections for
// workflowID back onto the file-store copy. GetWorkflow prefers the file
// store, so without this, nodes/connections written via SaveWorkflowNodes/
// SaveWorkflowConnections would be invisible to every read path.
func (h *HybridWorkflowStore) syncFileNodes(ctx context.Context, workflowID string) {
	if h.files == nil {
		return
	}
	wf, err := h.files.GetWorkflow(ctx, workflowID)
	if err != nil || wf == nil {
		return
	}
	sqlWF, err := h.sql.GetWorkflow(ctx, workflowID)
	if err != nil || sqlWF == nil {
		return
	}
	wf.Nodes = sqlWF.Nodes
	wf.Connections = sqlWF.Connections
	_ = h.files.SaveWorkflow(ctx, wf)
}

func (h *HybridWorkflowStore) SaveWorkflowNodes(ctx context.Context, workflowID string, nodes []WorkflowNode) error {
	h.ensureSQLWorkflow(ctx, workflowID)
	if err := h.sql.SaveWorkflowNodes(ctx, workflowID, nodes); err != nil {
		return err
	}
	h.syncFileNodes(ctx, workflowID)
	return nil
}

func (h *HybridWorkflowStore) SaveWorkflowConnections(ctx context.Context, workflowID string, conns []WorkflowConnection) error {
	h.ensureSQLWorkflow(ctx, workflowID)
	if err := h.sql.SaveWorkflowConnections(ctx, workflowID, conns); err != nil {
		return err
	}
	h.syncFileNodes(ctx, workflowID)
	return nil
}

// ---------------------------------------------------------------------------
// Executions — always SQLite
// ---------------------------------------------------------------------------

func (h *HybridWorkflowStore) CreateExecution(ctx context.Context, e *WorkflowExecution) error {
	h.ensureSQLWorkflow(ctx, e.WorkflowID)
	return h.sql.CreateExecution(ctx, e)
}

func (h *HybridWorkflowStore) GetExecution(ctx context.Context, id string) (*WorkflowExecution, error) {
	return h.sql.GetExecution(ctx, id)
}

func (h *HybridWorkflowStore) ListExecutions(ctx context.Context, workflowID string, limit int) ([]WorkflowExecution, error) {
	return h.sql.ListExecutions(ctx, workflowID, limit)
}

func (h *HybridWorkflowStore) UpdateExecutionStatus(ctx context.Context, id string, status string, errMsg string) error {
	return h.sql.UpdateExecutionStatus(ctx, id, status, errMsg)
}

func (h *HybridWorkflowStore) SetExecutionStarted(ctx context.Context, id string) error {
	return h.sql.SetExecutionStarted(ctx, id)
}

func (h *HybridWorkflowStore) SetExecutionFinished(ctx context.Context, id string, status string, errMsg string) error {
	return h.sql.SetExecutionFinished(ctx, id, status, errMsg)
}

func (h *HybridWorkflowStore) CreateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error {
	return h.sql.CreateExecutionNode(ctx, en)
}

func (h *HybridWorkflowStore) UpdateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error {
	return h.sql.UpdateExecutionNode(ctx, en)
}

func (h *HybridWorkflowStore) SetExecutionNodeFinished(ctx context.Context, id string, status string, outputItems []Item, errMsg string) error {
	return h.sql.SetExecutionNodeFinished(ctx, id, status, outputItems, errMsg)
}

// ---------------------------------------------------------------------------
// Maintenance — SQLite
// ---------------------------------------------------------------------------

func (h *HybridWorkflowStore) RecoverStaleExecutions(ctx context.Context) error {
	return h.sql.RecoverStaleExecutions(ctx)
}

func (h *HybridWorkflowStore) ReapStaleRunningExecutions(ctx context.Context, olderThan time.Time) error {
	return h.sql.ReapStaleRunningExecutions(ctx, olderThan)
}

func (h *HybridWorkflowStore) CancelQueuedExecution(ctx context.Context, id string) (bool, error) {
	return h.sql.CancelQueuedExecution(ctx, id)
}

func (h *HybridWorkflowStore) SetExecutionWaiting(ctx context.Context, id string, resumeState string) error {
	return h.sql.SetExecutionWaiting(ctx, id, resumeState)
}

func (h *HybridWorkflowStore) ResumeWaitingExecution(ctx context.Context, id string) (bool, error) {
	return h.sql.ResumeWaitingExecution(ctx, id)
}

func (h *HybridWorkflowStore) ListResumableExecutions(ctx context.Context) ([]string, error) {
	return h.sql.ListResumableExecutions(ctx)
}

func (h *HybridWorkflowStore) ListAdoptableExecutions(ctx context.Context) ([]string, error) {
	return h.sql.ListAdoptableExecutions(ctx)
}

func (h *HybridWorkflowStore) ClaimQueuedExecution(ctx context.Context, id string) (bool, error) {
	return h.sql.ClaimQueuedExecution(ctx, id)
}

func (h *HybridWorkflowStore) PruneExecutions(ctx context.Context, workflowID string, keepCount int) error {
	return h.sql.PruneExecutions(ctx, workflowID, keepCount)
}

// RawDB delegates to the SQLite store's underlying DB.
func (h *HybridWorkflowStore) RawDB() *sql.DB { return h.sql.RawDB() }
