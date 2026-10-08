package account_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// followRedirect plays the user's browser: it follows the authorize URL's redirect_uri
// back to the loopback listener with the given extra query, and returns the status.
func followRedirect(t *testing.T, authURL string, query url.Values) int {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Error(err)
		return 0
	}
	resp, err := http.Get(u.Query().Get("redirect_uri") + "?" + query.Encode())
	if err != nil {
		t.Errorf("browser: %v", err)
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

// inBrowser runs browse on a goroutine of its own, as the user's browser runs beside the
// sign-in, and the test waits for that goroutine before it ends: what browse reports
// reaches the test, never a test that is already over.
func inBrowser(t *testing.T, browse func()) {
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		browse()
	}()
}

func TestAuthorizeReturnsTheCodeTheVerifierAndTheRedirect(t *testing.T) {
	var shown string
	o := account.AuthorizeOptions{Label: "test login", Timeout: 10 * time.Second, SuccessText: "done",
		OnURL: func(u string) { shown = u },
		Open: func(u string) error {
			uq, _ := url.Parse(u)
			inBrowser(t, func() { followRedirect(t, u, url.Values{"code": {"c1"}, "state": {uq.Query().Get("state")}}) })
			return nil
		}}
	res, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", url.Values{"client_id": {"monoagent"}, "resource": {"https://r"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(shown)
	got := q.Query()
	sum := sha256.Sum256([]byte(res.Verifier))
	if res.Code != "c1" || res.Redirect != got.Get("redirect_uri") || !strings.HasPrefix(res.Redirect, "http://127.0.0.1:") ||
		got.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || got.Get("code_challenge_method") != "S256" ||
		got.Get("response_type") != "code" || got.Get("client_id") != "monoagent" || got.Get("resource") != "https://r" {
		t.Fatalf("result: code ok %v, redirect %q, verifier of %d bytes; authorize URL %s", res.Code == "c1", res.Redirect, len(res.Verifier), shown)
	}
}

func TestAuthorizeIgnoresAForeignStateAndKeepsWaiting(t *testing.T) {
	o := account.AuthorizeOptions{Timeout: 10 * time.Second, Open: func(u string) error {
		inBrowser(t, func() {
			if followRedirect(t, u, url.Values{"code": {"evil"}, "state": {"not-ours"}}) != http.StatusBadRequest {
				t.Error("a foreign state was not refused")
			}
			uq, _ := url.Parse(u)
			followRedirect(t, u, url.Values{"code": {"good"}, "state": {uq.Query().Get("state")}})
		})
		return nil
	}}
	res, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, o)
	if err != nil || res.Code != "good" {
		t.Fatalf("result: %v, %v", res != nil && res.Code == "good", err)
	}
}

func TestAuthorizeReportsARefusalAndATimeout(t *testing.T) {
	o := account.AuthorizeOptions{Label: "account login", Timeout: 10 * time.Second, Open: func(u string) error {
		uq, _ := url.Parse(u)
		inBrowser(t, func() {
			followRedirect(t, u, url.Values{"error": {"access_denied"}, "state": {uq.Query().Get("state")}})
		})
		return nil
	}}
	if _, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, o); err == nil ||
		!strings.Contains(err.Error(), "account login: monoes.me refused: access_denied") {
		t.Fatalf("refusal: %v", err)
	}
	_, err := account.AuthorizeInBrowser(context.Background(), "https://monoes.example/authorize", nil, account.AuthorizeOptions{Label: "library login", Timeout: 200 * time.Millisecond})
	if err == nil || err.Error() != "library login: no answer from the browser within 200ms" {
		t.Fatalf("timeout: %v", err)
	}
}

func TestDiscoverPinsEveryEndpointToTheBaseHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"authorization_endpoint":"https://evil.example/oauth/authorize?x=1","token_endpoint":"` + "http://" + r.Host + `/oauth/token"}`))
	}))
	defer srv.Close()
	ep, err := account.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if err != nil || ep.AuthorizationEndpoint != srv.URL+"/oauth/authorize?x=1" || ep.TokenEndpoint != srv.URL+"/oauth/token" ||
		ep.RevocationEndpoint != srv.URL+"/api/auth/oauth2/revoke" {
		t.Fatalf("endpoints %+v, %v", ep, err)
	}
	dead := refusedURL(t)
	ep, err = account.DiscoverEndpoints(context.Background(), http.DefaultClient, dead)
	if err == nil || ep.TokenEndpoint != dead+"/api/auth/oauth2/token" {
		t.Fatalf("an unreachable server must set the error and still give the defaults: %+v, %v", ep, err)
	}

	// url.Parse also takes references that name no host and do not start with a slash.
	// Appended to the base as they stand, they move the host: "@evil.example/x" turns the
	// base into a user name, ".evil.example/x" into a sub-domain of the base host. Each
	// shape, in any of the three endpoints, must come out on the base host.
	for _, c := range []struct{ name, endpoint, path string }{
		{"user name", "@evil.example/x", "/@evil.example/x"},
		{"leading dot", ".evil.example/x", "/.evil.example/x"},
		{"host without a scheme", "evil.example/x", "/evil.example/x"},
		{"path without a slash", "oauth/token", "/oauth/token"},
		{"base host under another scheme", "https://{host}/oauth/token", "/oauth/token"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				e := strings.ReplaceAll(c.endpoint, "{host}", r.Host)
				json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": e, "token_endpoint": e, "revocation_endpoint": e})
			}))
			defer srv.Close()
			ep, err := account.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
			want := srv.URL + c.path
			if err != nil || ep.AuthorizationEndpoint != want || ep.TokenEndpoint != want || ep.RevocationEndpoint != want {
				t.Errorf("%q: endpoints %+v, %v; want all three at %s", c.endpoint, ep, err, want)
			}
		})
	}
}
