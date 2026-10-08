package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/storage"
)

// A running daemon keeps the code it started with, so an update leaves the process that runs the
// schedules on the old version until it restarts. `update` and `update --app` therefore restart a
// registered daemon, unless an execution is in flight: an update never interrupts a run. The
// restart goes through the service manager in this process, not through the new binary's
// `daemon restart`, which is a gated command the new binary may refuse.

// daemonUpdate is what an update did about the daemon, in `update --app --json` as "daemon".
type daemonUpdate struct {
	// Action is restarted, busy (an execution is in flight), settings (the saved API settings cannot
	// be used), unknown (the database could not be read), not_registered or failed.
	Action   string `json:"action"`
	Running  string `json:"running_version,omitempty"` // the version the daemon runs, "" when its heartbeat names none
	InFlight int    `json:"in_flight,omitempty"`       // the executions that kept it from restarting
	Via      string `json:"via,omitempty"`             // the service manager that restarted it
	Message  string `json:"message"`
}

// restartBlockers reads the database the command is pointed at, opened without migrating it: how
// many executions the daemon with this pid owns, which a restart ends (RUNNING, and QUEUED rows it
// has claimed; a WAITING run is persisted and resumes), and whether the settings saved with
// `api config` can be used, which `daemon restart` checks first. A database that is absent or
// unreadable is err: the caller does not restart what it cannot see into. A variable for tests.
var restartBlockers = func(ctx context.Context, cfg *globalConfig, pid int) (inFlight int, settingsErr, err error) {
	path := expandPath(cfg.DBPath)
	if _, err = os.Stat(path); err != nil {
		return 0, nil, err
	}
	db, err := storage.NewDatabase(path)
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()
	err = db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workflow_executions WHERE pid = ? AND status IN ('RUNNING', 'QUEUED')`, pid).Scan(&inFlight)
	if err != nil {
		return 0, nil, err
	}
	_, settingsErr = savedSettings(ctx, db.DB)
	return inFlight, settingsErr, nil
}

// daemonAfterUpdate restarts the running daemon onto newVersion, or says why not. nil means there
// was nothing to do: no daemon runs, or it already runs newVersion. Only the daemon of this home's
// heartbeat is considered, and only through the service manager it is registered with.
func daemonAfterUpdate(ctx context.Context, cfg *globalConfig, newVersion string) *daemonUpdate {
	hb, live := daemonhb.Read()
	if !live || !health.DaemonVersionStale(hb.Version, newVersion) {
		return nil
	}
	was := hb.Version
	if was == "" {
		was = "an older version"
	}
	d := &daemonUpdate{Running: hb.Version}
	notRestarted := fmt.Sprintf("The daemon (pid %d) runs %s and was not restarted: ", hb.PID, was)
	const keeps = " Run `monoagentcli daemon restart` when that is settled; until then it keeps that version."

	n, settingsErr, err := restartBlockers(ctx, cfg, hb.PID)
	switch {
	case err != nil:
		d.Action, d.Message = "unknown", notRestarted+fmt.Sprintf("it could not be told whether a run is in flight (%v).", err)+keeps
		return d
	case n > 0:
		d.Action, d.InFlight = "busy", n
		d.Message = notRestarted + fmt.Sprintf("%d workflow execution(s) are running in it, and an update never interrupts a run.", n) + keeps
		return d
	case settingsErr != nil:
		d.Action = "settings"
		d.Message = notRestarted + fmt.Sprintf("the settings saved with `monoagentcli api config` cannot be used, and a daemon that cannot use them starts without the OpenAI-compatible API (%v).", settingsErr) + keeps
		return d
	}

	res, err := autostart.RestartRegistered(ctx, newInstaller())
	var notRegistered *autostart.NotRegisteredError
	switch {
	case errors.As(err, &notRegistered):
		d.Action = "not_registered"
		d.Message = fmt.Sprintf("The daemon (pid %d) runs %s and is not registered for auto-start, so nothing can restart it: stop it and start `monoagentcli daemon` again, or run `monoagentcli daemon install` to have the system manage it.", hb.PID, was)
	case err != nil:
		d.Action, d.Message = "failed", notRestarted+fmt.Sprintf("the service manager failed (%v).", err)+keeps
	default:
		d.Action, d.Via = "restarted", res.Via
		d.Message = fmt.Sprintf("Restarted the daemon through %s so that it runs %s (it ran %s).", res.Via, newVersion, was)
	}
	return d
}
