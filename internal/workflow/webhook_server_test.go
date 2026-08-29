package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// TestWebhookAuthHeaderEnforced is a regression test: the "auth_header"/"auth_token"
// webhook config fields (exposed by both the GUI and the JSON schema) were
// previously read nowhere in ServeHTTP, so a webhook configured with an auth
// token was silently unauthenticated — anyone who found the path could trigger it.
func TestWebhookAuthHeaderEnforced(t *testing.T) {
	s := NewWebhookServer(":0", zerolog.Nop())
	fired := false
	if err := s.Register(&WebhookRegistration{
		Path:       "secure-hook",
		Method:     "POST",
		AuthHeader: "X-Webhook-Secret",
		AuthToken:  "s3cr3t",
		TriggerFn:  func(items []Item) { fired = true },
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// No auth header at all → rejected.
	req := httptest.NewRequest(http.MethodPost, "/webhook/secure-hook", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: status = %d, want 401", rec.Code)
	}
	if fired {
		t.Fatal("missing header: trigger fired, want rejected")
	}

	// Wrong token → rejected.
	req = httptest.NewRequest(http.MethodPost, "/webhook/secure-hook", nil)
	req.Header.Set("X-Webhook-Secret", "wrong")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}
	if fired {
		t.Fatal("wrong token: trigger fired, want rejected")
	}

	// Correct token → accepted.
	req = httptest.NewRequest(http.MethodPost, "/webhook/secure-hook", nil)
	req.Header.Set("X-Webhook-Secret", "s3cr3t")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", rec.Code)
	}
	if !fired {
		t.Fatal("correct token: trigger did not fire")
	}
}

// TestWebhookCORSDisabledByDefault is a regression test (RA2-8): the server
// previously reflected ANY Origin and advertised the auth header name in
// Allow-Headers, letting arbitrary websites probe and call webhooks from a
// browser. Hardened behavior: without MONOAGENT_WEBHOOK_ALLOWED_ORIGINS set,
// NO CORS headers are emitted at all — cross-origin browser callers are
// locked out (correct for loopback webhook use).
func TestWebhookCORSDisabledByDefault(t *testing.T) {
	t.Setenv("MONOAGENT_WEBHOOK_ALLOWED_ORIGINS", "")
	s := NewWebhookServer(":0", zerolog.Nop())
	if err := s.Register(&WebhookRegistration{
		Path:       "cors-hook",
		Method:     "POST",
		AuthHeader: "X-Webhook-Secret",
		AuthToken:  "s3cr3t",
		TriggerFn:  func(items []Item) {},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Authenticated request with an Origin header → served, but no CORS headers.
	req := httptest.NewRequest(http.MethodPost, "/webhook/cors-hook", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("X-Webhook-Secret", "s3cr3t")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if got := rec.Header().Get(h); got != "" {
			t.Fatalf("%s = %q, want unset without an allowlist", h, got)
		}
	}

	// Preflight → 204, still no CORS headers.
	req = httptest.NewRequest(http.MethodOptions, "/webhook/cors-hook", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if got := rec.Header().Get(h); got != "" {
			t.Fatalf("preflight %s = %q, want unset without an allowlist", h, got)
		}
	}
}

// TestWebhookCORSAllowlist covers MONOAGENT_WEBHOOK_ALLOWED_ORIGINS: listed
// origins are reflected (and may use the registration's auth header from a
// browser); unlisted origins get no CORS headers, so browsers block them.
func TestWebhookCORSAllowlist(t *testing.T) {
	t.Setenv("MONOAGENT_WEBHOOK_ALLOWED_ORIGINS", "https://app.example.com, https://other.example.org")
	s := NewWebhookServer(":0", zerolog.Nop())
	if err := s.Register(&WebhookRegistration{
		Path:       "cors-hook",
		Method:     "POST",
		AuthHeader: "X-Webhook-Secret",
		AuthToken:  "s3cr3t",
		TriggerFn:  func(items []Item) {},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Allowed origin → reflected, standard methods/headers advertised.
	req := httptest.NewRequest(http.MethodPost, "/webhook/cors-hook", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("X-Webhook-Secret", "s3cr3t")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want allowed origin reflected", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q, want %q", got, "GET, POST, OPTIONS")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Webhook-Secret") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to include X-Webhook-Secret", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want Origin", got)
	}

	// Disallowed origin → no CORS headers.
	req = httptest.NewRequest(http.MethodPost, "/webhook/cors-hook", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("X-Webhook-Secret", "s3cr3t")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin: Access-Control-Allow-Origin = %q, want unset", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "" {
		t.Fatalf("disallowed origin: Access-Control-Allow-Headers = %q, want unset", got)
	}
}

// TestEngineWebhookAddrEnvOverride covers MONOAGENT_WEBHOOK_ADDR resolution
// in applyEngineDefaults: env > config > default, with host:port validation.
func TestEngineWebhookAddrEnvOverride(t *testing.T) {
	t.Run("default when env and config empty", func(t *testing.T) {
		t.Setenv("MONOAGENT_WEBHOOK_ADDR", "")
		cfg := EngineConfig{}
		if err := applyEngineDefaults(&cfg); err != nil {
			t.Fatalf("applyEngineDefaults: %v", err)
		}
		if cfg.WebhookAddr != "127.0.0.1:9321" {
			t.Fatalf("WebhookAddr = %q, want loopback default", cfg.WebhookAddr)
		}
	})

	t.Run("config wins when env empty", func(t *testing.T) {
		t.Setenv("MONOAGENT_WEBHOOK_ADDR", "")
		cfg := EngineConfig{WebhookAddr: "127.0.0.1:9500"}
		if err := applyEngineDefaults(&cfg); err != nil {
			t.Fatalf("applyEngineDefaults: %v", err)
		}
		if cfg.WebhookAddr != "127.0.0.1:9500" {
			t.Fatalf("WebhookAddr = %q, want config value kept", cfg.WebhookAddr)
		}
	})

	t.Run("env overrides config", func(t *testing.T) {
		t.Setenv("MONOAGENT_WEBHOOK_ADDR", "0.0.0.0:9321")
		cfg := EngineConfig{WebhookAddr: "127.0.0.1:9500"}
		if err := applyEngineDefaults(&cfg); err != nil {
			t.Fatalf("applyEngineDefaults: %v", err)
		}
		if cfg.WebhookAddr != "0.0.0.0:9321" {
			t.Fatalf("WebhookAddr = %q, want env value", cfg.WebhookAddr)
		}
	})

	t.Run("invalid env format returns error", func(t *testing.T) {
		t.Setenv("MONOAGENT_WEBHOOK_ADDR", "not-a-host-port")
		cfg := EngineConfig{WebhookAddr: "127.0.0.1:9500"}
		err := applyEngineDefaults(&cfg)
		if err == nil {
			t.Fatal("applyEngineDefaults: want error for invalid env value, got nil")
		}
		if !strings.Contains(err.Error(), "MONOAGENT_WEBHOOK_ADDR") || !strings.Contains(err.Error(), "not-a-host-port") {
			t.Fatalf("error %q should name MONOAGENT_WEBHOOK_ADDR and the bad value", err)
		}
		// Config/default value stays in place as the fallback.
		if cfg.WebhookAddr != "127.0.0.1:9500" {
			t.Fatalf("WebhookAddr = %q, want config value untouched on error", cfg.WebhookAddr)
		}
	})
}
