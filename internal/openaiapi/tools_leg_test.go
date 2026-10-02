package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// fakeLegExec plays the part of monomind.Exec for a leg. Events reach onEvent in
// order from the calling goroutine, as they do from Exec's event goroutine; a
// tool_call event also starts a goroutine that runs opts.OnToolCall, as Exec
// does, and the fake returns only when those are done, as Exec does. play
// returns when the runtime would: it waits for ctx (a runtime waiting for the
// result of its call, until it is cancelled) or just returns (the process ended).
func fakeLegExec(play func(ctx context.Context, emit func(monomind.Event))) execFunc {
	return func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		res := &monomind.TurnResult{}
		var wg sync.WaitGroup
		emit := func(ev monomind.Event) {
			onEvent(ev)
			monomind.ApplyEventToResult(res, ev)
			if ev.Type == monomind.EventToolCall && opts.OnToolCall != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = opts.OnToolCall(ctx, ev.Name, ev.Args)
				}()
			}
		}
		play(ctx, emit)
		wg.Wait()
		if ctx.Err() != nil && res.Err == nil && !res.SawDone {
			res.Err = &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled by caller", ExitCode: 130}
			res.ExitCode = 130
		}
		return res, nil
	}
}

func evSession(id string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventSession, SessionID: id}
}

func evCall(name, args string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventToolCall, ID: "tc_1", Name: name, Args: json.RawMessage(args)}
}

// legTurn is a turn that declares one tool, for runLeg.
func legTurn(runtime string) turn {
	return turn{Runtime: runtime, Model: "default", Prompt: "p", Policy: anyPolicy, ProfileID: "alice",
		Tools: []monomind.ToolSpec{{Name: "get_weather", Description: "d"}}}
}

// runLegWithin runs a leg and fails the test instead of hanging when it does not end.
func runLegWithin(t *testing.T, g *Gateway, ctx context.Context, tn turn) legResult {
	t.Helper()
	done := make(chan legResult, 1)
	go func() { done <- g.runLeg(ctx, tn) }()
	select {
	case lr := <-done:
		return lr
	case <-time.After(10 * time.Second):
		t.Fatal("the leg did not end")
		return legResult{}
	}
}

// shortGrace makes a leg's wait for the runtime to die brief for the test.
func shortGrace(t *testing.T) {
	t.Helper()
	old := legEndGrace
	legEndGrace = 5 * time.Millisecond
	t.Cleanup(func() { legEndGrace = old })
}

func TestLegEndsAtTheFirstCall(t *testing.T) {
	shortGrace(t)
	var execCtxCancelledByTheLeg atomic.Bool
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("sess-1"))
		emit(evText("che"))
		emit(evText("cking"))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		<-ctx.Done() // the runtime waits for the result of its call
		execCtxCancelledByTheLeg.Store(true)
	}))
	lr := runLegWithin(t, h.g, context.Background(), legTurn("claude"))
	if lr.Err != nil || lr.Res == nil {
		t.Fatalf("leg: %+v", lr)
	}
	if lr.Call == nil || lr.Call.Name != "get_weather" || string(lr.Call.Args) != `{"city":"Paris"}` {
		t.Fatalf("the call: %+v", lr.Call)
	}
	if lr.Text != "checking" {
		t.Errorf("what the assistant said before the call = %q", lr.Text)
	}
	if lr.Res.SessionID != "sess-1" {
		t.Errorf("the session id of a leg that was cancelled at its call: %q", lr.Res.SessionID)
	}
	if !execCtxCancelledByTheLeg.Load() {
		t.Error("the leg must cancel its turn at the call: nothing else ends it")
	}
}

