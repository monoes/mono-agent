package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// imageContextWhy says why a key created with --context is held to the context
// maximum on this route, where none of its knowledge is added to the turn.
const imageContextWhy = "such a key is held to it on every route, because its chat requests carry excerpts of the profile's knowledge, which includes captured web pages nobody vetted"

// imagesResponse is the answer of POST /v1/images/generations: the images the
// runtime saved, each as the base64 of its bytes (encoding/json writes a []byte so).
type imagesResponse struct {
	Created int64       `json:"created"`
	Data    []imageData `json:"data"`
}

type imageData struct {
	B64JSON []byte `json:"b64_json"`
}

// handleImages is POST /v1/images/generations: one turn of a runtime that can make
// images, in a slot folder, told to save what it makes there; what it saved is read
// before the folder is emptied and returned.
func (g *Gateway) handleImages(p Policy) func(http.ResponseWriter, *http.Request, Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr Principal) {
		begin := time.Now()

		status, model, detail, autoBy := http.StatusOK, "", "", ""
		fail := func(e *apiError) {
			status, detail = e.Status, e.detail
			writeError(w, e)
		}
		defer func() { g.logRequest(pr, begin, model, status, "", autoBy, detail) }()

		var req ImageRequest
		if e := decodeBody(w, r, min(g.cfg.BodyLimit, maxImageBody), &req); e != nil {
			fail(e)
			return
		}
		n, size, e := validateImages(&req)
		if e != nil {
			fail(e)
			return
		}

		eff := policyFor(p, pr)
		m, candidates, e := g.resolveImageModel(r.Context(), pr, p, eff, req.Model)
		if e != nil {
			fail(e)
			return
		}
		isAuto := req.Model == autoModelID
		model = m.ID
		if isAuto {
			model = autoModelID
		}

		slot, release, ok := g.limiter.tryAcquire()
		if !ok {
			fail(errBusy())
			return
		}
		defer release()

		// The slot comes first, as for chat: a request that would be refused for lack of
		// one never costs a Jev call.
		if isAuto {
			pick, gone := g.pickForRequest(w, r, pr, req.Prompt, candidates)
			if gone { // the caller left while Jev was asked: there is nobody to answer
				status = 499
				return
			}
			m, autoBy, model = pick.Model, pick.By, pick.Model.ID
		}

		var found collected
		t := turn{
			Runtime: m.Runtime, Model: m.Model, System: imageSystemPrompt, Prompt: imagePrompt(req.Prompt, n, size),
			Policy: eff, ProfileID: pr.ProfileID, Slot: slot, RequireSandbox: m.Class == Sandboxed,
			Collect: func(dir string) { found = collectImages(dir, n) },
		}
		w.Header().Set("X-Monoagent-Model", m.ID)
		extendWriteDeadline(w, g.cfg.TurnTimeout+2*turnGrace)

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
		switch {
		case len(found.images) > 0: // what the runtime saved decides: it did not lack the tool
		case saidNoImageTool(res.ResultText):
			fail(errNoImageTool(m))
			return
		default:
			e := errNoImageMade(res.ResultText)
			e.detail = "no image was collected from the turn's folder"
			if note := found.note(); note != "" {
				e.detail += " (left out: " + note + ")"
			}
			fail(e)
			return
		}
		if res.SandboxStatus != "" {
			w.Header().Set("X-Monoagent-Sandbox", res.SandboxStatus)
		}
		data := make([]imageData, len(found.images))
		for i, img := range found.images {
			data[i] = imageData{B64JSON: img}
		}
		writeJSON(w, http.StatusOK, imagesResponse{Created: time.Now().Unix(), Data: data})
	}
}

// resolveImageModel finds the model of an image request: for auto the models it
// may pick among (the model is chosen later), without a model the first installed
// runtime of the image list that the policy allows, otherwise the model named.
func (g *Gateway) resolveImageModel(ctx context.Context, pr Principal, p, eff Policy, name string) (ModelInfo, []ModelInfo, *apiError) {
	switch name {
	case autoModelID:
		candidates, e := g.autoCandidatesFor(ctx, pr, eff, capImage)
		return ModelInfo{}, candidates, e
	case "":
		m, e := g.defaultImageModel(ctx, pr, p, eff)
		return m, nil, e
	}
	m, err := g.catalog.Resolve(ctx, name)
	switch {
	case errors.Is(err, ErrUnknownModel):
		return ModelInfo{}, nil, errModelNotFound(name)
	case err != nil:
		return ModelInfo{}, nil, catalogError(err)
	case !g.cfg.CanMakeImages(m):
		return ModelInfo{}, nil, g.errNotAnImageModel(ctx, m)
	}
	return m, nil, policyRefusal(m, p, eff, imageContextWhy)
}

