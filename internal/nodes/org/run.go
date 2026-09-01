// Package org provides a workflow node that runs a monomind agent
// organization as one step in an automation and waits for it to finish.
package org

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/monoes/mono-agent/internal/workflow"
)

var templatePattern = regexp.MustCompile(`\{\{\$json\.(\w+)\}\}`)

// orgStatus mirrors the fields of `monomind org status <name> --format
// json` that this node cares about (org.ts:740-784's statusAction
// response) — everything else in that payload is opaque and unused here.
type orgStatus struct {
	Status   string `json:"status"`
	ClosedBy string `json:"closed_by"`
	Error    string `json:"error"`
}

// OrgRunNode starts (or resumes waiting on) a monomind org run and pauses
// the workflow execution until the org's boss/root role signals
// completion. "closed_by == org-complete" is, by construction on the
// monomind side, only ever set by the boss role's org_complete tool call
// (that tool is only registered on the boss role's session) — so this
// node's completion is inherently gated on the boss role, with no extra
// role-detection logic needed here.
//
// Config fields:
//
//	"org_name"   (string, required): the target org's name.
//	"task"       (string): initial message/goal override for this run.
//	  Supports {{$json.FIELD}} placeholders, expanded from the first input
//	  item — one org run corresponds to one node invocation, not one per item.
//	"output_key" (string): key to store the run report under (default "org_result").
//
// Resilience: idempotency is derived purely from the org's own live status
// (no separate pending-run table) — if it's already "running" this node
// just keeps waiting; if idle, it starts a run. This is intentionally
// simple rather than restart-proof like core.human_in_loop's DB-row
// tracking: the workflow engine's resume checks are several seconds apart
// in practice, so the check-then-start race is a non-issue day to day.
type OrgRunNode struct{}

func (n *OrgRunNode) Type() string { return "org.run" }

func (n *OrgRunNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	orgName := configString(config, "org_name", "")
	if orgName == "" {
		return nil, fmt.Errorf("%w: org.run node requires \"org_name\"", workflow.ErrInvalidConfig)
	}
	taskTemplate := configString(config, "task", "")
	outputKey := configString(config, "output_key", "org_result")

	db := vault.DBFromContext(ctx)
	profileID := vault.ProfileIDFromContext(ctx)
	if profileID == "" {
		profileID = "default"
	}
	if err := profiledir.EnsureLayout(db, profileID); err != nil {
		return nil, fmt.Errorf("org.run: could not prepare profile folder for %q: %w", profileID, err)
	}
	root := profiledir.Root(db, profileID)

	raw, err := monomind.OrgStatus(ctx, root, orgName)
	if err != nil {
		return nil, fmt.Errorf("org.run (%s): %w", orgName, err)
	}
	var st orgStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("org.run (%s): unreadable status: %w", orgName, err)
	}

	switch {
	case st.ClosedBy == "org-complete":
		report, err := monomind.OrgReport(ctx, root, orgName, false)
		if err != nil {
			return nil, fmt.Errorf("org.run (%s): completed but report fetch failed: %w", orgName, err)
		}
		var reportVal interface{}
		if err := json.Unmarshal(report, &reportVal); err != nil {
			reportVal = string(report)
		}
		outJSON := copyItemJSON(firstItem(input.Items))
		outJSON[outputKey] = reportVal
		outJSON["_org_closed_by"] = st.ClosedBy
		return []workflow.NodeOutput{{Handle: "main", Items: []workflow.Item{{JSON: outJSON}}}}, nil

	case st.ClosedBy != "":
		return nil, fmt.Errorf("org.run (%s): run ended without completing (closed_by=%s)", orgName, st.ClosedBy)

	case st.Status == "crashed":
		msg := st.Error
		if msg == "" {
			msg = "org crashed"
		}
		return nil, fmt.Errorf("org.run (%s): %s", orgName, msg)

	case st.Status == "running":
		return nil, workflow.ErrNodePaused

	default:
		task := expandTemplate(taskTemplate, firstItem(input.Items))
		if err := monomind.OrgRunStart(ctx, root, orgName, task); err != nil {
			return nil, fmt.Errorf("org.run (%s): %w", orgName, err)
		}
		return nil, workflow.ErrNodePaused
	}
}

func firstItem(items []workflow.Item) workflow.Item {
	if len(items) == 0 {
		return workflow.Item{}
	}
	return items[0]
}

// expandTemplate replaces {{$json.KEY}} placeholders in template with values from item.JSON.
func expandTemplate(template string, item workflow.Item) string {
	return templatePattern.ReplaceAllStringFunc(template, func(match string) string {
		parts := templatePattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		val, ok := item.JSON[parts[1]]
		if !ok {
			return match
		}
		return fmt.Sprintf("%v", val)
	})
}

// configString extracts a non-empty string value from config.
func configString(config map[string]interface{}, key, defaultVal string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return defaultVal
}

// copyItemJSON creates a shallow copy of an item's JSON map.
func copyItemJSON(item workflow.Item) map[string]interface{} {
	m := make(map[string]interface{}, len(item.JSON)+2)
	for k, v := range item.JSON {
		m[k] = v
	}
	return m
}

// RegisterAll registers the org node types into the given registry.
func RegisterAll(r *workflow.NodeTypeRegistry) {
	r.Register("org.run", func() workflow.NodeExecutor { return &OrgRunNode{} })
}
