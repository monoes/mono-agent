package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// newID generates a new UUID string for use as a primary key.
func newID() string {
	return uuid.New().String()
}

// sqliteTime wraps time.Time so it can scan both time.Time and string values
// from SQLite (modernc.org/sqlite stores timestamps as text).
type sqliteTime struct{ time.Time }

func (st *sqliteTime) Scan(src interface{}) error {
	switch v := src.(type) {
	case time.Time:
		st.Time = v
		return nil
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05Z", "2006-01-02 15:04:05",
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999 +0000 UTC",
		} {
			if t, err := time.Parse(layout, v); err == nil {
				st.Time = t
				return nil
			}
		}
		return fmt.Errorf("sqliteTime: cannot parse %q", v)
	case nil:
		st.Time = time.Time{}
		return nil
	default:
		return fmt.Errorf("sqliteTime: unsupported type %T", src)
	}
}

// sqliteNullTime wraps *time.Time for nullable timestamp columns.
type sqliteNullTime struct{ ptr **time.Time }

func newSqliteNullTime(dst **time.Time) sqliteNullTime { return sqliteNullTime{ptr: dst} }

func (sn sqliteNullTime) Scan(src interface{}) error {
	if src == nil {
		*sn.ptr = nil
		return nil
	}
	var st sqliteTime
	if err := st.Scan(src); err != nil {
		return err
	}
	t := st.Time
	*sn.ptr = &t
	return nil
}

// ---------------------------------------------------------------------------
// WorkflowStore interface
// ---------------------------------------------------------------------------

// WorkflowStore defines all persistence operations for the workflow system.
type WorkflowStore interface {
	// Workflows
	CreateWorkflow(ctx context.Context, w *Workflow) error
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	ListWorkflows(ctx context.Context, profileID string) ([]Workflow, error)
	UpdateWorkflow(ctx context.Context, w *Workflow) error
	DeleteWorkflow(ctx context.Context, id string) error
	SetWorkflowActive(ctx context.Context, id string, active bool) error

	// Nodes (upsert all nodes for a workflow — delete removed, insert/update existing)
	SaveWorkflowNodes(ctx context.Context, workflowID string, nodes []WorkflowNode) error
	// Connections (upsert all connections for a workflow — delete removed, insert/update existing)
	SaveWorkflowConnections(ctx context.Context, workflowID string, conns []WorkflowConnection) error

	// Executions
	CreateExecution(ctx context.Context, e *WorkflowExecution) error
	GetExecution(ctx context.Context, id string) (*WorkflowExecution, error)
	GetExecutionStatus(ctx context.Context, id string) (string, error)
	ListExecutions(ctx context.Context, workflowID string, limit int) ([]WorkflowExecution, error)
	UpdateExecutionStatus(ctx context.Context, id string, status string, errMsg string) error
	SetExecutionStarted(ctx context.Context, id string) error
	SetExecutionFinished(ctx context.Context, id string, status string, errMsg string) error
	SetExecutionWaiting(ctx context.Context, id string, resumeState string) error
	ResumeWaitingExecution(ctx context.Context, id string) (bool, error)
	ListResumableExecutions(ctx context.Context) ([]string, error)

	// Adoption: unowned QUEUED executions (pid 0, no resume state) are
	// claimed and run by whichever engine is alive.
	ListAdoptableExecutions(ctx context.Context) ([]string, error)
	ClaimQueuedExecution(ctx context.Context, id string) (bool, error)

	// Execution nodes
	CreateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error
	UpdateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error
	SetExecutionNodeFinished(ctx context.Context, id string, status string, outputItems []Item, errMsg string) error

	// Recovery
	RecoverStaleExecutions(ctx context.Context) error
	ReapStaleRunningExecutions(ctx context.Context, olderThan time.Time) error
	CancelQueuedExecution(ctx context.Context, id string) (bool, error)
	PruneExecutions(ctx context.Context, workflowID string, keepCount int) error

	// RawDB returns the underlying *sql.DB for use by subsystems that need
	// direct DB access (e.g., vault registration).
	RawDB() *sql.DB
}

// ---------------------------------------------------------------------------
// SQLiteWorkflowStore
// ---------------------------------------------------------------------------

// SQLiteWorkflowStore implements WorkflowStore using a *sql.DB (SQLite).
type SQLiteWorkflowStore struct {
	db *sql.DB
}

