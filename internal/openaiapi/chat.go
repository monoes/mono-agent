package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// knowledgeTimeout bounds the knowledge search of a context key.
const knowledgeTimeout = 30 * time.Second

// handleChat is POST /v1/chat/completions.
func (g *Gateway) handleChat(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		begin := time.Now()

		status, model, ctxState, detail := http.StatusOK, "", "", ""
		fail := func(e *apiError) {
			status, detail = e.Status, e.detail
			writeError(w, e)
		}
		// One line per request. It names the request, the key, the profile, the
		// model and the outcome, and never a prompt, an answer or a key. detail
		// is what a failure keeps for the operator: a Go error or a runtime's
		// error code, never what the client sent.
		defer func() {
			line := fmt.Sprintf("req=%s key=%s profile=%s model=%s status=%d ms=%d context=%s",
				pr.RequestID, pr.KeyID, pr.ProfileID, model, status, time.Since(begin).Milliseconds(), ctxState)
			if detail != "" {
				line += fmt.Sprintf(" detail=%q", detail)
			}
			g.deps.Logf("%s", line)
		}()

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

		m, err := g.catalog.Resolve(r.Context(), req.Model)
		switch {
		case errors.Is(err, ErrUnknownModel):
			fail(errModelNotFound(req.Model))
			return
		case err != nil:
			fail(catalogError(err))
			return
		}
		model = m.ID
		eff := policyFor(p, pr)
		if !eff.Allows(m.Class) {
			if p.Allows(m.Class) { // only the cap on a context key refuses it
				fail(errPolicy(fmt.Sprintf("model %s runs as %s, which is above what a key created with --context may use here (%s): its requests carry excerpts of the profile's knowledge, which includes captured web pages nobody vetted; the operator can raise this with --context-confinement", m.ID, m.Class, eff)))
			} else {
				fail(errPolicy(fmt.Sprintf("model %s runs as %s, which this server's confinement policy (%s) does not allow; the operator can raise it with --confinement", m.ID, m.Class, p)))
			}
			return
		}

		slot, release, ok := g.limiter.tryAcquire()
		if !ok {
			fail(errBusy())
			return
		}
		defer release()

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
			fail(errUnsupported("stream", "streaming is not supported yet"))
			return
		}

		res, err := g.runTurn(r.Context(), t)
		if errors.Is(err, errPolicyDenied) {
			fail(policyDeniedAtStart(m, eff))
			return
		}
		if e := turnError(res, err); e != nil {
			fail(e)
			return
		}
		if res.Err != nil { // the only error turnError lets through: a cancellation
			if r.Context().Err() != nil {
				status = 499 // the caller left; there is nobody to answer
				return
			}
			fail(&apiError{Status: http.StatusBadGateway, Type: "api_error", Code: "runtime_error", Message: "The turn was cancelled. " + quoteRequestID})
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

func policyDeniedAtStart(m ModelInfo, p Policy) *apiError {
	return errPolicy(fmt.Sprintf("model %s started with a confinement the server policy (%s) does not allow, so the turn was stopped", m.ID, p))
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
