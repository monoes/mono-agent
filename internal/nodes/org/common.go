package org

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/monoes/mono-agent/internal/workflow"
)

// orgEnv is what every org node resolves from its execution context.
type orgEnv struct {
	db        *sql.DB
	profileID string
	root      string
}

func resolveOrgEnv(ctx context.Context, node string) (*orgEnv, error) {
	db := vault.DBFromContext(ctx)
	profileID := vault.ProfileIDFromContext(ctx)
	if profileID == "" {
		profileID = "default"
	}
	if err := profiledir.EnsureLayout(db, profileID); err != nil {
		return nil, fmt.Errorf("%s: could not prepare profile folder for %q: %w", node, profileID, err)
	}
	return &orgEnv{db: db, profileID: profileID, root: profiledir.Root(db, profileID)}, nil
}

// automationRoleFor returns the org's automation role that runs workflowID,
// or nil when the workflow is not one of the org's automation roles.
func automationRoleFor(root, org, workflowID string) *orgdesign.Role {
	doc, err := orgdesign.Load(root, org)
	if err != nil {
		return nil
	}
	for i := range doc.Roles {
		r := &doc.Roles[i]
		if r.IsEndpoint() && r.Automation != nil && r.Automation.WorkflowID == workflowID {
			return r
		}
	}
	return nil
}

// senderFor is the address a workflow node sends as: the automation role
// when the workflow is one of that org's automation roles, otherwise
// "workflow:<first 8 chars of the execution id>".
func senderFor(root, org string, input workflow.NodeInput) string {
	if r := automationRoleFor(root, org, input.WorkflowID); r != nil {
		return org + ":" + r.ID
	}
	id := input.ExecutionID
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		id = "unknown"
	}
	return "workflow:" + id
}

// incomingTrace continues the chain of whatever started this execution.
// Only a run mono-agent's org side started carries a trace it wrote. Any
// other run (a webhook, a manual run with input, a schedule) starts a fresh
// chain whatever `trace` its data holds: otherwise a webhook caller could
// join someone else's chain and push its hop to the limit, or keep a loop
// on a chain of its choosing.
//
// In an org-started run the chain is the one in the trigger data, which
// mono-agent wrote. The node's first input item may carry a later hop on
// that chain (a previous org node's output), and then the higher hop is
// taken; a trace on the item naming another chain is ignored, since items
// can hold outside data (an HTTP response lifted to the top level) and that
// would let the outside pick the chain again. With no trace in the trigger
// data the item's is used, and failing both a fresh chain starts. Losing
// the chain would start a new one on every loop iteration and defeat the
// hop limit (U10).
func incomingTrace(ctx context.Context, item workflow.Item) orgbridge.Trace {
	if !workflow.OrgStarted(workflow.TriggerTypeFrom(ctx)) {
		return orgbridge.Trace{}
	}
	fromItem, itemOK := traceField(item.JSON)
	fromTrigger, triggerOK := traceField(workflow.TriggerDataFrom(ctx))
	switch {
	case triggerOK && itemOK && fromItem.ChainID == fromTrigger.ChainID && fromItem.Hop > fromTrigger.Hop:
		return fromItem
	case triggerOK:
		return fromTrigger
	case itemOK:
		return fromItem
	}
	return orgbridge.Trace{}
}

func traceField(m map[string]interface{}) (orgbridge.Trace, bool) {
	t, ok := m["trace"].(map[string]interface{})
	if !ok {
		return orgbridge.Trace{}, false
	}
	chain, _ := t["chain_id"].(string)
	var hop int
	switch h := t["hop"].(type) {
	case float64:
		hop = int(h)
	case int:
		hop = h
	}
	if strings.HasPrefix(chain, "chn_") && hop >= 0 {
		return orgbridge.Trace{ChainID: chain, Hop: hop}, true
	}
	return orgbridge.Trace{}, false
}

// orgLimits reads run_config.max_hops / max_repeats from the org. The org
// JSON is writable by any role whose fileWrite reaches .monomind/, so these
// are requests, not guarantees: Limits.withDefaults clamps them to
// orgbridge.MaxHopsCeiling / MaxRepeatsCeiling before Admit uses them.
func orgLimits(root, org string) orgbridge.Limits {
	var lim orgbridge.Limits
	doc, err := orgdesign.Load(root, org)
	if err != nil {
		return lim
	}
	if v, ok := doc.RunConfig["max_hops"]; ok {
		fmt.Sscan(string(v), &lim.MaxHops)
	}
	if v, ok := doc.RunConfig["max_repeats"]; ok {
		fmt.Sscan(string(v), &lim.MaxRepeats)
	}
	return lim
}

func configBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key].(bool); ok {
		return v
	}
	return def
}

func configInt(config map[string]interface{}, key string, def int) int {
	switch v := config[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}
