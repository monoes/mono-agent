package openaiapi

import (
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

func objectFor(m ModelInfo) modelObject {
	return modelObject{
		ID: m.ID, Object: "model", OwnedBy: m.Runtime,
		Monoagent: modelMeta{
			Runtime: m.Runtime, Model: m.Model, Label: m.Label,
			Confinement: m.Class.String(), Validated: m.Validated,
			Capabilities: []string{"text"},
		},
	}
}

// catalogError turns a failure to list models into a response.
func catalogError(err error) *apiError {
	if monomind.IsAgentNotSetup(err) {
		return errRuntimeUnavailable(err.Error())
	}
	return errInternal("could not list the models: " + err.Error())
}

// handleModels is GET /v1/models: the models this listener's policy allows
// the key (a context key is held to the context maximum).
func (g *Gateway) handleModels(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		models, err := g.catalog.Visible(r.Context(), policyFor(p, pr))
		if err != nil {
			writeError(w, catalogError(err))
			return
		}
		out := modelList{Object: "list", Data: make([]modelObject, 0, len(models))}
		for _, m := range models {
			out.Data = append(out.Data, objectFor(m))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleModel is GET /v1/models/{id...}. A model the policy disallows is
// "not found" here; asking a completion to use it says why (403).
func (g *Gateway) handleModel(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		id := r.PathValue("id")
		m, err := g.catalog.Resolve(r.Context(), id)
		switch {
		case errors.Is(err, ErrUnknownModel):
			writeError(w, errModelNotFound(id))
		case err != nil:
			writeError(w, catalogError(err))
		case !policyFor(p, pr).Allows(m.Class):
			writeError(w, errModelNotFound(id))
		default:
			writeJSON(w, http.StatusOK, objectFor(m))
		}
	}
}
