package workflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// nodeGateStep is a plain node (no agent turn, no browser action). The test's hook
// for it runs while it is in flight, and then it tells the test it ran to its end.
type nodeGateStep struct{ ran func(nodeID string) }

func (nodeGateStep) Type() string { return "test.step" }

func (s nodeGateStep) Execute(_ context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
	s.ran(in.NodeID)
	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
}

// nodeGateStore is the stub store that hands the engine the run's execution and
// keeps the terminal status and message the engine records.
type nodeGateStore struct {
	*stubStore
	exec     *WorkflowExecution
	finished []string // "<status> <message>"
}

func (s *nodeGateStore) GetExecution(context.Context, string) (*WorkflowExecution, error) {
	cp := *s.exec
	return &cp, nil
}

func (s *nodeGateStore) SetExecutionFinished(_ context.Context, _ string, status, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, status+" "+msg)
	return nil
}

// nodeGateWorkflow is a trigger and three plain nodes in a row: t1, n1, n2, n3.
func nodeGateWorkflow() *Workflow {
	node := func(id, typ string) WorkflowNode {
		return WorkflowNode{ID: id, WorkflowID: "wf-node-gate", Type: typ, Name: id}
	}
	edge := func(from, to string) WorkflowConnection {
		return WorkflowConnection{SourceNodeID: from, SourceHandle: "main", TargetNodeID: to, TargetHandle: "main"}
	}
	return &Workflow{ID: "wf-node-gate", Name: "node gate", IsActive: true, ProfileID: "engine-profile",
		Nodes:       []WorkflowNode{node("t1", "trigger.manual"), node("n1", "test.step"), node("n2", "test.step"), node("n3", "test.step")},
		Connections: []WorkflowConnection{edge("t1", "n1"), edge("n1", "n2"), edge("n2", "n3")}}
}

// nodeGateRegistry registers test.step, whose nodes call during(nodeID) while they
// are in flight and then append their id to *ran.
func nodeGateRegistry(during func(nodeID string), ran *[]string) *NodeTypeRegistry {
	var mu sync.Mutex
	reg := NewNodeTypeRegistry()
	reg.Register("test.step", func() NodeExecutor {
		return nodeGateStep{ran: func(id string) {
			if during != nil {
				during(id)
			}
			mu.Lock()
			*ran = append(*ran, id)
			mu.Unlock()
		}}
	})
	return reg
}

// runNodeGateWorkflow runs nodeGateWorkflow through the engine's queue handler, as
// every run starts, and returns the plain nodes that ran to their end, the terminal
// status and message the engine recorded, and the store with the node records.
func runNodeGateWorkflow(t *testing.T, during func(nodeID string)) (ran []string, final string, store *nodeGateStore) {
	t.Helper()
	wf := nodeGateWorkflow()
	store = &nodeGateStore{stubStore: &stubStore{workflowToReturn: wf},
		exec: &WorkflowExecution{ID: "exec-node-gate", WorkflowID: wf.ID, ProfileID: wf.ProfileID}}
	eng := NewWorkflowEngineWithStore(store, nil, nil, nodeGateRegistry(during, &ran),
		EngineConfig{ProfileID: "engine-profile"}, zerolog.Nop())

	eng.handleExecution(context.Background(), ExecutionRequest{WorkflowID: wf.ID, ExecutionID: "exec-node-gate"})

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.finished) != 1 {
		t.Fatalf("the engine recorded %d terminal statuses %q, want one", len(store.finished), store.finished)
	}
	return ran, store.finished[0], store
}

// Ruling R4: a run in flight ends at its next node once the account is locked. The
// 24 hours run out while n1 runs (the guard's clock jumps past the grace): n1
// finishes, n2 never starts, and the run is FAILED with "login_required: " and the
// first line of the refusal. A run of plain nodes, with no agent turn or browser
// action of its own, cannot outlive the gate.
func TestARunEndsAtItsNextNodeOnceTheAccountLocks(t *testing.T) {
	_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)

	ran, final, store := runNodeGateWorkflow(t, func(id string) {
		if id == "n1" {
			fx.Clock.Advance(25 * time.Hour) // no refresh for 25 hours: the grace is over
		}
	})

	if st := account.CurrentStatus(); st.State != account.StateLocked || st.Reason != account.ReasonExpired {
		t.Fatalf("the verdict after the jump is %s(%s), want locked(expired)", st.State, st.Reason)
	}
	if !slices.Equal(ran, []string{"n1"}) {
		t.Fatalf("the nodes that ran to their end are %v, want [n1]: the node in flight finishes, the next one is refused", ran)
	}
	n1, n2 := store.nodeRecord("n1"), store.nodeRecord("n2")
	if n1 == nil || n1.Status != "SUCCESS" || n2 != nil {
		t.Fatalf("n1 recorded SUCCESS %v, n2 recorded at all %v: want n1, the node in flight, finished and no record of n2",
			n1 != nil && n1.Status == "SUCCESS", n2 != nil)
	}
	if want := "FAILED login_required: " + account.LoginRequiredMessage; final != want {
		t.Fatalf("the run recorded %q, want %q", final, want)
	}
}