// NewSQLiteWorkflowStore creates a new SQLiteWorkflowStore backed by db.
// The store does not run migrations; use the existing migration system.
func NewSQLiteWorkflowStore(db *sql.DB) *SQLiteWorkflowStore {
	return &SQLiteWorkflowStore{db: db}
}

// RawDB returns the underlying *sql.DB.
func (s *SQLiteWorkflowStore) RawDB() *sql.DB { return s.db }

// ---------------------------------------------------------------------------
// Workflow CRUD
// ---------------------------------------------------------------------------

// CreateWorkflow inserts a new workflow row. If w.ID is empty a UUID is generated.
// w.Nodes and w.Connections are ignored here; use SaveWorkflowNodes /
// SaveWorkflowConnections to persist the graph.
func (s *SQLiteWorkflowStore) CreateWorkflow(ctx context.Context, w *Workflow) error {
	if w.ID == "" {
		w.ID = newID()
	}
	now := time.Now().UTC()
	w.CreatedAt = now
	w.UpdatedAt = now
	if w.Version == 0 {
		w.Version = 1
	}
	if w.ProfileID == "" {
		w.ProfileID = "default"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workflows (id, name, description, is_active, version, profile_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, boolToInt(w.IsActive), w.Version, w.ProfileID,
		w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("creating workflow %s: %w", w.ID, err)
	}
	return nil
}

// GetWorkflow retrieves a workflow by ID along with its nodes and connections.
// Returns nil, nil when not found.
func (s *SQLiteWorkflowStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	w := &Workflow{}
	var isActive int
	var createdAt, updatedAt sqliteTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, is_active, version, profile_id, created_at, updated_at
		FROM workflows WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Description, &isActive, &w.Version, &w.ProfileID, &createdAt, &updatedAt)
	w.CreatedAt = createdAt.Time
	w.UpdatedAt = updatedAt.Time
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting workflow %s: %w", id, err)
	}
	w.IsActive = isActive != 0

	nodes, err := s.loadWorkflowNodes(ctx, id)
	if err != nil {
		return nil, err
	}
	w.Nodes = nodes

	conns, err := s.loadWorkflowConnections(ctx, id)
	if err != nil {
		return nil, err
	}
	w.Connections = conns

	return w, nil
}

