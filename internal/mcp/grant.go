package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/workflow"
)

// Grant mode (`monoagentcli mcp --grant <id>`, plan §7.1, contracts §3) is
// the tool provider monomind spawns for an org role. It serves only the
// automations the role's grant rows allow, and it is a thin client: a call
// enqueues an unowned execution for `monoagentcli daemon` to run and
// watches its row. It never starts an engine, opens the vault, or launches
// a browser, so the provider process is cheap (C-22) and never the pid a
// cancel would signal (C-38).

// Grant-mode error codes, at the start of refusal texts.
const (
	codeRefusedGrant   = "refused_grant"
	codeDaemonRequired = "daemon_required"
	codeRefusedCap     = "refused_cap"
)

// grantPollInterval is how often a waiting call re-reads its execution.
var grantPollInterval = 500 * time.Millisecond

type metaKey struct{}

// callMeta is the MCP request _meta monomind M1 sends.
type callMeta struct {
	Trace struct {
		Org     string `json:"org"`
		Run     string `json:"run"`
		Role    string `json:"role"`
		ChainID string `json:"chain_id"`
		Hop     int    `json:"hop"`
	} `json:"trace"`
}

func withMeta(ctx context.Context, raw json.RawMessage) context.Context {
	if len(raw) == 0 {
		return ctx
	}
	var m callMeta
	if json.Unmarshal(raw, &m) != nil {
		return ctx
	}
	return context.WithValue(ctx, metaKey{}, m)
}

func metaFrom(ctx context.Context) callMeta {
	m, _ := ctx.Value(metaKey{}).(callMeta)
	return m
}

// grantScope resolves the bundle and checks it against the process: the
// --profile flag and the org/role monomind put in the environment must name
// the bundle's own scope, so copying a grant id into another role's provider
// block buys nothing (C-3). The environment is required, not merely checked
// when present: a grant id is not a secret — it sits in every role's
// provider args in the org JSON — so an unset variable must refuse rather
// than wave the call through.
func (s *Server) grantScope(ctx context.Context) (*orggrant.Bundle, *runtime, error) {
	rt, err := s.runtime()
	if err != nil {
		return nil, nil, err
	}
	b, err := orggrant.NewStore(rt.db.DB).ResolveBundle(ctx, s.opts.Grant)
	if errors.Is(err, orggrant.ErrNotFound) {
		return nil, nil, fmt.Errorf("%s: grant %s does not exist", codeRefusedGrant, s.opts.Grant)
	}
	if err != nil {
		return nil, nil, err
	}
	if s.opts.Profile != "" && s.opts.Profile != b.ProfileID {
		return nil, nil, fmt.Errorf("%s: grant %s belongs to profile %q, not %q", codeRefusedGrant, s.opts.Grant, b.ProfileID, s.opts.Profile)
	}
	org, role := os.Getenv("MONOMIND_ORG_NAME"), os.Getenv("MONOMIND_ORG_ROLE")
	if org == "" {
		return nil, nil, fmt.Errorf("%s: grant mode needs MONOMIND_ORG_NAME in the environment; monomind sets it for a role's tool provider", codeRefusedGrant)
	}
	if role == "" {
		return nil, nil, fmt.Errorf("%s: grant mode needs MONOMIND_ORG_ROLE in the environment; monomind sets it for a role's tool provider", codeRefusedGrant)
	}
	if org != b.OrgName {
		return nil, nil, fmt.Errorf("%s: grant %s is for org %q, not %q", codeRefusedGrant, s.opts.Grant, b.OrgName, org)
	}
	if role != b.RoleID {
		return nil, nil, fmt.Errorf("%s: grant %s is for role %q, not %q", codeRefusedGrant, s.opts.Grant, b.RoleID, role)
	}
	return b, rt, nil
}

