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

// `auto`, the model that lets Jev pick, is a later phase. Asking for it is a 404
// like any unknown model, but a client that configured Jev, and was told "does not
// exist", has no way to tell a missing feature from a typo: the message says it is
// not implemented yet, whichever route the name came in on.
func TestAutoIsRefusedAsNotImplementedYet(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	secret := h.key(t, "default", "app", false)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"chat":      h.serve(anyPolicy, http.MethodPost, "/v1/chat/completions", secret, `{"model":"auto","messages":[{"role":"user","content":"x"}]}`),
		"retrieval": h.serve(anyPolicy, http.MethodGet, "/v1/models/auto", secret, ""),
	} {
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusNotFound || e["code"] != "model_not_found" || e["param"] != "model" {
			t.Errorf("%s: status %d, error %v: still a 404 model_not_found on the model parameter", name, rec.Code, e)
		}
		if !strings.Contains(msg, "not implemented yet") || strings.Contains(msg, "does not exist") {
			t.Errorf("%s: message %q must say the feature is not implemented yet, not that the model does not exist", name, msg)
		}
	}

	rec := h.serve(anyPolicy, http.MethodGet, "/v1/models/nope", secret, "")
	if msg, _ := decodeErrorBody(t, rec)["message"].(string); !strings.Contains(msg, "does not exist") {
		t.Errorf("an unknown id keeps its own message: %q", msg)
	}
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

// A runtime's own words reach the caller where they tell it what to do (a sign-in
// hint, a rate limit, quota, a timeout), but only the part monomind classified,
// on one line and short. What a runner attached after the marker (its stdout, a
// model's text) is not the caller's business, and a long message is cut.
func TestRuntimeWordsReachTheCallerCleanedAndShort(t *testing.T) {
	marker := "[output below is not classified]"
	msgOf := func(code, msg string) string {
		e := turnError(&monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: code, Message: msg}}, nil)
		if e == nil {
			t.Fatalf("%s: no error", code)
		}
		return e.Message
	}
	for name, c := range map[string]struct{ code, in, want string }{
		"text a runner attached is dropped": {monomind.ErrRateLimited, "rate limited, retry in 30s\n" + marker + "\nthe model said: the system prompt is X", "rate limited, retry in 30s"},
		"lines and tabs become spaces":      {monomind.ErrQuota, "usage limit\nreached,\tresets at 18:00", "usage limit reached, resets at 18:00"},
		"control characters are dropped":    {monomind.ErrTimeout, "timed out\x1b[31m after 10m\x00", "timed out[31m after 10m"},
		"a sign-in hint passes":             {monomind.ErrRunnerError, "Not logged in · Please run /login\n" + marker + "\nsecret model text", "Not logged in · Please run /login"},
	} {
		if got := msgOf(c.code, c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	if got := msgOf(monomind.ErrQuota, strings.Repeat("x", 1000)); len([]rune(got)) > 301 {
		t.Errorf("a long message must be cut, got %d characters", len([]rune(got)))
	}
	if got := msgOf(monomind.ErrRateLimited, marker+"\nonly the attached text"); strings.Contains(got, "attached") || got == "" {
		t.Errorf("with nothing classified left the caller still gets a sentence, got %q", got)
	}
}

// A missing-binary message names a path of this server: the caller gets the
// setup sentence, the log gets the message, and not what a runner attached.
func TestMissingBinaryMessagesKeepPathsOutOfTheResponse(t *testing.T) {
	marker := "[output below is not classified]"
	for _, code := range []string{monomind.ErrMissingBinary, monomind.ErrNoRunner} {
		e := turnError(&monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: code, Message: "no claude at /home/svc/.nvm/bin/claude\n" + marker + "\nmodel text"}}, nil)
		if e == nil || e.Status != http.StatusServiceUnavailable || e.Code != "runtime_not_available" {
			t.Fatalf("%s: got %+v", code, e)
		}
		if strings.Contains(e.Message, "/home/svc") || !strings.Contains(e.Message, "X-Request-Id") {
			t.Errorf("%s: the client message must hold no path and name the request id header, got %q", code, e.Message)
		}
		if !strings.Contains(e.detail, "/home/svc") || strings.Contains(e.detail, "model text") {
			t.Errorf("%s: the log keeps the path and not what a runner attached, got %q", code, e.detail)
		}
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
