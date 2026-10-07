package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/monomind"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// wantEnvelopeRefusal fails unless rec is the /v1 refusal of a locked account:
// the OpenAI envelope SDKs parse, status 401, code login_required, the fixed
// message, and the request id every /v1 answer carries. It is never the flat
// body of the HTTP API, and carries no bearer challenge.
func wantEnvelopeRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body)
	}
	e := decodeErrorBody(t, rec)
	if e["type"] != "authentication_error" || e["code"] != "login_required" || e["message"] != loginRequiredText || e["param"] != nil {
		t.Errorf("error = %v, want authentication_error/login_required with the fixed message", e)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("the refusal carries no X-Request-Id")
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q: the key is not what was refused", got)
	}
	var flat map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &flat)
	if _, isFlat := flat["login_required"]; isFlat {
		t.Errorf("the body carries the HTTP API's flat login_required field: %s", rec.Body)
	}
}

// Every /v1 route, in every state, with the right key, a wrong one and none: a
// locked account is refused before the key is looked at (so the answer does not
// say whether a key is good) and before any turn is started.
func TestV1RoutesAreRefusedWhileLocked(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/models", ""},
		{http.MethodGet, "/v1/models/claude/default", ""},
		{http.MethodPost, "/v1/chat/completions", chatBody},
		{http.MethodPost, "/v1/images/generations", `{"model":"x","prompt":"a"}`},
	}
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			var turns atomic.Int32
			h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
				turns.Add(1)
				return okTurn("x")(ctx, o, on)
			})
			key := h.key(t, "default", "app", false)
			accounttest.Install(t, c.Mode)

			for _, r := range routes {
				for _, secret := range []string{key, "sk-ma-wrong", ""} {
					rec := h.serve(anyPolicy, r.method, r.path, secret, r.body)
					if c.Refused {
						wantEnvelopeRefusal(t, rec)
						continue
					}
					// Allowed: the account is no longer in the way and the old
					// rules apply: a good key is not a 401, a bad one is.
					isAuth := rec.Code == http.StatusUnauthorized
					if (secret == key) == isAuth {
						t.Errorf("%s %s with the right key=%v: %d, want the key's own verdict", r.method, r.path, secret == key, rec.Code)
					}
					if isAuth && decodeErrorBody(t, rec)["code"] != "invalid_api_key" {
						t.Errorf("%s %s: %s, want invalid_api_key", r.method, r.path, rec.Body)
					}
				}
			}
			if c.Refused && turns.Load() != 0 {
				t.Fatalf("%d turns started for a locked account", turns.Load())
			}
		})
	}
}

// The dedicated listener (--v1-addr) does not pass through the HTTP API's mux:
// the gateway's own check is all there is, here on a live socket. Its /health
// stays open and says nothing of the account, because the listener may face the
// network.
func TestTheDedicatedListenerRefusesToo(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			h := newHarness(t, okTurn("x"))
			key := h.key(t, "default", "app", false)
			accounttest.Install(t, c.Mode)
			addr, stop := startServe(t, h, anyPolicy, nil)
			defer func() {
				if err := stop(); err != nil {
					t.Errorf("Serve: %v", err)
				}
			}()

			code, body := get(t, http.DefaultClient, "http://"+addr+"/v1/models", key)
			refused := code == http.StatusUnauthorized && strings.Contains(body, `"code":"login_required"`)
			if refused != c.Refused || (!c.Refused && code != http.StatusOK) {
				t.Errorf("GET /v1/models = %d %s, want refused = %v", code, body, c.Refused)
			}
			if code, body := get(t, http.DefaultClient, "http://"+addr+"/health", ""); code != http.StatusOK || strings.Contains(body, "account") {
				t.Errorf("GET /health = %d %s, want 200 with no account object", code, body)
			}
		})
	}
}

// On the main listener the gateway rides the HTTP API's mux. The HTTP API's gate
// stands aside for /v1 so that the gateway answers in its own envelope, and
// answers everything else in the flat body: both on one server, in one state.
func TestTheLoopbackMountRefusesInTheEnvelopeAndTheRestFlat(t *testing.T) {
	keyring.MockInit() // the legacy auth resolves its token in the vault: never the real OS keychain
	h := newHarness(t, okTurn("x"))
	key := h.key(t, "default", "app", false)
	srv, err := httpapi.NewServer(httpapi.Options{
		DB: h.db, Profile: "default", Version: "mount-test",
		ExtraRoutes: func(mux *http.ServeMux) { h.g.Mount(mux, anyPolicy) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	accounttest.Install(t, accounttest.LockedNoLogin)

	do := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}
	wantEnvelopeRefusal(t, do(http.MethodGet, "/v1/models"))
	wantEnvelopeRefusal(t, do(http.MethodPost, "/v1/chat/completions"))

	flat := do(http.MethodGet, "/workflows")
	var body map[string]any
	if err := json.Unmarshal(flat.Body.Bytes(), &body); err != nil || flat.Code != http.StatusUnauthorized ||
		body["error"] != "login_required" || body["login_required"] != true {
		t.Errorf("GET /workflows = %d %s, want the HTTP API's flat login_required body", flat.Code, flat.Body)
	}
	if health := do(http.MethodGet, "/health"); health.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", health.Code)
	}
}

// The door let the request in and the account locked before the turn began:
// monomind.Exec refuses with its own typed error, and the caller gets the same
// 401 as the door gives, not an internal error an SDK would retry.
func TestAnExecRefusedForTheAccountIsA401NotA500(t *testing.T) {
	refusal := &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused, Enforced: true}}
	h := newHarness(t, func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
		return nil, refusal
	})
	key := h.key(t, "default", "app", false)
	accounttest.Install(t, accounttest.SignedIn) // the door says yes; Exec says no

	for _, body := range []string{chatBody, strings.Replace(chatBody, `{"model"`, `{"stream":true,"model"`, 1)} {
		wantEnvelopeRefusal(t, post(h, anyPolicy, key, body))
	}
}
