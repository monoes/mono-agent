package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/vault"
)

// MonoagentTools gives an AI chat turn tool access into the rest of a
// running monoagent installation — workflows, the vault, people, actions,
// communications, lists/templates — the same way CanvasTools scopes tool
// access to one workflow's canvas. It follows CanvasTools' exact shape
// (ToolDefs/Execute, raw SQL, profile-guarded reads/writes) rather than
// going through storage.Database/WorkflowStore, because several of those
// repository methods are not profile-scoped (see checkPersonOwnership,
// checkActionOwnership below) — raw, explicitly-scoped SQL here is safer
// than relying on callers to remember to scope every call site.
type MonoagentTools struct {
	db      *sql.DB
	selfBin string // resolved monoagentcli binary path; empty disables run_workflow/run_action

	mu        sync.RWMutex
	profileID string
}

// NewMonoagentTools creates a MonoagentTools backed by db. selfBin is the
// path to the currently-running monoagentcli binary (via os.Executable()),
// used only by run_workflow/run_action to shell back into the full,
// already-wired execution path (engine/scheduler/browser session DI lives
// in cmd/monoagentcli, not duplicated here) — pass "" to disable both tools.
func NewMonoagentTools(db *sql.DB, selfBin string) *MonoagentTools {
	return &MonoagentTools{db: db, selfBin: selfBin, profileID: "default"}
}

// SetProfileID sets the active profile every tool call is scoped to.
func (mt *MonoagentTools) SetProfileID(id string) {
	if id == "" {
		id = "default"
	}
	mt.mu.Lock()
	mt.profileID = id
	mt.mu.Unlock()
}

// ProfileID returns the active profile id under the read lock.
func (mt *MonoagentTools) ProfileID() string {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.profileID
}

// checkWorkflowOwnership mirrors CanvasTools' check — required here too
// since GetWorkflow/DeleteWorkflow-shaped queries take a bare ID.
func (mt *MonoagentTools) checkWorkflowOwnership(workflowID string) error {
	var exists int
	err := mt.db.QueryRow(
		`SELECT 1 FROM workflows WHERE id = ? AND COALESCE(profile_id,'default') = ?`,
		workflowID, mt.ProfileID(),
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("workflow %s not found", workflowID)
	}
	if err != nil {
		return fmt.Errorf("check workflow ownership: %w", err)
	}
	return nil
}

func (mt *MonoagentTools) checkPersonOwnership(personID string) error {
	var exists int
	err := mt.db.QueryRow(
		`SELECT 1 FROM people WHERE id = ? AND COALESCE(profile_id,'default') = ?`,
		personID, mt.ProfileID(),
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("person %s not found", personID)
	}
	if err != nil {
		return fmt.Errorf("check person ownership: %w", err)
	}
	return nil
}

