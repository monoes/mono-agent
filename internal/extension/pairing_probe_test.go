package extension

import (
	"net/http"
	"os"
	"strconv"
	"testing"
)

func TestCheckPairing(t *testing.T) {
	_, _, _ = startCaptureServer(t) // isolated HOME; the bridge wrote its token there
	base := "http://127.0.0.1:" + os.Getenv(ExtensionPortEnv)
	if _, err := strconv.Atoi(os.Getenv(ExtensionPortEnv)); err != nil {
		t.Fatal(err)
	}
	if got, err := CheckPairing(base); got != PairingOK || err != nil {
		t.Fatalf("same token: %s, %v", got, err)
	}
	// Another HOME / a reset token: the file no longer holds the bridge's.
	path, _ := tokenPath()
	if err := os.WriteFile(path, []byte("not-the-bridges-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := CheckPairing(base); got != PairingMismatch {
		t.Fatalf("mismatched token: %s", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := CheckPairing(base); got != PairingMismatch {
		t.Fatalf("missing token: %s", got)
	}
	// Nothing listening: unknown, not mismatch.
	if got, err := CheckPairing("http://127.0.0.1:1"); got != PairingUnknown || err == nil {
		t.Fatalf("no bridge: %s, %v", got, err)
	}
}

func TestAuthProbeRefusesWebCallersAndQueryTokens(t *testing.T) {
	_, _, _ = startCaptureServer(t)
	base := "http://127.0.0.1:" + os.Getenv(ExtensionPortEnv)
	token, err := loadToken()
	if err != nil {
		t.Fatal(err)
	}
	do := func(mutate func(*http.Request)) int {
		req, _ := http.NewRequest(http.MethodGet, base+"/monoagent/auth", nil)
		req.Header.Set(tokenHeader, token)
		mutate(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do(func(*http.Request) {}); got != http.StatusNoContent {
		t.Fatalf("local caller with the token: %d", got)
	}
	if got := do(func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }); got != http.StatusForbidden {
		t.Fatalf("web origin: %d, want 403", got)
	}
	// DNS rebinding: the page's own domain resolves to 127.0.0.1.
	if got := do(func(r *http.Request) { r.Host = "evil.test:" + os.Getenv(ExtensionPortEnv) }); got != http.StatusForbidden {
		t.Fatalf("rebound host: %d, want 403", got)
	}
	if got := do(func(r *http.Request) { r.Header.Set("Origin", "chrome-extension://abc") }); got != http.StatusNoContent {
		t.Fatalf("extension origin: %d", got)
	}
	// The token only counts in the header.
	if got := do(func(r *http.Request) {
		r.Header.Del(tokenHeader)
		r.URL.RawQuery = "token=" + token
	}); got != http.StatusUnauthorized {
		t.Fatalf("query-string token: %d, want 401", got)
	}
}
