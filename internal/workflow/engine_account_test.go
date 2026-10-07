package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// engineGateStore is the stub store plus a record of what the engine did with
// it ("load", "finished <id> <status> <message>", "resume <id>", "list"), so a
// gate test can tell "refused at the gate" from "went on and failed later".
type engineGateStore struct {
	*stubStore
	mu        sync.Mutex
	calls     []string
	waitingOn string // when set, GetExecution answers a WAITING run with this id
}

func newEngineGateStore(wf *Workflow) *engineGateStore {
	return &engineGateStore{stubStore: &stubStore{workflowToReturn: wf}}
}

func (s *engineGateStore) note(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprintf(format, args...))
}

// count is how many recorded calls start with prefix; first is the earliest one, or "".
func (s *engineGateStore) count(prefix string) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (s *engineGateStore) first(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func (s *engineGateStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	s.note("load")
	return s.stubStore.GetWorkflow(ctx, id)
}

func (s *engineGateStore) SetExecutionFinished(_ context.Context, id, status, msg string) error {
	s.note("finished %s %s %s", id, status, msg)
	return nil
}

func (s *engineGateStore) GetExecution(ctx context.Context, id string) (*WorkflowExecution, error) {
	if s.waitingOn == id {
		return &WorkflowExecution{ID: id, WorkflowID: "wf-gate", Status: "WAITING"}, nil
	}
	return s.stubStore.GetExecution(ctx, id)
}

func (s *engineGateStore) ResumeWaitingExecution(_ context.Context, id string) (bool, error) {
	s.note("resume %s", id)
	return true, nil
}

func (s *engineGateStore) ListResumableExecutions(context.Context) ([]string, error) {
	s.note("list")
	return []string{"exec-waiting"}, nil
}

func (s *engineGateStore) ListAdoptableExecutions(context.Context) ([]string, error) {
	s.note("list")
	return nil, nil
}

// engineGateWorkflow is a loadable, active workflow of the engine's own profile.
func engineGateWorkflow() *Workflow {
	return &Workflow{ID: "wf-gate", IsActive: true, ProfileID: "engine-profile",
		Nodes: []WorkflowNode{{ID: "t1", Type: "trigger.schedule"}}}
}

// engineGateMode is one state of the account that a gate site is judged in.
type engineGateMode struct {
	name    string
	set     func(t *testing.T)
	refused bool
}

// engineGatePastDate is an enforcement date that has already passed on the real clock,
// which is the one Require reads when no guard is installed.
func engineGatePastDate(t *testing.T) {
	account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour))
}

// engineGateNoGuard is a process with no guard installed that is judged as a
// release binary is: whatever guard an earlier test left behind is removed (and put
// back when this test ends) and the test binary's fail-open rule is switched off.
func engineGateNoGuard(t *testing.T) {
	account.InstallForTest(t, nil)
	account.StrictForTest(t)
}

// engineGateModes: refusal with no guard under the strict hook (the enforcement date
// is set, or the dormant rule would let it pass), both locked verdicts, then the
// states that must let work through: dormant and the warn period with no guard, and
// a guard that says dormant, ok or grace. The test binary's fail-open rule is
// switched off by the strict hook and by every accounttest.Install.
var engineGateModes = []engineGateMode{
	{"strict, no guard installed", func(t *testing.T) { engineGateNoGuard(t); engineGatePastDate(t) }, true},
	{"locked, not logged in", func(t *testing.T) { accounttest.Install(t, accounttest.LockedNoLogin) }, true},
	{"locked, refused", func(t *testing.T) { accounttest.Install(t, accounttest.LockedRefused) }, true},
	{"dormant, strict, no guard", func(t *testing.T) { engineGateNoGuard(t); account.SetEnforceFromForTest(t, time.Time{}) }, false},
	{"warn period, strict, no guard", func(t *testing.T) {
		engineGateNoGuard(t)
		account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
	}, false},
	{"dormant, guard installed", func(t *testing.T) { accounttest.Install(t, accounttest.Dormant) }, false},
	{"signed in", func(t *testing.T) { accounttest.Install(t, accounttest.SignedIn) }, false},
	{"grace", func(t *testing.T) { accounttest.Install(t, accounttest.InGrace) }, false},
}

func eachEngineGateMode(t *testing.T, fn func(t *testing.T, refused bool)) {
	t.Helper()
	for _, m := range engineGateModes {
		t.Run(m.name, func(t *testing.T) {
			m.set(t)
			fn(t, m.refused)
		})
	}
}

// TestHandleExecutionGate: a refused run is recorded FAILED with a
// "login_required:" message before its workflow is loaded; an admitted one
// goes on to load it (the store has none, so it ends "workflow not found").
func TestHandleExecutionGate(t *testing.T) {
	eachEngineGateMode(t, func(t *testing.T, refused bool) {
		store := newEngineGateStore(nil)
		eng := newTriggerEngine(store)

		eng.handleExecution(context.Background(), ExecutionRequest{WorkflowID: "wf-gate", ExecutionID: "exec-1"})

		row := store.first("finished exec-1 ")
		if refused {
			if !strings.HasPrefix(row, "finished exec-1 FAILED login_required: ") || store.count("load") != 0 {
				t.Fatalf("refused run: %q after %d loads, want FAILED with a login_required: message and no load", row, store.count("load"))
			}
			return
		}
		if row != "finished exec-1 FAILED workflow not found" || store.count("load") != 1 {
			t.Fatalf("admitted run: %q after %d loads, want it to go on and end \"workflow not found\"", row, store.count("load"))
		}
	})
}

// TestTriggerEntryPointsGate: the three calls that create an execution row
// return the typed error, and create nothing, when the account is locked.
func TestTriggerEntryPointsGate(t *testing.T) {
	ctx := context.Background()
	sites := map[string]func(e *WorkflowEngine) error{
		"TriggerWorkflow": func(e *WorkflowEngine) error {
			_, err := e.TriggerWorkflow(ctx, "wf-gate", nil)
			return err
		},
		"TriggerWorkflowPersistOnly": func(e *WorkflowEngine) error {
			_, err := e.TriggerWorkflowPersistOnly(ctx, "wf-gate", nil)
			return err
		},
		"RetryExecution": func(e *WorkflowEngine) error {
			_, err := e.RetryExecution(ctx, "exec-old")
			return err
		},
	}
	for name, call := range sites {
		t.Run(name, func(t *testing.T) {
			eachEngineGateMode(t, func(t *testing.T, refused bool) {
				store := newEngineGateStore(engineGateWorkflow())

				err := call(newTriggerEngine(store))

				if got := account.IsLoginRequired(err); got != refused {
					t.Fatalf("err = %v, login required = %v, want %v", err, got, refused)
				}
				if n := len(store.createdExecs); refused && n != 0 {
					t.Fatalf("a refused call created %d rows, want none", n)
				}
			})
		})
	}
}

// TestExecuteWithRetry_LoginRequiredNotRetried: a node refused by a gate fails
// once; a retry policy must not hold the run for minutes of backoff.
func TestExecuteWithRetry_LoginRequiredNotRetried(t *testing.T) {
	refused := fmt.Errorf("agent.ask: %w", &account.LoginRequiredError{})
	ex := &countingExecutor{typ: "t", errs: []error{refused}}

	_, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, noWait)

	if ex.calls != 1 {
		t.Fatalf("executed %d times, want 1", ex.calls)
	}
	if !account.IsLoginRequired(err) {
		t.Fatalf("err = %v, want the login_required error back", err)
	}
}
