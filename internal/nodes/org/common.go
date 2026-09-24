package org

import (
	"context"
	"database/sql"
	"fmt"

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
// Only a run whose trigger data carries a trace mono-agent wrote itself
// (workflow.CarriesChain: org-started runs, and webhook runs whose trace the
// server set from a verified signed header) continues a chain. Any other
// run (a manual run with input, a schedule) starts a fresh chain whatever
// `trace` its data holds: otherwise a caller could join someone else's
// chain and push its hop to the limit, or keep a loop on a chain of its
// choosing.
//
// The chain is the one in the trigger data. The node's first input item
// may carry a later hop on that chain (a previous org node's output), and
// then the higher hop is taken; a trace on the item naming another chain is
// ignored, since items can hold outside data (an HTTP response lifted to
// the top level) and that would let the outside pick the chain again. With
// no trace in the trigger data the item's is used (except in a webhook run,
// whose first item is the request body), and failing both a fresh chain
// starts. Losing the chain would start a new one on every loop
// iteration and defeat the hop limit (U10).
func incomingTrace(ctx context.Context, item workflow.Item) orgbridge.Trace {
	tt := workflow.TriggerTypeFrom(ctx)
	if !workflow.CarriesChain(tt) {
		return orgbridge.Trace{}
	}
	if chain, hop, ok := workflow.RunTrace(ctx, item.JSON); ok {
		return orgbridge.Trace{ChainID: chain, Hop: hop}
	}
	// A webhook's first item is the request body: its `trace` is the
	// sender's data, never a chain.
	if chain, hop, ok := workflow.ParseTrace(item.JSON); ok && tt != workflow.TriggerNodeTypeWebhook {
		return orgbridge.Trace{ChainID: chain, Hop: hop}
	}
	return orgbridge.Trace{}
}

// orgLimits reads run_config.max_hops / max_repeats from the org
// (orgbridge.LimitsFor, which Admit clamps to the ceilings).
func orgLimits(root, org string) orgbridge.Limits { return orgbridge.LimitsFor(root, org) }

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
