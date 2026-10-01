package openaiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not an OpenAI error: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestWriteErrorUsesTheOpenAIShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, errUnsupported("tools", "tool calling is not supported yet"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	e := decodeErrorBody(t, rec)
	if e["type"] != "invalid_request_error" || e["code"] != "unsupported_parameter" || e["param"] != "tools" || e["message"] == "" {
		t.Fatalf("unexpected error body %v", e)
	}
}

func TestBusyErrorCarriesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, errBusy())
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("status %d, Retry-After %q; want 429 and 2", rec.Code, rec.Header().Get("Retry-After"))
	}
	if e := decodeErrorBody(t, rec); e["code"] != "rate_limit_exceeded" || e["type"] != "rate_limit_error" {
		t.Fatalf("unexpected error body %v", e)
	}
}

func TestErrorConstructors(t *testing.T) {
	cases := []struct {
		name       string
		err        *apiError
		status     int
		typ, code  string
		paramIsSet bool
	}{
		{"auth", errAuth(), 401, "authentication_error", "invalid_api_key", false},
		{"model", errModelNotFound("nope/x"), 404, "invalid_request_error", "model_not_found", true},
		{"policy", errPolicy("raise --confinement"), 403, "permission_error", "policy_denied", false},
		{"too large", errTooLarge(2 << 20), 413, "invalid_request_error", "request_too_large", false},
		{"invalid", errInvalid("invalid_value", "messages", "messages must not be empty"), 400, "invalid_request_error", "invalid_value", true},
		{"internal", errInternal("boom"), 500, "api_error", "internal_error", false},
		{"unavailable", errRuntimeUnavailable("claude is not logged in"), 503, "api_error", "runtime_not_available", false},
	}
	for _, c := range cases {
		if c.err.Status != c.status || c.err.Type != c.typ || c.err.Code != c.code || (c.err.Param != "") != c.paramIsSet {
			t.Errorf("%s: got %+v", c.name, c.err)
		}
	}
}

func TestModelNotFoundQuotesTheClientString(t *testing.T) {
	e := errModelNotFound("bad\nmodel`\x00")
	for _, r := range e.Message {
		if r == '\n' || r == 0 {
			t.Fatalf("message %q carries a raw control character from the client", e.Message)
		}
	}
}

func TestTurnErrorMapping(t *testing.T) {
	pe := func(code string) *monomind.TurnResult {
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: code, Message: "m"}}
	}
	cases := []struct {
		name   string
		res    *monomind.TurnResult
		err    error
		status int
		code   string
	}{
		{"auth", pe(monomind.ErrAuth), nil, 503, "runtime_not_available"},
		{"missing binary", pe(monomind.ErrMissingBinary), nil, 503, "runtime_not_available"},
		{"no runner", pe(monomind.ErrNoRunner), nil, 503, "runtime_not_available"},
		{"rate limited", pe(monomind.ErrRateLimited), nil, 429, "rate_limit_exceeded"},
		{"quota", pe(monomind.ErrQuota), nil, 429, "insufficient_quota"},
		{"budget", pe(monomind.ErrBudget), nil, 429, "insufficient_quota"},
		{"timeout", pe(monomind.ErrTimeout), nil, 504, "timeout"},
		{"runner error", pe(monomind.ErrRunnerError), nil, 502, "runtime_error"},
		{"bad frame", pe(monomind.ErrBadFrame), nil, 502, "runtime_error"},
		{"not logged in text", &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: "Not logged in · Please run /login"}}, nil, 503, "runtime_not_available"},
		{"no done event", &monomind.TurnResult{}, nil, 502, "runtime_error"},
		{"never started: monomind missing", nil, &monomind.ErrNotFound{}, 503, "runtime_not_available"},
		{"never started: other", nil, errors.New("disk full"), 500, "internal_error"},
		{"never started: the sandbox could not be applied", nil, fmt.Errorf("%w: workspace-write sandbox for codex is unsupported", monomind.ErrSandboxRequired), 403, "policy_denied"},
	}
	for _, c := range cases {
		got := turnError(c.res, c.err)
		if got == nil || got.Status != c.status || got.Code != c.code {
			t.Errorf("%s: got %+v, want %d %s", c.name, got, c.status, c.code)
		}
	}
	if got := turnError(&monomind.TurnResult{SawDone: true, StopReason: monomind.StopEndTurn}, nil); got != nil {
		t.Errorf("a clean turn mapped to %+v", got)
	}
	if got := turnError(&monomind.TurnResult{SawDone: true, StopReason: monomind.StopMaxTurns}, nil); got != nil {
		t.Errorf("hitting max_turns is a length finish, not an error: %+v", got)
	}
	// A turn the caller cancelled has nobody to answer.
	if got := turnError(pe(monomind.ErrCancelled), nil); got != nil {
		t.Errorf("a cancelled turn mapped to %+v", got)
	}
}

