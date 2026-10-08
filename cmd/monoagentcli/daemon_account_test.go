package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

var supOK = account.Status{State: account.StateOK, Enforced: true}

func supLocked(r account.Reason) account.Status {
	return account.Status{State: account.StateLocked, Reason: r, Enforced: true}
}

// supRig is an accountSupervisor over fakes, running until its test ends.
type supRig struct {
	verdict atomic.Pointer[account.Status]
	done    chan struct{} // closed when run returns

	mu      sync.Mutex
	gens    []context.Context // one per start of the org services
	cancels int               // calls to cancelRunning
	logs    []string
	drained bool // what waitOrg answers
}

func (r *supRig) with(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

func newSupRig(t *testing.T, first account.Status) *supRig {
	t.Helper()
	r := &supRig{drained: true, done: make(chan struct{})}
	r.set(first)
	sup := &accountSupervisor{
		status:        func() account.Status { return *r.verdict.Load() },
		poll:          5 * time.Millisecond,
		startOrg:      func(ctx context.Context) { r.with(func() { r.gens = append(r.gens, ctx) }) },
		waitOrg:       func(time.Duration) (ended bool) { r.with(func() { ended = r.drained }); return ended },
		cancelRunning: func() int { r.with(func() { r.cancels++ }); return 2 },
		logf:          func(f string, a ...any) { r.with(func() { r.logs = append(r.logs, fmt.Sprintf(f, a...)) }) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { sup.run(ctx); close(r.done) }()
	t.Cleanup(func() { cancel(); <-r.done })
	return r
}

func (r *supRig) set(st account.Status) { r.verdict.Store(&st) }

// state: how many times the org services were started, whether the newest start
// is still live, and how many times the running executions were cancelled.
func (r *supRig) state() (starts int, live bool, cancels int) {
	r.with(func() {
		starts, cancels = len(r.gens), r.cancels
		live = starts > 0 && r.gens[starts-1].Err() == nil
	})
	return
}

func (r *supRig) logged(substr string) (n int) {
	r.with(func() {
		for _, l := range r.logs {
			if strings.Contains(l, substr) {
				n++
			}
		}
	})
	return n
}

func supUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}

// supSettle gives the supervisor several polls to do something it should not.
func supSettle() { time.Sleep(60 * time.Millisecond) }

// The supervisor follows the account: it starts the org services while the
// account is valid; at the 24 hours (expired) it stops them, cancels nothing
// (the engine ends each run at its next node), logs once and does not end the
// daemon; at the next sign-in it starts them again; on a refusal it stops them
// and cancels what the engine is running, once per lock.
func TestSupervisorFollowsTheAccount(t *testing.T) {
	rig := newSupRig(t, supOK)
	supUntil(t, "the org services start", func() bool { n, live, _ := rig.state(); return n == 1 && live })

	rig.set(supLocked(account.ReasonExpired))
	supUntil(t, "the lock is logged", func() bool { return rig.logged("account is locked (expired)") > 0 })
	supSettle()
	if n, live, c := rig.state(); live || c != 0 || n != 1 || rig.logged("account is locked (expired)") != 1 {
		t.Fatalf("org services live %v, %d cancels and %d lock lines at expiry, want stopped, 0 and 1", live, c, rig.logged("account is locked (expired)"))
	}
	select {
	case <-rig.done:
		t.Fatal("the supervisor returned because of the lock")
	default:
	}

	rig.set(supOK) // a sign-in from the CLI or the app
	supUntil(t, "the org services start again", func() bool { n, live, _ := rig.state(); return n == 2 && live })

	rig.set(supLocked(account.ReasonRefused))
	supUntil(t, "the refusal is handled", func() bool { return rig.logged("cancelled 2 running execution") > 0 })
	supSettle()
	if _, live, c := rig.state(); live || c != 1 || rig.logged("cancelled 2 running execution") != 1 {
		t.Fatalf("org services live %v, %d cancels and %d log lines across many polls of one refusal, want stopped, 1 and 1", live, c, rig.logged("cancelled 2 running execution"))
	}

	rig.set(supOK)
	supUntil(t, "the org services start again", func() bool { n, live, _ := rig.state(); return n == 3 && live })
	rig.set(supLocked(account.ReasonRefused))
	supUntil(t, "the second refusal cancels again", func() bool { _, _, c := rig.state(); return c == 2 })
}

// A daemon that is locked when it starts never starts the org services and says
// how to fix it; it starts them by itself when a valid session appears.
func TestSupervisorStartedLockedWaitsForTheLogin(t *testing.T) {
	rig := newSupRig(t, supLocked(account.ReasonNotLoggedIn))
	supUntil(t, "the lock is logged with its command", func() bool { return rig.logged("run: monoagentcli account login") == 1 })
	supSettle()
	if n, _, c := rig.state(); n != 0 || c != 0 {
		t.Fatalf("%d starts and %d cancels while locked at start, want none", n, c)
	}

	rig.set(supOK)
	supUntil(t, "the org services start", func() bool { n, live, _ := rig.state(); return n == 1 && live })
}

// Before the enforcement date a status that says locked is still allowed: the
// org services start, once, and keep running.
func TestSupervisorLeavesTheOrgServicesAloneBeforeTheEnforcementDate(t *testing.T) {
	rig := newSupRig(t, account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn})
	supUntil(t, "the org services start", func() bool { n, _, _ := rig.state(); return n == 1 })
	supSettle()
	if n, live, c := rig.state(); n != 1 || !live || c != 0 || rig.logged("locked") != 0 {
		t.Fatalf("%d starts, live %v, %d cancels, %d lock lines: want one live start and nothing said", n, live, c, rig.logged("locked"))
	}
}

// A start after a stop waits until the previous org services have ended, so two
// generations never overlap.
func TestSupervisorWaitsForTheOldOrgServicesBeforeStartingNew(t *testing.T) {
	rig := newSupRig(t, supOK)
	supUntil(t, "the org services start", func() bool { _, live, _ := rig.state(); return live })
	setDrained := func(v bool) { rig.with(func() { rig.drained = v }) }

	setDrained(false) // the old services are still winding down
	rig.set(supLocked(account.ReasonExpired))
	supUntil(t, "the org services stop", func() bool { _, live, _ := rig.state(); return !live })
	rig.set(supOK)
	supSettle()
	if n, _, _ := rig.state(); n != 1 {
		t.Fatalf("%d starts while the old services are still ending, want 1", n)
	}

	setDrained(true)
	supUntil(t, "the new generation starts", func() bool { n, live, _ := rig.state(); return n == 2 && live })
}

// What the daemon says: the lock line names the command that ends it (update,
// for an unknown key), and a locked daemon does not claim live triggers.
func TestDaemonAccountTexts(t *testing.T) {
	if got := daemonLockedLine(supLocked(account.ReasonNotLoggedIn)); !strings.Contains(got, "run: monoagentcli account login") || !strings.Contains(got, "--email") {
		t.Errorf("line = %q, want the login command and its headless form", got)
	}
	if got := daemonLockedLine(supLocked(account.ReasonKeyUnknown)); !strings.Contains(got, "run: monoagentcli update") {
		t.Errorf("line = %q, want the update command", got)
	}
	if got := daemonRunningLine(supOK); !strings.Contains(got, "triggers are live") {
		t.Errorf("line = %q", got)
	}
	if got := daemonRunningLine(supLocked(account.ReasonRefused)); strings.Contains(got, "triggers are live") || !strings.Contains(got, "locked") {
		t.Errorf("line = %q, want it to say the account is locked", got)
	}
}

// The real org services: every goroutine start launches ends with its context,
// and wait says so, in both generations; that is what lets the supervisor stop
// them and start them again.
func TestOrgServicesStopWhenTheirContextEnds(t *testing.T) {
	db, err := storage.NewDatabase(t.TempDir() + "/org.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	engine := workflow.NewWorkflowEngineWithStore(workflow.NewSQLiteWorkflowStore(db.DB), db.DB, nil,
		workflow.NewNodeTypeRegistry(), workflow.EngineConfig{WebhookAddr: "127.0.0.1:0"}, zerolog.Nop())
	orgs := newOrgServices(db, engine)
	orgs.logf = t.Logf
	orgs.watchInterval = 20 * time.Millisecond

	for generation := 1; generation <= 2; generation++ {
		ctx, cancel := context.WithCancel(context.Background())
		orgs.start(ctx, engine)
		if orgs.wait(50 * time.Millisecond) {
			t.Fatalf("generation %d: wait said the services had ended while their context was live", generation)
		}
		cancel()
		if !orgs.wait(10 * time.Second) {
			t.Fatalf("generation %d: the org services did not end within 10s of their context ending", generation)
		}
	}
}