func TestLegKeepsTheFirstOfSeveralSimultaneousCalls(t *testing.T) {
	shortGrace(t)
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "workspace-write"))
		emit(evSession("s"))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		emit(evCall("get_weather", `{"city":"Tokyo"}`)) // arrives with the first, as on codex
		emit(evCall("other", `{}`))
		<-ctx.Done()
	}))
	lr := runLegWithin(t, h.g, context.Background(), legTurn("codex"))
	if lr.Call == nil || string(lr.Call.Args) != `{"city":"Paris"}` {
		t.Fatalf("a leg returns one call, the first: %+v", lr.Call)
	}
}

func TestLegKeepsTheTextBeforeTheCallAndDropsWhatComesAfter(t *testing.T) {
	shortGrace(t)
	play := func(incremental bool, parts ...string) execFunc {
		return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
			emit(evStart(incremental, "monomind"))
			for _, p := range parts[:len(parts)-1] {
				emit(evText(p))
			}
			emit(monomind.Event{V: 1, Type: monomind.EventAssistant, Text: "a subagent", CoderFields: monomind.CoderFields{ParentToolUseID: "toolu_1"}})
			emit(evCall("get_weather", `{}`))
			emit(evText(parts[len(parts)-1])) // a runtime that goes on talking while it waits
			<-ctx.Done()
		})
	}
	// An incremental runtime sends pieces of one reply: they are joined as they are.
	h := newHarness(t, play(true, "Let me ", "look.", "AFTER"))
	if lr := runLegWithin(t, h.g, context.Background(), legTurn("claude")); lr.Text != "Let me look." {
		t.Errorf("incremental text = %q", lr.Text)
	}
	// Any other sends whole messages: they are kept apart.
	h = newHarness(t, play(false, "First thought.", "Second thought.", "AFTER"))
	if lr := runLegWithin(t, h.g, context.Background(), legTurn("codex")); lr.Text != "First thought.\n\nSecond thought." {
		t.Errorf("whole messages = %q", lr.Text)
	}
}

func TestLegForwardsTheDeltasBeforeTheCallOnly(t *testing.T) {
	shortGrace(t)
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evText("one "))
		emit(evText("two"))
		emit(evCall("get_weather", `{}`))
		emit(evText("three"))
		<-ctx.Done()
	}))
	var got []string
	tn := legTurn("claude")
	tn.OnDelta = func(s string) { got = append(got, s) }
	lr := runLegWithin(t, h.g, context.Background(), tn)
	if lr.Call == nil || strings.Join(got, "|") != "one |two" {
		t.Errorf("deltas forwarded: %v", got)
	}
}

func TestLegWithoutACallIsAnOrdinaryAnswer(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(false, "monomind"), evSession("s"), evText("42"), evUsage(3, 4), evResult("42", monomind.StopEndTurn), evDone(0)))
	lr := runLegWithin(t, h.g, context.Background(), legTurn("claude"))
	if lr.Err != nil || lr.Call != nil || lr.Res == nil || lr.Res.ResultText != "42" || lr.Res.Err != nil || !lr.Res.SawDone {
		t.Fatalf("leg: %+v call %+v", lr, lr.Call)
	}
	if lr.Res.SessionID != "s" || !lr.SawText {
		t.Errorf("session %q sawText %v", lr.Res.SessionID, lr.SawText)
	}
}

// Exec waits for the goroutines of its handlers: one that stayed blocked would
// hold the leg, and the slot, until the turn timed out.
func TestLegHandlersReturnWhenTheRuntimeEndsByItself(t *testing.T) {
	old := legEndGrace
	legEndGrace = time.Hour // only the done event can free the handler in time
	t.Cleanup(func() { legEndGrace = old })
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "monomind"))
		emit(evCall("get_weather", `{}`))
		emit(evDone(0)) // monomind's own tool timeout: the turn ends, the call is unanswered
	}))
	lr := runLegWithin(t, h.g, context.Background(), legTurn("claude"))
	if lr.Call == nil {
		t.Fatalf("the call was lost: %+v", lr)
	}
}

