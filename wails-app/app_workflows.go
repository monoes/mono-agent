package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/workflow"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Workflow types
// ─────────────────────────────────────────────────────────────────────────────

type WorkflowSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	// NodeCount is how many nodes the workflow has. The list query leaves
	// Nodes unpopulated on purpose, so the UI had nothing to count and
	// showed "0 nodes" for every workflow.
	NodeCount int `json:"node_count"`
}

type WorkflowNodeData struct {
	ID        string                 `json:"id"`
	NodeType  string                 `json:"node_type"`
	Name      string                 `json:"name"`
	Config    map[string]interface{} `json:"config"`
	PositionX float64                `json:"position_x"`
	PositionY float64                `json:"position_y"`
	Disabled  bool                   `json:"disabled"`
	Schema    *workflow.NodeSchema   `json:"schema,omitempty"`
}

type WorkflowConnectionData struct {
	ID           string `json:"id"`
	SourceNodeID string `json:"source_node_id"`
	SourceHandle string `json:"source_handle"`
	TargetNodeID string `json:"target_node_id"`
	TargetHandle string `json:"target_handle"`
	Position     int    `json:"position"`
}

type WorkflowDetail struct {
	WorkflowSummary
	Nodes       []WorkflowNodeData       `json:"nodes"`
	Connections []WorkflowConnectionData `json:"connections"`
}

type SaveWorkflowRequest struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	IsActive    bool                     `json:"is_active"`
	Nodes       []WorkflowNodeData       `json:"nodes"`
	Connections []WorkflowConnectionData `json:"connections"`
}

