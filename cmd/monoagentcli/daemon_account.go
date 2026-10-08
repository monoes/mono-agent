package main

import (
	"context"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/workflow"
)

// The daemon's locked mode (spec D8, section 6.4). While the monoes.me account is
// locked the daemon stays up, because launchd's KeepAlive would respawn an exiting
// one in a loop, and starts nothing: the engine refuses executions and drops
// triggers by itself (internal/workflow), the doors refuse, and this supervisor
// stops the org services. It resumes by itself when a valid session appears: a
// sign-in from the CLI or the app writes session.json and the guard reads it.

// orgStopWait bounds how long the supervisor waits for the org services to end,
// when the account locks and before it starts them again. Their loops end with
// their context; the longest thing in one is an agent turn, killed within seconds.
const orgStopWait = 30 * time.Second

// accountSupervisor starts and stops the org services as the account verdict
// changes, and cancels the running executions when monoes.me refused the account.
// Its dependencies are fields so that a test can drive it.
type accountSupervisor struct {
	status        func() account.Status            // account.CurrentStatus in the daemon
	poll          time.Duration                    // account.PollInterval in the daemon
	startOrg      func(ctx context.Context)        // launches the org services; they end when ctx ends
	waitOrg       func(timeout time.Duration) bool // whether everything startOrg launched has ended
	cancelRunning func() int                       // cancels the engine's running executions
	logf          func(format string, args ...any)
}

func newAccountSupervisor(orgs *orgServices, engine *workflow.WorkflowEngine) *accountSupervisor {
	return &accountSupervisor{
		status:        account.CurrentStatus,
		poll:          account.PollInterval,
		startOrg:      func(ctx context.Context) { orgs.start(ctx, engine) },
		waitOrg:       orgs.wait,
		cancelRunning: engine.CancelRunning,
		logf:          orgs.logf,
	}
}

// run applies the verdict at once and then every poll, until ctx ends. It never
// returns because of a lock. A daemon that starts locked never starts the org
// services; one that is locked later stops them and starts them again when the
// account is valid. A refusal (and only a refusal: at the 24 hours, expired, the
// engine ends each run at its next node) also cancels what the engine is running,
// once per lock.
func (s *accountSupervisor) run(ctx context.Context) {
	var (
		stop      context.CancelFunc // non-nil while the org services run
		ran       bool               // the org services ran at some point, so a stop may still be under way
		reported  bool               // this lock has been logged
		cancelled bool               // this lock has cancelled the running executions
	)
	apply := func(st account.Status) {
		if st.Allowed() {
			if reported {
				s.logf("daemon: the monoes.me account is valid again; the org services start again")
			}
			reported, cancelled = false, false
			if stop != nil {
				return
			}
			if ran && !s.waitOrg(orgStopWait) {
				s.logf("daemon: the org services are still stopping; they start again at the next check")
				return
			}
			orgCtx, cancel := context.WithCancel(ctx)
			stop, ran = cancel, true
			s.startOrg(orgCtx)
			return
		}
		// The running executions are cancelled before the wait for the org
		// services, which can last orgStopWait: a refused account runs nothing.
		if st.Reason == account.ReasonRefused && !cancelled {
			cancelled = true
			if n := s.cancelRunning(); n > 0 {
				s.logf("daemon: monoes.me refused this account: cancelled %d running execution(s)", n)
			}
		}
		if stop != nil {
			stop()
			stop = nil
			s.waitOrg(orgStopWait)
		}
		if !reported {
			reported = true
			s.logf("%s", daemonLockedLine(st))
		}
	}

	apply(s.status())
	t := time.NewTicker(s.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			apply(s.status())
		}
	}
}

// daemonLockedLine is what a locked daemon logs, once per lock: why, and the
// exact command that ends it (spec section 9).
func daemonLockedLine(st account.Status) string {
	fix := "run: monoagentcli account login   (without a browser: monoagentcli account login --email <address>)"
	if st.Reason == account.ReasonKeyUnknown {
		fix = "run: monoagentcli update"
	}
	return fmt.Sprintf("daemon: the monoes.me account is locked (%s): nothing runs and the org services are stopped until a login is valid again; %s", st.Reason, fix)
}

// daemonRunningLine is the line the daemon prints once it is up. A locked one
// says so, since "triggers are live" would be untrue.
func daemonRunningLine(st account.Status) string {
	if st.Allowed() {
		return "Daemon running. Active workflows' triggers are live."
	}
	return "Daemon running, but the monoes.me account is locked (" + string(st.Reason) + "): nothing runs until a login is valid again."
}
