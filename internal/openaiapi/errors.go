// Package openaiapi serves standard OpenAI-style endpoints (models, chat
// completions) on top of the local agent runtimes, driven through
// monomind.Exec. Requests are authenticated by per-profile API keys
// (internal/apikeys).
package openaiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/monoes/mono-agent/internal/monomind"
)

// apiError is an error in the OpenAI wire shape:
// {"error":{"message","type","param","code"}}.
type apiError struct {
	Status     int
	Type       string
	Code       string
	Message    string
	Param      string
	RetryAfter int // seconds; sets the Retry-After header when > 0
	// detail is what the server log gets for this error. It is never sent to
	// the client: it can hold a path or a runtime's own words.
	detail string
}

// quoteRequestID ends the generic messages the client gets for an error whose
// details only the operator should see.
const quoteRequestID = "Quote the X-Request-Id response header to the operator."

func (e *apiError) Error() string { return e.Message }

func (e *apiError) body() []byte {
	var param any
	if e.Param != "" {
		param = e.Param
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": e.Message, "type": e.Type, "param": param, "code": e.Code,
	}})
	return b
}

// writeError sends e as the whole response.
func writeError(w http.ResponseWriter, e *apiError) {
	w.Header().Set("Content-Type", "application/json")
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	w.WriteHeader(e.Status)
	_, _ = w.Write(e.body())
}

func errInvalid(code, param, msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Type: "invalid_request_error", Code: code, Param: param, Message: msg}
}

func errUnsupported(param, msg string) *apiError {
	return errInvalid("unsupported_parameter", param, msg)
}

func errTooLarge(limit int64) *apiError {
	return &apiError{Status: http.StatusRequestEntityTooLarge, Type: "invalid_request_error", Code: "request_too_large",
		Message: fmt.Sprintf("the request body is larger than %d bytes", limit)}
}

func errAuth() *apiError {
	return &apiError{Status: http.StatusUnauthorized, Type: "authentication_error", Code: "invalid_api_key",
		Message: "Incorrect API key provided. Send it as `Authorization: Bearer sk-ma-…`."}
}

// autoModelID is the model that will let Jev pick a runtime and model for a
// request. It is a later phase: until then it is refused like any unknown model,
// but the message says why.
const autoModelID = "auto"

// errModelNotFound quotes the client's string so control characters never
// reach a log line or a terminal.
func errModelNotFound(model string) *apiError {
	if model == autoModelID {
		return &apiError{Status: http.StatusNotFound, Type: "invalid_request_error", Code: "model_not_found", Param: "model",
			Message: `The model "auto" is not available in this version: choosing the model automatically is not implemented yet. List the available ids with GET /v1/models.`}
	}
	if len(model) > 64 {
		model = model[:64]
	}
	return &apiError{Status: http.StatusNotFound, Type: "invalid_request_error", Code: "model_not_found", Param: "model",
		Message: "The model " + strconv.Quote(model) + " does not exist or is not available to this key. List the available ids with GET /v1/models."}
}

func errPolicy(msg string) *apiError {
	return &apiError{Status: http.StatusForbidden, Type: "permission_error", Code: "policy_denied", Message: msg}
}

func errBusy() *apiError {
	return &apiError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "rate_limit_exceeded",
		Message: "The server is running its maximum number of concurrent turns. Retry shortly.", RetryAfter: 2}
}

func errRuntimeUnavailable(msg string) *apiError {
	return &apiError{Status: http.StatusServiceUnavailable, Type: "api_error", Code: "runtime_not_available", Message: msg}
}

// errStopping is the answer to a request the server's shutdown refused or cut
// short: a 503 the client can retry, here a moment later or on another instance.
func errStopping() *apiError {
	return errRuntimeUnavailable("The server is shutting down. Retry shortly, or on another instance.")
}

// errSetup is the answer to a monomind or a runtime that is not set up on the
// server. The caller gets a sentence, not the error: monomind's discovery
// error lists every path it tried, which is the server's home directory and
// install layout. The error itself goes to the log.
func errSetup(err error) *apiError {
	e := errRuntimeUnavailable("The agent runtime is not available on this server (not installed, not found or not signed in). " + quoteRequestID)
	e.detail = err.Error()
	return e
}

