package openaiapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// streamToolLeg runs the leg and streams its answer: the words the model says
// before its call as they come (where the runtime streams), then the call as
// tool_calls chunks, then the finish. It returns what handleChat logs.
func (g *Gateway) streamToolLeg(w http.ResponseWriter, r *http.Request, run toolRun, includeUsage bool) (int, string, toolLog) {
	// A write that fails (the connection broke, or the client stopped reading and
	// the write deadline fired) ends the leg.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sw := newSSE(w, run.id, run.m.ID)
	sw.onBroken = cancel
	run.t.OnDelta = sw.delta
	stopKeepAlive := sw.startKeepAlive(g.cfg.StreamCommitAfter, g.cfg.KeepAlive)
	defer stopKeepAlive()

	pl := g.runPlanned(ctx, run.t, run.plan, run.req)
	stopKeepAlive()
	tl := toolLog{tools: len(run.req.toolDecls), leg: pl.Kind}

	e, gone := g.legError(ctx, pl.legResult, run.m, run.eff)
	switch {
	case gone:
		return 499, "", tl
	case e != nil:
		if !sw.fail(e) {
			writeError(w, e)
		}
		return e.Status, e.detail, tl
	case pl.Call == nil:
		sw.finish(pl.Res, includeUsage)
		return http.StatusOK, "", tl
	}
	call, bad := g.rememberCall(run, pl)
	tl.badArgs = bad
	sw.finishAtCall(pl.Text, call)
	return http.StatusOK, "", tl
}

// finishAtCall completes a stream whose leg ended at a tool call. A runtime that
// did not stream gets the words it said before the call as one chunk here. The
// call goes out as OpenAI's stream does: a chunk with the index, the id, the type
// and the name and empty arguments, then the arguments, then a chunk that finishes
// with tool_calls. There is no usage chunk, even when one was asked for.
func (s *sseWriter) finishAtCall(said string, call wireToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitLocked()
	if !s.sentContent && said != "" {
		s.data(s.chunk(delta{Content: &said}, nil))
	}
	s.data(s.chunk(delta{ToolCalls: []deltaToolCall{{
		Index: 0, ID: call.ID, Type: call.Type, Function: &deltaToolFunc{Name: call.Function.Name, Arguments: ""},
	}}}, nil))
	s.data(s.chunk(delta{ToolCalls: []deltaToolCall{{
		Index: 0, Function: &deltaToolFunc{Arguments: call.Function.Arguments},
	}}}, nil))
	reason := "tool_calls"
	s.data(s.chunk(delta{}, &reason))
	s.write("data: [DONE]\n\n")
}

// startKeepAlive commits the stream if the leg stays silent for commitAfter, so
// that a failure later is an event and not a status, and then keeps it alive. The
// function it returns stops the helper and waits for it: however the caller ends,
// even in a panic, a ticker must never write to a response that is finished. It
// can be called more than once.
func (s *sseWriter) startKeepAlive(commitAfter, every time.Duration) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	stop = func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(commitAfter)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.commit()
		case <-done:
			return
		}
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				s.keepAlive()
			case <-done:
				return
			}
		}
	}()
	return stop
}