// defaultImageModel is the default model of the first installed runtime of the
// image list that can make images and that the policy allows.
func (g *Gateway) defaultImageModel(ctx context.Context, pr Principal, p, eff Policy) (ModelInfo, *apiError) {
	var denied Class // the weakest class among the image models the policy refuses; 0 when none was refused
	for _, runtime := range g.cfg.ImageRuntimeList() {
		m, err := g.catalog.Resolve(ctx, runtime) // a bare runtime is its default model
		switch {
		case errors.Is(err, ErrUnknownModel): // not installed
			continue
		case err != nil:
			return ModelInfo{}, catalogError(err)
		case !g.cfg.CanMakeImages(m):
			continue
		case !eff.Allows(m.Class):
			if denied == 0 || m.Class < denied {
				denied = m.Class
			}
			continue
		}
		return m, nil
	}
	if denied != 0 {
		return ModelInfo{}, errNoImagePolicy(pr, p, eff, denied)
	}
	return ModelInfo{}, errNoImageRuntime(g.cfg.ImageRuntimeList())
}

// imageRuntimes are the installed runtimes that can make images, in the order of the
// model list.
func (g *Gateway) imageRuntimes(ctx context.Context) []string {
	models, err := g.catalog.Models(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range models {
		if !m.Alias && g.cfg.CanMakeImages(m) && !slices.Contains(out, m.Runtime) {
			out = append(out, m.Runtime)
		}
	}
	return out
}

// errNotAnImageModel is the 400 for a model that exists but cannot make images: a bad
// value for model, not a 404 (it exists) and not a 403 (no policy would make it work).
func (g *Gateway) errNotAnImageModel(ctx context.Context, m ModelInfo) *apiError {
	why := "its runtime is not one of the image runtimes"
	if slices.Contains(g.cfg.ImageRuntimeList(), m.Runtime) {
		why = "it runs as chat-only, which has no tool to save a file"
	}
	msg := fmt.Sprintf("The model %s cannot generate images: %s.", strconv.Quote(m.ID), why)
	if runtimes := g.imageRuntimes(ctx); len(runtimes) > 0 {
		msg += " Models of " + strings.Join(runtimes, ", ") + " can; GET /v1/models marks the models that can with the capability image."
	} else {
		msg += " No model on this server can."
	}
	return errInvalid("invalid_value", "model", msg)
}

// errNoImagePolicy is the 403 for a request that names no model when the key's policy
// allows none of the installed image runtimes, the weakest of which runs as need: what
// the operator has to raise is said, --confinement and, for a key created with
// --context, --context-confinement, to the class that would help.
func errNoImagePolicy(pr Principal, p, eff Policy, need Class) *apiError {
	level := "any"
	if need == Sandboxed {
		level = "sandboxed (or any)"
	}
	var raise []string
	if !p.Allows(need) {
		raise = append(raise, "--confinement "+level)
	}
	if pr.Context && !contextCapAllows(p, need) {
		raise = append(raise, "--context-confinement "+level+", because this key was created with --context")
	}
	return errPolicy(fmt.Sprintf("image generation needs a runtime that can write a file, and this key's policy (%s) allows none of the installed image runtimes, the weakest of which runs as %s; the operator can raise it with %s",
		eff, need, strings.Join(raise, ", and ")))
}

// contextCapAllows reports whether the cap on a key created with --context allows a
// class on its own, whatever the listener serves: it is what --context-confinement
// sets, so that the operator is told to raise it only when it is in the way.
func contextCapAllows(p Policy, c Class) bool {
	p.Max = Unconfined
	return p.ForContextKey().Allows(c)
}

// errNoImageRuntime is the 404 for a request that names no model when no runtime of
// the image list is installed.
func errNoImageRuntime(list []string) *apiError {
	return &apiError{Status: http.StatusNotFound, Type: "invalid_request_error", Code: "model_not_found", Param: "model",
		Message: "No model on this server can generate images: none of the image runtimes (" + strings.Join(list, ", ") + ") is installed. List the available ids with GET /v1/models."}
}

// errNoImageTool is the 400 for a runtime that said it has no image tool.
func errNoImageTool(m ModelInfo) *apiError {
	return errInvalid("image_generation_unsupported", "model", "The model "+strconv.Quote(m.ID)+" has no built-in image generation tool, so it cannot make images. "+
		"GET /v1/models marks the models that can with the capability image.")
}

// errNoImageMade is the 502 for a turn that ended without an image to return, with what
// the runtime said, on one line and cut short.
func errNoImageMade(reply string) *apiError {
	msg := "The runtime did not produce an image"
	if said := runtimeWords(reply, ""); said != "" {
		msg += ". What it said: " + said
	} else {
		msg += " and said nothing"
	}
	return &apiError{Status: http.StatusBadGateway, Type: "api_error", Code: "image_generation_failed", Message: msg}
}
