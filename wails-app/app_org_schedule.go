package main

import (
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// Schedule audit of an org (monomind 2.24+): scheduled ticks that did not
// start a run. Built by `monoagentcli org schedule-audit`, like
// app_org_documents.go; no internal/monomind import here.

func scheduleAuditArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"schedule-audit", org}, nil
}

// GetOrgScheduleAudit returns `org schedule-audit <org>`'s JSON
// (orgbridge.ScheduleAuditView) or {"error"}.
func (a *App) GetOrgScheduleAudit(org string) string {
	sub, err := scheduleAuditArgs(org)
	return a.runOrgSub("schedule-audit", sub, err)
}

// SetOrgSchedule sets (or, with "", clears) the org's run interval — "30s",
// "15m", "2h". Allowed on a sections org too: monomind starts every tick as a
// fresh run. Returns the saved org like UpdateOrgRole, or {"error"}.
func (a *App) SetOrgSchedule(org, schedule string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, org)
	if err != nil {
		return aiError(err)
	}
	if err := d.SetSchedule(schedule); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}
