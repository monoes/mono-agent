package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/monomind"
)

func TestAuthAcceptsOnlyAValidKey(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	secret := h.key(t, "default", "app", false)
	revoked := h.key(t, "default", "old", false)
	if _, err := h.keys.Revoke(context.Background(), "default", "old"); err != nil {
		t.Fatal(err)
	}
	unknown, _ := apikeys.GenerateKey()

	for name, token := range map[string]string{
		"no credential":           "",
		"legacy-token lookalike":  strings.Repeat("a", 64),
		"unknown key":             unknown,
		"revoked key":             revoked,
		"truncated key":           secret[:len(secret)-1],
		"key with a wrong prefix": "sk-" + secret[len(apikeys.KeyPrefix):],
	} {
		rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", token, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
			continue
		}
		if e := decodeErrorBody(t, rec); e["code"] != "invalid_api_key" || e["type"] != "authentication_error" {
			t.Errorf("%s: error body %v", name, e)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate header", name)
		}
	}

	// Another scheme is no credential at all.
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Basic "+secret)
	if rec := h.do(anyPolicy, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("Basic scheme: status %d, want 401", rec.Code)
	}

	if rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, ""); rec.Code != http.StatusOK {
		t.Fatalf("a valid key: status %d, body %s", rec.Code, rec.Body)
	}
}

func decodeModelList(t *testing.T, rec *httptest.ResponseRecorder) modelList {
	t.Helper()
	var list modelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("not a model list: %v\n%s", err, rec.Body)
	}
	return list
}

func TestModelsListRespectsThePolicy(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	secret := h.key(t, "default", "app", false)

	rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, "")
	list := decodeModelList(t, rec)
	if list.Object != "list" || len(list.Data) != 7 {
		t.Fatalf("any policy: object %q, %d models", list.Object, len(list.Data))
	}
	first := list.Data[0]
	if first.ID != "claude/default" || first.Object != "model" || first.OwnedBy != "claude" ||
		first.Monoagent.Runtime != "claude" || first.Monoagent.Model != "default" ||
		first.Monoagent.Confinement != "chat-only" || len(first.Monoagent.Capabilities) != 1 || first.Monoagent.Capabilities[0] != "text" {
		t.Errorf("first model: %+v", first)
	}
	for _, m := range list.Data {
		if m.ID == "auto" {
			t.Error("auto is not offered in phase 1")
		}
		if m.ID == "claude/opus" {
			t.Error("an alias must not be listed")
		}
	}

	chat := decodeModelList(t, h.serve(Policy{Max: ChatOnly}, http.MethodGet, "/v1/models", secret, ""))
	if len(chat.Data) != 2 {
		t.Fatalf("chat-only policy listed %d models, want 2 (claude only)", len(chat.Data))
	}
	for _, m := range chat.Data {
		if m.Monoagent.Runtime != "claude" {
			t.Errorf("chat-only policy listed %s", m.ID)
		}
	}
}

func TestModelRetrieve(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	secret := h.key(t, "default", "app", false)

	for path, want := range map[string]string{
		"/v1/models/claude/opus[1m]":           "claude/opus[1m]",
		"/v1/models/claude/opus%5B1m%5D":       "claude/opus[1m]",
		"/v1/models/codex/gpt-6-astra":         "codex/gpt-6-astra",
		"/v1/models/agy/gemini-3.8-flash-high": "antigravity/gemini-3.8-flash-high",
		"/v1/models/claude":                    "claude/default",
		"/v1/models/claude/opus":               "claude/opus", // an alias resolves
	} {
		rec := h.serve(anyPolicy, http.MethodGet, path, secret, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, body %s", path, rec.Code, rec.Body)
			continue
		}
		var m modelObject
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.ID != want {
			t.Errorf("%s: got %q (%v), want %q", path, m.ID, err, want)
		}
	}

	for _, path := range []string{"/v1/models/nope/x", "/v1/models/claude/--help", "/v1/models/auto"} {
		rec := h.serve(anyPolicy, http.MethodGet, path, secret, "")
		if rec.Code != http.StatusNotFound || decodeErrorBody(t, rec)["code"] != "model_not_found" {
			t.Errorf("%s: status %d, body %s", path, rec.Code, rec.Body)
		}
	}

	// A model the policy disallows is not found, not forbidden.
	rec := h.serve(Policy{Max: ChatOnly}, http.MethodGet, "/v1/models/codex/gpt-6-astra", secret, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a disallowed model: status %d, want 404", rec.Code)
	}
}

func TestModelsWhenMonomindIsMissing(t *testing.T) {
	h := newHarness(t, okTurn("x"), func(d *Deps, _ *Config) {
		d.Catalog.Scan = func(context.Context) (*monomind.ScanResult, error) {
			return nil, &monomind.ErrNotFound{Tried: []string{"/home/svc/.nvm/bin/monomind"}}
		}
	})
	secret := h.key(t, "default", "app", false)
	rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, "")
	if rec.Code != http.StatusServiceUnavailable || decodeErrorBody(t, rec)["code"] != "runtime_not_available" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	// The caller learns it, not where monomind was looked for; the operator's log has both.
	if strings.Contains(rec.Body.String(), "/home/svc") {
		t.Errorf("the response leaks a path of the server: %s", rec.Body)
	}
	id := rec.Header().Get("X-Request-Id")
	if joined := strings.Join(h.logged(), "\n"); !strings.Contains(joined, "/home/svc") || !strings.Contains(joined, id) {
		t.Errorf("the log must hold the request id and the detail: %q", joined)
	}
}

