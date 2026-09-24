package health

import (
	"context"
	"fmt"
	"time"
)

// GroupServices covers monoagent's long-running processes.
const GroupServices = "services"

const (
	CheckAutostart = "services.autostart"
	CheckDaemon    = "services.daemon"

	FixAutostart   = "services.autostart.install"
	FixDaemonStart = "services.daemon.start"
)

var daemonFeatures = []string{"schedules and triggers", "org automations", "extension bridge"}

func serviceChecks() []Check {
	return []Check{
		{ID: CheckAutostart, Group: GroupServices, Title: "Start at login", Run: checkAutostart},
		{ID: CheckDaemon, Group: GroupServices, Title: "Workflow daemon", Features: daemonFeatures, Run: checkDaemon},
	}
}

func serviceFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixAutostart, Label: "Start the daemon at login", Safety: SafetyConfirm,
			Command: "monoagentcli daemon install", Optional: true}, Apply: func(ctx context.Context, env *Env, progress func(string)) error {
			if env.InstallAutostart == nil {
				return fmt.Errorf("not available here")
			}
			return env.InstallAutostart(ctx, progress)
		}},
		{FixInfo: FixInfo{ID: FixDaemonStart, Label: "Start the workflow daemon", Safety: SafetyConfirm,
			Command: "monoagentcli daemon (in the background)"}, Apply: fixDaemonStart},
	}
}

func checkAutostart(ctx context.Context, env *Env) Result {
	if env.AutostartStatus == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	ok, where := env.AutostartStatus(ctx)
	if ok {
		return Result{Status: StatusOK, Summary: where}
	}
	// where, when set, says why an entry that exists doesn't count (a
	// unit file that systemd doesn't have enabled, a plist not loaded).
	return Result{Status: StatusInfo, Summary: "the daemon does not start at login", Detail: where, FixID: FixAutostart}
}

func checkDaemon(ctx context.Context, env *Env) Result {
	if env.Daemon == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	d := env.Daemon(ctx)
	if !d.Running {
		return Result{Status: StatusWarn, Summary: "not running", FixID: FixDaemonStart}
	}
	summary := fmt.Sprintf("pid %d, heartbeat %s ago", d.PID, (time.Duration(d.AgeMS) * time.Millisecond).Round(time.Second))
	if d.APIAddr != "" && env.APIHealth != nil {
		if err := env.APIHealth(ctx, d.APIAddr); err != nil {
			return Result{Status: StatusWarn, Summary: summary + " — its HTTP API does not answer", Detail: err.Error()}
		}
		summary += ", API " + d.APIAddr
	}
	return Result{Status: StatusOK, Summary: summary}
}

// fixDaemonStart starts the daemon unless it already runs: through the
// login service when one is registered (idempotent), else as a detached
// background process. Then waits for its heartbeat.
func fixDaemonStart(ctx context.Context, env *Env, progress func(string)) error {
	if env.Daemon == nil || env.StartDaemon == nil {
		return fmt.Errorf("starting the daemon is not available here")
	}
	if env.Daemon(ctx).Running {
		progress("the daemon is already running")
		return nil
	}
	if err := env.StartDaemon(ctx, progress); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if d := env.Daemon(ctx); d.Running {
			progress(fmt.Sprintf("daemon running (pid %d)", d.PID))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("the daemon was started but no heartbeat appeared within 15s — see ~/.monoagent/logs/daemon.log")
}