// ListWorkflows returns workflows ordered by created_at DESC.
// If profileID is non-empty, only that profile's workflows are returned.
// Nodes and Connections are not populated; call GetWorkflow for full detail.
func (s *SQLiteWorkflowStore) ListWorkflows(ctx context.Context, profileID string) ([]Workflow, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if profileID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, name, description, is_active, version, COALESCE(profile_id,'default'), created_at, updated_at
			FROM workflows WHERE COALESCE(profile_id,'default') = ? ORDER BY created_at DESC`, profileID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, name, description, is_active, version, COALESCE(profile_id,'default'), created_at, updated_at
			FROM workflows ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("listing workflows: %w", err)
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		w := Workflow{}
		var isActive int
		var createdAt, updatedAt sqliteTime
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &isActive, &w.Version,
			&w.ProfileID, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning workflow row: %w", err)
		}
		w.CreatedAt = createdAt.Time
		w.UpdatedAt = updatedAt.Time
		w.IsActive = isActive != 0
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWorkflow updates the mutable fields of an existing workflow.
// Nodes and Connections are not touched; use SaveWorkflowNodes /
// SaveWorkflowConnections for that.
func (s *SQLiteWorkflowStore) UpdateWorkflow(ctx context.Context, w *Workflow) error {
	w.UpdatedAt = time.Now().UTC()
	w.Version++

	_, err := s.db.ExecContext(ctx, `
		UPDATE workflows
		SET name = ?, description = ?, is_active = ?, version = ?, updated_at = ?
		WHERE id = ?`,
		w.Name, w.Description, boolToInt(w.IsActive), w.Version, w.UpdatedAt, w.ID,
	)
	if err != nil {
		return fmt.Errorf("updating workflow %s: %w", w.ID, err)
	}
	return nil
}

// DeleteWorkflow removes a workflow and, via ON DELETE CASCADE, all associated
// nodes, connections, and executions.
func (s *SQLiteWorkflowStore) DeleteWorkflow(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM workflows WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting workflow %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		// Wrapped in the package sentinel (not a bare fmt.Errorf) so callers
		// like HybridWorkflowStore.DeleteWorkflow can distinguish "already
		// gone, nothing to do" from a genuine deletion failure via errors.Is
		// instead of matching on this exact message text.
		return fmt.Errorf("%w: %s", ErrWorkflowNotFound, id)
	}
	return nil
}

// SetWorkflowActive toggles the is_active flag on a workflow.
func (s *SQLiteWorkflowStore) SetWorkflowActive(ctx context.Context, id string, active bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE workflows SET is_active = ?, updated_at = ? WHERE id = ?",
		boolToInt(active), time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("setting workflow %s active=%v: %w", id, active, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Nodes & Connections
// ---------------------------------------------------------------------------

// SaveWorkflowNodes upserts all supplied nodes for a workflow atomically and
// deletes only the nodes that are no longer present. Connections of
// surviving nodes are untouched; connections of deleted nodes cascade via
// the source/target FKs as before.
func (s *SQLiteWorkflowStore) SaveWorkflowNodes(ctx context.Context, workflowID string, nodes []WorkflowNode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning save-nodes transaction: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO workflow_nodes
			(id, workflow_id, node_type, name, config, position_x, position_y, disabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workflow_id = excluded.workflow_id,
			node_type   = excluded.node_type,
			name        = excluded.name,
			config      = excluded.config,
			position_x  = excluded.position_x,
			position_y  = excluded.position_y,
			disabled    = excluded.disabled,
			updated_at  = excluded.updated_at`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("preparing node upsert: %w", err)
	}
	defer stmt.Close()

	args := make([]interface{}, 0, len(nodes)+1)
	args = append(args, workflowID)
	now := time.Now().UTC()
	for i := range nodes {
		n := &nodes[i]
		if n.ID == "" {
			n.ID = newID()
		}
		n.WorkflowID = workflowID
		if err := n.MarshalConfig(); err != nil {
			tx.Rollback()
			return fmt.Errorf("marshalling config for node %s: %w", n.ID, err)
		}
		n.CreatedAt = now
		n.UpdatedAt = now

		if _, err := stmt.ExecContext(ctx,
			n.ID, n.WorkflowID, n.Type, n.Name, n.ConfigRaw,
			n.PositionX, n.PositionY, boolToInt(n.Disabled),
			n.CreatedAt, n.UpdatedAt,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("upserting node %s: %w", n.ID, err)
		}
		args = append(args, n.ID)
	}

	// Delete only genuinely-removed nodes; their connections cascade via FK.
	var delSQL string
	if len(nodes) == 0 {
		delSQL = "DELETE FROM workflow_nodes WHERE workflow_id = ?"
	} else {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(nodes)), ",")
		delSQL = "DELETE FROM workflow_nodes WHERE workflow_id = ? AND id NOT IN (" + placeholders + ")"
	}
	if _, err := tx.ExecContext(ctx, delSQL, args...); err != nil {
		tx.Rollback()
		return fmt.Errorf("deleting removed nodes for workflow %s: %w", workflowID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing save-nodes transaction: %w", err)
	}
	return nil
}

