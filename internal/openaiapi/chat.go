package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// knowledgeTimeout bounds the knowledge search of a context key.
const knowledgeTimeout = 30 * time.Second

// handleChat is POST /v1/chat/completions.
func (g *Gateway) handleChat(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		begin := time.Now()

		status, model, ctxState, detail, autoBy := http.StatusOK, "", "", "", ""
		fail := func(e *apiError) {
			status, detail = e.Status, e.detail
			writeError(w, e)
		}
		// One line per request, whatever its outcome.
		defer func() { g.logRequest(pr, begin, model, status, ctxState, autoBy, detail) }()

		var req ChatRequest
		if e := decodeBody(w, r, g.cfg.BodyLimit, &req); e != nil {
			fail(e)
			return
		}
		if req.Model == "" {
			fail(errInvalid("missing_required_parameter", "model", "model is required"))
			return
		}
		if e := validateChat(&req); e != nil {
			fail(e)
			return
		}

		eff := policyFor(p, pr)
		var m ModelInfo
		var candidates []ModelInfo // for auto: what Jev may pick among, all of them allowed
		isAuto := req.Model == autoModelID
		if isAuto {
			model = autoModelID
			var e *apiError
			if candidates, e = g.autoCandidates(r.Context(), pr, eff); e != nil {
				fail(e)
				return
			}
		} else {
			var err error
			m, err = g.catalog.Resolve(r.Context(), req.Model)
			switch {
			case errors.Is(err, ErrUnknownModel):
				fail(errModelNotFound(req.Model))
				return
			case err != nil:
				fail(catalogError(err))
				return
			}
			model = m.ID
			if e := policyRefusal(m, p, eff, "its requests carry excerpts of the profile's knowledge, which includes captured web pages nobody vetted"); e != nil {
				fail(e)
				return
			}
		}

		slot, release, ok := g.limiter.tryAcquire()
		if !ok {
			fail(errBusy())
			return
		}
		defer release()

		// The slot comes first, so that a request that would be refused for lack of
		// one never costs a Jev call, and no more than MaxConcurrent picks run at once.
		if isAuto {
			pick, gone := g.pickForRequest(w, r, pr, lastUserText(&req), candidates)
			if gone { // the caller left while Jev was asked: there is nobody to answer
				status = 499
				return
			}
			m, autoBy, model = pick.Model, pick.By, pick.Model.ID
		}

		var ctxBlock string
		if pr.Context {
			ctxBlock, ctxState = g.knowledgeContext(r.Context(), pr.ProfileID, lastUserText(&req))
			w.Header().Set("X-Monoagent-Context", ctxState)
		}

		effort := ""
		if re := req.ReasoningEffort; re != "" && slices.Contains(m.Efforts, re) {
			effort = re
		}
		tr := translateChat(&req, ctxBlock)
		t := turn{
			Runtime: m.Runtime, Model: m.Model, Effort: effort, System: tr.System, Prompt: tr.Prompt,
			Policy: eff, ProfileID: pr.ProfileID, Slot: slot, RequireSandbox: m.Class == Sandboxed,
		}

		w.Header().Set("X-Monoagent-Model", m.ID)
		extendWriteDeadline(w, g.cfg.TurnTimeout+2*turnGrace)
		id := newRequestID("chatcmpl-")

		if req.Stream {
			status, detail = g.streamChat(w, r, t, id, m.ID, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
			return
		}

		res, err := g.runTurn(r.Context(), t)
		e, gone := g.resultError(r.Context(), res, err, m, eff)
		if gone {
			status = 499 // the caller left; there is nobody to answer
			return
		}
		if e != nil {
			fail(e)
			return
		}
		if res.SandboxStatus != "" {
			w.Header().Set("X-Monoagent-Sandbox", res.SandboxStatus)
		}
		writeJSON(w, http.StatusOK, completion{
			ID: id, Object: "chat.completion", Created: time.Now().Unix(), Model: m.ID,
			Choices: []completionChoice{{Message: assistantMessage{Role: "assistant", Content: res.ResultText}, FinishReason: finishReason(res)}},
			Usage:   usageFrom(res),
		})
	}
}

// cancelledError is the answer to a turn that ended cancelled although its
// caller is still there: the server cut it short, because it is stopping.
func (g *Gateway) cancelledError() *apiError {
	if g.stopping() {
		e := errStopping()
		e.detail = "the turn was cancelled by the server stopping"
		return e
	}
	return &apiError{Status: http.StatusBadGateway, Type: "api_error", Code: "runtime_error",
		Message: "The turn was cancelled. " + quoteRequestID, detail: "the turn was cancelled by the server"}
}

func policyDeniedAtStart(m ModelInfo, p Policy) *apiError {
	return errPolicy(fmt.Sprintf("model %s started with a confinement the server policy (%s) does not allow, so the turn was stopped", m.ID, p))
}

// streamChat runs the turn and streams it. It returns the HTTP status the
// request ended with and the operator-only detail of a failure.
func (g *Gateway) streamChat(w http.ResponseWriter, r *http.Request, t turn, id, model string, includeUsage bool) (int, string) {
	// A write that fails (the connection broke, or the client stopped reading
	// and the write deadline fired) ends the turn.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sw := newSSE(w, id, model)
	sw.onBroken = cancel
	t.OnDelta = sw.delta

	// Commit the stream if the turn stays silent, then keep it alive. However
	// this function ends, even in a panic, the helper stops: a ticker must
	// never write to a response that is finished.
	stop := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	stopKeepAlive := func() {
		once.Do(func() { close(stop) })
		wg.Wait()
	}
	defer stopKeepAlive()
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(g.cfg.StreamCommitAfter)
		defer timer.Stop()
		select {
		case <-timer.C:
			sw.commit()
		case <-stop:
			return
		}
		tick := time.NewTicker(g.cfg.KeepAlive)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				sw.keepAlive()
			case <-stop:
				return
			}
		}
	}()

	res, err := g.runTurn(ctx, t)
	stopKeepAlive()

	failure := func(e *apiError) (int, string) {
		if !sw.fail(e) {
			writeError(w, e)
		}
		return e.Status, e.detail
	}
	if errors.Is(err, errPolicyDenied) {
		return failure(errPolicy(fmt.Sprintf("model %s/%s started with a confinement the server policy (%s) does not allow, so the turn was stopped", t.Runtime, t.Model, t.Policy)))
	}
	if e := turnError(res, err); e != nil {
		return failure(e)
	}
	switch {
	case r.Context().Err() != nil || sw.isBroken(): // the caller left, or stopped reading
		return 499, ""
	case res.Err != nil: // cancelled by the server itself, which is stopping
		return failure(g.cancelledError())
	}
	sw.finish(res, includeUsage)
	return http.StatusOK, ""
}

// knowledgeContext searches the profile's knowledge for the user's message
// and returns the system-prompt block and the X-Monoagent-Context value: the
// number of excerpts, "none" or "unavailable". A failed search never fails
// the request.
func (g *Gateway) knowledgeContext(ctx context.Context, profileID, userText string) (block, state string) {
	if g.deps.Knowledge == nil {
		return "", "unavailable"
	}
	kctx, cancel := context.WithTimeout(ctx, knowledgeTimeout)
	defer cancel()
	results, err := g.deps.Knowledge(kctx, profileID, contextQuery(userText))
	if err != nil {
		g.deps.Logf("knowledge search for profile %s failed", profileID) // not the error: it might echo the query
		return "", "unavailable"
	}
	block, n := contextBlock(results)
	if n == 0 {
		return "", "none"
	}
	return block, strconv.Itoa(n)
}
