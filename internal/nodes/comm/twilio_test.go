package comm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestTwilioPostNonJSONErrorBody verifies that a non-2xx response with a
// non-JSON body (e.g. an HTML error page from an intermediate proxy) is
// reported with its real HTTP status code and body, rather than being
// swallowed by a generic "decode response" error from a failed JSON
// Unmarshal that runs before the status check.
func TestTwilioPostNonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
	}))
	defer srv.Close()

	_, err := twilioPost(context.Background(), srv.URL, "AC_test", "token", url.Values{})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected error to mention the HTTP status code 502, got: %v", err)
	}
	if strings.Contains(err.Error(), "decode response") {
		t.Fatalf("error should surface the HTTP status, not a generic decode error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("expected error to include the raw response body, got: %v", err)
	}
}

// TestTwilioPostJSONErrorBody verifies the success path for Twilio's own
// structured JSON error responses is preserved: the message/code fields
// still surface when the body does parse as JSON.
func TestTwilioPostJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Authenticate","code":20003}`))
	}))
	defer srv.Close()

	_, err := twilioPost(context.Background(), srv.URL, "AC_test", "token", url.Values{})
	if err == nil {
		t.Fatal("expected an error for a non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Authenticate") {
		t.Fatalf("expected error to mention status 401 and Twilio message, got: %v", err)
	}
}

// TestTwilioPostSuccess verifies the success path still decodes a normal
// JSON response body.
func TestTwilioPostSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sid":"SM123","status":"queued"}`))
	}))
	defer srv.Close()

	result, err := twilioPost(context.Background(), srv.URL, "AC_test", "token", url.Values{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["sid"] != "SM123" {
		t.Fatalf("expected sid SM123, got: %v", result)
	}
}