// What only the operator should see stays out of the response and goes to
// apiError.detail for the log.
func TestInternalDetailsStayOutOfTheResponse(t *testing.T) {
	secretPath := "/home/svc/.monoagent/workspaces/api/slot-0"
	for name, e := range map[string]*apiError{
		"exec error":   turnError(nil, errors.New("creating the turn's folder: mkdir "+secretPath+": disk full")),
		"runner error": turnError(&monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: "spawn " + secretPath + " ENOENT"}}, nil),
		"bad frame":    turnError(&monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrBadFrame, Message: "junk from " + secretPath}}, nil),
		"no done":      turnError(&monomind.TurnResult{}, nil),
	} {
		if strings.Contains(e.Message, secretPath) || !strings.Contains(e.Message, "X-Request-Id") {
			t.Errorf("%s: the client message must be generic and name the request id header, got %q", name, e.Message)
		}
	}
	if e := turnError(nil, errors.New("mkdir "+secretPath)); !strings.Contains(e.detail, secretPath) {
		t.Errorf("the log keeps the detail, got %q", e.detail)
	}
	// Words the caller can act on pass through.
	if e := turnError(&monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrQuota, Message: "usage limit reached, resets at 18:00"}}, nil); e.Message != "usage limit reached, resets at 18:00" {
		t.Errorf("a quota message must reach the caller as is, got %q", e.Message)
	}
}

// monomind's discovery error lists every path it tried, which is the server's
// home directory and install layout: the caller gets a sentence, the log gets
// the error. The same goes for any error of the model catalog.
func TestSetupAndCatalogErrorsKeepPathsOutOfTheResponse(t *testing.T) {
	notFound := &monomind.ErrNotFound{Tried: []string{"/home/svc/.nvm/bin/monomind", "/home/svc/.npm-global/bin/monomind"}}
	for name, e := range map[string]*apiError{
		"turn, monomind not found":  turnError(nil, notFound),
		"catalog, monomind missing": catalogError(notFound),
		"catalog, any other error":  catalogError(errors.New("exec: /home/svc/bin/monomind: permission denied")),
	} {
		if strings.Contains(e.Message, "/home/svc") || !strings.Contains(e.Message, "X-Request-Id") {
			t.Errorf("%s: the client message must hold no path and name the request id header, got %q", name, e.Message)
		}
		if !strings.Contains(e.detail, "/home/svc") {
			t.Errorf("%s: the log keeps the detail, got %q", name, e.detail)
		}
	}
	if e := turnError(nil, notFound); e.Status != http.StatusServiceUnavailable || e.Code != "runtime_not_available" {
		t.Errorf("a monomind that is not installed is a 503 runtime_not_available, got %d %s", e.Status, e.Code)
	}
	if e := catalogError(errors.New("boom")); e.Status != http.StatusInternalServerError || e.Code != "internal_error" {
		t.Errorf("any other catalog error is a 500 internal_error, got %d %s", e.Status, e.Code)
	}
}

func TestFinishReason(t *testing.T) {
	for stop, want := range map[string]string{
		monomind.StopEndTurn:      "stop",
		"":                        "stop",
		monomind.StopMaxTurns:     "length",
		monomind.StopToolRoundCap: "length",
	} {
		if got := finishReason(&monomind.TurnResult{StopReason: stop}); got != want {
			t.Errorf("finishReason(%q) = %q, want %q", stop, got, want)
		}
	}
}