// SaveWorkflowConnections replaces all connections for a workflow atomically.
// Existing connections are deleted and the supplied slice is inserted fresh.
func (s *SQLiteWorkflowStore) SaveWorkflowConnections(ctx context.Context, workflowID string, conns []WorkflowConnection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning save-connections transaction: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM workflow_connections WHERE workflow_id = ?", workflowID); err != nil {
		tx.Rollback()
		return fmt.Errorf("deleting old connections for workflow %s: %w", workflowID, err)
	}

	// Validate node references up front, in the same transaction the insert
	// below would otherwise rely on: without this, a connection naming an
	// unknown node id (a stale id after a node was renamed/removed, a typo
	// in a hand-edited or generated workflow JSON, etc.) surfaces only as
	// the SQLite foreign-key error itself ("constraint failed: FOREIGN KEY
	// constraint failed (787)") — accurate but useless for finding which
	// connection or which node id is actually wrong.
	nodeRows, err := tx.QueryContext(ctx, "SELECT id FROM workflow_nodes WHERE workflow_id = ?", workflowID)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("loading node ids for workflow %s: %w", workflowID, err)
	}
	validNodeIDs := make(map[string]bool)
	for nodeRows.Next() {
		var id string
		if err := nodeRows.Scan(&id); err != nil {
			nodeRows.Close()
			tx.Rollback()
			return fmt.Errorf("scanning node id for workflow %s: %w", workflowID, err)
		}
		validNodeIDs[id] = true
	}
	if err := nodeRows.Err(); err != nil {
		nodeRows.Close()
		tx.Rollback()
		return fmt.Errorf("reading node ids for workflow %s: %w", workflowID, err)
	}
	nodeRows.Close()
	for _, c := range conns {
		if !validNodeIDs[c.SourceNodeID] {
			tx.Rollback()
			return fmt.Errorf("%w: connection %q references unknown source node %q", ErrDanglingConnection, c.ID, c.SourceNodeID)
		}
		if !validNodeIDs[c.TargetNodeID] {
			tx.Rollback()
			return fmt.Errorf("%w: connection %q references unknown target node %q", ErrDanglingConnection, c.ID, c.TargetNodeID)
		}
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO workflow_connections
			(id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("preparing connection insert: %w", err)
	}
	defer stmt.Close()

	for i := range conns {
		c := &conns[i]
		if c.ID == "" {
			c.ID = newID()
		}
		c.WorkflowID = workflowID

		if _, err := stmt.ExecContext(ctx,
			c.ID, c.WorkflowID, c.SourceNodeID, c.SourceHandle,
			c.TargetNodeID, c.TargetHandle, c.Position,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("inserting connection %s: %w", c.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing save-connections transaction: %w", err)
	}
	return nil
}

// loadWorkflowNodes is an internal helper to fetch nodes for a workflow.
func (s *SQLiteWorkflowStore) loadWorkflowNodes(ctx context.Context, workflowID string) ([]WorkflowNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workflow_id, node_type, name, config, position_x, position_y, disabled, created_at, updated_at
		FROM workflow_nodes WHERE workflow_id = ? ORDER BY created_at ASC`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("loading nodes for workflow %s: %w", workflowID, err)
	}
	defer rows.Close()

	var nodes []WorkflowNode
	for rows.Next() {
		n := WorkflowNode{}
		var disabled int
		var createdAt, updatedAt sqliteTime
		if err := rows.Scan(
			&n.ID, &n.WorkflowID, &n.Type, &n.Name, &n.ConfigRaw,
			&n.PositionX, &n.PositionY, &disabled, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}
		n.CreatedAt = createdAt.Time
		n.UpdatedAt = updatedAt.Time
		n.Disabled = disabled != 0
		if err := n.ParseConfig(); err != nil {
			return nil, fmt.Errorf("parsing config for node %s: %w", n.ID, err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// loadWorkflowConnections is an internal helper to fetch connections for a workflow.
func (s *SQLiteWorkflowStore) loadWorkflowConnections(ctx context.Context, workflowID string) ([]WorkflowConnection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position
		FROM workflow_connections WHERE workflow_id = ? ORDER BY position ASC`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("loading connections for workflow %s: %w", workflowID, err)
	}
	defer rows.Close()

	var conns []WorkflowConnection
	for rows.Next() {
		c := WorkflowConnection{}
		if err := rows.Scan(
			&c.ID, &c.WorkflowID, &c.SourceNodeID, &c.SourceHandle,
			&c.TargetNodeID, &c.TargetHandle, &c.Position,
		); err != nil {
			return nil, fmt.Errorf("scanning connection row: %w", err)
		}
		conns = append(conns, c)
	}
	return conns, rows.Err()
}

// ---------------------------------------------------------------------------
// Executions
// ---------------------------------------------------------------------------

// CreateExecution inserts a new workflow execution record.
// If e.ID is empty a UUID is generated.
func (s *SQLiteWorkflowStore) CreateExecution(ctx context.Context, e *WorkflowExecution) error {
	if e.ID == "" {
		e.ID = newID()
	}
	e.CreatedAt = time.Now().UTC()
	if e.Status == "" {
		e.Status = "QUEUED"
	}

	if err := e.MarshalTriggerData(); err != nil {
		return fmt.Errorf("marshalling trigger data for execution %s: %w", e.ID, err)
	}

	profileID := e.ProfileID
	if profileID == "" {
		profileID = "default"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workflow_executions
			(id, workflow_id, status, trigger_type, trigger_data, started_at, finished_at, error_message, created_at, profile_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.WorkflowID, e.Status, e.TriggerType, e.TriggerDataRaw,
		e.StartedAt, e.FinishedAt, e.ErrorMessage, e.CreatedAt, profileID,
	)
	if err != nil {
		return fmt.Errorf("creating execution %s: %w", e.ID, err)
	}
	return nil
}

// GetExecution retrieves a single execution by ID, populating its Nodes slice.
// Returns nil, nil when not found.
func (s *SQLiteWorkflowStore) GetExecution(ctx context.Context, id string) (*WorkflowExecution, error) {
	e := &WorkflowExecution{}
	var createdAt sqliteTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workflow_id, status, trigger_type, trigger_data, started_at, finished_at, error_message, created_at, COALESCE(resume_state,''), COALESCE(profile_id,'default')
		FROM workflow_executions WHERE id = ?`, id,
	).Scan(
		&e.ID, &e.WorkflowID, &e.Status, &e.TriggerType, &e.TriggerDataRaw,
		newSqliteNullTime(&e.StartedAt), newSqliteNullTime(&e.FinishedAt), &e.ErrorMessage, &createdAt, &e.ResumeState, &e.ProfileID,
	)
	e.CreatedAt = createdAt.Time
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting execution %s: %w", id, err)
	}

	if err := e.ParseTriggerData(); err != nil {
		return nil, fmt.Errorf("parsing trigger data for execution %s: %w", id, err)
	}

	nodes, err := s.loadExecutionNodes(ctx, id)
	if err != nil {
		return nil, err
	}
	e.Nodes = nodes

	return e, nil
}

// ListExecutions returns executions for a workflow ordered by created_at DESC.
// Pass limit <= 0 to return all executions. Execution nodes are not populated.
func (s *SQLiteWorkflowStore) ListExecutions(ctx context.Context, workflowID string, limit int) ([]WorkflowExecution, error) {
	query := `
		SELECT id, workflow_id, status, trigger_type, trigger_data, started_at, finished_at, error_message, created_at
		FROM workflow_executions WHERE workflow_id = ? ORDER BY created_at DESC`
	var args []interface{}
	args = append(args, workflowID)

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing executions for workflow %s: %w", workflowID, err)
	}
	defer rows.Close()

	var out []WorkflowExecution
	for rows.Next() {
		e := WorkflowExecution{}
		var createdAt sqliteTime
		if err := rows.Scan(
			&e.ID, &e.WorkflowID, &e.Status, &e.TriggerType, &e.TriggerDataRaw,
			newSqliteNullTime(&e.StartedAt), newSqliteNullTime(&e.FinishedAt), &e.ErrorMessage, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning execution row: %w", err)
		}
		e.CreatedAt = createdAt.Time
		if err := e.ParseTriggerData(); err != nil {
			return nil, fmt.Errorf("parsing trigger data for execution %s: %w", e.ID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateExecutionStatus sets status and error_message on an execution.
func (s *SQLiteWorkflowStore) UpdateExecutionStatus(ctx context.Context, id string, status string, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE workflow_executions SET status = ?, error_message = ? WHERE id = ?",
		status, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("updating execution status %s: %w", id, err)
	}
	return nil
}

// SetExecutionStarted marks an execution as RUNNING and records started_at and the current PID.
func (s *SQLiteWorkflowStore) SetExecutionStarted(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE workflow_executions SET status = 'RUNNING', started_at = ?, pid = ? WHERE id = ?",
		now, os.Getpid(), id,
	)
	if err != nil {
		return fmt.Errorf("setting execution started %s: %w", id, err)
	}
	return nil
}

// SetExecutionFinished records finished_at, final status and optional error.
// It also clears any resume_state — a finished execution is never resumed.
func (s *SQLiteWorkflowStore) SetExecutionFinished(ctx context.Context, id string, status string, errMsg string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		"UPDATE workflow_executions SET status = ?, finished_at = ?, error_message = ?, resume_state = '' WHERE id = ?",
		status, now, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("setting execution finished %s: %w", id, err)
	}
	return nil
}

// SetExecutionWaiting marks an execution as paused (WAITING) and stores the
// serialized in-flight state needed to resume it. Unlike SetExecutionFinished,
// it does not set finished_at — the execution is suspended, not done.
func (s *SQLiteWorkflowStore) SetExecutionWaiting(ctx context.Context, id string, resumeState string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE workflow_executions SET status = 'WAITING', resume_state = ? WHERE id = ?",
		resumeState, id,
	)
	if err != nil {
		return fmt.Errorf("setting execution waiting %s: %w", id, err)
	}
	return nil
}

// ListResumableExecutions returns the IDs of WAITING executions that have no
// still-pending Human-in-Loop items — i.e. every pause point has been resolved
// (approved or rejected), so the execution can be resumed.
func (s *SQLiteWorkflowStore) ListResumableExecutions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM workflow_executions
		WHERE status = 'WAITING'
		  AND id NOT IN (SELECT execution_id FROM hil_pending WHERE status = 'pending')`)
	if err != nil {
		return nil, fmt.Errorf("listing resumable executions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning resumable execution: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---------------------------------------------------------------------------
// Execution nodes
// ---------------------------------------------------------------------------

// CreateExecutionNode inserts a new execution-node record.
// If en.ID is empty a UUID is generated.
func (s *SQLiteWorkflowStore) CreateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error {
	if en.ID == "" {
		en.ID = newID()
	}
	if en.Status == "" {
		en.Status = "PENDING"
	}

	if err := en.MarshalItems(); err != nil {
		return fmt.Errorf("marshalling items for execution node %s: %w", en.ID, err)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workflow_execution_nodes
			(id, execution_id, node_id, node_name, status, input_items, output_items, error_message, started_at, finished_at, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		en.ID, en.ExecutionID, en.NodeID, en.NodeName, en.Status,
		en.InputRaw, en.OutputRaw, en.ErrorMessage,
		en.StartedAt, en.FinishedAt, en.RetryCount,
	)
	if err != nil {
		return fmt.Errorf("creating execution node %s: %w", en.ID, err)
	}
	return nil
}

// UpdateExecutionNode updates all mutable fields of an execution-node record.
func (s *SQLiteWorkflowStore) UpdateExecutionNode(ctx context.Context, en *WorkflowExecutionNode) error {
	if err := en.MarshalItems(); err != nil {
		return fmt.Errorf("marshalling items for execution node %s: %w", en.ID, err)
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE workflow_execution_nodes
		SET status = ?, input_items = ?, output_items = ?, error_message = ?,
		    started_at = ?, finished_at = ?, retry_count = ?
		WHERE id = ?`,
		en.Status, en.InputRaw, en.OutputRaw, en.ErrorMessage,
		en.StartedAt, en.FinishedAt, en.RetryCount, en.ID,
	)
	if err != nil {
		return fmt.Errorf("updating execution node %s: %w", en.ID, err)
	}
	return nil
}

// SetExecutionNodeFinished marks an execution node as finished, marshals
// outputItems to JSON, and records status, error_message, and finished_at.
func (s *SQLiteWorkflowStore) SetExecutionNodeFinished(ctx context.Context, id string, status string, outputItems []Item, errMsg string) error {
	outputRaw, err := marshalItems(outputItems)
	if err != nil {
		return fmt.Errorf("marshalling output items for execution node %s: %w", id, err)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE workflow_execution_nodes
		SET status = ?, output_items = ?, error_message = ?, finished_at = ?
		WHERE id = ?`,
		status, outputRaw, errMsg, now, id,
	)
	if err != nil {
		return fmt.Errorf("setting execution node finished %s: %w", id, err)
	}
	return nil
}

// loadExecutionNodes is an internal helper to fetch execution-node records.
func (s *SQLiteWorkflowStore) loadExecutionNodes(ctx context.Context, executionID string) ([]WorkflowExecutionNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, node_id, node_name, status, input_items, output_items, error_message, started_at, finished_at, retry_count
		FROM workflow_execution_nodes WHERE execution_id = ? ORDER BY rowid ASC`, executionID)
	if err != nil {
		return nil, fmt.Errorf("loading execution nodes for execution %s: %w", executionID, err)
	}
	defer rows.Close()

	var out []WorkflowExecutionNode
	for rows.Next() {
		en := WorkflowExecutionNode{}
		if err := rows.Scan(
			&en.ID, &en.ExecutionID, &en.NodeID, &en.NodeName, &en.Status,
			&en.InputRaw, &en.OutputRaw, &en.ErrorMessage,
			newSqliteNullTime(&en.StartedAt), newSqliteNullTime(&en.FinishedAt), &en.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scanning execution node row: %w", err)
		}
		if err := en.ParseItems(); err != nil {
			return nil, fmt.Errorf("parsing items for execution node %s: %w", en.ID, err)
		}
		out = append(out, en)
	}
	return out, rows.Err()
}

// ListAdoptableExecutions returns the IDs of QUEUED executions that have no
// owner (pid 0/NULL) and no resume_state. These are the rows
// `workflow run --no-wait` persists before exiting, waiting for any live
// engine to claim them; crash-resume residue (QUEUED with resume_state) is
// excluded — RecoverStaleExecutions flips those back to WAITING for the
// resume loop instead.
func (s *SQLiteWorkflowStore) ListAdoptableExecutions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM workflow_executions
		WHERE status = 'QUEUED'
		  AND COALESCE(pid, 0) = 0
		  AND COALESCE(resume_state, '') = ''`)
	if err != nil {
		return nil, fmt.Errorf("listing adoptable executions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning adoptable execution: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClaimQueuedExecution atomically claims an unowned QUEUED execution for this
// process: a CAS on pid 0/NULL → self. Among engines sharing the DB exactly
// one wins the claim; losers get false. The status stays QUEUED —
// SetExecutionStarted flips it to RUNNING (and re-stamps pid) once the run
// actually dispatches.
func (s *SQLiteWorkflowStore) ClaimQueuedExecution(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE workflow_executions SET pid = ? WHERE id = ? AND status = 'QUEUED' AND (pid = 0 OR pid IS NULL)`,
		os.Getpid(), id)
	if err != nil {
		return false, fmt.Errorf("claiming queued execution %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// Recovery & maintenance
// ---------------------------------------------------------------------------

// RecoverStaleExecutions transitions stale RUNNING or QUEUED executions to
// FAILED. This should be called once on process startup to handle executions
// that were interrupted by a previous crash or restart.
//
// Two exceptions for QUEUED rows with no pid:
//   - with a persisted resume_state it is the residue of a crash between the
//     resume CAS (WAITING → QUEUED) and the enqueue — flipped back to WAITING
//     so the resume loop re-enqueues the approved Human-in-Loop run;
//   - without one it is an unowned row awaiting adoption (`workflow run
//     --no-wait` persists one and exits). It is left QUEUED — failing it here
//     would destroy the run before any daemon ever saw it.
func (s *SQLiteWorkflowStore) RecoverStaleExecutions(ctx context.Context) error {
	// Only fail executions whose owning process is no longer alive. A concurrent
	// engine (e.g. the `daemon` process) may legitimately have RUNNING executions;
	// blindly failing all RUNNING/QUEUED rows corrupts its in-flight state. QUEUED
	// rows carry no live worker (pid is only set once RUNNING), so a null/zero/dead
	// pid means the row is genuinely stale and safe to recover.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, status, COALESCE(pid, 0), COALESCE(resume_state, '') FROM workflow_executions WHERE status IN ('RUNNING', 'QUEUED')`)
	if err != nil {
		return fmt.Errorf("recovering stale executions: %w", err)
	}
	defer rows.Close()

	self := os.Getpid()
	var staleIDs []string
	var resumableIDs []string
	for rows.Next() {
		var id, status, resumeState string
		var pid int
		if err := rows.Scan(&id, &status, &pid, &resumeState); err != nil {
			return fmt.Errorf("recovering stale executions: scan: %w", err)
		}
		if pid == self {
			continue // never fail our own in-flight rows
		}
		if !processAlive(pid) {
			if status == "QUEUED" && pid == 0 {
				if resumeState != "" {
					// Crashed between resume-CAS and Enqueue: flip back to
					// WAITING so the resume loop re-enqueues the approved run.
					resumableIDs = append(resumableIDs, id)
				}
				// else: unowned --no-wait row — an adoption candidate, not
				// stale. Leave it QUEUED for a live engine to claim.
				continue
			}
			staleIDs = append(staleIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("recovering stale executions: %w", err)
	}

	for _, id := range resumableIDs {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE workflow_executions
			SET status = 'WAITING'
			WHERE id = ? AND status = 'QUEUED'`, id); err != nil {
			return fmt.Errorf("recovering resumable execution %s: %w", id, err)
		}
	}

	for _, id := range staleIDs {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE workflow_executions
			SET status = 'FAILED',
			    error_message = 'recovered: process restart',
			    finished_at = CURRENT_TIMESTAMP,
			    resume_state = ''
			WHERE id = ? AND status IN ('RUNNING', 'QUEUED')`, id); err != nil {
			return fmt.Errorf("recovering stale execution %s: %w", id, err)
		}
	}
	return nil
}

// ReapStaleRunningExecutions fails executions that have been RUNNING longer
// than olderThan and whose owning process is dead (or is this process, which
// cannot legitimately have a run that old). A crashed daemon can leave such
// rows behind when RecoverStaleExecutions missed them (e.g. the pid was
// recycled); this periodic sweep keeps them from masquerading as active runs.
func (s *SQLiteWorkflowStore) ReapStaleRunningExecutions(ctx context.Context, olderThan time.Time) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(pid, 0) FROM workflow_executions
		 WHERE status = 'RUNNING' AND started_at IS NOT NULL AND started_at < ?`,
		olderThan,
	)
	if err != nil {
		return fmt.Errorf("reaping stale running executions: %w", err)
	}
	defer rows.Close()

	self := os.Getpid()
	var ids []string
	for rows.Next() {
		var id string
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			return fmt.Errorf("reaping stale running executions: scan: %w", err)
		}
		if pid <= 0 {
			continue // unknown owner — can't prove it's dead, stay conservative
		}
		if pid == self || !processAlive(pid) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reaping stale running executions: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, olderThan)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `UPDATE workflow_executions
		SET status = 'FAILED',
		    error_message = 'stale: running for over 24h',
		    finished_at = CURRENT_TIMESTAMP,
		    resume_state = ''
		WHERE status = 'RUNNING' AND started_at < ? AND id IN (` +
		strings.Join(placeholders, ",") + `)`
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("reaping stale running executions: %w", err)
	}
	return nil
}

// CancelQueuedExecution marks a not-yet-dispatched (QUEUED) execution CANCELLED
// so it will not run when a worker later picks it up. Returns true if a queued
// row was actually transitioned. Running executions are unaffected (they are
// stopped via context cancellation instead).
func (s *SQLiteWorkflowStore) CancelQueuedExecution(ctx context.Context, id string) (bool, error) {
	// Covers QUEUED (not yet dispatched) and WAITING (paused at a HIL node with
	// no live goroutine) — both are cancellable purely via a status flip.
	// Clearing resume_state ensures a cancelled paused run can never be resumed.
	res, err := s.db.ExecContext(ctx,
		`UPDATE workflow_executions SET status = 'CANCELLED', finished_at = CURRENT_TIMESTAMP, resume_state = '' WHERE id = ? AND status IN ('QUEUED','WAITING')`, id)
	if err != nil {
		return false, fmt.Errorf("cancelling queued execution %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResumeWaitingExecution atomically flips a WAITING execution to QUEUED, so that
// among concurrent engines sharing the DB exactly one wins the resume (the
// others get false). Returns whether this caller flipped it.
func (s *SQLiteWorkflowStore) ResumeWaitingExecution(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE workflow_executions SET status = 'QUEUED' WHERE id = ? AND status = 'WAITING'`, id)
	if err != nil {
		return false, fmt.Errorf("resuming waiting execution %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PruneExecutions deletes the oldest executions for a workflow when the total
// count exceeds keepCount. Executions are ordered by created_at; the oldest
// are removed first.
//
// WAITING (Human-in-Loop paused) executions are never prune candidates —
// deleting one would destroy a paused run that is still awaiting approval.
// In the same pass, hil_pending rows are cleaned up: rows whose execution no
// longer exists (orphans — the table has no FK), and rows belonging to the
// executions being pruned.
func (s *SQLiteWorkflowStore) PruneExecutions(ctx context.Context, workflowID string, keepCount int) error {
	if keepCount < 0 {
		keepCount = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pruning executions for workflow %s: %w", workflowID, err)
	}

	// hil_pending rows referencing no existing execution (orphans).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM hil_pending WHERE execution_id NOT IN (SELECT id FROM workflow_executions)`); err != nil {
		tx.Rollback()
		return fmt.Errorf("pruning executions for workflow %s: %w", workflowID, err)
	}

	candidates := `
		SELECT id FROM workflow_executions
		WHERE workflow_id = ?
		  AND status != 'WAITING'
		  AND id NOT IN (
		      SELECT id FROM workflow_executions
		      WHERE workflow_id = ?
		      ORDER BY created_at DESC
		      LIMIT ?
		  )`

	// hil_pending rows of executions about to be pruned.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM hil_pending WHERE execution_id IN (`+candidates+`)`,
		workflowID, workflowID, keepCount); err != nil {
		tx.Rollback()
		return fmt.Errorf("pruning executions for workflow %s: %w", workflowID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM workflow_executions WHERE id IN (`+candidates+`)`,
		workflowID, workflowID, keepCount); err != nil {
		tx.Rollback()
		return fmt.Errorf("pruning executions for workflow %s: %w", workflowID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pruning executions for workflow %s: %w", workflowID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// boolToInt converts a Go bool to a SQLite-friendly integer (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// marshalItems encodes a slice of Items to a JSON string.
// Returns "[]" for a nil or empty slice.
func marshalItems(items []Item) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
