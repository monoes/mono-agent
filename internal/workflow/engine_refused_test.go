package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// refusalRefresher answers every refresh with invalid_grant: monoes.me said no.
type refusalRefresher struct{}

func (refusalRefresher) Refresh(context.Context, string) (*account.TokenSet, error) {
	return nil, &account.RefusedError{Description: "the account was blocked"}
}

// refusalGuard installs a signed-in guard whose next refresh is refused and
// returns the func that makes it try: the clock moves inside the refresh margin
// and the guard refreshes, learns the refusal and tells whoever registered.
func refusalGuard(t *testing.T) (refuse func()) {
	t.Helper()
	fx := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	signed := fx.Token(accounttest.TokenOptions{Sub: "u1"})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: signed, User: &account.User{ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh("refresh-1"); err != nil {
		t.Fatal(err)
	}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: refusalRefresher{}, Now: fx.Clock.Now})
	account.InstallForTest(t, g)
	return func() {
		fx.Clock.Advance(56 * time.Minute)
		_, _ = g.Refresh(context.Background())
	}
}

// refusalRun starts an engine over a real store whose one workflow takes a
// minute to run (at most maxConcurrent runs at once), triggers n runs and waits
// until one of them is RUNNING. It returns the engine, the store and the run ids.
func refusalRun(t *testing.T, maxConcurrent, n int) (*WorkflowEngine, *SQLiteWorkflowStore, []string) {
	t.Helper()
	store := newFullEngineStore(t)
	reg := NewNodeTypeRegistry()
	reg.Register("test.slow", func() NodeExecutor { return slowNode{d: time.Minute} })
	eng := NewWorkflowEngineWithStore(store, store.RawDB(), &fakeScheduler{}, reg,
		EngineConfig{ProfileID: "default", WebhookAddr: "127.0.0.1:0", MaxConcurrent: maxConcurrent}, zerolog.Nop())
	t.Cleanup(func() { _ = eng.Stop() })
	ctx := context.Background()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wf := createActiveManualWorkflow(t, ctx, eng, "test.slow")
	var ids []string
	for i := 0; i < n; i++ {
		id, err := eng.TriggerWorkflow(ctx, wf, nil)
		if err != nil {
			t.Fatalf("TriggerWorkflow: %v", err)
		}
		ids = append(ids, id)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		for _, id := range ids {
			if st, _ := store.GetExecutionStatus(ctx, id); st == "RUNNING" {
				return eng, store, ids
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no run started")
		}
	}
}

// TestRefusalCancelsWhatIsRunning is the registration test: an engine that was
// started while the guard was installed cancels its running execution when the
// guard learns that monoes.me refused the account, and the run records why. A
// person's own cancel records nothing about the account.
func TestRefusalCancelsWhatIsRunning(t *testing.T) {
	t.Run("refused by monoes.me", func(t *testing.T) {
		refuse := refusalGuard(t)
		_, store, ids := refusalRun(t, 0, 1)

		refuse()

		if st := waitForTerminalStatus(t, store, ids[0], 10*time.Second); st != "CANCELLED" {
			t.Fatalf("status = %s, want CANCELLED", st)
		}
		exec, _ := store.GetExecution(context.Background(), ids[0])
		if !strings.HasPrefix(exec.ErrorMessage, "login_required: ") {
			t.Errorf("the cancelled run recorded %q, want it to start with login_required: ", exec.ErrorMessage)
		}
	})
	t.Run("cancelled by a person while signed in", func(t *testing.T) {
		accounttest.Install(t, accounttest.SignedIn)
		eng, store, ids := refusalRun(t, 0, 1)

		eng.CancelExecution(ids[0])

		if st := waitForTerminalStatus(t, store, ids[0], 10*time.Second); st != "CANCELLED" {
			t.Fatalf("status = %s, want CANCELLED", st)
		}
		exec, _ := store.GetExecution(context.Background(), ids[0])
		if strings.Contains(exec.ErrorMessage, "login_required") {
			t.Errorf("a person's cancel recorded %q, want no mention of the account", exec.ErrorMessage)
		}
	})
}

// TestRefusalEndsRunsQueuedBehindTheLimit: a run still waiting for a concurrency
// slot has no goroutine of its own to stop, and cancelling only its context
// would leave its row QUEUED for good. Both runs end: CANCELLED, or, when the
// freed slot reached the queued run before its cancel did, FAILED by the gate.
func TestRefusalEndsRunsQueuedBehindTheLimit(t *testing.T) {
	refuse := refusalGuard(t)
	_, store, ids := refusalRun(t, 1, 2)
	time.Sleep(200 * time.Millisecond)
	queued := 0
	for _, id := range ids {
		if st, _ := store.GetExecutionStatus(context.Background(), id); st == "QUEUED" {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("%d of the two runs are QUEUED, want exactly the one behind the limit", queued)
	}

	refuse()

	for _, id := range ids {
		st := waitForTerminalStatus(t, store, id, 10*time.Second)
		exec, _ := store.GetExecution(context.Background(), id)
		if st != "CANCELLED" && (st != "FAILED" || !strings.HasPrefix(exec.ErrorMessage, "login_required: ")) {
			t.Errorf("execution %s ended %s (%q), want CANCELLED, or FAILED with login_required", id, st, exec.ErrorMessage)
		}
	}
}

// TestOnlyARefusalCancels: a refusal cancels; at the 24 hours (expired) nothing is
// cancelled (a run in flight ends at its next node), and before the
// enforcement date nothing is cancelled.
func TestOnlyARefusalCancels(t *testing.T) {
	for name, c := range map[string]struct {
		st   account.Status
		want bool
	}{
		"refused":                             {account.Status{State: account.StateLocked, Reason: account.ReasonRefused, Enforced: true}, true},
		"expired":                             {account.Status{State: account.StateLocked, Reason: account.ReasonExpired, Enforced: true}, false},
		"refused before the enforcement date": {account.Status{State: account.StateLocked, Reason: account.ReasonRefused}, false},
		"signed in":                           {account.Status{State: account.StateOK, Enforced: true}, false},
	} {
		if got := refusalCancels(c.st); got != c.want {
			t.Errorf("%s: refusalCancels = %v, want %v", name, got, c.want)
		}
	}
}

// TestCancelledMessageFollowsTheCause: the login_required text comes from the
// cause that travelled with the cancel, not from the verdict at the moment the
// run ends. A person's cancel that lands after a refusal stays a person's cancel;
// a refusal's cancel says so whatever the verdict has become by then.
func TestCancelledMessageFollowsTheCause(t *testing.T) {
	refusalGuard(t)() // the verdict is refused from here on
	if st := account.CurrentStatus(); st.Reason != account.ReasonRefused {
		t.Fatalf("verdict = %s(%s), want refused", st.State, st.Reason)
	}
	if got := cancelledMessage(context.Background(), ErrExecutionCancelled); strings.Contains(got, "login_required") {
		t.Errorf("a cancel without a cause recorded %q, want no mention of the account", got)
	}
	rctx, rcancel := context.WithCancelCause(context.Background())
	rcancel(errAccountRefused)
	if got := cancelledMessage(rctx, ErrExecutionCancelled); !strings.HasPrefix(got, "login_required: ") {
		t.Errorf("a cancel caused by a refusal recorded %q, want login_required", got)
	}
}