// grantToolDefinitions lists the bundle's tools. An unusable bundle lists
// nothing; the reason surfaces on the first call.
func (s *Server) grantToolDefinitions(ctx context.Context) []map[string]interface{} {
	b, rt, err := s.grantScope(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: grant mode: %v\n", err)
		return []map[string]interface{}{}
	}
	var specs map[string]orgdesign.GrantSpec
	if doc, err := orgdesign.Load(profiledir.Root(rt.db.DB, b.ProfileID), b.OrgName); err == nil {
		if r, _ := doc.FindRole(b.RoleID); r != nil {
			specs = map[string]orgdesign.GrantSpec{}
			for _, g := range r.Automations {
				specs[g.Alias] = g
			}
		}
	}
	var out []map[string]interface{}
	hasAutomation := false
	for _, g := range b.Grants {
		t := g.Automation()
		if t == nil {
			continue
		}
		hasAutomation = true
		name := "the workflow"
		var fields []string
		if wf, err := rt.store.GetWorkflow(ctx, t.WorkflowID); err == nil && wf != nil {
			name = fmt.Sprintf("workflow %q", wf.Name)
			fields = orggrant.InputFields(wf)
		}
		desc := fmt.Sprintf("Run automation %q (%s). ", t.Alias, name)
		if t.Wait && t.Mode == "run" {
			desc += fmt.Sprintf("Waits up to %ds and returns the result; longer runs return an execution_id for automation_status.", t.Timeout)
		} else {
			desc += "Starts the run and returns an execution_id; check it with automation_status."
		}
		if t.Approval == "required" {
			desc += " Each call waits for a decision before it runs."
		}
		desc += " A run paused for human review reports status waiting; you cannot approve it."
		schema := map[string]interface{}{
			"type":                 "object",
			"additionalProperties": true,
			"description":          "Input passed to the workflow as trigger data (field input).",
		}
		// monomind keeps only the arguments a tool's schema lists as
		// properties (a zod object strips the rest), so a schema without
		// properties delivers input: {} whatever the role passed. Advertise
		// the input fields the workflow's templates read.
		if len(fields) > 0 {
			props := make(map[string]interface{}, len(fields))
			for _, f := range fields {
				props[f] = map[string]interface{}{"description": fmt.Sprintf("Workflow input field %q (the workflow reads input.%s).", f, f)}
			}
			schema["properties"] = props
		}
		if spec, ok := specs[t.Alias]; ok && len(spec.InputSchema) > 0 {
			var custom map[string]interface{}
			if json.Unmarshal(spec.InputSchema, &custom) == nil && custom["type"] == "object" {
				schema = custom
			}
		}
		out = append(out, map[string]interface{}{"name": t.Tool, "description": desc, "inputSchema": schema})
	}
	if hasAutomation {
		out = append(out,
			map[string]interface{}{
				"name":        "automation_status",
				"description": "Status and output of an automation run you started.",
				"inputSchema": objSchema(map[string]interface{}{"execution_id": strParam("Execution id an automation tool returned")}, "execution_id"),
			},
			map[string]interface{}{
				"name":        "automation_output",
				"description": "Read a finished run's full output page by page when it was truncated.",
				"inputSchema": objSchema(map[string]interface{}{
					"execution_id": strParam("Execution id"),
					"node":         strParam("Node name (default: the last node)"),
					"offset":       intParam("Byte offset (default 0)"),
				}, "execution_id"),
			})
	}
	out = append(out, orgToolDefinitions(b)...)
	out = append(out, decisionToolDefinitions(b)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out
}

// callGrantTool serves one tools/call in grant mode.
func (s *Server) callGrantTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	b, rt, err := s.grantScope(ctx)
	if err != nil {
		return "", err
	}
	var result interface{}
	switch name {
	case "automation_status":
		result, err = s.grantStatus(ctx, rt, b, args)
	case "automation_output":
		result, err = s.grantOutput(ctx, rt, b, args)
	default:
		var handled bool
		if result, handled, err = s.callOrgTool(ctx, rt, b, name, args); !handled {
			if result, handled, err = s.callDecisionTool(ctx, rt, b, name, args); !handled {
				result, err = s.grantRun(ctx, rt, b, name, args)
			}
		}
	}
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *Server) grantRun(ctx context.Context, rt *runtime, b *orggrant.Bundle, name string, args json.RawMessage) (interface{}, error) {
	var grant *orggrant.Grant
	for i := range b.Grants {
		if t := b.Grants[i].Automation(); t != nil && t.Tool == name {
			grant = &b.Grants[i]
		}
	}
	if grant == nil {
		ledger := orgbridge.NewLedger(rt.db.DB)
		_ = ledger.Refuse(ctx, orgbridge.Call{ProfileID: b.ProfileID, Direction: orgbridge.DirRoleTool, OrgName: b.OrgName, RoleID: b.RoleID, GrantID: b.ID}, orgbridge.StatusRefusedGrant)
		return nil, fmt.Errorf("%s: %s is not granted to %s:%s — the grant changed; the tool list refreshes on your next turn", codeRefusedGrant, name, b.OrgName, b.RoleID)
	}
	tool := grant.Automation()
	if _, live := daemonhb.Read(); !live {
		return nil, fmt.Errorf("%s: automations run in `monoagentcli daemon`, which is not running — ask the operator to start it", codeDaemonRequired)
	}

	meta := metaFrom(ctx)
	runID := os.Getenv("MONOMIND_ORG_RUN")
	if runID == "" {
		runID = meta.Trace.Run
	}
	ledger := orgbridge.NewLedger(rt.db.DB)
	call := orgbridge.Call{
		ProfileID: b.ProfileID, Trace: orgbridge.Trace{ChainID: meta.Trace.ChainID, Hop: meta.Trace.Hop},
		OriginOrg: b.OrgName, Direction: orgbridge.DirRoleTool, OrgName: b.OrgName, RoleID: b.RoleID,
		WorkflowID: tool.WorkflowID, GrantID: grant.ID, RunID: runID,
	}
	// A cap that cannot be read refuses: these two counts are the only
	// quantity bound on a granted automation, and a transient read failure
	// (SQLite "database is locked" while the daemon writes) must not lift
	// it for that call. The role can call again on its next turn, which is
	// cheaper than holding its turn open on a retry loop here.
	if runID != "" {
		n, err := ledger.CountGrantCalls(ctx, grant.ID, runID, time.Time{})
		if err != nil {
			return nil, fmt.Errorf("%s: cannot read this run's call count for %s: %v", codeRefusedCap, name, err)
		}
		if n >= tool.MaxCallsPerRun {
			_ = ledger.Refuse(ctx, call, orgbridge.StatusRefusedCap)
			return nil, fmt.Errorf("%s: %s already ran %d times this org run (limit %d)", codeRefusedCap, name, n, tool.MaxCallsPerRun)
		}
	}
	n, err := ledger.CountGrantCalls(ctx, grant.ID, "", time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read the daily call count for %s: %v", codeRefusedCap, name, err)
	}
	if n >= tool.MaxCallsPerDay {
		_ = ledger.Refuse(ctx, call, orgbridge.StatusRefusedCap)
		return nil, fmt.Errorf("%s: %s already ran %d times in 24 hours (limit %d)", codeRefusedCap, name, n, tool.MaxCallsPerDay)
	}
	adm, err := ledger.Admit(ctx, call, orgLimitsFor(rt.db.DB, b))
	if err != nil {
		return nil, err
	}
	if !adm.OK() {
		return nil, fmt.Errorf("%s: %s", adm.Status, adm.Reason)
	}

	var input interface{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &input)
	}
	// C-46: the run's file nodes confine their paths to the role's workdir.
	// It sits under org, which the role's arguments (under input) cannot set.
	orgTrigger := map[string]interface{}{"name": b.OrgName, "role": b.RoleID, "grant": grant.ID, "run": runID,
		"automation": tool.Alias, "workdir": grantWorkdir(rt.db.DB, b)}
	exec, err := workflow.CreateUnownedExecution(ctx, rt.store, workflow.UnownedExecutionOptions{
		WorkflowID: tool.WorkflowID, ProfileID: b.ProfileID, TriggerType: workflow.TriggerTypeOrgTool, AllowInactive: true,
		TriggerData: map[string]interface{}{
			"trigger_type": workflow.TriggerTypeOrgTool,
			"org":          orgTrigger,
			"input":        input,
			"trace":        map[string]interface{}{"chain_id": adm.Trace.ChainID, "hop": adm.Trace.Hop},
		},
	})
	if err != nil {
		_ = ledger.SetStatus(ctx, adm.ID, orgbridge.StatusError)
		return nil, fmt.Errorf("start %s: %w", tool.Alias, err)
	}
	_ = ledger.SetExecution(ctx, adm.ID, exec.ID)

	if !tool.Wait || tool.Mode != "run" {
		return map[string]interface{}{"execution_id": exec.ID, "status": "queued"}, nil
	}
	wctx, cancel := context.WithTimeout(ctx, time.Duration(tool.Timeout)*time.Second)
	defer cancel()
	for {
		view, final, err := executionView(wctx, rt, exec.ID, tool.MaxOutputBytes)
		if err != nil {
			return nil, err
		}
		if final {
			return view, nil
		}
		select {
		case <-wctx.Done():
			view["note"] = fmt.Sprintf("still running after %ds; check it with automation_status", tool.Timeout)
			return view, nil
		case <-time.After(grantPollInterval):
		}
	}
}

