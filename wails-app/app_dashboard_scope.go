package main

// The dashboard's All profiles view: the same four reads as the profile
// view (GetSummary, GetOrgSummary, ListWorkflows, GetRecentExecutions),
// each with --all-profiles. The CLI runs the per-profile code for every
// profile and tags each row with profile_id/profile_name.

import (
	"strconv"

	"github.com/monoes/mono-agent/internal/workflow"
)

// GetGlobalSummary returns `summary --all-profiles --json` verbatim.
func (a *App) GetGlobalSummary() string {
	return a.rawCLI(summaryCLITimeout, "summary", "--all-profiles")
}

// GetGlobalOrgSummary returns `org summary --all-profiles [--fast]` verbatim.
func (a *App) GetGlobalOrgSummary(fast bool) string {
	args := []string{"org", "summary", "--all-profiles"}
	if fast {
		args = append(args, "--fast")
	}
	return a.rawCLI(orgSummaryCLITimeout, args...)
}

// ListAllProfilesWorkflows is ListWorkflows across every profile.
func (a *App) ListAllProfilesWorkflows() ([]WorkflowSummary, error) {
	var rows []struct {
		workflow.Workflow
		NodeCount   int    `json:"node_count"`
		ProfileName string `json:"profile_name"`
	}
	if err := a.runMonoCLI("", &rows, "workflow", "list", "--all-profiles"); err != nil {
		return nil, err
	}
	out := make([]WorkflowSummary, 0, len(rows))
	for _, r := range rows {
		s := workflowSummaryOf(&r.Workflow)
		s.NodeCount = r.NodeCount
		s.ProfileID = r.ProfileID
		s.ProfileName = r.ProfileName
		out = append(out, s)
	}
	return out, nil
}

// GetAllProfilesRecentExecutions is GetRecentExecutions across every profile.
func (a *App) GetAllProfilesRecentExecutions(limit int) ([]WorkflowExecutionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows := []WorkflowExecutionSummary{}
	if err := a.cliJSON(summaryCLITimeout, &rows, "workflow", "executions", "--all", "--all-profiles", "--limit", strconv.Itoa(limit)); err != nil {
		return nil, err
	}
	return rows, nil
}