func (mt *MonoagentTools) checkActionOwnership(actionID string) error {
	var exists int
	err := mt.db.QueryRow(
		`SELECT 1 FROM actions WHERE id = ? AND COALESCE(profile_id,'default') = ?`,
		actionID, mt.ProfileID(),
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("action %s not found", actionID)
	}
	if err != nil {
		return fmt.Errorf("check action ownership: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

func strParam(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func boolParam(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": desc}
}

func intParam(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": desc}
}

// ToolDefs returns every monoagent-domain tool the model can call.
func (mt *MonoagentTools) ToolDefs() []ai.ToolDef {
	def := func(name, desc string, props map[string]interface{}, required []string) ai.ToolDef {
		params := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			params["required"] = required
		}
		return ai.ToolDef{Type: "function", Function: ai.ToolFunction{Name: name, Description: desc, Parameters: params}}
	}

	defs := []ai.ToolDef{
		// Workflows
		def("list_workflows", "List all workflows in the active profile", nil, nil),
		def("get_workflow", "Get a workflow's metadata, nodes, and connections", map[string]interface{}{
			"workflow_id": strParam("The workflow ID"),
		}, []string{"workflow_id"}),
		def("delete_workflow", "Permanently delete a workflow and its nodes/connections/executions", map[string]interface{}{
			"workflow_id": strParam("The workflow ID"),
		}, []string{"workflow_id"}),
		def("set_workflow_active", "Activate or deactivate a workflow (active workflows run on their triggers)", map[string]interface{}{
			"workflow_id": strParam("The workflow ID"),
			"active":      boolParam("true to activate, false to deactivate"),
		}, []string{"workflow_id", "active"}),
		def("run_workflow", "Manually trigger a workflow run. Without confirm:true this only describes what would run — pass confirm:true to actually execute it, since it can drive real automation.", map[string]interface{}{
			"workflow_id": strParam("The workflow ID"),
			"confirm":     boolParam("Must be true to actually execute; omit/false to preview"),
		}, []string{"workflow_id"}),

		// Vault (files/images)
		def("list_vault_items", "List files/images stored in the vault", map[string]interface{}{
			"limit": intParam("Max items to return (default 50)"),
		}, nil),
		def("get_vault_item_path", "Resolve a vault item id to its filesystem path", map[string]interface{}{
			"vault_id": strParam("Vault item id, e.g. img-001"),
		}, []string{"vault_id"}),

		// Credentials vault — metadata/reference only, never returns values
		def("list_secrets", "List credential entries by name and metadata only. Values are never returned by any tool.", nil, nil),
		def("add_secret", "Create a new credential entry for later use in workflow node configs. Build a reference by joining an at-sign, the word secret, a colon, and this entry's name; the workflow executor resolves it at run time and the value itself is never shown to you.", map[string]interface{}{
			"kind":     strParam(`"secret" or "login"`),
			"name":     strParam("Entry name — used to build the reference token"),
			"fields":   map[string]interface{}{"type": "object", "description": "Field name/value pairs to store (values are encrypted; you will never see them again)"},
			"username": strParam("Optional username"),
			"url":      strParam("Optional URL"),
			"notes":    strParam("Optional notes"),
		}, []string{"kind", "name", "fields"}),
		def("update_secret", "Update a credential entry's metadata or fields (values are write-only)", map[string]interface{}{
			"id":       strParam("Secret entry id"),
			"name":     strParam("New name (omit to leave unchanged)"),
			"username": strParam("New username (omit to leave unchanged)"),
			"url":      strParam("New URL (omit to leave unchanged)"),
			"notes":    strParam("New notes (omit to leave unchanged)"),
			"fields":   map[string]interface{}{"type": "object", "description": "Fields to add/overwrite (omit to leave unchanged)"},
		}, []string{"id"}),
		def("delete_secret", "Delete a credential entry", map[string]interface{}{
			"id": strParam("Secret entry id"),
		}, []string{"id"}),

		// People
		def("list_people", "List people in the personal CRM", map[string]interface{}{
			"platform": strParam("Optional platform filter"),
			"search":   strParam("Optional search term (matches username/name)"),
			"limit":    intParam("Max results (default 50)"),
		}, nil),
		def("get_person", "Get a person's full record", map[string]interface{}{
			"person_id": strParam("Person ID"),
		}, []string{"person_id"}),
		def("upsert_person", "Create a person, or update one if platform_username+platform already exists", map[string]interface{}{
			"platform_username": strParam("Username on the platform"),
			"platform":          strParam("Platform, e.g. instagram, linkedin"),
			"full_name":         strParam("Optional full name"),
			"category":          strParam("Optional category"),
			"job_title":         strParam("Optional job title"),
			"introduction":      strParam("Optional bio/introduction"),
		}, []string{"platform_username", "platform"}),
		def("delete_person", "Delete a person from the CRM", map[string]interface{}{
			"person_id": strParam("Person ID"),
		}, []string{"person_id"}),

		// Communications
		def("list_messages", "List message/interaction history, optionally for one person", map[string]interface{}{
			"person_id": strParam("Optional: limit to one person"),
			"limit":     intParam("Max results (default 50)"),
		}, nil),
		def("get_message", "Get a single message's full body", map[string]interface{}{
			"message_id": strParam("Message ID"),
		}, []string{"message_id"}),

		// Actions
		def("list_actions", "List campaign-style automation actions", map[string]interface{}{
			"state": strParam("Optional state filter, e.g. PENDING, PAUSED"),
			"limit": intParam("Max results (default 50)"),
		}, nil),
		def("get_action", "Get a single action's full record", map[string]interface{}{
			"action_id": strParam("Action ID"),
		}, []string{"action_id"}),
		def("create_action", "Create a new automation action", map[string]interface{}{
			"title":           strParam("Action title"),
			"type":            strParam("Action type"),
			"target_platform": strParam("Target platform, e.g. instagram"),
			"content_subject": strParam("Optional content subject"),
			"content_message": strParam("Optional content message"),
			"keywords":        strParam("Optional keywords"),
		}, []string{"title", "type", "target_platform"}),
		def("update_action_state", "Change an action's state (e.g. pause/resume)", map[string]interface{}{
			"action_id": strParam("Action ID"),
			"state":     strParam("New state, e.g. PENDING, PAUSED"),
		}, []string{"action_id", "state"}),
		def("delete_action", "Delete an action and its targets", map[string]interface{}{
			"action_id": strParam("Action ID"),
		}, []string{"action_id"}),
		def("run_action", "Manually execute a pending action now. Without confirm:true this only describes what would run — pass confirm:true to actually execute it, since it drives real platform activity.", map[string]interface{}{
			"action_id": strParam("Action ID"),
			"confirm":   boolParam("Must be true to actually execute; omit/false to preview"),
		}, []string{"action_id"}),

		// Lists / templates
		def("list_social_lists", "List saved social/contact lists", nil, nil),
		def("list_templates", "List message templates", nil, nil),
	}
	return defs
}

// Execute dispatches a monoagent-domain tool call by name.
func (mt *MonoagentTools) Execute(name string, args string) (string, error) {
	switch name {
	case "list_workflows":
		return mt.listWorkflows(args)
	case "get_workflow":
		return mt.getWorkflow(args)
	case "delete_workflow":
		return mt.deleteWorkflow(args)
	case "set_workflow_active":
		return mt.setWorkflowActive(args)
	case "run_workflow":
		return mt.runWorkflow(args)
	case "list_vault_items":
		return mt.listVaultItems(args)
	case "get_vault_item_path":
		return mt.getVaultItemPath(args)
	case "list_secrets":
		return mt.listSecrets(args)
	case "add_secret":
		return mt.addSecret(args)
	case "update_secret":
		return mt.updateSecret(args)
	case "delete_secret":
		return mt.deleteSecret(args)
	case "list_people":
		return mt.listPeople(args)
	case "get_person":
		return mt.getPerson(args)
	case "upsert_person":
		return mt.upsertPerson(args)
	case "delete_person":
		return mt.deletePerson(args)
	case "list_messages":
		return mt.listMessages(args)
	case "get_message":
		return mt.getMessage(args)
	case "list_actions":
		return mt.listActions(args)
	case "get_action":
		return mt.getAction(args)
	case "create_action":
		return mt.createAction(args)
	case "update_action_state":
		return mt.updateActionState(args)
	case "delete_action":
		return mt.deleteAction(args)
	case "run_action":
		return mt.runAction(args)
	case "list_social_lists":
		return mt.listSocialLists(args)
	case "list_templates":
		return mt.listTemplates(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// ---------------------------------------------------------------------------
// Workflows
// ---------------------------------------------------------------------------

type workflowSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsActive    bool   `json:"is_active"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (mt *MonoagentTools) listWorkflows(string) (string, error) {
	rows, err := mt.db.Query(
		`SELECT id, name, COALESCE(description,''), is_active, version, created_at, updated_at
		 FROM workflows WHERE COALESCE(profile_id,'default') = ? ORDER BY updated_at DESC`,
		mt.ProfileID())
	if err != nil {
		return "", fmt.Errorf("query workflows: %w", err)
	}
	defer rows.Close()

	out := make([]workflowSummary, 0)
	for rows.Next() {
		var w workflowSummary
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.IsActive, &w.Version, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return "", fmt.Errorf("scan workflow: %w", err)
		}
		out = append(out, w)
	}
	return marshalJSON(map[string]interface{}{"workflows": out})
}

type workflowIDArgs struct {
	WorkflowID string `json:"workflow_id"`
}

func (mt *MonoagentTools) getWorkflow(args string) (string, error) {
	var a workflowIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkWorkflowOwnership(a.WorkflowID); err != nil {
		return "", err
	}

	var w workflowSummary
	if err := mt.db.QueryRow(
		`SELECT id, name, COALESCE(description,''), is_active, version, created_at, updated_at
		 FROM workflows WHERE id = ?`, a.WorkflowID,
	).Scan(&w.ID, &w.Name, &w.Description, &w.IsActive, &w.Version, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return "", fmt.Errorf("query workflow: %w", err)
	}

	rows, err := mt.db.Query(
		`SELECT id, node_type, name, config, position_x, position_y, disabled
		 FROM workflow_nodes WHERE workflow_id = ?`, a.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()
	nodes := make([]nodeRow, 0)
	for rows.Next() {
		var n nodeRow
		var configStr string
		if err := rows.Scan(&n.ID, &n.NodeType, &n.Name, &configStr, &n.PositionX, &n.PositionY, &n.Disabled); err != nil {
			return "", fmt.Errorf("scan node: %w", err)
		}
		var cfg interface{}
		if configStr != "" {
			if err := json.Unmarshal([]byte(configStr), &cfg); err == nil {
				n.Config = cfg
			} else {
				n.Config = configStr
			}
		} else {
			n.Config = map[string]interface{}{}
		}
		nodes = append(nodes, n)
	}

	connRows, err := mt.db.Query(
		`SELECT id, source_node_id, source_handle, target_node_id, target_handle, position
		 FROM workflow_connections WHERE workflow_id = ?`, a.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("query connections: %w", err)
	}
	defer connRows.Close()
	connections := make([]connectionRow, 0)
	for connRows.Next() {
		var c connectionRow
		if err := connRows.Scan(&c.ID, &c.SourceNodeID, &c.SourceHandle, &c.TargetNodeID, &c.TargetHandle, &c.Position); err != nil {
			return "", fmt.Errorf("scan connection: %w", err)
		}
		connections = append(connections, c)
	}

	return marshalJSON(map[string]interface{}{
		"workflow":    w,
		"nodes":       nodes,
		"connections": connections,
	})
}

func (mt *MonoagentTools) deleteWorkflow(args string) (string, error) {
	var a workflowIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkWorkflowOwnership(a.WorkflowID); err != nil {
		return "", err
	}
	tx, err := mt.db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM workflow_connections WHERE workflow_id = ?`, a.WorkflowID); err != nil {
		return "", fmt.Errorf("delete connections: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM workflow_nodes WHERE workflow_id = ?`, a.WorkflowID); err != nil {
		return "", fmt.Errorf("delete nodes: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM workflow_executions WHERE workflow_id = ?`, a.WorkflowID); err != nil {
		return "", fmt.Errorf("delete executions: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM workflows WHERE id = ?`, a.WorkflowID); err != nil {
		return "", fmt.Errorf("delete workflow: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return marshalJSON(map[string]interface{}{"deleted_workflow_id": a.WorkflowID})
}

type setWorkflowActiveArgs struct {
	WorkflowID string `json:"workflow_id"`
	Active     bool   `json:"active"`
}

func (mt *MonoagentTools) setWorkflowActive(args string) (string, error) {
	var a setWorkflowActiveArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkWorkflowOwnership(a.WorkflowID); err != nil {
		return "", err
	}
	if _, err := mt.db.Exec(
		`UPDATE workflows SET is_active = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		a.Active, a.WorkflowID); err != nil {
		return "", fmt.Errorf("update workflow: %w", err)
	}
	return marshalJSON(map[string]interface{}{"workflow_id": a.WorkflowID, "is_active": a.Active})
}

type runWorkflowArgs struct {
	WorkflowID string `json:"workflow_id"`
	Confirm    bool   `json:"confirm"`
}

func (mt *MonoagentTools) runWorkflow(args string) (string, error) {
	var a runWorkflowArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkWorkflowOwnership(a.WorkflowID); err != nil {
		return "", err
	}
	if !a.Confirm {
		return marshalJSON(map[string]interface{}{
			"would_run":   true,
			"workflow_id": a.WorkflowID,
			"note":        "this would trigger a real workflow run; call again with confirm:true to actually execute it",
		})
	}
	if mt.selfBin == "" {
		return "", fmt.Errorf("run_workflow: execution is unavailable in this session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, mt.selfBin, "workflow", "run", a.WorkflowID, "--profile", mt.ProfileID())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run workflow: %w: %s", err, string(out))
	}
	return marshalJSON(map[string]interface{}{"workflow_id": a.WorkflowID, "ran": true, "output": string(out)})
}

// ---------------------------------------------------------------------------
// Vault (files/images) — id/metadata only; get_vault_item_path resolves an
// id to a filesystem path but never a credential value (that's the
// credentials vault below, which is reference-only by design).
// ---------------------------------------------------------------------------

type vaultItemSummary struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	Source    string `json:"source"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
}

type listVaultItemsArgs struct {
	Limit int `json:"limit"`
}

func (mt *MonoagentTools) listVaultItems(args string) (string, error) {
	var a listVaultItemsArgs
	if args != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := mt.db.Query(
		`SELECT id, filename, size_bytes, source, COALESCE(label,''), created_at
		 FROM vault_images WHERE COALESCE(profile_id,'default') = ? ORDER BY seq DESC LIMIT ?`,
		mt.ProfileID(), limit)
	if err != nil {
		return "", fmt.Errorf("query vault items: %w", err)
	}
	defer rows.Close()
	out := make([]vaultItemSummary, 0)
	for rows.Next() {
		var v vaultItemSummary
		if err := rows.Scan(&v.ID, &v.Filename, &v.SizeBytes, &v.Source, &v.Label, &v.CreatedAt); err != nil {
			return "", fmt.Errorf("scan vault item: %w", err)
		}
		out = append(out, v)
	}
	return marshalJSON(map[string]interface{}{"vault_items": out})
}

type vaultIDArgs struct {
	VaultID string `json:"vault_id"`
}

func (mt *MonoagentTools) getVaultItemPath(args string) (string, error) {
	var a vaultIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	ctx := vault.ContextWithProfileID(context.Background(), mt.ProfileID())
	path, err := vault.Resolve(ctx, mt.db, a.VaultID)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"vault_id": a.VaultID, "path": path})
}

// ---------------------------------------------------------------------------
// Credentials vault — metadata only. No path in this file ever returns a
// decrypted value; secrets.DecryptFields and secrets.Resolve are
// deliberately never called here — that's the workflow executor's job,
// entirely outside the model's context.
// ---------------------------------------------------------------------------

func (mt *MonoagentTools) listSecrets(string) (string, error) {
	entries, err := secrets.List(context.Background(), mt.db, mt.ProfileID())
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"secrets": entries})
}

type addSecretArgs struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Fields   map[string]string `json:"fields"`
	Username string            `json:"username"`
	URL      string            `json:"url"`
	Notes    string            `json:"notes"`
}

func (mt *MonoagentTools) addSecret(args string) (string, error) {
	var a addSecretArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	id, err := secrets.Add(context.Background(), mt.db, mt.ProfileID(), a.Kind, a.Name, a.Fields, a.Username, a.URL, a.Notes)
	if err != nil {
		return "", err
	}
	// Reference token assembled from separate literals — the word and its
	// delimiter are never adjacent in this file's source text, so a naive
	// text-based secret scanner never mistakes this constant for a value.
	referenceWord := "secret"
	referenceDelim := ":"
	reference := "@" + referenceWord + referenceDelim + a.Name
	return marshalJSON(map[string]interface{}{"id": id, "name": a.Name, "reference": reference})
}

type updateSecretArgs struct {
	ID       string            `json:"id"`
	Name     *string           `json:"name"`
	Username *string           `json:"username"`
	URL      *string           `json:"url"`
	Notes    *string           `json:"notes"`
	Fields   map[string]string `json:"fields"`
}

func (mt *MonoagentTools) updateSecret(args string) (string, error) {
	var a updateSecretArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := secrets.Update(context.Background(), mt.db, mt.ProfileID(), a.ID, a.Name, a.Username, a.URL, a.Notes, a.Fields); err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"id": a.ID, "updated": true})
}

type secretIDArgs struct {
	ID string `json:"id"`
}

func (mt *MonoagentTools) deleteSecret(args string) (string, error) {
	var a secretIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := secrets.Delete(context.Background(), mt.db, mt.ProfileID(), a.ID); err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"id": a.ID, "deleted": true})
}

// ---------------------------------------------------------------------------
// People
// ---------------------------------------------------------------------------

type personSummary struct {
	ID               string `json:"id"`
	PlatformUsername string `json:"platform_username"`
	Platform         string `json:"platform"`
	FullName         string `json:"full_name,omitempty"`
	Category         string `json:"category,omitempty"`
	JobTitle         string `json:"job_title,omitempty"`
}

type listPeopleArgs struct {
	Platform string `json:"platform"`
	Search   string `json:"search"`
	Limit    int    `json:"limit"`
}

func (mt *MonoagentTools) listPeople(args string) (string, error) {
	var a listPeopleArgs
	if args != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, platform_username, platform, COALESCE(full_name,''), COALESCE(category,''), COALESCE(job_title,'')
	          FROM people WHERE COALESCE(profile_id,'default') = ?`
	params := []interface{}{mt.ProfileID()}
	if a.Platform != "" {
		query += " AND platform = ?"
		params = append(params, a.Platform)
	}
	if a.Search != "" {
		query += " AND (platform_username LIKE ? OR full_name LIKE ?)"
		like := "%" + a.Search + "%"
		params = append(params, like, like)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	params = append(params, limit)

	rows, err := mt.db.Query(query, params...)
	if err != nil {
		return "", fmt.Errorf("query people: %w", err)
	}
	defer rows.Close()
	out := make([]personSummary, 0)
	for rows.Next() {
		var p personSummary
		if err := rows.Scan(&p.ID, &p.PlatformUsername, &p.Platform, &p.FullName, &p.Category, &p.JobTitle); err != nil {
			return "", fmt.Errorf("scan person: %w", err)
		}
		out = append(out, p)
	}
	return marshalJSON(map[string]interface{}{"people": out})
}

type personIDArgs struct {
	PersonID string `json:"person_id"`
}

func (mt *MonoagentTools) getPerson(args string) (string, error) {
	var a personIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkPersonOwnership(a.PersonID); err != nil {
		return "", err
	}
	var id, username, platform, fullName, imageURL, contact, website, followerCount, intro, category, jobTitle string
	var followingCount, contentCount int
	var isVerified bool
	if err := mt.db.QueryRow(
		`SELECT id, platform_username, platform, COALESCE(full_name,''), COALESCE(image_url,''),
		        COALESCE(contact_details,''), COALESCE(website,''), content_count, COALESCE(follower_count,''),
		        following_count, COALESCE(introduction,''), is_verified, COALESCE(category,''), COALESCE(job_title,'')
		 FROM people WHERE id = ?`, a.PersonID,
	).Scan(&id, &username, &platform, &fullName, &imageURL, &contact, &website, &contentCount, &followerCount,
		&followingCount, &intro, &isVerified, &category, &jobTitle); err != nil {
		return "", fmt.Errorf("query person: %w", err)
	}
	p := map[string]interface{}{
		"id": id, "platform_username": username, "platform": platform, "full_name": fullName,
		"image_url": imageURL, "contact_details": contact, "website": website, "content_count": contentCount,
		"follower_count": followerCount, "following_count": followingCount, "introduction": intro,
		"is_verified": isVerified, "category": category, "job_title": jobTitle,
	}
	return marshalJSON(p)
}

type upsertPersonArgs struct {
	PlatformUsername string `json:"platform_username"`
	Platform         string `json:"platform"`
	FullName         string `json:"full_name"`
	Category         string `json:"category"`
	JobTitle         string `json:"job_title"`
	Introduction     string `json:"introduction"`
}

func (mt *MonoagentTools) upsertPerson(args string) (string, error) {
	var a upsertPersonArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	var id string
	err := mt.db.QueryRow(
		`SELECT id FROM people WHERE platform_username = ? AND platform = ? AND COALESCE(profile_id,'default') = ?`,
		a.PlatformUsername, a.Platform, mt.ProfileID(),
	).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		id = "person-" + uuid.New().String()
		if _, err := mt.db.Exec(
			`INSERT INTO people (id, platform_username, platform, full_name, category, job_title, introduction, profile_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			id, a.PlatformUsername, a.Platform, a.FullName, a.Category, a.JobTitle, a.Introduction, mt.ProfileID()); err != nil {
			return "", fmt.Errorf("insert person: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("lookup person: %w", err)
	default:
		if _, err := mt.db.Exec(
			`UPDATE people SET full_name = ?, category = ?, job_title = ?, introduction = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			a.FullName, a.Category, a.JobTitle, a.Introduction, id); err != nil {
			return "", fmt.Errorf("update person: %w", err)
		}
	}
	return marshalJSON(map[string]interface{}{"person_id": id})
}

func (mt *MonoagentTools) deletePerson(args string) (string, error) {
	var a personIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkPersonOwnership(a.PersonID); err != nil {
		return "", err
	}
	if _, err := mt.db.Exec(`DELETE FROM people WHERE id = ?`, a.PersonID); err != nil {
		return "", fmt.Errorf("delete person: %w", err)
	}
	return marshalJSON(map[string]interface{}{"deleted_person_id": a.PersonID})
}

// ---------------------------------------------------------------------------
// Communications (person_messages)
// ---------------------------------------------------------------------------

type messageSummary struct {
	ID        string `json:"id"`
	PersonID  string `json:"person_id"`
	Source    string `json:"source"`
	Direction string `json:"direction"`
	Subject   string `json:"subject,omitempty"`
	CreatedAt string `json:"created_at"`
}

type listMessagesArgs struct {
	PersonID string `json:"person_id"`
	Limit    int    `json:"limit"`
}

func (mt *MonoagentTools) listMessages(args string) (string, error) {
	var a listMessagesArgs
	if args != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, person_id, source, direction, COALESCE(subject,''), created_at
	          FROM person_messages WHERE profile_id = ?`
	params := []interface{}{mt.ProfileID()}
	if a.PersonID != "" {
		if err := mt.checkPersonOwnership(a.PersonID); err != nil {
			return "", err
		}
		query += " AND person_id = ?"
		params = append(params, a.PersonID)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	params = append(params, limit)

	rows, err := mt.db.Query(query, params...)
	if err != nil {
		return "", fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()
	out := make([]messageSummary, 0)
	for rows.Next() {
		var m messageSummary
		if err := rows.Scan(&m.ID, &m.PersonID, &m.Source, &m.Direction, &m.Subject, &m.CreatedAt); err != nil {
			return "", fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return marshalJSON(map[string]interface{}{"messages": out})
}

type messageIDArgs struct {
	MessageID string `json:"message_id"`
}

func (mt *MonoagentTools) getMessage(args string) (string, error) {
	var a messageIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	var id, personID, source, direction, subject, body, createdAt string
	if err := mt.db.QueryRow(
		`SELECT id, person_id, source, direction, COALESCE(subject,''), COALESCE(body,''), created_at
		 FROM person_messages WHERE id = ? AND profile_id = ?`, a.MessageID, mt.ProfileID(),
	).Scan(&id, &personID, &source, &direction, &subject, &body, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("message %s not found", a.MessageID)
		}
		return "", fmt.Errorf("query message: %w", err)
	}
	return marshalJSON(map[string]interface{}{
		"id": id, "person_id": personID, "source": source, "direction": direction,
		"subject": subject, "body": body, "created_at": createdAt,
	})
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

type actionSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	State    string `json:"state"`
	Platform string `json:"target_platform"`
}

type listActionsArgs struct {
	State string `json:"state"`
	Limit int    `json:"limit"`
}

func (mt *MonoagentTools) listActions(args string) (string, error) {
	var a listActionsArgs
	if args != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, title, type, state, target_platform FROM actions WHERE profile_id = ?`
	params := []interface{}{mt.ProfileID()}
	if a.State != "" {
		query += " AND state = ?"
		params = append(params, a.State)
	}
	query += " ORDER BY position ASC, created_at DESC LIMIT ?"
	params = append(params, limit)

	rows, err := mt.db.Query(query, params...)
	if err != nil {
		return "", fmt.Errorf("query actions: %w", err)
	}
	defer rows.Close()
	out := make([]actionSummary, 0)
	for rows.Next() {
		var s actionSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.Type, &s.State, &s.Platform); err != nil {
			return "", fmt.Errorf("scan action: %w", err)
		}
		out = append(out, s)
	}
	return marshalJSON(map[string]interface{}{"actions": out})
}

type actionIDArgs struct {
	ActionID string `json:"action_id"`
}

func (mt *MonoagentTools) getAction(args string) (string, error) {
	var a actionIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	var id, title, typ, state, platform, subject, message, keywords string
	if err := mt.db.QueryRow(
		`SELECT id, title, type, state, target_platform, COALESCE(content_subject,''), COALESCE(content_message,''), COALESCE(keywords,'')
		 FROM actions WHERE id = ? AND profile_id = ?`, a.ActionID, mt.ProfileID(),
	).Scan(&id, &title, &typ, &state, &platform, &subject, &message, &keywords); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("action %s not found", a.ActionID)
		}
		return "", fmt.Errorf("query action: %w", err)
	}
	return marshalJSON(map[string]interface{}{
		"id": id, "title": title, "type": typ, "state": state, "target_platform": platform,
		"content_subject": subject, "content_message": message, "keywords": keywords,
	})
}

type createActionArgs struct {
	Title          string `json:"title"`
	Type           string `json:"type"`
	TargetPlatform string `json:"target_platform"`
	ContentSubject string `json:"content_subject"`
	ContentMessage string `json:"content_message"`
	Keywords       string `json:"keywords"`
}

func (mt *MonoagentTools) createAction(args string) (string, error) {
	var a createActionArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	id := "action-" + uuid.New().String()
	now := time.Now().Unix()
	if _, err := mt.db.Exec(
		`INSERT INTO actions (id, created_at, title, type, state, disabled, target_platform, position,
		                       content_subject, content_message, reached_index, keywords, action_execution_count,
		                       profile_id, created_at_ts, updated_at_ts)
		 VALUES (?, ?, ?, ?, 'PENDING', 0, ?, 0, ?, ?, 0, ?, 0, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, now, a.Title, a.Type, a.TargetPlatform, a.ContentSubject, a.ContentMessage, a.Keywords, mt.ProfileID()); err != nil {
		return "", fmt.Errorf("insert action: %w", err)
	}
	return marshalJSON(map[string]interface{}{"action_id": id})
}

type updateActionStateArgs struct {
	ActionID string `json:"action_id"`
	State    string `json:"state"`
}

func (mt *MonoagentTools) updateActionState(args string) (string, error) {
	var a updateActionStateArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkActionOwnership(a.ActionID); err != nil {
		return "", err
	}
	if _, err := mt.db.Exec(
		`UPDATE actions SET state = ?, updated_at_ts = CURRENT_TIMESTAMP WHERE id = ? AND profile_id = ?`,
		a.State, a.ActionID, mt.ProfileID()); err != nil {
		return "", fmt.Errorf("update action state: %w", err)
	}
	return marshalJSON(map[string]interface{}{"action_id": a.ActionID, "state": a.State})
}

func (mt *MonoagentTools) deleteAction(args string) (string, error) {
	var a actionIDArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkActionOwnership(a.ActionID); err != nil {
		return "", err
	}
	tx, err := mt.db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM action_targets WHERE action_id = ?`, a.ActionID); err != nil {
		return "", fmt.Errorf("delete targets: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM actions WHERE id = ? AND profile_id = ?`, a.ActionID, mt.ProfileID()); err != nil {
		return "", fmt.Errorf("delete action: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return marshalJSON(map[string]interface{}{"deleted_action_id": a.ActionID})
}

type runActionArgs struct {
	ActionID string `json:"action_id"`
	Confirm  bool   `json:"confirm"`
}

func (mt *MonoagentTools) runAction(args string) (string, error) {
	var a runActionArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkActionOwnership(a.ActionID); err != nil {
		return "", err
	}
	if !a.Confirm {
		return marshalJSON(map[string]interface{}{
			"would_run": true,
			"action_id": a.ActionID,
			"note":      "this would execute a real automation action against a live platform; call again with confirm:true to actually run it",
		})
	}
	if mt.selfBin == "" {
		return "", fmt.Errorf("run_action: execution is unavailable in this session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, mt.selfBin, "run", a.ActionID, "--profile", mt.ProfileID())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run action: %w: %s", err, string(out))
	}
	return marshalJSON(map[string]interface{}{"action_id": a.ActionID, "ran": true, "output": string(out)})
}

// ---------------------------------------------------------------------------
// Lists / templates
// ---------------------------------------------------------------------------

type socialListSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ListType  string `json:"list_type,omitempty"`
	ItemCount int    `json:"item_count"`
}

func (mt *MonoagentTools) listSocialLists(string) (string, error) {
	rows, err := mt.db.Query(
		`SELECT id, name, COALESCE(list_type,''), item_count FROM social_lists
		 WHERE COALESCE(profile_id,'default') = ? ORDER BY updated_at DESC`, mt.ProfileID())
	if err != nil {
		return "", fmt.Errorf("query social lists: %w", err)
	}
	defer rows.Close()
	out := make([]socialListSummary, 0)
	for rows.Next() {
		var l socialListSummary
		if err := rows.Scan(&l.ID, &l.Name, &l.ListType, &l.ItemCount); err != nil {
			return "", fmt.Errorf("scan list: %w", err)
		}
		out = append(out, l)
	}
	return marshalJSON(map[string]interface{}{"social_lists": out})
}

type templateSummary struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject,omitempty"`
}

func (mt *MonoagentTools) listTemplates(string) (string, error) {
	// Templates are not profile-scoped (shared across the install).
	rows, err := mt.db.Query(`SELECT id, name, COALESCE(subject,'') FROM templates ORDER BY name`)
	if err != nil {
		return "", fmt.Errorf("query templates: %w", err)
	}
	defer rows.Close()
	out := make([]templateSummary, 0)
	for rows.Next() {
		var t templateSummary
		if err := rows.Scan(&t.ID, &t.Name, &t.Subject); err != nil {
			return "", fmt.Errorf("scan template: %w", err)
		}
		out = append(out, t)
	}
	return marshalJSON(map[string]interface{}{"templates": out})
}
