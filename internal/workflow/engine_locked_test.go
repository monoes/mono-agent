package workflow

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// TestHandleTriggerGate: a schedule or webhook trigger that fires while the
// account is locked is dropped before anything is loaded or created.
func TestHandleTriggerGate(t *testing.T) {
	eachEngineGateMode(t, func(t *testing.T, refused bool) {
		store := newEngineGateStore(engineGateWorkflow())
		eng := newTriggerEngine(store)

		eng.handleTrigger("wf-gate", "t1", nil)

		if refused {
			if n := len(store.createdExecs); n != 0 || store.count("load") != 0 {
				t.Fatalf("a dropped trigger created %d rows and loaded %d workflows, want none", n, store.count("load"))
			}
			return
		}
		if n := len(store.createdExecs); n != 1 {
			t.Fatalf("an admitted trigger created %d rows, want 1", n)
		}
	})
}

// TestHandleTriggerLogsOnceAMinute: a schedule that fires every second while
// the account is locked writes one line a minute, with the count it covers.
func TestHandleTriggerLogsOnceAMinute(t *testing.T) {
	accounttest.Install(t, accounttest.LockedNoLogin)
	var logs bytes.Buffer
	eng := NewWorkflowEngineWithStore(newEngineGateStore(engineGateWorkflow()), nil, nil, NewNodeTypeRegistry(),
		EngineConfig{ProfileID: "engine-profile"}, zerolog.New(&logs))
	clock := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	eng.drops.now = func() time.Time { return clock }

	for i := 0; i < 3; i++ {
		eng.handleTrigger("wf-gate", "t1", nil)
		clock = clock.Add(time.Second)
	}
	if n := strings.Count(logs.String(), "trigger dropped"); n != 1 {
		t.Fatalf("%d log lines for 3 drops inside a minute, want 1:\n%s", n, logs.String())
	}

	clock = clock.Add(time.Minute)
	eng.handleTrigger("wf-gate", "t1", nil)
	if n := strings.Count(logs.String(), "trigger dropped"); n != 2 {
		t.Fatalf("%d log lines after a minute passed, want 2:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), `"dropped":3`) {
		t.Errorf("the second line does not say that it covers 3 drops (the two held back and the one that broke the silence):\n%s", logs.String())
	}
}

// TestResumeTickGate: a locked account neither adopts queued runs nor resumes
// paused ones, so they are still there when the account is valid again.
func TestResumeTickGate(t *testing.T) {
	eachEngineGateMode(t, func(t *testing.T, refused bool) {
		store := newEngineGateStore(engineGateWorkflow())
		store.waitingOn = "exec-waiting"
		eng := newTriggerEngine(store)

		eng.resumeTick(context.Background())

		if refused {
			if store.count("list") != 0 || store.count("resume") != 0 {
				t.Fatalf("a locked tick listed %d times and resumed %d, want it to do nothing", store.count("list"), store.count("resume"))
			}
			return
		}
		if store.count("list") != 2 || store.count("resume exec-waiting") != 1 {
			t.Fatalf("an admitted tick listed %d times and resumed %d, want 2 and 1", store.count("list"), store.count("resume exec-waiting"))
		}
	})
}

// TestResumeExecutionGate: the public call that other loops (the org waker and
// receiver) use refuses before it flips a paused run to QUEUED.
func TestResumeExecutionGate(t *testing.T) {
	eachEngineGateMode(t, func(t *testing.T, refused bool) {
		store := newEngineGateStore(engineGateWorkflow())
		store.waitingOn = "exec-waiting"
		eng := newTriggerEngine(store)

		err := eng.ResumeExecution("exec-waiting")

		resumes := 1
		if refused {
			resumes = 0
		}
		if account.IsLoginRequired(err) != refused || store.count("resume") != resumes {
			t.Fatalf("err = %v, %d resumes: want the typed error and none when refused, nil and one otherwise", err, store.count("resume"))
		}
	})
}
