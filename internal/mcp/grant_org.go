package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/orggroup"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// Org tools a holding org's Initiator holds through an org-tool grant
// (plan §7.5): each is scoped to the orgs listed in its grant — the holding
// org's children. Messaging needs no tool here: every role already has
// monomind's org_send.

// orgToolFuncs are replaced in tests.
var (
	orgRunStartFunc = monomind.OrgRunStart
	orgStopFunc     = monomind.OrgStop
	orgStatusFunc   = monomind.OrgStatus
	orgReportFunc   = func(ctx context.Context, root, name, run string) (json.RawMessage, error) {
		return monomind.OrgReport(ctx, root, name, false, run)
	}
	groupEnvFunc = orggroup.NewEnv
)

func orgToolScope(b *orggrant.Bundle, tool string) []string {
	for _, g := range b.Grants {
		for _, ot := range g.OrgTools {
			if ot.Tool == tool {
				return ot.Orgs
			}
		}
	}
	return nil
}

func orgToolDefinitions(b *orggrant.Bundle) []map[string]interface{} {
	descs := map[string]string{
		orggroup.ToolOrgStart:  "Start a child org's run with a task. Needs a decision unless your operator made it routine; refused when the group budget is spent.",
		orggroup.ToolOrgStop:   "Ask a running child org to stop.",
		orggroup.ToolOrgStatus: "Status of a child org.",
		orggroup.ToolOrgReport: "Report of a child org's run (default: its latest run).",
	}
	var out []map[string]interface{}
	for _, name := range orggroup.InitiatorTools {
		orgs := orgToolScope(b, name)
		if len(orgs) == 0 {
			continue
		}
		props := map[string]interface{}{"org": map[string]interface{}{"type": "string", "enum": orgs, "description": "Child org"}}
		required := []string{"org"}
		switch name {
		case orggroup.ToolOrgStart:
			props["task"] = strParam("What the child org should do this run")
		case orggroup.ToolOrgReport:
			props["run"] = strParam("Run id (default: latest)")
		}
		out = append(out, map[string]interface{}{"name": name, "description": descs[name], "inputSchema": objSchema(props, required...)})
	}
	return out
}

// callOrgTool serves an Initiator tool; handled reports whether name is one.
func (s *Server) callOrgTool(ctx context.Context, rt *runtime, b *orggrant.Bundle, name string, args json.RawMessage) (interface{}, bool, error) {
	scope := orgToolScope(b, name)
	if scope == nil || !containsStr(orggroup.InitiatorTools, name) {
		return nil, false, nil
	}
	var a struct {
		Org  string `json:"org"`
		Task string `json:"task"`
		Run  string `json:"run"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, true, err
	}
	if !containsStr(scope, a.Org) {
		return nil, true, fmt.Errorf("%s: %s may only act on %v", codeRefusedGrant, name, scope)
	}
	root := profiledir.Root(rt.db.DB, b.ProfileID)
	switch name {
	case orggroup.ToolOrgStatus:
		raw, err := orgStatusFunc(ctx, root, a.Org)
		return json.RawMessage(raw), true, err
	case orggroup.ToolOrgReport:
		raw, err := orgReportFunc(ctx, root, a.Org, a.Run)
		return json.RawMessage(raw), true, err
	case orggroup.ToolOrgStop:
		out, err := orgStopFunc(ctx, root, a.Org)
		return map[string]interface{}{"org": a.Org, "stopping": err == nil, "output": out}, true, err
	case orggroup.ToolOrgStart:
		if err := groupEnvFunc(rt.db.DB, b.ProfileID, root).CheckStart(ctx, b.OrgName, a.Org); err != nil {
			_ = orgbridge.NewLedger(rt.db.DB).Refuse(ctx, orgbridge.Call{ProfileID: b.ProfileID, Direction: orgbridge.DirOrgStart, OrgName: a.Org, RoleID: b.RoleID, GrantID: b.ID, OriginOrg: b.OrgName}, orgbridge.StatusRefusedCap)
			return nil, true, fmt.Errorf("%s: %v", codeRefusedCap, err)
		}
		meta := metaFrom(ctx)
		runID := os.Getenv("MONOMIND_ORG_RUN")
		if runID == "" {
			runID = meta.Trace.Run
		}
		ledger := orgbridge.NewLedger(rt.db.DB)
		adm, err := ledger.Admit(ctx, orgbridge.Call{
			ProfileID: b.ProfileID, Trace: orgbridge.Trace{ChainID: meta.Trace.ChainID, Hop: meta.Trace.Hop},
			OriginOrg: b.OrgName, Direction: orgbridge.DirOrgStart, OrgName: a.Org, RoleID: b.RoleID, GrantID: b.ID, RunID: runID,
		}, orgLimitsFor(rt.db.DB, b))
		if err != nil {
			return nil, true, err
		}
		if !adm.OK() {
			return nil, true, fmt.Errorf("%s: %s", adm.Status, adm.Reason)
		}
		task := a.Task
		if task != "" {
			task = orgbridge.WithTrace(task, adm.Trace)
		}
		if err := orgRunStartFunc(ctx, root, a.Org, task); err != nil {
			_ = ledger.SetStatus(ctx, adm.ID, orgbridge.StatusError)
			return nil, true, err
		}
		_, daemonLive := daemonhb.Read()
		return map[string]interface{}{"org": a.Org, "started": true, "trace": adm.Trace, "daemon_running": daemonLive}, true, nil
	}
	return nil, false, nil
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
