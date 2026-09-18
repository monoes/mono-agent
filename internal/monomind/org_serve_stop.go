package monomind

import (
	"context"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// serveStopGrace is how long OrgServeStop waits after SIGTERM before it
// kills the serve daemon's process group outright.
var serveStopGrace = 10 * time.Second

// OrgServeStop stops the `monomind org serve` daemon that owns projectRoot,
// found through its heartbeat (monomind has no `org serve --stop`). It
// sends SIGTERM to the daemon's process group — OrgServeStart puts it in
// its own, so agent-CLI grandchildren go with it — waits up to
// serveStopGrace, then kills the group. It returns the daemon's pid and
// whether one was running; a missing or stale heartbeat is "not running",
// and its pid is never signalled since it may belong to another process by
// now. Used when a profile's folder moves or the profile goes away (C-24).
func OrgServeStop(ctx context.Context, projectRoot string) (pid int, stopped bool, err error) {
	hb, live := ReadServeHeartbeat(projectRoot)
	if !live {
		return 0, false, nil
	}
	pid = hb.PID
	if err := signalServe(pid, false); err != nil {
		return pid, false, fmt.Errorf("stop org serve (pid %d): %w", pid, err)
	}
	if waitServeExit(ctx, pid, serveStopGrace) {
		return pid, true, nil
	}
	if err := signalServe(pid, true); err != nil {
		return pid, false, fmt.Errorf("kill org serve (pid %d): %w", pid, err)
	}
	if !waitServeExit(ctx, pid, 5*time.Second) {
		return pid, false, fmt.Errorf("org serve (pid %d) did not exit", pid)
	}
	return pid, true, nil
}

func waitServeExit(ctx context.Context, pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if !daemonhb.ProcessAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}