type WorkflowExecutionSummary struct {
	ID           string `json:"id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	Status       string `json:"status"`
	TriggerType  string `json:"trigger_type"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Error        string `json:"error"`
	CreatedAt    string `json:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow CRUD
// ─────────────────────────────────────────────────────────────────────────────

// workflowToDetail converts `workflow get`'s document into the editor's shape.
func workflowToDetail(wf *workflow.Workflow) *WorkflowDetail {
	detail := &WorkflowDetail{
		WorkflowSummary: workflowSummaryOf(wf),
		Nodes:           []WorkflowNodeData{},
		Connections:     []WorkflowConnectionData{},
	}
	for _, n := range wf.Nodes {
		detail.Nodes = append(detail.Nodes, WorkflowNodeData{
			ID:        n.ID,
			NodeType:  n.Type,
			Name:      n.Name,
			Config:    n.Config,
			PositionX: n.PositionX,
			PositionY: n.PositionY,
			Disabled:  n.Disabled,
			Schema:    n.Schema,
		})
	}
	for _, c := range wf.Connections {
		detail.Connections = append(detail.Connections, WorkflowConnectionData{
			ID:           c.ID,
			SourceNodeID: c.SourceNodeID,
			SourceHandle: c.SourceHandle,
			TargetNodeID: c.TargetNodeID,
			TargetHandle: c.TargetHandle,
			Position:     c.Position,
		})
	}
	return detail
}

// ListWorkflowTemplates returns metadata for all bundled, ready-to-use
// workflow templates (e.g. "Outlook Email Sync") shipped with the app.
func (a *App) ListWorkflowTemplates() []workflow.Template {
	return workflow.ListTemplates()
}

// CreateWorkflowFromTemplate instantiates a bundled template as a new,
// editable workflow for the active profile. Node IDs from the template are
// remapped to fresh UUIDs so multiple instantiations never collide, then
// saved via the same path as a normal SaveWorkflow call.
func (a *App) CreateWorkflowFromTemplate(templateID string) (*WorkflowSummary, error) {
	tmpl, ok := workflow.GetTemplate(templateID)
	if !ok {
		return nil, fmt.Errorf("unknown template %q", templateID)
	}

	idMap := make(map[string]string, len(tmpl.Nodes))
	for _, n := range tmpl.Nodes {
		idMap[n.ID] = uuid.New().String()
	}

	req := SaveWorkflowRequest{Name: tmpl.Name, Description: tmpl.Description, IsActive: false}
	for _, n := range tmpl.Nodes {
		config := n.Config
		if config == nil {
			config = map[string]interface{}{}
		}
		// people.sync_outlook_message scopes synced people/messages by its
		// own "profile_id" config field, independent of the workflow's
		// profile — default it to the active profile so the template works
		// correctly out of the box.
		if n.Type == "people.sync_outlook_message" {
			config["profile_id"] = a.getActiveProfileID()
		}
		req.Nodes = append(req.Nodes, WorkflowNodeData{
			ID:        idMap[n.ID],
			NodeType:  n.Type,
			Name:      n.Name,
			Config:    config,
			PositionX: n.Position.X,
			PositionY: n.Position.Y,
			Disabled:  n.Disabled,
		})
	}
	for _, c := range tmpl.Connections {
		req.Connections = append(req.Connections, WorkflowConnectionData{
			ID:           uuid.New().String(),
			SourceNodeID: idMap[c.Source],
			SourceHandle: c.SourceHandle,
			TargetNodeID: idMap[c.Target],
			TargetHandle: c.TargetHandle,
		})
	}

	return a.SaveWorkflow(req)
}

// ListWorkflows returns `monoagentcli workflow list`: the active profile's
// workflows from the file store and SQLite, with node counts, newest first.
func (a *App) ListWorkflows() ([]WorkflowSummary, error) {
	var rows []struct {
		workflow.Workflow
		NodeCount int `json:"node_count"`
	}
	if err := a.runMonoCLI("", &rows, "workflow", "list"); err != nil {
		return nil, err
	}
	summaries := make([]WorkflowSummary, 0, len(rows))
	for _, r := range rows {
		s := workflowSummaryOf(&r.Workflow)
		s.NodeCount = r.NodeCount
		summaries = append(summaries, s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})
	return summaries, nil
}

// GetWorkflow returns `workflow get <id>`; another profile's workflow is not
// found.
func (a *App) GetWorkflow(id string) (*WorkflowDetail, error) {
	var wf workflow.Workflow
	if err := a.runMonoCLI("", &wf, "workflow", "get", id); err != nil {
		return nil, err
	}
	return workflowToDetail(&wf), nil
}

// SaveWorkflow hands the editor's document to `workflow save` on stdin. Its
// JSON is the shape `workflow get` prints, so the request goes as it is. An
// existing workflow keeps its activation (that is SetWorkflowActive's job):
// the editor never sends is_active.
func (a *App) SaveWorkflow(req SaveWorkflowRequest) (*WorkflowSummary, error) {
	doc, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var wf workflow.Workflow
	if err := a.runMonoCLI(string(doc), &wf, "workflow", "save"); err != nil {
		return nil, err
	}
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Saved workflow: %s [%s]", wf.Name, wf.ID))
	s := workflowSummaryOf(&wf)
	return &s, nil
}

// DeleteWorkflow runs `workflow delete <id> --yes`: no prompt, but a
// workflow that orgs use is refused with the CLI's explanation.
func (a *App) DeleteWorkflow(id string) error {
	if err := a.runMonoCLI("", nil, "workflow", "delete", id, "--yes"); err != nil {
		return err
	}
	a.emitLog("WORKFLOW", "WARN", "Deleted workflow: "+id)
	return nil
}

// SetWorkflowActive runs `workflow activate` or `workflow deactivate`.
func (a *App) SetWorkflowActive(id string, active bool) error {
	verb := "deactivate"
	if active {
		verb = "activate"
	}
	return a.runMonoCLI("", nil, "workflow", verb, id)
}

// workflowSummaryOf is the list/save row for wf, timestamps in RFC 3339.
func workflowSummaryOf(wf *workflow.Workflow) WorkflowSummary {
	return WorkflowSummary{
		ID:          wf.ID,
		Name:        wf.Name,
		Description: wf.Description,
		IsActive:    wf.IsActive,
		Version:     wf.Version,
		CreatedAt:   wf.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   wf.UpdatedAt.Format(time.RFC3339),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow import/export
// ─────────────────────────────────────────────────────────────────────────────

// WorkflowImportResult is the {id, name} pair `workflow import --json` emits.
type WorkflowImportResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Status is the CLI's import outcome: "created", "updated" (the same
	// workflow was imported before and changed) or "unchanged".
	Status string `json:"status,omitempty"`
}

// ExportWorkflow returns `monoagentcli workflow export <id>` verbatim: the
// documented WorkflowFile JSON that `workflow import` reads back.
func (a *App) ExportWorkflow(workflowID string) (string, error) {
	var file json.RawMessage
	if err := a.runMonoCLI("", &file, "workflow", "export", workflowID); err != nil {
		return "", err
	}
	var head struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(file, &head)
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Exported workflow: %s [%s]", head.Name, head.ID))
	return string(file), nil
}

// ImportWorkflow imports a workflow from raw WorkflowFile JSON or a path to
// a JSON file, by feeding it to `monoagentcli workflow import` on stdin (the
// CLI import reads stdin when --file is absent) — the same path a CLI import
// takes, including legacy-format normalization and node-id collision
// remapping. Returns the imported workflow's {id, name}.
func (a *App) ImportWorkflow(jsonOrPath string) (*WorkflowImportResult, error) {
	jsonOrPath = strings.TrimSpace(jsonOrPath)
	if jsonOrPath == "" {
		return nil, fmt.Errorf("import input is empty — pass workflow JSON or a path to a JSON file")
	}
	raw := []byte(jsonOrPath)
	if fileExists(jsonOrPath) {
		data, err := os.ReadFile(jsonOrPath)
		if err != nil {
			return nil, fmt.Errorf("read workflow file: %w", err)
		}
		raw = data
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("import input is neither an existing file nor valid workflow JSON")
	}

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return nil, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, cliBin, "--profile", a.getActiveProfileID(), "--json", "workflow", "import")
	hideWindow(cmd)
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("workflow import failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("workflow import failed: %w", err)
	}
	var res WorkflowImportResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("unexpected workflow import output: %w", err)
	}
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Imported workflow (%s): %s [%s]", res.Status, res.Name, res.ID))
	return &res, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow execution (subprocess)
// ─────────────────────────────────────────────────────────────────────────────

// GetWorkflowTriggerInputs returns `monoagentcli workflow inputs <id> --json`
// verbatim: which trigger fields the workflow reads, and a skeleton payload
// filled from the reading nodes' schema examples.
//
// The detection lives in the CLI, not here — the editor only renders what it
// is told, so the same answer is available to a script, a test, and the
// dialog (see internal/workflow/trigger_inputs.go).
func (a *App) GetWorkflowTriggerInputs(id string) string {
	if strings.TrimSpace(id) == "" {
		return aiError(fmt.Errorf("workflow id required"))
	}
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin,
		"--profile", a.getActiveProfileID(), "--json", "workflow", "inputs", id)
	hideWindow(cmd)
	out, runErr := cmd.Output()
	return cliResultJSON(cliBin, out, runErr)
}

// RunWorkflow spawns `monoagentcli workflow run <id>` as a subprocess with no
// trigger data. Kept as its own binding so existing callers (the Dashboard's
// run button) are unchanged.
func (a *App) RunWorkflow(id string) error {
	return a.RunWorkflowWithInput(id, "")
}

// RunWorkflowWithInput is RunWorkflow with trigger data — the GUI equivalent
// of `workflow run <id> --input '{"prompts":[…]}'`.
//
// Without it a manual-trigger workflow whose nodes read {{ $json.<field> }}
// was simply unrunnable from the GUI: the run button sent nothing, every such
// expression resolved to nothing, and the run did nothing. inputJSON must be
// a JSON object; empty means no trigger data.
func (a *App) RunWorkflowWithInput(id, inputJSON string) error {
	trimmed := strings.TrimSpace(inputJSON)
	if trimmed != "" {
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
			return fmt.Errorf("trigger input must be a JSON object: %w", err)
		}
	}
	return a.runWorkflowProcess(id, trimmed)
}

func (a *App) runWorkflowProcess(id, inputJSON string) error {
	// `workflow get` is profile-scoped, so another profile's id is not found.
	wf, err := a.GetWorkflow(id)
	if err != nil {
		return err
	}
	// The engine rejects inactive workflows — surface that here instead of
	// silently flipping is_active behind the user's back.
	if !wf.IsActive {
		return fmt.Errorf("workflow %q is inactive — activate it first", wf.Name)
	}
	// Refuse a second concurrent run of the same workflow: runningCmds is
	// keyed by workflow ID, so starting another would orphan the first
	// subprocess (unkillable via CancelWorkflow).
	a.runningMu.Lock()
	if _, running := a.runningCmds[id]; running {
		a.runningMu.Unlock()
		return fmt.Errorf("workflow %s is already running", id)
	}
	a.runningMu.Unlock()

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return err
	}

	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Starting workflow %s", id))

	runArgs := []string{"--profile", a.getActiveProfileID(), "workflow", "run", id}
	if inputJSON != "" {
		runArgs = append(runArgs, "--input", inputJSON)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, cliBin, runArgs...)
	hideWindow(cmd)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	// Reserve the registry slot before Start so a concurrent RunWorkflow
	// call loses the race cleanly; released on Start failure or process exit.
	a.runningMu.Lock()
	a.runningCmds[id] = cmd
	a.runningMu.Unlock()

	if err := cmd.Start(); err != nil {
		a.runningMu.Lock()
		delete(a.runningCmds, id)
		a.runningMu.Unlock()
		return fmt.Errorf("failed to start workflow: %w", err)
	}
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Workflow %s started (pid %d)", id, cmd.Process.Pid))

	var execID string // set once the CLI names the execution
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			a.emitLog("WORKFLOW", "INFO", line)
			// Detect execution ID from CLI output and notify the frontend
			if strings.HasPrefix(line, "Execution started: ") {
				started := strings.TrimSpace(strings.TrimPrefix(line, "Execution started: "))
				// Also track the process by execution id, so CancelWorkflow
				// can kill it without looking the execution up.
				a.runningMu.Lock()
				if a.runningCmds[id] == cmd {
					execID = started
					a.runningCmds[execID] = cmd
				}
				a.runningMu.Unlock()
				a.emitWorkflowEvent("workflow:exec-started", map[string]interface{}{
					"workflow_id":  id,
					"execution_id": started,
				})
			}
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			a.emitLog("WORKFLOW", "INFO", scanner.Text())
		}
	}()
	go func() {
		waitErr := cmd.Wait()
		a.runningMu.Lock()
		for key, c := range a.runningCmds {
			if c == cmd {
				delete(a.runningCmds, key)
			}
		}
		a.runningMu.Unlock()
		if waitErr != nil {
			a.emitLog("WORKFLOW", "ERROR", fmt.Sprintf("Workflow %s failed: %v", id, waitErr))
			a.emitWorkflowEvent("workflow:complete", map[string]interface{}{"workflow_id": id, "success": false})
		} else {
			a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Workflow %s completed", id))
			a.emitWorkflowEvent("workflow:complete", map[string]interface{}{"workflow_id": id, "success": true})
		}
	}()
	return nil
}

// emitWorkflowEvent emits to the frontend once the Wails runtime is up
// (emitLog's guard): tests drive the run bindings without one.
func (a *App) emitWorkflowEvent(name string, data map[string]interface{}) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data)
	}
}

// workflowPollCLITimeout bounds the execution bindings the editor polls
// while a run is in progress.
const workflowPollCLITimeout = 20 * time.Second

// GetWorkflowExecutions returns `workflow executions <id> --limit <n>`,
// newest first (50 when limit is not positive).
func (a *App) GetWorkflowExecutions(workflowID string, limit int) ([]WorkflowExecutionSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []workflow.WorkflowExecution
	if err := a.cliJSON(workflowPollCLITimeout, &rows, "workflow", "executions", workflowID, "--limit", strconv.Itoa(limit)); err != nil {
		return nil, err
	}
	stamp := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format(time.RFC3339Nano)
	}
	execs := make([]WorkflowExecutionSummary, 0, len(rows))
	for _, e := range rows {
		execs = append(execs, WorkflowExecutionSummary{
			ID:          e.ID,
			WorkflowID:  e.WorkflowID,
			Status:      e.Status,
			TriggerType: e.TriggerType,
			StartedAt:   stamp(e.StartedAt),
			FinishedAt:  stamp(e.FinishedAt),
			Error:       e.ErrorMessage,
			CreatedAt:   stamp(&e.CreatedAt),
		})
	}
	return execs, nil
}

// GetExecutionDetail returns `workflow execution <id>`: the execution with
// per-node status, items already redacted by the CLI. The editor reads each
// node's input_items/output_items as a JSON string, as it always has.
func (a *App) GetExecutionDetail(executionID string) (map[string]interface{}, error) {
	var d struct {
		ID          string `json:"id"`
		WorkflowID  string `json:"workflow_id"`
		Status      string `json:"status"`
		TriggerType string `json:"trigger_type"`
		StartedAt   string `json:"started_at"`
		FinishedAt  string `json:"finished_at"`
		Error       string `json:"error_message"`
		CreatedAt   string `json:"created_at"`
		Nodes       []struct {
			ID           string          `json:"id"`
			NodeID       string          `json:"node_id"`
			NodeName     string          `json:"node_name"`
			Status       string          `json:"status"`
			ErrorMessage string          `json:"error_message"`
			StartedAt    string          `json:"started_at"`
			FinishedAt   string          `json:"finished_at"`
			InputItems   json.RawMessage `json:"input_items"`
			OutputItems  json.RawMessage `json:"output_items"`
			RetryCount   int             `json:"retry_count"`
		} `json:"nodes"`
	}
	if err := a.cliJSON(workflowPollCLITimeout, &d, "workflow", "execution", executionID); err != nil {
		return nil, err
	}
	nodesList := make([]map[string]interface{}, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		nodesList = append(nodesList, map[string]interface{}{
			"id":            n.ID,
			"node_id":       n.NodeID,
			"node_name":     n.NodeName,
			"status":        n.Status,
			"error_message": n.ErrorMessage,
			"started_at":    n.StartedAt,
			"finished_at":   n.FinishedAt,
			"input_items":   itemsString(n.InputItems),
			"output_items":  itemsString(n.OutputItems),
			"retry_count":   n.RetryCount,
		})
	}
	return map[string]interface{}{
		"id":           d.ID,
		"workflow_id":  d.WorkflowID,
		"status":       d.Status,
		"trigger_type": d.TriggerType,
		"started_at":   d.StartedAt,
		"finished_at":  d.FinishedAt,
		"error":        d.Error,
		"created_at":   d.CreatedAt,
		"nodes":        nodesList,
	}, nil
}

// itemsString turns the CLI's item array back into the compact JSON string
// the editor parses; a value the CLI kept as a string comes back unchanged.
func itemsString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

// CancelWorkflow cancels a running workflow execution. A process this app
// started for it (RunWorkflow) is the app's to kill; the rest — stopping a
// process started elsewhere, which must never be the daemon, and marking
// the execution cancelled — is `workflow cancel <id>`.
func (a *App) CancelWorkflow(executionID string) error {
	a.runningMu.Lock()
	if cmd, ok := a.runningCmds[executionID]; ok {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		for key, c := range a.runningCmds {
			if c == cmd {
				delete(a.runningCmds, key)
			}
		}
	}
	a.runningMu.Unlock()

	if err := a.runMonoCLI("", nil, "workflow", "cancel", executionID); err != nil {
		a.emitLog("WORKFLOW", "ERROR", fmt.Sprintf("Execution %s: %v", executionID, err))
		return err
	}
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("Execution %s cancelled", executionID))
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
