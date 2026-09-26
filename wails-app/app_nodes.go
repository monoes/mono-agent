package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Node types registry
// ─────────────────────────────────────────────────────────────────────────────

// nodePaletteTimeout bounds `node palette`, which builds the node registry.
const nodePaletteTimeout = 30 * time.Second

// GetWorkflowNodeTypes is the node palette (`node palette`): node types
// grouped by category, each with a label, description and schema. An empty
// palette when the CLI fails — the palette just shows nothing.
func (a *App) GetWorkflowNodeTypes() map[string]interface{} {
	palette := map[string]interface{}{}
	if err := a.cliJSON(nodePaletteTimeout, &palette, "node", "palette"); err != nil {
		a.emitLog("SYSTEM", "WARN", "node palette: "+err.Error())
		return map[string]interface{}{}
	}
	return palette
}

// ─────────────────────────────────────────────────────────────────────────────
// Node runner — execute any node type directly
// ─────────────────────────────────────────────────────────────────────────────

// NodeRunRequest is the payload sent by the frontend to run a node.
type NodeRunRequest struct {
	NodeType string                   `json:"node_type"`
	Config   map[string]interface{}   `json:"config"`
	Items    []map[string]interface{} `json:"items"` // each element is a JSON object (the item's .json field)
}

// NodeRunResult is returned after running a node.
type NodeRunResult struct {
	Outputs    []NodeRunOutput `json:"outputs"`
	Error      string          `json:"error,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	RunID      string          `json:"run_id,omitempty"` // pass to StopNodeRun to cancel the run
}

// NodeRunOutput is one output handle's items.
type NodeRunOutput struct {
	Handle string                   `json:"handle"`
	Items  []map[string]interface{} `json:"items"`
}

// RunNode executes any registered node type directly via the CLI subprocess.
// Config and input items are passed as JSON; results are returned as structured data.
// legacyNodeTypes maps old short type names to their new prefixed equivalents.
var legacyNodeTypes = map[string]string{
	"if": "core.if", "switch": "core.switch", "merge": "core.merge",
	"split_in_batches": "core.split_in_batches", "wait": "core.wait",
	"stop_error": "core.stop_error", "set": "core.set", "code": "core.code",
	"filter": "core.filter", "sort": "core.sort", "limit": "core.limit",
	"remove_duplicates": "core.remove_duplicates", "compare_datasets": "core.compare_datasets",
	"aggregate": "core.aggregate",
	"datetime":  "data.datetime", "crypto": "data.crypto", "html": "data.html",
	"xml": "data.xml", "markdown": "data.markdown", "spreadsheet": "data.spreadsheet",
	"compression": "data.compression", "write_binary_file": "data.write_binary_file",
	"mysql": "db.mysql", "postgres": "db.postgres", "mongodb": "db.mongodb", "redis": "db.redis",
	"email_send": "comm.email_send", "email_read": "comm.email_read",
	"slack": "comm.slack", "telegram": "comm.telegram", "discord": "comm.discord",
	"twilio": "comm.twilio", "whatsapp": "comm.whatsapp",
	"github": "service.github", "airtable": "service.airtable", "notion": "service.notion",
	"jira": "service.jira", "linear": "service.linear", "asana": "service.asana",
	"stripe": "service.stripe", "shopify": "service.shopify", "salesforce": "service.salesforce",
	"hubspot": "service.hubspot", "google_sheets": "service.google_sheets",
	"gmail": "service.gmail", "google_drive": "service.google_drive",
}

func (a *App) RunNode(req NodeRunRequest) NodeRunResult {
	if mapped, ok := legacyNodeTypes[req.NodeType]; ok {
		req.NodeType = mapped
	}

	// Extract credential_id and pass it to the CLI via --credential so the
	// subprocess resolves the stored tokens internally against the same DB.
	// Never merge plaintext credential data into --config: process arguments
	// are world-readable to other local processes (ps / /proc/<pid>/cmdline).
	credID, _ := req.Config["credential_id"].(string)
	delete(req.Config, "credential_id")

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return NodeRunResult{Error: err.Error()}
	}

	configBytes, err := json.Marshal(req.Config)
	if err != nil {
		return NodeRunResult{Error: "invalid config: " + err.Error()}
	}

	items := req.Items
	if len(items) == 0 {
		items = []map[string]interface{}{}
	}
	inputItems := make([]map[string]interface{}, len(items))
	for i, it := range items {
		inputItems[i] = map[string]interface{}{"json": it}
	}
	inputBytes, err := json.Marshal(inputItems)
	if err != nil {
		return NodeRunResult{Error: "invalid input: " + err.Error()}
	}

	// Pass config + input via stdin, not argv: any secrets typed directly into
	// node fields (DB passwords, API keys) would otherwise be world-readable via
	// ps / /proc/<pid>/cmdline for the subprocess lifetime.
	payload, err := json.Marshal(map[string]json.RawMessage{
		"config": json.RawMessage(configBytes),
		"input":  json.RawMessage(inputBytes),
	})
	if err != nil {
		return NodeRunResult{Error: "encoding node payload: " + err.Error()}
	}

	start := time.Now()
	args := []string{
		"--profile", a.getActiveProfileID(),
		"node", "run", req.NodeType,
		"--stdin",
		"--output", "json",
	}
	if credID != "" {
		args = append(args, "--credential", credID)
	}
	cmd := exec.Command(cliBin, args...)
	hideWindow(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Register the subprocess under a counter-based run id so the frontend
	// can cancel it with StopNodeRun (RA1-9). Namespaced "noderun:" in the
	// shared registry so shutdown reaps it too.
	runID := strconv.FormatInt(a.nodeRunCounter.Add(1), 10)
	runKey := "noderun:" + runID
	a.runningMu.Lock()
	a.runningCmds[runKey] = cmd
	a.runningMu.Unlock()

	startErr := cmd.Start()
	if startErr != nil {
		a.runningMu.Lock()
		delete(a.runningCmds, runKey)
		a.runningMu.Unlock()
		return NodeRunResult{Error: "failed to start node run: " + startErr.Error(), RunID: runID}
	}
	runErr := cmd.Wait()
	a.runningMu.Lock()
	delete(a.runningCmds, runKey)
	a.runningMu.Unlock()
	elapsed := time.Since(start).Milliseconds()
	out := stdout.Bytes()

	if runErr != nil {
		msg := runErr.Error()
		if _, ok := runErr.(*exec.ExitError); ok && stderr.Len() > 0 {
			msg = strings.TrimSpace(stderr.String())
		}
		return NodeRunResult{Error: msg, DurationMs: elapsed, RunID: runID}
	}

	var raw map[string][]struct {
		JSON map[string]interface{} `json:"json"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return NodeRunResult{Error: "failed to parse output: " + err.Error(), DurationMs: elapsed}
	}

	var outputs []NodeRunOutput
	for handle, rawItems := range raw {
		flat := make([]map[string]interface{}, len(rawItems))
		for i, ri := range rawItems {
			flat[i] = ri.JSON
		}
		outputs = append(outputs, NodeRunOutput{Handle: handle, Items: flat})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Handle < outputs[j].Handle })
	return NodeRunResult{Outputs: outputs, DurationMs: elapsed, RunID: runID}
}

// StopNodeRun kills the subprocess started by RunNode for the given run id
// (NodeRunResult.run_id). The interrupted RunNode call returns with its Error
// field set to the kill signal.
func (a *App) StopNodeRun(runID string) error {
	runKey := "noderun:" + runID
	a.runningMu.Lock()
	cmd, ok := a.runningCmds[runKey]
	if ok {
		delete(a.runningCmds, runKey)
	}
	a.runningMu.Unlock()
	if !ok || cmd.Process == nil {
		return fmt.Errorf("node run %s is not running", runID)
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("stop node run %s: %w", runID, err)
	}
	return nil
}
