package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/monoes/mono-agent/internal/monomind"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func objectFor(m ModelInfo, capabilities []string) modelObject {
	return modelObject{
		ID: m.ID, Object: "model", OwnedBy: m.Runtime,
		Monoagent: modelMeta{
			Runtime: m.Runtime, Model: m.Model, Label: m.Label,
			Confinement: m.Class.String(), Validated: m.Validated,
			Capabilities: capabilities,
		},
	}
}

// catalogError turns a failure to list models into a response. The error goes
// to the log (apiError.detail), never to the caller.
func catalogError(err error) *apiError {
	switch {
	case monomind.IsAgentNotSetup(err):
		return errSetup(err)
	case errors.Is(err, context.Canceled): // the caller left while the list loaded
		return &apiError{Status: 499, Type: "api_error", Code: "request_cancelled", Message: "The request was cancelled.", detail: err.Error()}
	}
	e := errInternal("The model list could not be loaded. " + quoteRequestID)
	e.detail = err.Error()
	return e
}

// logFailure writes the line an error response leaves in the server log: who
// asked, and the detail the caller was not given.
func (g *Gateway) logFailure(pr Principal, what string, e *apiError) {
	g.deps.Logf("req=%s key=%s profile=%s %s status=%d detail=%q", pr.RequestID, pr.KeyID, pr.ProfileID, what, e.Status, e.detail)
}

// handleModels is GET /v1/models: the models this listener's policy allows
// the key (a context key is held to the context maximum).
func (g *Gateway) handleModels(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		eff := policyFor(p, pr)
		models, err := g.catalog.Visible(r.Context(), eff)
		if err != nil {
			e := catalogError(err)
			g.logFailure(pr, "list models", e)
			writeError(w, e)
			return
		}
		out := modelList{Object: "list", Data: make([]modelObject, 0, len(models)+1)}
		for _, m := range models {
			out.Data = append(out.Data, objectFor(m, g.cfg.Capabilities(m)))
		}
		// Auto comes last, where it works: a client that takes the first model of
		// the list must not be moved to it, and its prompts to TypeSafe, by an
		// operator switching the surface on.
		if len(models) > 0 && g.autoStatus(r.Context(), pr.ProfileID).Available {
			out.Data = append(out.Data, g.autoObject(eff, models))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleModel is GET /v1/models/{id...}. A model the policy disallows is
// "not found" here; asking a completion to use it says why (403).
func (g *Gateway) handleModel(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		id := r.PathValue("id")
		if id == autoModelID {
			eff := policyFor(p, pr)
			candidates, e := g.autoCandidates(r.Context(), pr, eff)
			if e != nil {
				if e.Status != http.StatusNotFound { // the list could not be loaded
					g.logFailure(pr, "get model", e)
				}
				writeError(w, e)
				return
			}
			writeJSON(w, http.StatusOK, g.autoObject(eff, candidates))
			return
		}
		m, err := g.catalog.Resolve(r.Context(), id)
		switch {
		case errors.Is(err, ErrUnknownModel):
			writeError(w, errModelNotFound(id))
		case err != nil:
			e := catalogError(err)
			g.logFailure(pr, "get model", e)
			writeError(w, e)
		case !policyFor(p, pr).Allows(m.Class):
			writeError(w, errModelNotFound(id))
		default:
			writeJSON(w, http.StatusOK, objectFor(m, g.cfg.Capabilities(m)))
		}
	}
}
