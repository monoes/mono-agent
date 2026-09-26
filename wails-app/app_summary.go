// wails-app/app_summary.go
//
// Dashboard data. Everything here shells out to `monoagentcli … --json`:
// the CLI owns the facts (see app_orgs.go's header); this file only
// forwards them. No SQL.
package main

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

const (
	summaryCLITimeout    = 20 * time.Second
	orgSummaryCLITimeout = 60 * time.Second
)

// rawCLI runs `monoagentcli --profile <active> --json <args…>` and returns
// stdout verbatim, or {"error": …}.
func (a *App) rawCLI(timeout time.Duration, args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	full := append([]string{"--profile", a.getActiveProfileID(), "--json"}, args...)
	cmd := exec.CommandContext(ctx, cliBin, full...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return cliResultJSON(cliBin, out, err)
}

// GetSummary returns `monoagentcli summary --json` verbatim.
func (a *App) GetSummary() string { return a.rawCLI(summaryCLITimeout, "summary") }

// GetOrgSummary returns `monoagentcli org summary [--fast]` verbatim.
func (a *App) GetOrgSummary(fast bool) string {
	args := []string{"org", "summary"}
	if fast {
		args = append(args, "--fast")
	}
	return a.rawCLI(orgSummaryCLITimeout, args...)
}

// DashboardStats feeds the Sidebar and StatusBar (same JSON keys as before
// the dashboard moved to `summary`), now filled from `summary`.
type DashboardStats struct {
	ActiveSessions     int              `json:"active_sessions"`
	TotalWorkflows     int              `json:"total_workflows"`
	ExecutionsByStatus map[string]int   `json:"executions_by_status"`
	TotalPeople        int              `json:"total_people"`
	TotalLists         int              `json:"total_lists"`
	Sessions           []SessionSummary `json:"sessions"`
	DBPath             string           `json:"db_path"`
}

type SessionSummary struct {
	Platform string `json:"platform"`
	Username string `json:"username"`
	Expiry   string `json:"expiry"`
	Active   bool   `json:"active"`
}

// statsSummary is the slice of `summary --json` DashboardStats is built from.
type statsSummary struct {
	Workflows struct {
		Total int `json:"total"`
	} `json:"workflows"`
	Executions struct {
		Running int `json:"running"`
		Queued  int `json:"queued"`
	} `json:"executions"`
	People struct {
		Total int `json:"total"`
		Lists int `json:"lists"`
	} `json:"people"`
	Accounts struct {
		Active   int `json:"active"`
		Sessions []struct {
			Platform string `json:"platform"`
			Username string `json:"username"`
			Expiry   string `json:"expiry"`
			Status   string `json:"status"`
		} `json:"sessions"`
	} `json:"accounts"`
}

func (a *App) GetDashboardStats() DashboardStats {
	stats := DashboardStats{ExecutionsByStatus: map[string]int{}, DBPath: a.dbPath}
	var s statsSummary
	// On failure the Sidebar/StatusBar show zeros; the dashboard itself
	// reads GetSummary and surfaces the CLI's error there.
	if err := a.runMonoCLI("", &s, "summary", "--section", "workflows,executions,people,accounts"); err != nil {
		return stats
	}
	stats.TotalWorkflows = s.Workflows.Total
	stats.TotalPeople = s.People.Total
	stats.TotalLists = s.People.Lists
	stats.ActiveSessions = s.Accounts.Active
	stats.ExecutionsByStatus["RUNNING"] = s.Executions.Running
	stats.ExecutionsByStatus["QUEUED"] = s.Executions.Queued
	for _, r := range s.Accounts.Sessions {
		stats.Sessions = append(stats.Sessions, SessionSummary{Platform: r.Platform, Username: r.Username,
			Expiry: r.Expiry, Active: r.Status != "expired"})
	}
	return stats
}

// GetRecentExecutions lists recent runs across the profile, newest first
// (`workflow executions --all`).
func (a *App) GetRecentExecutions(limit int) ([]WorkflowExecutionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows := []WorkflowExecutionSummary{}
	if err := a.runMonoCLI("", &rows, "workflow", "executions", "--all", "--limit", strconv.Itoa(limit)); err != nil {
		return nil, err
	}
	return rows, nil
}
