package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A leg is one turn of a conversation with tools. It ends at the first tool call
// the model makes: the call is recorded and the turn is cancelled there (monomind's
// cancel frame; for a read-access leg Exec also sends SIGTERM to the process group;
// the group is killed only if monomind has not exited within the kill grace, and
// not at all once it has), and the client gets the call to run. No process waits
// for the result: the follow-up request starts a new leg, which continues the
// runtime's session or starts again from the transcript.

// legEndGrace is how long the handler of a call waits for the runtime to be gone
// before it answers. Exec hands the handler's answer to the runtime as the
// call's result, and a runtime that is still dying could take it for the real
// one and start another turn. The cancel that ends the leg reaches the process
// first when the handler holds back for a moment. A variable so a test can
// shorten it.
var legEndGrace = 100 * time.Millisecond

// errLegEnded is the handler's answer. If the runtime does hear it, it must not
// read it as a failure of the tool: the result is on its way in the next message.
var errLegEnded = errors.New("the result of this call arrives in the next message")

// legCall is the call a leg ended at.
type legCall struct {
	Name string
	Args json.RawMessage
}

// legResult is what a leg produced. Res and Err are runTurn's; Call is the call
// the leg ended at, nil when it ended without one; Text is what the assistant
// said before the call (the whole answer when there was none); SawText says
// whether it said anything at all; Ran says a tool of the runtime's own ran and
// monomind did not deny it (codex using one of the user's own MCP servers): the
// model ran, and what the tool did is done.
type legResult struct {
	Res     *monomind.TurnResult
	Err     error
	Call    *legCall
	Text    string
	SawText bool
	Ran     bool
}

// legCollector watches the events of a leg. onEvent runs in the one goroutine
// that delivers them, and what it writes is read after the turn has returned,
// which is after that goroutine has ended: no lock is needed.
type legCollector struct {
	cancel context.CancelFunc // ends the leg

	incremental bool // the runtime sends pieces of one reply, not whole messages
	text        strings.Builder
	sawText     bool
	call        *legCall
	ran         map[string]bool // the runtime's own tools that ran, by id, none of them denied

	ended   chan struct{} // closed by the done event: the process is over
	endOnce sync.Once
}

// onEvent keeps the text before the first call and the call itself, and ends the
// leg at the call. The calls are taken from the events, in the order they arrive,
// not from the handler: Exec runs a handler per call in a goroutine of its own,
// and those race.
func (c *legCollector) onEvent(ev monomind.Event) {
	switch ev.Type {
	case monomind.EventStart:
		c.incremental = ev.StreamsIncrementally
	case monomind.EventAssistant:
		if ev.Text == "" || ev.ParentToolUseID != "" { // a native subagent's words are not the assistant's
			return
		}
		c.sawText = true
		if c.call != nil {
			return // a runtime may go on talking while it waits for the result
		}
		if !c.incremental && c.text.Len() > 0 {
			c.text.WriteString("\n\n")
		}
		c.text.WriteString(ev.Text)
	case monomind.EventToolCall:
		if c.call == nil {
			c.call = &legCall{Name: ev.Name, Args: ev.Args}
			c.cancel()
		}
	case monomind.EventToolActivity:
		// A tool of the runtime's own. One that monomind refuses is a start and then an end
		// marked denied, and did nothing; any other did what it does.
		if c.ran == nil {
			c.ran = map[string]bool{}
		}
		if ev.Denied {
			delete(c.ran, ev.ID)
		} else {
			c.ran[ev.ID] = true
		}
	case monomind.EventDone:
		c.endOnce.Do(func() { close(c.ended) })
	}
}

// handle is the handler of the leg's tool calls. Exec needs one, and waits for
// every one it started before it returns, so it must come back when the leg is
// cancelled and when the process ends by itself (monomind's own tool timeout ends
// a turn that way, with the call unanswered). It never carries a result.
func (c *legCollector) handle(ctx context.Context, _ string, _ json.RawMessage) (string, error) {
	select {
	case <-ctx.Done():
	case <-c.ended:
	}
	grace := time.NewTimer(legEndGrace)
	defer grace.Stop()
	select {
	case <-c.ended:
	case <-grace.C:
	}
	return "", errLegEnded
}

// runLeg runs t as a leg: it declares t.Tools, ends the turn at the first call
// and returns what the turn produced. The context it gives runTurn is derived
// from ctx, so the gateway's own timeout is not mistaken for a cancel.
func (g *Gateway) runLeg(ctx context.Context, t turn) legResult {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &legCollector{cancel: cancel, ended: make(chan struct{})}
	t.OnEvent, t.OnToolCall = c.onEvent, c.handle
	if send := t.OnDelta; send != nil {
		// onEvent has seen the event by the time runTurn passes its text on.
		t.OnDelta = func(text string) {
			if c.call == nil {
				send(text)
			}
		}
	}
	res, err := g.runTurn(lctx, t)
	return legResult{Res: res, Err: err, Call: c.call, Text: c.text.String(), SawText: c.sawText, Ran: len(c.ran) > 0}
}

// legError says how a finished leg is answered: the error to send, nil when it
// ended at a call or answered, gone when its caller left, so that nobody is left
// to answer. In this order: the caller who left; a call, whatever the cancelled
// result says, since the cancel was the leg's own (a policy denial has none:
// the hook sees nothing after it); and then how any turn's result is classified.
func (g *Gateway) legError(ctx context.Context, lr legResult, m ModelInfo, eff Policy) (e *apiError, gone bool) {
	switch {
	case ctx.Err() != nil:
		return nil, true
	case lr.Call != nil:
		return nil, false
	case errors.Is(lr.Err, monomind.ErrSandboxRequired):
		// Every leg requires the sandbox (toolChat). The text says what the caller can
		// do about it and holds no name of the request; the detail is monomind's own.
		e := errPolicy("Tool calling needs monomind's sandbox, which could not be applied to this model's runtime here, so the turn was not run. Retry without tools, or ask the operator.")
		e.detail = lr.Err.Error()
		return e, false
	}
	return g.resultError(ctx, lr.Res, lr.Err, m, eff)
}

// sessionUntouched reports whether a leg ended before the model ran, in a way that
// says nothing about the runtime's session: the runtime was rate limited, out of
// quota or budget, or not signed in, and nothing was said or called and no tool of
// its own ran. The session is as it was, and a retry may continue it.
func (lr legResult) sessionUntouched() bool {
	if lr.Err != nil || lr.Res == nil || lr.Res.Err == nil || lr.Call != nil || lr.SawText || lr.Ran {
		return false
	}
	switch lr.Res.Err.Code {
	case monomind.ErrRateLimited, monomind.ErrQuota, monomind.ErrBudget, monomind.ErrAuth:
		return true
	}
	return false
}

// resumeFailed reports whether a leg that continued a runtime's session could not:
// it ended in an error of the runtime's own (an unknown session ends that way,
// on claude and on codex) before the model said or called anything or ran a tool
// of its own (a replay would run it again), or the process vanished. No message
// is matched. Quota, rate limit, auth and timeout say nothing about the session,
// and a leg that was cancelled is not a failure.
func (lr legResult) resumeFailed() bool {
	if lr.Err != nil || lr.Res == nil || lr.Call != nil || lr.SawText || lr.Ran {
		return false
	}
	if pe := lr.Res.Err; pe != nil {
		return pe.Code == monomind.ErrRunnerError || pe.Code == monomind.ErrBadFrame
	}
	return !lr.Res.SawDone
}