func orgLimitsFor(db *sql.DB, b *orggrant.Bundle) orgbridge.Limits {
	var lim orgbridge.Limits
	doc, err := orgdesign.Load(profiledir.Root(db, b.ProfileID), b.OrgName)
	if err != nil {
		return lim
	}
	if v, ok := doc.RunConfig["max_hops"]; ok {
		_ = json.Unmarshal(v, &lim.MaxHops)
	}
	if v, ok := doc.RunConfig["max_repeats"]; ok {
		_ = json.Unmarshal(v, &lim.MaxRepeats)
	}
	// run_config is in the org JSON, which any role whose fileWrite reaches
	// `.monomind/` can edit — the reason grants, endpoints and autonomy live
	// in the database — so clamp here as well as in Limits.withDefaults,
	// which bounds every Admit caller.
	if lim.MaxHops > orgbridge.MaxHopsCeiling {
		lim.MaxHops = orgbridge.MaxHopsCeiling
	}
	if lim.MaxRepeats > orgbridge.MaxRepeatsCeiling {
		lim.MaxRepeats = orgbridge.MaxRepeatsCeiling
	}
	return lim
}

// executionView renders an execution for a role: status, bounded redacted
// outputs of its last finished node, and a pending HIL item when it paused
// for review. final is true when the role should stop waiting.
func executionView(ctx context.Context, rt *runtime, id string, maxBytes int) (map[string]interface{}, bool, error) {
	exec, err := rt.store.GetExecution(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if exec == nil {
		return nil, false, fmt.Errorf("execution %s not found", id)
	}
	view := map[string]interface{}{"execution_id": id, "status": strings.ToLower(exec.Status)}
	switch exec.Status {
	case "SUCCESS":
		node, text := lastOutput(exec, "")
		view["node"] = node
		if maxBytes <= 0 {
			maxBytes = orggrant.DefaultMaxOutputBytes
		}
		if len(text) > maxBytes {
			view["output"] = text[:maxBytes]
			view["truncated"] = true
			view["output_bytes"] = len(text)
			view["note"] = "output truncated; read the rest with automation_output"
		} else {
			view["output"] = text
		}
		return view, true, nil
	case "FAILED", "CANCELLED":
		view["error"] = exec.ErrorMessage
		return view, true, nil
	case "WAITING":
		var hilID, nodeName string
		err := rt.db.DB.QueryRowContext(ctx, `SELECT id, node_name FROM hil_pending WHERE execution_id = ? AND status = 'pending' LIMIT 1`, id).Scan(&hilID, &nodeName)
		if err == nil {
			view["hil"] = map[string]interface{}{"id": hilID, "hint": fmt.Sprintf("the run paused at %q for review by the operator or the org's decider; you cannot approve it", nodeName)}
			return view, true, nil
		}
	}
	return view, false, nil
}

// lastOutput returns a node's output items (the last finished successful
// node when node is ""), redacted, as JSON text.
func lastOutput(exec *workflow.WorkflowExecution, node string) (string, string) {
	var pick *workflow.WorkflowExecutionNode
	for i := range exec.Nodes {
		n := &exec.Nodes[i]
		if n.Status != "SUCCESS" || len(n.OutputItems) == 0 && node == "" {
			continue
		}
		if node != "" {
			if n.NodeName == node {
				pick = n
			}
			continue
		}
		if pick == nil || (n.FinishedAt != nil && pick.FinishedAt != nil && !n.FinishedAt.Before(*pick.FinishedAt)) {
			pick = n
		}
	}
	if pick == nil {
		return "", "[]"
	}
	items := make([]interface{}, 0, len(pick.OutputItems))
	for _, it := range pick.OutputItems {
		items = append(items, workflow.RedactItemJSON(it).JSON)
	}
	b, _ := json.Marshal(items)
	return pick.NodeName, string(b)
}

// ownsExecution reports whether exec was started through one of the
// bundle's grants — a role reads only runs its own grants started.
func ownsExecution(ctx context.Context, rt *runtime, b *orggrant.Bundle, id string) bool {
	var n int
	_ = rt.db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_bridge_calls c JOIN org_grants g ON g.id = c.grant_id
		 WHERE c.execution_id = ? AND g.profile_id = ? AND g.org_name = ? AND g.role_id = ?`,
		id, b.ProfileID, b.OrgName, b.RoleID).Scan(&n)
	return n > 0
}

func (s *Server) grantStatus(ctx context.Context, rt *runtime, b *orggrant.Bundle, args json.RawMessage) (interface{}, error) {
	var a struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ExecutionID == "" || !ownsExecution(ctx, rt, b, a.ExecutionID) {
		return nil, fmt.Errorf("%s: execution %q was not started by your automations", codeRefusedGrant, a.ExecutionID)
	}
	max := orggrant.DefaultMaxOutputBytes
	for _, g := range b.Grants {
		if t := g.Automation(); t != nil && t.MaxOutputBytes > 0 {
			max = t.MaxOutputBytes
		}
	}
	view, _, err := executionView(ctx, rt, a.ExecutionID, max)
	return view, err
}

func (s *Server) grantOutput(ctx context.Context, rt *runtime, b *orggrant.Bundle, args json.RawMessage) (interface{}, error) {
	var a struct {
		ExecutionID string `json:"execution_id"`
		Node        string `json:"node"`
		Offset      int    `json:"offset"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ExecutionID == "" || !ownsExecution(ctx, rt, b, a.ExecutionID) {
		return nil, fmt.Errorf("%s: execution %q was not started by your automations", codeRefusedGrant, a.ExecutionID)
	}
	exec, err := rt.store.GetExecution(ctx, a.ExecutionID)
	if err != nil || exec == nil {
		return nil, fmt.Errorf("execution %s not found", a.ExecutionID)
	}
	node, text := lastOutput(exec, a.Node)
	if a.Offset < 0 || a.Offset > len(text) {
		a.Offset = len(text)
	}
	end := a.Offset + orggrant.DefaultMaxOutputBytes
	if end > len(text) {
		end = len(text)
	}
	out := map[string]interface{}{"execution_id": a.ExecutionID, "node": node, "offset": a.Offset, "total_bytes": len(text), "chunk": text[a.Offset:end]}
	if end < len(text) {
		out["next_offset"] = end
	}
	return out, nil
}
