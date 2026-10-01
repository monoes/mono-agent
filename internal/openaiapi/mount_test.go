package openaiapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
