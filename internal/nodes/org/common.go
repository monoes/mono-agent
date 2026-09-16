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

// incomingTrace continues the chain of whatever started this execution: a
// trace object on the first input item (trigger.org and grant tool calls
// put one there), else a fresh chain.
func incomingTrace(item workflow.Item) orgbridge.Trace {
	if m, ok := item.JSON["trace"].(map[string]interface{}); ok {
		chain, _ := m["chain_id"].(string)
		var hop int
		switch h := m["hop"].(type) {
		case float64:
			hop = int(h)
		case int:
			hop = h
		}
		if strings.HasPrefix(chain, "chn_") && hop >= 0 {
			return orgbridge.Trace{ChainID: chain, Hop: hop}
		}
	}
	return orgbridge.Trace{}
}

// orgLimits reads run_config.max_hops / max_repeats from the org.
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