func TestEveryResponseCarriesARequestIDAndAFailedAuthIsLogged(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	secret := h.key(t, "default", "app", false)
	wrong := "sk-ma-" + strings.Repeat("q", 43)

	rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", wrong, "")
	id := rec.Header().Get("X-Request-Id")
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(id, "req_") {
		t.Fatalf("a 401 must carry a request id: %d %q", rec.Code, id)
	}
	if ok := h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, ""); ok.Header().Get("X-Request-Id") == "" || ok.Header().Get("X-Request-Id") == id {
		t.Errorf("every response gets its own request id, got %q", ok.Header().Get("X-Request-Id"))
	}

	var line string
	for _, l := range h.logged() {
		if strings.Contains(l, "req="+id) {
			line = l
		}
	}
	if !strings.Contains(line, "status=401") || !strings.Contains(line, "remote=") || strings.Contains(line, wrong) {
		t.Errorf("a failed authentication is logged with its request id and the caller, never the key: %q (all %q)", line, h.logged())
	}
}

// `api status` probes a listener with no credential at all: that is not a failed
// login, and must not fill the log with lines that look like one. A request that
// did send a credential, of whatever kind, still is an attempt.
func TestARequestWithoutACredentialIsNotLoggedAsAFailedLogin(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", "", "")
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("X-Request-Id"), "req_") {
		t.Fatalf("a keyless request is still a 401 with a request id: %d %q", rec.Code, rec.Header().Get("X-Request-Id"))
	}
	if lines := h.logged(); len(lines) != 0 {
		t.Errorf("a request with no credential must not be logged as a failed login: %q", lines)
	}

	if rec := h.serve(anyPolicy, http.MethodGet, "/v1/models", "sk-ma-"+strings.Repeat("q", 43), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if rec := h.do(anyPolicy, r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	if lines := h.logged(); len(lines) != 2 {
		t.Errorf("a wrong key and a credential of the wrong kind are each logged once: %q", lines)
	}
}

// A key created with --context puts the profile's knowledge, which includes
// captured web pages nobody vetted, into the prompt, so by default only models
// with no native tools may receive it.
func TestModelsForAContextKeyAreChatOnlyModels(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	plain, withContext := h.key(t, "default", "plain", false), h.key(t, "default", "ctx", true)

	forContext := decodeModelList(t, h.serve(anyPolicy, http.MethodGet, "/v1/models", withContext, ""))
	if len(forContext.Data) == 0 {
		t.Fatal("a context key must still be offered the chat-only models")
	}
	for _, m := range forContext.Data {
		if m.Monoagent.Confinement != "chat-only" {
			t.Errorf("a context key was offered %s (%s)", m.ID, m.Monoagent.Confinement)
		}
	}
	if forPlain := decodeModelList(t, h.serve(anyPolicy, http.MethodGet, "/v1/models", plain, "")); len(forPlain.Data) <= len(forContext.Data) {
		t.Errorf("a plain key must see more than a context key: %d vs %d", len(forPlain.Data), len(forContext.Data))
	}
	if rec := h.serve(anyPolicy, http.MethodGet, "/v1/models/codex/gpt-6-astra", withContext, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a sandboxed model is not found for a context key: %d", rec.Code)
	}
	if rec := h.serve(anyPolicy, http.MethodGet, "/v1/models/codex/gpt-6-astra", plain, ""); rec.Code != http.StatusOK {
		t.Errorf("the same model is there for a plain key: %d", rec.Code)
	}
}

// The operator can raise that maximum, and it never goes above the listener.
func TestModelsForAContextKeyFollowTheContextMax(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	withContext := h.key(t, "default", "ctx", true)
	classes := func(p Policy) map[string]bool {
		seen := map[string]bool{}
		for _, m := range decodeModelList(t, h.serve(p, http.MethodGet, "/v1/models", withContext, "")).Data {
			seen[m.Monoagent.Confinement] = true
		}
		return seen
	}

	if got := classes(Policy{Max: Unconfined, ContextMax: Sandboxed}); !got["chat-only"] || !got["sandboxed"] || got["unconfined"] {
		t.Errorf("a context maximum of sandboxed offers chat-only and sandboxed models: %v", got)
	}
	if got := classes(Policy{Max: ChatOnly, ContextMax: Unconfined}); len(got) != 1 || !got["chat-only"] {
		t.Errorf("a context maximum never raises what the listener serves: %v", got)
	}
	if rec := h.serve(Policy{Max: Unconfined, ContextMax: Sandboxed}, http.MethodGet, "/v1/models/codex/gpt-6-astra", withContext, ""); rec.Code != http.StatusOK {
		t.Errorf("a sandboxed model is found for a context key once the maximum allows it: %d", rec.Code)
	}
}