func errInternal(msg string) *apiError {
	return &apiError{Status: http.StatusInternalServerError, Type: "api_error", Code: "internal_error", Message: msg}
}

// turnError classifies a finished turn. execErr is Exec's own error, which
// means the turn never started. A nil result means the turn succeeded, or
// that the caller cancelled it and nobody is left to answer.
//
// Setup hints, rate limits, quota and timeouts pass the runtime's own words
// through: they are short, and they tell the caller what to do. Anything else
// gets a generic message, and the detail goes to the log through apiError.detail.
func turnError(res *monomind.TurnResult, execErr error) *apiError {
	if execErr != nil {
		switch {
		case errors.Is(execErr, errShuttingDown):
			return errStopping()
		case monomind.IsAgentNotSetup(execErr):
			return errSetup(execErr)
		case errors.Is(execErr, monomind.ErrSandboxRequired):
			e := errPolicy("The sandbox this model runs in could not be applied, so the turn was not run.")
			e.detail = execErr.Error()
			return e
		}
		e := errInternal("An internal error occurred. " + quoteRequestID)
		e.detail = execErr.Error()
		return e
	}
	if res == nil {
		return errInternal("the turn returned no result")
	}
	if pe := res.Err; pe != nil {
		switch pe.Code {
		case monomind.ErrCancelled:
			return nil
		case monomind.ErrRateLimited:
			return &apiError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "rate_limit_exceeded",
				Message: runtimeWords(pe.Message, "The runtime is rate limited. Retry shortly.")}
		case monomind.ErrQuota, monomind.ErrBudget:
			return &apiError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Code: "insufficient_quota",
				Message: runtimeWords(pe.Message, "The runtime's usage limit has been reached.")}
		case monomind.ErrTimeout:
			return &apiError{Status: http.StatusGatewayTimeout, Type: "api_error", Code: "timeout",
				Message: runtimeWords(pe.Message, "The turn timed out.")}
		}
		if monomind.IsAgentNotSetup(pe) {
			switch pe.Code {
			case monomind.ErrMissingBinary, monomind.ErrNoRunner:
				// These name a path of this server, like monomind's own discovery
				// error: the caller gets the setup sentence, the log the message.
				return errSetup(fmt.Errorf("%s: %s", pe.Code, monomind.ClassifiableMessage(pe.Message)))
			}
			return errRuntimeUnavailable(runtimeWords(pe.Message, "The runtime is not signed in on this server."))
		}
		return &apiError{Status: http.StatusBadGateway, Type: "api_error", Code: "runtime_error",
			Message: "The runtime reported an error (" + pe.Code + "). " + quoteRequestID, detail: "code=" + pe.Code}
	}
	if !res.SawDone {
		return &apiError{Status: http.StatusBadGateway, Type: "api_error", Code: "runtime_error",
			Message: "The runtime ended the turn without finishing it. " + quoteRequestID}
	}
	return nil
}

// maxRuntimeWords is the longest message of a runtime that a caller is given.
const maxRuntimeWords = 300

// runtimeWords is a runtime's own message made fit for a caller: only the part
// monomind classified (a runner can attach its stdout, or a model's text, after
// a marker), on one line, without control characters, and short. Where nothing
// is left it is fallback.
func runtimeWords(msg, fallback string) string {
	msg = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, monomind.ClassifiableMessage(msg))
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxRuntimeWords {
		msg = string(r[:maxRuntimeWords]) + "…"
	}
	if msg == "" {
		return fallback
	}
	return msg
}

// finishReason is the OpenAI finish_reason of a successful turn.
func finishReason(res *monomind.TurnResult) string {
	switch res.StopReason {
	case monomind.StopMaxTurns, monomind.StopToolRoundCap:
		return "length"
	}
	return "stop"
}

// errPolicyDenied is returned by the turn runner when the turn's own start
// event reports a confinement the policy does not allow.
var errPolicyDenied = errors.New("the runtime reported a confinement the server policy does not allow")