func TestLegHandlerWaitsForTheRuntimeToDieAndSaysTheResultComesLater(t *testing.T) {
	old := legEndGrace
	legEndGrace = 80 * time.Millisecond
	t.Cleanup(func() { legEndGrace = old })
	ctx, cancel := context.WithCancel(context.Background())
	c := &legCollector{cancel: cancel, ended: make(chan struct{})}

	cancel() // the leg ends at its call: Exec has begun to stop the process
	begin := time.Now()
	text, err := c.handle(ctx, "get_weather", json.RawMessage(`{}`))
	// Returning at once would hand the dying runtime a result, which it may take
	// for the call's answer: the handler gives Exec the time to kill it first.
	if waited := time.Since(begin); waited < 60*time.Millisecond {
		t.Errorf("the handler returned after %v, before the process could be dead", waited)
	}
	if text != "" || err == nil {
		t.Fatalf("the handler answers with an error: %q, %v", text, err)
	}
	// If the runtime does hear it, it must not read it as a failure of the tool.
	for _, bad := range []string{"cancel", "context", "deadline", "error"} {
		if strings.Contains(strings.ToLower(err.Error()), bad) {
			t.Errorf("the error text %q says %q", err, bad)
		}
	}
	if !strings.Contains(err.Error(), "next message") {
		t.Errorf("the error text should say that the result comes in the next message: %q", err)
	}
}

func TestLegHandlerReturnsAtOnceWhenTheProcessReportsDone(t *testing.T) {
	old := legEndGrace
	legEndGrace = time.Hour
	t.Cleanup(func() { legEndGrace = old })
	c := &legCollector{cancel: func() {}, ended: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		_, _ = c.handle(context.Background(), "f", nil)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	c.onEvent(evDone(130))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return when the process ended")
	}
}

func TestLegOfAClientThatLeavesEnds(t *testing.T) {
	shortGrace(t)
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "monomind"))
		<-ctx.Done() // a runtime thinking
	}))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	lr := runLegWithin(t, h.g, ctx, legTurn("claude"))
	if lr.Call != nil || lr.Res == nil || lr.Res.Err == nil || lr.Res.Err.Code != monomind.ErrCancelled {
		t.Errorf("leg: %+v", lr)
	}
}

func TestLegPolicyDenialRecordsNothing(t *testing.T) {
	shortGrace(t)
	h := newHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "none")) // above a policy of sandboxed
		emit(evSession("s"))
		emit(evCall("get_weather", `{}`))
		<-ctx.Done()
	}))
	tn := legTurn("codex")
	tn.Policy = Policy{Max: Sandboxed}
	lr := runLegWithin(t, h.g, context.Background(), tn)
	if lr.Err == nil || lr.Call != nil {
		t.Fatalf("a denied turn has no call: %+v call %+v", lr.Err, lr.Call)
	}
}

