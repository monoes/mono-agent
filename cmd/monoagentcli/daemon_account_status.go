package main

import (
	"context"
	"database/sql"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/workflow"
)

// heartbeatAccount is the account verdict as the heartbeat file carries it. It
// is nil while the gate is dormant (no enforcement date: nothing to report, D22).
func heartbeatAccount(st account.Status) *daemonhb.AccountState {
	if st.EnforceFrom.IsZero() {
		return nil
	}
	return &daemonhb.AccountState{State: string(st.State), Reason: string(st.Reason), ValidUntil: st.ValidUntil, Enforced: st.Enforced}
}

// daemonHeartbeatRefresh is what the daemon updates in its heartbeat before every
// write: the schedules, and the account verdict, so that the desktop and doctor
// see "locked" within one heartbeat interval.
func daemonHeartbeatRefresh(engine *workflow.WorkflowEngine) func(*daemonhb.Heartbeat) {
	return func(hb *daemonhb.Heartbeat) {
		hb.Schedules = heartbeatSchedules(engine.ScheduledRuns())
		hb.Account = heartbeatAccount(account.CurrentStatus())
	}
}

// busyOrLocked is the Busy check of the daemon's automatic re-validation with
// the account folded in. A locked account starts nothing, and a model check that
// was refused would be stored as a failed validation, so the scheduler waits.
func busyOrLocked(ctx context.Context, db *sql.DB) (bool, string) {
	if !account.CurrentStatus().Allowed() {
		return true, "the monoes.me account is locked"
	}
	return appBusy(ctx, db)
}
