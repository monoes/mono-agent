package openaiapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/httpapi"
)

// The gateway rides httpapi's single ExtraRoutes slot on the main listener:
// it must coexist with the legacy routes, and registering it must not panic
// on a pattern conflict.
func TestGatewayMountsNextToTheLegacyHTTPAPI(t *testing.T) {
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

	do := func(path, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}
	if rec := do("/health", ""); rec.Code != http.StatusOK {
		t.Errorf("the legacy /health: %d", rec.Code)
	}
	rec := do("/v1/models", "")
	if rec.Code != http.StatusUnauthorized || decodeErrorBody(t, rec)["code"] != "invalid_api_key" {
		t.Errorf("/v1/models without a key: %d %s", rec.Code, rec.Body)
	}
	if rec := do("/v1/models", key); rec.Code != http.StatusOK {
		t.Errorf("/v1/models with a key: %d %s", rec.Code, rec.Body)
	}
}

// D14: a key never opens the legacy routes. The legacy server compares the
// bearer credential with its own token, whatever the gateway thinks of it. (The
// other direction, the legacy token never opening /v1, is in
// TestAuthAcceptsOnlyAValidKey.)
func TestAKeyDoesNotOpenALegacyRoute(t *testing.T) {
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

	for _, path := range []string{"/workflows", "/nodes", "/hil"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "monoagentcli-httpapi") {
			t.Errorf("%s with a gateway key: %d %q, want the legacy API's 401", path, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
}