// How a finished leg is answered: the caller who left first, then a policy
// denial, then a call (whatever the cancelled result says: the cancel is the
// leg's own), then the existing mapping of a turn's errors.
func TestLegErrorClassification(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	m := ModelInfo{ID: "codex/default", Runtime: "codex", Model: "default", Class: Sandboxed}
	cancelled := &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled by caller"}, ExitCode: 130}
	ok := &monomind.TurnResult{SawDone: true}
	call := &legCall{Name: "f", Args: json.RawMessage(`{}`)}
	live, left := context.Background(), func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()

	cases := []struct {
		name     string
		ctx      context.Context
		lr       legResult
		gone     bool
		status   int
		code     string
		stopping bool
	}{
		{"a call", live, legResult{Res: cancelled, Call: call}, false, 0, "", false},
		{"a call on a leg that also failed", live, legResult{Res: &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError}}, Call: call}, false, 0, "", false},
		{"a caller who left, with a call", left, legResult{Res: cancelled, Call: call}, true, 0, "", false},
		{"a caller who left", left, legResult{Res: cancelled}, true, 0, "", false},
		{"a policy denial", live, legResult{Err: errPolicyDenied}, false, http.StatusForbidden, "policy_denied", false},
		{"a runner error", live, legResult{Res: &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: "boom"}, SawDone: true}}, false, http.StatusBadGateway, "runtime_error", false},
		{"quota", live, legResult{Res: &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrQuota}, SawDone: true}}, false, http.StatusTooManyRequests, "insufficient_quota", false},
		{"no done event", live, legResult{Res: &monomind.TurnResult{}}, false, http.StatusBadGateway, "runtime_error", false},
		{"cancelled by the server stopping", live, legResult{Res: cancelled}, false, http.StatusServiceUnavailable, "runtime_not_available", true},
		{"cancelled by something else", live, legResult{Res: cancelled}, false, http.StatusBadGateway, "runtime_error", false},
		{"an answer", live, legResult{Res: ok, SawText: true}, false, 0, "", false},
	}
	for _, c := range cases {
		if c.stopping {
			h.g.shutdown()
		}
		e, gone := h.g.legError(c.ctx, c.lr, m, anyPolicy)
		switch {
		case gone != c.gone:
			t.Errorf("%s: gone = %v, want %v", c.name, gone, c.gone)
		case c.status == 0 && e != nil:
			t.Errorf("%s: unexpected error %+v", c.name, e)
		case c.status != 0 && (e == nil || e.Status != c.status || e.Code != c.code):
			t.Errorf("%s: error %+v, want %d %s", c.name, e, c.status, c.code)
		}
		if c.stopping {
			h = newHarness(t, okTurn("x")) // a gateway that is running again for the cases after
		}
	}
}

func TestResumeFailedMeansTheRuntimeCouldNotContinueTheSession(t *testing.T) {
	failed := func(code string) legResult {
		return legResult{Res: &monomind.TurnResult{SawDone: true, ExitCode: 1, Err: &monomind.ProtocolError{Code: code, Message: "No conversation found with session ID: x"}}}
	}
	cases := []struct {
		name string
		lr   legResult
		want bool
	}{
		{"a runner error with nothing said", failed(monomind.ErrRunnerError), true},
		// What the two runtimes say for a session id they do not have (the spike's raw
		// events): a runner error before anything is said, 1 to 3 seconds after the
		// start. The class is what counts; no wording is matched.
		{"claude's unknown session", legResult{Res: &monomind.TurnResult{SawDone: true, ExitCode: 1, Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError,
			Message: "Claude Code returned an error result: No conversation found with session ID: 00000000-0000-0000-0000-000000000000"}}}, true},
		{"codex's unknown thread", legResult{Res: &monomind.TurnResult{SawDone: true, ExitCode: 1, Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError,
			Message: "CodexAgentRunner: codex exec failed (exit 1)\nstderr: Error: thread/resume: thread/resume failed: no rollout found for thread id 00000000-0000-0000-0000-000000000000 (code -32600)\n"}}}, true},
		{"a bad frame", failed(monomind.ErrBadFrame), true},
		{"a process that vanished", legResult{Res: &monomind.TurnResult{}}, true},
		{"quota is not the session's fault", failed(monomind.ErrQuota), false},
		{"rate limit", failed(monomind.ErrRateLimited), false},
		{"auth", failed(monomind.ErrAuth), false},
		{"timeout", failed(monomind.ErrTimeout), false},
		{"cancelled", failed(monomind.ErrCancelled), false},
		{"a runner error after the model spoke", legResult{Res: failed(monomind.ErrRunnerError).Res, SawText: true}, false},
		{"a runner error after a call", legResult{Res: failed(monomind.ErrRunnerError).Res, Call: &legCall{Name: "f"}}, false},
		{"an answer", legResult{Res: &monomind.TurnResult{SawDone: true}, SawText: true}, false},
		{"a leg that never started", legResult{Err: errPolicyDenied}, false},
		{"an empty answer that finished", legResult{Res: &monomind.TurnResult{SawDone: true}}, false},
		{"a runner error after a tool of the runtime's own ran: a replay would run it again", legResult{Res: failed(monomind.ErrRunnerError).Res, Ran: true}, false},
		{"a process that vanished after a tool of the runtime's own ran", legResult{Res: &monomind.TurnResult{}, Ran: true}, false},
	}
	for _, c := range cases {
		if got := c.lr.resumeFailed(); got != c.want {
			t.Errorf("%s: resumeFailed = %v, want %v", c.name, got, c.want)
		}
	}
}

