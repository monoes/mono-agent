package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// The pieces every route that runs a turn does the same way: the log line, the
// refusal of a model above the policy, how a finished turn is classified, and
// the choice of the auto model. Chat and image generation share them.

// logRequest writes the one line a request leaves in the server log. It names the
// request, the key, the profile, the model and the outcome, and never a prompt, an
// answer or a key. detail is what a failure keeps for the operator: a Go error or a
// runtime's error code, never what the client sent. notes are more fields of the
// line, each with its leading space, that a route adds (chat with tools says how
// many tools and how its leg started): never a prompt, a name or a result.
func (g *Gateway) logRequest(pr Principal, begin time.Time, model string, status int, ctxState, autoBy, detail string, notes ...string) {
	line := fmt.Sprintf("req=%s key=%s profile=%s model=%s status=%d ms=%d context=%s",
		pr.RequestID, pr.KeyID, pr.ProfileID, model, status, time.Since(begin).Milliseconds(), ctxState)
	if autoBy != "" {
		line += " auto=" + autoBy
	}
	line += strings.Join(notes, "")
	if detail != "" {
		line += fmt.Sprintf(" detail=%q", detail)
	}
	g.deps.Logf("%s", line)
}

// policyRefusal is the 403 for a model whose class the key's policy eff does not
// allow, nil when it does. p is the listener's policy: when it allows the class and
// eff does not, only the cap on a key created with --context refuses it, and
// contextWhy says why such a key is capped on this route.
func policyRefusal(m ModelInfo, p, eff Policy, contextWhy string) *apiError {
	if eff.Allows(m.Class) {
		return nil
	}
	if p.Allows(m.Class) {
		return errPolicy(fmt.Sprintf("model %s runs as %s, which is above what a key created with --context may use here (%s): %s; the operator can raise this with --context-confinement", m.ID, m.Class, eff, contextWhy))
	}
	return errPolicy(fmt.Sprintf("model %s runs as %s, which this server's confinement policy (%s) does not allow; the operator can raise it with --confinement", m.ID, m.Class, p))
}

// resultError says how a turn that went through runTurn ended: the error to send,
// or nil when it succeeded. gone is true when the caller left, so that there is
// nobody to answer.
func (g *Gateway) resultError(ctx context.Context, res *monomind.TurnResult, err error, m ModelInfo, eff Policy) (e *apiError, gone bool) {
	if errors.Is(err, errPolicyDenied) {
		return policyDeniedAtStart(m, eff), false
	}
	if e := turnError(res, err); e != nil {
		return e, false
	}
	if res.Err != nil { // the only error turnError lets through: a cancellation
		if ctx.Err() != nil {
			return nil, true
		}
		return g.cancelledError(), false
	}
	return nil, false
}

// pickForRequest has the auto model choose among candidates for a prompt, and
// says in X-Monoagent-Auto who chose. gone is true when the caller left while Jev
// was asked: there is nobody to answer.
func (g *Gateway) pickForRequest(w http.ResponseWriter, r *http.Request, pr Principal, prompt string, candidates []ModelInfo) (pick autoPick, gone bool) {
	pick = g.pickAuto(r.Context(), pr.ProfileID, prompt, candidates)
	if r.Context().Err() != nil {
		return pick, true
	}
	w.Header().Set("X-Monoagent-Auto", pick.By)
	return pick, false
}
