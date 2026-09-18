package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Profile folder moves and orgs (plan C-24)
//
// A profile's orgs run out of its folder: `monomind org serve` watches
// <root>/.monomind/orgs/ and the org files carry tool-provider blocks written
// for this install. MoveProfileFolder therefore stops the old folder's orgs
// and org daemon before any file moves, and afterwards reconciles the org
// files and restarts the daemon at the new folder when one was running. As
// with every org binding, all of it goes through `monoagentcli` (never
// internal/monomind).
// ─────────────────────────────────────────────────────────────────────────────

// orgServeStopResult is `org serve --stop`'s JSON.
type orgServeStopResult struct {
	Error       string   `json:"error"`
	PID         int      `json:"pid"`
	Status      string   `json:"status"`
	StoppedOrgs []string `json:"stopped_orgs"`
	Warnings    []string `json:"warnings"`
}

// parseServeStop reads `org serve --stop`'s output as returned by
// runUnifiedCLI. wasRunning is true when a daemon was stopped, so the move
// knows to start one at the new folder.
func parseServeStop(res string) (*orgServeStopResult, bool, error) {
	var out orgServeStopResult
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		return nil, false, fmt.Errorf("unreadable org serve --stop output: %w", err)
	}
	if out.Error != "" {
		return &out, false, fmt.Errorf("%s", out.Error)
	}
	return &out, out.Status == "stopped", nil
}

// stopProfileOrgs stops every org running out of profileID's current folder
// and that folder's org daemon. An error means something may still be
// running there, and the folder must not be moved.
func (a *App) stopProfileOrgs(profileID string) (wasServing bool, err error) {
	res := a.runUnifiedCLI("org serve --stop", orgCLIArgs(profileID, "", []string{"serve", "--stop"}))
	out, wasServing, err := parseServeStop(res)
	if err != nil {
		return false, err
	}
	if len(out.StoppedOrgs) > 0 {
		a.emitLog("ORG", "WARN", fmt.Sprintf("profile %s: stopped org(s) %s to move the folder; start them again from Orgs", profileID, strings.Join(out.StoppedOrgs, ", ")))
	}
	for _, w := range out.Warnings {
		a.emitLog("ORG", "WARN", fmt.Sprintf("profile %s: %s", profileID, w))
	}
	return wasServing, nil
}

// resumeProfileOrgs runs once profiles.root_dir points at the new folder:
// it rewrites the org files' generated blocks from the grant rows, then
// restarts the org daemon there when one ran before the move. Failures are
// logged, not returned — the files have already moved, and both steps can be
// re-run by hand (`org reconcile`, `org serve`).
func (a *App) resumeProfileOrgs(profileID string, wasServing bool) {
	res := a.runUnifiedCLI("org reconcile", orgCLIArgs(profileID, "", []string{"reconcile"}))
	if msg, failed := cliFailure(res); failed {
		a.emitLog("ORG", "WARN", fmt.Sprintf("profile %s: reconciling org files after the move failed: %s (run `monoagentcli org reconcile`)", profileID, msg))
	}
	if !wasServing {
		return
	}
	res = a.runUnifiedCLI("org serve", orgCLIArgs(profileID, "", []string{"serve"}))
	if msg, failed := cliFailure(res); failed {
		a.emitLog("ORG", "WARN", fmt.Sprintf("profile %s: restarting the org daemon at the new folder failed: %s (run `monoagentcli org serve`)", profileID, msg))
	}
}

// cliFailure reports the top-level error of a runUnifiedCLI result, if any.
func cliFailure(res string) (string, bool) {
	var probe struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(res), &probe); err != nil {
		return "unreadable output: " + res, true
	}
	return probe.Error, probe.Error != ""
}
