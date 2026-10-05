package autostart

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// RestartResult is what a restart of the daemon's service reports: `daemon restart --json` and
// the MCP tool api_config_apply.
type RestartResult struct {
	// Restarted: the service manager accepted the restart.
	Restarted bool `json:"restarted"`
	// Via is the service manager of this system: launchd, systemd or schtasks.
	Via string `json:"via"`
}

// ServiceManager names the service manager of this system with one token: launchd, systemd
// or schtasks.
func ServiceManager() string {
	switch runtime.GOOS {
	case "darwin":
		return "launchd"
	case "linux":
		return "systemd"
	case "windows":
		return "schtasks"
	}
	return runtime.GOOS
}

// NotRegisteredError is the error of a restart of a daemon that is not registered for
// auto-start: nothing manages it, so nothing can restart it.
type NotRegisteredError struct {
	// Detail is what Status said about why, "" when there is no registration at all.
	Detail string
}

func (e *NotRegisteredError) Error() string {
	msg := "the daemon is not registered for auto-start, so nothing can restart it: stop it and start `monoagentcli daemon` again, " +
		"or run `monoagentcli daemon install` to have the system manage it"
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// RestartRegistered restarts the daemon through the service manager it is registered with. It
// asks Status first, and a service that is not registered is a *NotRegisteredError and nothing
// is run. A restart interrupts whatever the daemon is running (workflows, org runs): whoever
// asks says so first.
func RestartRegistered(ctx context.Context, in Installer) (RestartResult, error) {
	registered, where := in.Status(ctx)
	if !registered {
		return RestartResult{}, &NotRegisteredError{Detail: where}
	}
	if err := in.Restart(ctx); err != nil {
		return RestartResult{}, err
	}
	return RestartResult{Restarted: true, Via: ServiceManager()}, nil
}

// waitFor polls done every `every` until it holds, the timeout passes or ctx ends.
func waitFor(ctx context.Context, done func() bool, timeout, every time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if done() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("still waiting after %v", timeout)
		case <-tick.C:
		}
	}
}

// endThenRun restarts a scheduled task, which has no restart of its own: end it (this fails
// harmlessly when it is not running), wait for the daemon to let go of its lock, run it. A run
// that raced the old process would start a daemon that cannot take the lock and exits, and the
// task has no keep-alive to try again.
func endThenRun(ctx context.Context, schtasks func(context.Context, ...string) ([]byte, error), waitStopped func(context.Context) error, task string) error {
	_, _ = schtasks(ctx, "/end", "/tn", task)
	if err := waitStopped(ctx); err != nil {
		return err
	}
	out, err := schtasks(ctx, "/run", "/tn", task)
	if err != nil {
		return fmt.Errorf("schtasks /run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
