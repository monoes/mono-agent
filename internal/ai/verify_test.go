package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each adapter's model list authenticates the way its completions do, and
// a refused key comes back as an error naming the status.
func TestListModelsChecksKey(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		if r.Header.Get("Authorization") == "Bearer good" || r.Header.Get("x-api-key") == "good" || r.URL.Query().Get("key") == "good" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		list func(key string) error
		path string
	}{
		{"openai", func(k string) error { return NewOpenAIClient(k, srv.URL, "").listModels(ctx) }, "/models"},
		{"anthropic", func(k string) error { return NewAnthropicClient(k, srv.URL).listModels(ctx) }, "/models"},
		{"google", func(k string) error { return NewGoogleClient(k, srv.URL).listModels(ctx) }, "/models"},
	} {
		if err := tc.list("good"); err != nil {
			t.Errorf("%s good key: %v", tc.name, err)
		}
		if got.Method != http.MethodGet || got.URL.Path != tc.path {
			t.Errorf("%s: %s %s, want GET %s", tc.name, got.Method, got.URL.Path, tc.path)
		}
		err := tc.list("bad-key-123")
		if err == nil || !strings.Contains(err.Error(), "status 401") {
			t.Errorf("%s bad key: %v", tc.name, err)
		}
		if err != nil && strings.Contains(err.Error(), "bad-key-123") {
			t.Errorf("%s: key in error: %v", tc.name, err)
		}
	}
}

// Only providers whose model list needs the key, at their own URL, get the
// free check; the rest fall back to a completion.
func TestVerifyKeyScope(t *testing.T) {
	ctx := context.Background()
	for _, p := range []AIProvider{
		{ProviderID: "openrouter", APIKey: "k"},                            // public model list
		{ProviderID: "perplexity", APIKey: "k"},                            // no model list
		{ProviderID: "openai", APIKey: "k", BaseURL: "http://127.0.0.1:1"}, // a proxy
		{ProviderID: "nope", APIKey: "k", Tier: "gateway"},
	} {
		if ok, err := VerifyKey(ctx, p); ok || err != nil {
			t.Errorf("%s (%s): ok=%v err=%v, want no free check", p.ProviderID, p.BaseURL, ok, err)
		}
	}
}