// Ruling R4: ok and grace never stop a run, also when the verdict moves from ok to
// grace while it runs.
func TestARunGoesOnWhileTheAccountIsOkOrInGrace(t *testing.T) {
	for _, c := range []struct {
		name string
		mode accounttest.Mode
		jump time.Duration // how far the guard's clock moves while n1 runs
		want account.State
	}{
		{"ok", accounttest.SignedIn, 0, account.StateOK},
		{"grace", accounttest.InGrace, 0, account.StateGrace},
		{"ok, then grace while n1 runs", accounttest.SignedIn, 2 * time.Hour, account.StateGrace},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, fx := accounttest.InstallWithFixture(t, c.mode)

			ran, final, _ := runNodeGateWorkflow(t, func(id string) {
				if id == "n1" {
					fx.Clock.Advance(c.jump)
				}
			})

			if st := account.CurrentStatus(); st.State != c.want {
				t.Fatalf("the verdict is %s(%s), want %s", st.State, st.Reason, c.want)
			}
			if !slices.Equal(ran, []string{"n1", "n2", "n3"}) || final != "SUCCESS " {
				t.Fatalf("nodes %v, recorded %q: want all three and SUCCESS", ran, final)
			}
		})
	}
}

// A dormant gate (no enforcement date) or a date still ahead changes nothing: the
// run goes on when the guard's clock passes the 24 hours while it runs, and with no
// guard installed at all, judged strictly as a release binary is.
func TestADormantGateNeverEndsARun(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(t *testing.T) (jump func())
	}{
		{"dormant, a guard whose grace runs out while n1 runs", func(t *testing.T) func() {
			_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)
			account.SetEnforceFromForTest(t, time.Time{})
			return func() { fx.Clock.Advance(25 * time.Hour) }
		}},
		{"dormant, strict, no guard", func(t *testing.T) func() {
			account.InstallForTest(t, nil)
			account.StrictForTest(t)
			account.SetEnforceFromForTest(t, time.Time{})
			return func() {}
		}},
		{"a date still ahead, strict, no guard", func(t *testing.T) func() {
			account.InstallForTest(t, nil)
			account.StrictForTest(t)
			account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
			return func() {}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			jump := c.set(t)

			ran, final, _ := runNodeGateWorkflow(t, func(id string) {
				if id == "n1" {
					jump()
				}
			})

			if !slices.Equal(ran, []string{"n1", "n2", "n3"}) || final != "SUCCESS " {
				t.Fatalf("nodes %v, recorded %q: want all three and SUCCESS", ran, final)
			}
		})
	}
}

// The refusal of a node is a typed error. The execution's text is "login_required: "
// and the first line of the refusal; the error unwraps to the
// *account.LoginRequiredError, whose verdict says why, and whose own text keeps the
// reason line that the execution's leaves out.
func TestARefusedNodeEndsTheRunWithTheFirstLineOfTheRefusal(t *testing.T) {
	_, fx := accounttest.InstallWithFixture(t, accounttest.SignedIn)
	var ran []string
	reg := nodeGateRegistry(func(id string) {
		if id == "n1" {
			fx.Clock.Advance(25 * time.Hour)
		}
	}, &ran)
	wf := nodeGateWorkflow()
	dag, err := BuildDAG(wf.Nodes, wf.Connections)
	if err != nil {
		t.Fatalf("BuildDAG: %v", err)
	}

	err = RunExecution(context.Background(), &WorkflowExecution{ID: "exec-node-gate", WorkflowID: wf.ID}, wf, dag, reg,
		&stubStore{}, nil, NewExpressionEngine(), zerolog.Nop())

	var lr *account.LoginRequiredError
	if !errors.As(err, &lr) || lr.Status.Reason != account.ReasonExpired {
		t.Fatalf("RunExecution = %v, want a *account.LoginRequiredError for expired", err)
	}
	if got, want := err.Error(), "login_required: "+account.LoginRequiredMessage; got != want {
		t.Fatalf("the error text is %q, want %q", got, want)
	}
	if !strings.HasPrefix(lr.Error(), account.LoginRequiredMessage+"\n") {
		t.Fatalf("the refusal is %q, want its first line and then the reason", lr.Error())
	}
	if !slices.Equal(ran, []string{"n1"}) {
		t.Fatalf("the nodes that ran to their end are %v, want [n1]", ran)
	}
}

// Ruling R18: a refusal (monoes.me answered invalid_grant) that lands while n1 runs
// ends the run CANCELLED at its next node, the status that the cancel of
// everything in flight gives it, and not FAILED as the other locks do. The engine
// here is never started, so no handler cancels the run and the node check is the only
// thing that can reach the refusal: nothing races. (With a started engine the cancel
// and the check race, and both end the run CANCELLED.)
func TestARefusalBetweenTwoNodesEndsTheRunCancelledNeverFailed(t *testing.T) {
	refuse := refusalGuard(t) // a signed-in guard whose next refresh is refused

	ran, final, store := runNodeGateWorkflow(t, func(id string) {
		if id == "n1" {
			refuse() // monoes.me refuses the account while n1 runs
		}
	})

	if st := account.CurrentStatus(); st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("the verdict after the refusal is %s(%s), want locked(refused)", st.State, st.Reason)
	}
	if !slices.Equal(ran, []string{"n1"}) {
		t.Fatalf("the nodes that ran to their end are %v, want [n1]: the node in flight finishes, the next one is refused", ran)
	}
	n1, n2 := store.nodeRecord("n1"), store.nodeRecord("n2")
	if n1 == nil || n1.Status != "SUCCESS" || n2 != nil {
		t.Fatalf("n1 recorded SUCCESS %v, n2 recorded at all %v: want n1, the node in flight, finished and no record of n2",
			n1 != nil && n1.Status == "SUCCESS", n2 != nil)
	}
	if want := "CANCELLED login_required: monoes.me refused this account: " + ErrExecutionCancelled.Error(); final != want {
		t.Fatalf("the run recorded %q, want %q", final, want)
	}
}
