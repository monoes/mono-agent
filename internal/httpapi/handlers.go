package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/workflow"
)

// ── workflows ─────────────────────────────────────────────────────────────

// GET /workflows
func (s *Server) handleWorkflowList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wfs, err := s.rt.store.ListWorkflows(ctx, s.rt.profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workflows: "+err.Error())
		return
	}

	nodeCounts := map[string]int{}
	rows, err := s.rt.db.DB.QueryContext(ctx, `SELECT workflow_id, COUNT(*) FROM workflow_nodes GROUP BY workflow_id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count workflow nodes: "+err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			writeError(w, http.StatusInternalServerError, "scan node count: "+err.Error())
			return
		}
		nodeCounts[id] = n
	}

	out := []map[string]interface{}{}
	for _, wf := range wfs {
		nodeCount := len(wf.Nodes)
		if nodeCount == 0 {
			nodeCount = nodeCounts[wf.ID]
		}
		out = append(out, map[string]interface{}{
			"id":         wf.ID,
			"name":       wf.Name,
			"active":     wf.IsActive,
			"node_count": nodeCount,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /workflows/{id}
func (s *Server) handleWorkflowGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.rt.store.GetWorkflow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get workflow: "+err.Error())
		return
	}
	if wf == nil || (wf.ProfileID != "" && wf.ProfileID != s.rt.profileID) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("workflow %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

// GET /workflows/{id}/executions
func (s *Server) handleWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	wf, err := s.rt.store.GetWorkflow(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get workflow: "+err.Error())
		return
	}
	if wf == nil || (wf.ProfileID != "" && wf.ProfileID != s.rt.profileID) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("workflow %q not found", id))
		return
	}

	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	execs, err := s.rt.store.ListExecutions(ctx, id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list executions: "+err.Error())
		return
	}
	for i := range execs {
		execs[i].Nodes = nil // list view: summary only, no per-node output items
	}
	writeJSON(w, http.StatusOK, execs)
}

// parseWorkflowBytes accepts both the file-store workflow JSON format
// (type/source/target keys) and the legacy DB export format, mirroring
// `monoagentcli workflow import` and internal/mcp's workflow_validate tool.
func parseWorkflowBytes(raw []byte) (workflow.Workflow, error) {
	wf, err := workflow.ParseWorkflowFileBytes(raw)
	if err == nil {
		return wf, nil
	}
	var legacy workflow.Workflow
	if err2 := json.Unmarshal(raw, &legacy); err2 != nil {
		return wf, err
	}
	return legacy, nil
}

// POST /workflows/{id}/validate
//
// Validates the saved workflow named by {id}. A body is optional: when
// present and non-empty, it is treated as an inline workflow JSON document
// to validate INSTEAD of the saved one (id is still required by the route
// but only used to shape the URL — this mirrors the MCP workflow_validate
// tool's id/workflow modes without a second route).
func (s *Server) handleWorkflowValidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	var wf workflow.Workflow
	body, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		wf, err = parseWorkflowBytes(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "request body is not a valid workflow: "+err.Error())
			return
		}
	} else {
		loaded, err := s.rt.store.GetWorkflow(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get workflow: "+err.Error())
			return
		}
		if loaded == nil || (loaded.ProfileID != "" && loaded.ProfileID != s.rt.profileID) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("workflow %q not found", id))
			return
		}
		wf = *loaded
	}

	errs := []string{}
	if err := workflow.ValidateForSave(&wf); err != nil {
		errs = append(errs, err.Error())
	} else if err := workflow.ValidateForActivation(&wf); err != nil {
		errs = append(errs, err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":  len(errs) == 0,
		"errors": errs,
	})
}

// POST /workflows/{id}/run  (mutating)
func (s *Server) handleWorkflowRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	var body struct {
		Input          map[string]interface{} `json:"input"`
		TimeoutSeconds float64                `json:"timeout_seconds"`
	}
	raw, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}

	if err := s.rt.ensureEngine(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	execID, err := s.rt.engine.TriggerWorkflow(ctx, id, body.Input)
	if err != nil {
		writeErrorFor(w, err)
		return
	}

	timeout := clampTimeoutSeconds(body.TimeoutSeconds)
	exec := waitForExecution(ctx, s.rt, execID, timeout)

	nodeTypes := map[string]string{}
	if wf, err := s.rt.store.GetWorkflow(ctx, exec.WorkflowID); err == nil && wf != nil {
		for _, n := range wf.Nodes {
			nodeTypes[n.ID] = n.Type
		}
	}

	nodesOut := []map[string]interface{}{}
	for _, en := range exec.Nodes {
		items := redactItemsUnlessFull(r, en.OutputItems)
		if items == nil {
			items = []workflow.Item{}
		}
		row := map[string]interface{}{
			"node_id":      en.NodeID,
			"type":         nodeTypes[en.NodeID],
			"status":       en.Status,
			"output_items": items,
		}
		if en.ErrorMessage != "" {
			row["error"] = en.ErrorMessage
		}
		nodesOut = append(nodesOut, row)
	}
	result := map[string]interface{}{
		"execution_id": exec.ID,
		"status":       exec.Status,
		"nodes":        nodesOut,
	}
	if exec.ErrorMessage != "" {
		result["error"] = exec.ErrorMessage
	}
	if exec.Status == "RUNNING" || exec.Status == "QUEUED" {
		result["timed_out"] = true
	}
	writeJSON(w, http.StatusOK, result)
}

// waitForExecution polls the execution's status until it reaches a
// terminal or paused status, the timeout elapses, or ctx is cancelled.
// Mirrors internal/mcp/tools.go's waitForExecution.
func waitForExecution(ctx context.Context, rt *runtime, executionID string, timeout time.Duration) *workflow.WorkflowExecution {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	load := func() *workflow.WorkflowExecution {
		exec, err := rt.store.GetExecution(context.Background(), executionID)
		if err != nil || exec == nil {
			return &workflow.WorkflowExecution{ID: executionID, Status: "UNKNOWN"}
		}
		return exec
	}
	for {
		select {
		case <-ticker.C:
			status, err := rt.store.GetExecutionStatus(ctx, executionID)
			if err != nil || status == "" {
				continue
			}
			switch status {
			case "QUEUED", "RUNNING":
				if time.Now().After(deadline) {
					return load()
				}
			default: // SUCCESS, FAILED, CANCELLED, WAITING (HIL pause)
				return load()
			}
		case <-ctx.Done():
			return load()
		}
	}
}

// clampTimeoutSeconds maps the run timeout_seconds body field to a
// duration clamped to [1s, 600s]; zero/negative falls back to 120s.
func clampTimeoutSeconds(secs float64) time.Duration {
	switch {
	case secs <= 0:
		return 120 * time.Second
	case secs < 1:
		return 1 * time.Second
	case secs > 600:
		return 600 * time.Second
	default:
		return time.Duration(secs * float64(time.Second))
	}
}

// POST /workflows/{id}/activate  (mutating)
func (s *Server) handleWorkflowActivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	if err := s.rt.ensureEngine(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.rt.engine.ActivateWorkflow(ctx, id); err != nil {
		writeErrorFor(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     id,
		"status": "activated",
		"note":   "triggers are only served for as long as this server process stays running (or `monoagentcli daemon` if that's separately running) — see AGENTS.md",
	})
}

// POST /workflows/{id}/deactivate  (mutating)
func (s *Server) handleWorkflowDeactivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	if err := s.rt.ensureEngine(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.rt.engine.DeactivateWorkflow(ctx, id); err != nil {
		writeErrorFor(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "status": "deactivated"})
}

// ── nodes ─────────────────────────────────────────────────────────────────

// nodeCategory mirrors internal/mcp/tools.go's categorization (itself
// mirroring the CLI's node list).
func nodeCategory(t string) string {
	legacy := map[string]string{
		"mysql": "database", "postgres": "database", "mongodb": "database", "redis": "database",
		"github": "service", "notion": "service", "airtable": "service", "jira": "service",
		"linear": "service", "asana": "service", "stripe": "service", "shopify": "service",
		"salesforce": "service", "hubspot": "service", "google_sheets": "service",
		"gmail": "service", "google_drive": "service",
		"datetime": "data", "crypto": "data", "html": "data", "xml": "data",
		"markdown": "data", "spreadsheet": "data", "compression": "data", "write_binary_file": "data",
	}
	switch {
	case strings.HasPrefix(t, "trigger."):
		return "trigger"
	case strings.HasPrefix(t, "core."), strings.HasPrefix(t, "control."):
		return "control"
	case strings.HasPrefix(t, "http."):
		return "http"
	case strings.HasPrefix(t, "system."):
		return "system"
	case strings.HasPrefix(t, "comm."):
		return "communication"
	case strings.HasPrefix(t, "ai."), strings.HasPrefix(t, "agent."):
		return "ai"
	case strings.HasPrefix(t, "instagram."), strings.HasPrefix(t, "linkedin."),
		strings.HasPrefix(t, "x."), strings.HasPrefix(t, "tiktok."),
		strings.HasPrefix(t, "hackernews."), strings.HasPrefix(t, "producthunt."),
		strings.HasPrefix(t, "gemini."):
		return "browser/social"
	case strings.HasPrefix(t, "people."):
		return "people"
	case strings.HasPrefix(t, "db."), strings.HasPrefix(t, "mysql."), strings.HasPrefix(t, "postgres."):
		return "database"
	case strings.HasPrefix(t, "service."):
		return "service"
	case strings.HasPrefix(t, "data."):
		return "data"
	case legacy[t] != "":
		return legacy[t]
	default:
		return "other"
	}
}

func nodeTitle(t string) string {
	name := t
	if i := strings.Index(t, "."); i >= 0 {
		name = t[i+1:]
	}
	words := strings.Split(name, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// GET /nodes
func (s *Server) handleNodeList(w http.ResponseWriter, r *http.Request) {
	types := s.rt.registryOrDefault().Types()
	sort.Strings(types)
	out := []map[string]interface{}{}
	for _, t := range types {
		out = append(out, map[string]interface{}{
			"type":     t,
			"category": nodeCategory(t),
			"title":    nodeTitle(t),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /nodes/{type}/schema
func (s *Server) handleNodeSchema(w http.ResponseWriter, r *http.Request) {
	nodeType := r.PathValue("type")
	registry := s.rt.registryOrDefault()
	hasSchema := false
	for _, name := range workflow.ListEmbeddedSchemas() {
		if name == nodeType {
			hasSchema = true
			break
		}
	}
	if !registry.Has(nodeType) && !hasSchema {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown node type %q — GET /nodes for all types", nodeType))
		return
	}
	schema, err := workflow.LoadDefaultSchema(nodeType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load schema: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"type": nodeType, "schema": schema})
}

// ── HIL ───────────────────────────────────────────────────────────────────

type hilItem struct {
	ID           string                 `json:"id"`
	ExecutionID  string                 `json:"execution_id"`
	WorkflowID   string                 `json:"workflow_id"`
	WorkflowName string                 `json:"workflow_name"`
	NodeID       string                 `json:"node_id"`
	NodeName     string                 `json:"node_name"`
	Status       string                 `json:"status"`
	ReadonlyData map[string]interface{} `json:"readonly_data"`
	EditableData map[string]interface{} `json:"editable_data"`
	CreatedAt    string                 `json:"created_at"`
}

// GET /hil
func (s *Server) handleHilList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rt.db.DB.QueryContext(r.Context(),
		`SELECT h.id, h.execution_id, h.workflow_id, h.node_id, h.node_name, h.status,
		        h.readonly_data, h.editable_data, h.created_at, COALESCE(w.name,'')
		 FROM hil_pending h
		 LEFT JOIN workflows w ON w.id = h.workflow_id
		 WHERE h.status = 'pending' AND h.profile_id = ?
		 ORDER BY h.created_at ASC`,
		s.rt.profileID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query HIL items: "+err.Error())
		return
	}
	defer rows.Close()

	items := []hilItem{}
	for rows.Next() {
		var it hilItem
		var roRaw, edRaw string
		if err := rows.Scan(&it.ID, &it.ExecutionID, &it.WorkflowID, &it.NodeID, &it.NodeName,
			&it.Status, &roRaw, &edRaw, &it.CreatedAt, &it.WorkflowName); err != nil {
			writeError(w, http.StatusInternalServerError, "scan HIL item: "+err.Error())
			return
		}
		_ = json.Unmarshal([]byte(roRaw), &it.ReadonlyData)
		_ = json.Unmarshal([]byte(edRaw), &it.EditableData)
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, items)
}

// POST /hil/{id}/approve  (mutating)
func (s *Server) handleHilApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Data map[string]interface{} `json:"data"`
	}
	raw, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if body.Data == nil {
		body.Data = map[string]interface{}{}
	}
	edited, err := json.Marshal(body.Data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "render data: "+err.Error())
		return
	}
	res, err := s.rt.db.DB.ExecContext(r.Context(),
		`UPDATE hil_pending SET status='approved', edited_data=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND status='pending' AND profile_id = ?`,
		string(edited), id, s.rt.profileID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "approve HIL item: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf("HIL item %q not found or already resolved", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "approved"})
}

// POST /hil/{id}/reject  (mutating)
func (s *Server) handleHilReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.rt.db.DB.ExecContext(r.Context(),
		`UPDATE hil_pending SET status='rejected', updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND status='pending' AND profile_id = ?`,
		id, s.rt.profileID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reject HIL item: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf("HIL item %q not found or already resolved", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "rejected"})
}

// ── body helpers ─────────────────────────────────────────────────────────

// maxBodyBytes bounds request bodies (validate/run/hil-approve payloads)
// to keep one request from exhausting memory.
const maxBodyBytes = 4 * 1024 * 1024

func readLimitedBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", maxBodyBytes)
	}
	return body, nil
}