// A leg's session is as it was only when the model did not run: a rate limit, the quota,
// the budget or a missing sign-in with nothing said, no call and no tool of the runtime's
// own that ran.
func TestSessionUntouchedMeansTheModelNeverRan(t *testing.T) {
	failed := func(code string) *monomind.TurnResult {
		return &monomind.TurnResult{SawDone: true, ExitCode: 1, Err: &monomind.ProtocolError{Code: code, Message: "x"}}
	}
	cases := []struct {
		name string
		lr   legResult
		want bool
	}{
		{"rate limited", legResult{Res: failed(monomind.ErrRateLimited)}, true},
		{"out of quota", legResult{Res: failed(monomind.ErrQuota)}, true},
		{"over budget", legResult{Res: failed(monomind.ErrBudget)}, true},
		{"not signed in", legResult{Res: failed(monomind.ErrAuth)}, true},
		{"a runner error", legResult{Res: failed(monomind.ErrRunnerError)}, false},
		{"a timeout", legResult{Res: failed(monomind.ErrTimeout)}, false},
		{"rate limited after the model spoke", legResult{Res: failed(monomind.ErrRateLimited), SawText: true}, false},
		{"rate limited after a call", legResult{Res: failed(monomind.ErrRateLimited), Call: &legCall{Name: "f"}}, false},
		{"rate limited after a tool of the runtime's own ran", legResult{Res: failed(monomind.ErrRateLimited), Ran: true}, false},
		{"a leg that never started", legResult{Err: errPolicyDenied}, false},
		{"an answer", legResult{Res: &monomind.TurnResult{SawDone: true}, SawText: true}, false},
	}
	for _, c := range cases {
		if got := c.lr.sessionUntouched(); got != c.want {
			t.Errorf("%s: sessionUntouched = %v, want %v", c.name, got, c.want)
		}
	}
}

// A tool of the runtime's own ran unless monomind refused it: a refused one is a start and
// an end marked denied, or a start already marked, and does nothing. What is counted is by
// the id of the tool, so that one that ran is not forgotten when another is refused.
func TestLegKnowsWhichToolsOfTheRuntimeRan(t *testing.T) {
	deniedStart := evNativeStart("tu_1")
	deniedStart.Denied = true
	for _, c := range []struct {
		name   string
		events []monomind.Event
		ran    bool
	}{
		{"none tried", nil, false},
		{"one ran", []monomind.Event{evNativeStart("tu_1")}, true},
		{"one ran and its end came", []monomind.Event{evNativeStart("tu_1"), {V: 1, Type: monomind.EventToolActivity, ID: "tu_1", CoderFields: monomind.CoderFields{Phase: "end"}}}, true},
		{"one was refused", []monomind.Event{evNativeStart("tu_1"), evNativeDenied("tu_1")}, false},
		{"a start that was marked", []monomind.Event{deniedStart}, false},
		{"one ran and another was refused", []monomind.Event{evNativeStart("tu_1"), evNativeStart("tu_2"), evNativeDenied("tu_2")}, true},
		{"one was refused and another ran", []monomind.Event{evNativeStart("tu_1"), evNativeDenied("tu_1"), evNativeStart("tu_2")}, true},
	} {
		events := append([]monomind.Event{evStart(false, "monomind")}, c.events...)
		events = append(events, evDone(0))
		h := newHarness(t, scriptedExec(events...))
		if lr := runLegWithin(t, h.g, context.Background(), legTurn("claude")); lr.Ran != c.ran {
			t.Errorf("%s: Ran = %v, want %v", c.name, lr.Ran, c.ran)
		}
	}
}
