package library_test

// The base-host rule at the library's seam. The authorization server publishes where its OAuth endpoints
// are, and that document must never decide where a secret goes: whatever it names, the sign-in (the code
// and the PKCE verifier), the refresh (the refresh token) and the revocation (a token) all go to the host
// the client was built for. account.DiscoverEndpoints holds the rule and has its own tests; these pin that
// the library goes through it for every request that carries a secret, not only for the authorize URL.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/library"
)

// Each of the first four, joined to a base URL with no more care than a plus sign, names another host:
// against https://monoes.me they become https://monoes.me@evil.example/x, https://monoes.me.evil.example/x,
// https://monoes.meevil.example/x and https://monoes.meoauth/token. The last one is simply absolute.
var hostileEndpoints = []struct{ name, endpoint string }{
	{"at-sign", "@evil.example/x"},
	{"leading-dot", ".evil.example/x"},
	{"bare-host", "evil.example/x"},
	{"no-leading-slash", "oauth/token"},
	{"absolute-other-host", "http://other-host:8443/x"},
}

// hostileIssuer is an authorization server whose metadata names the same endpoint three times. It answers a
// request by what it is, on whatever path it arrives: an authorize request sends the browser straight back
// with a code, a grant is given, a revocation is accepted. It keeps the forms posted to it.
type hostileIssuer struct {
	endpoint string // what the metadata names for each of its three endpoints

	mu    sync.Mutex
	posts map[string][]url.Values // by grant_type, "revoke" for a revocation
}

func (h *hostileIssuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server":
		serveJSON(w, map[string]string{"authorization_endpoint": h.endpoint, "token_endpoint": h.endpoint, "revocation_endpoint": h.endpoint})
	case r.Method == http.MethodGet && r.URL.Path == "/api/library/me":
		serveJSON(w, library.Me{User: library.User{ID: "u-ada", Name: "Ada", Username: "ada", Email: "ada@example.com"}, Scopes: []string{"library:read"}})
	case r.Method == http.MethodGet && q.Get("redirect_uri") != "":
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {"code-1"}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	case r.Method == http.MethodPost:
		_ = r.ParseForm()
		kind := r.PostForm.Get("grant_type")
		if kind == "" && r.PostForm.Get("token") != "" {
			kind = "revoke"
		}
		if kind == "" {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		h.posts[kind] = append(h.posts[kind], r.PostForm)
		h.mu.Unlock()
		if kind != "revoke" {
			serveJSON(w, map[string]any{"access_token": "at-new", "refresh_token": "rt-new", "token_type": "Bearer", "expires_in": 3600})
		}
	default:
		http.NotFound(w, r)
	}
}

// got returns the forms of the given kind that reached the issuer.
func (h *hostileIssuer) got(kind string) []url.Values {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]url.Values(nil), h.posts[kind]...)
}

func serveJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// baseHostGuard is a client's transport. It refuses, and fails the test for, any request that is not for the
// base host, and it refuses before it dials, so a request that would have left costs no DNS lookup.
type baseHostGuard struct {
	t    *testing.T
	host string // host:port of the base URL
	next http.RoundTripper
}

func (g *baseHostGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != g.host {
		if r.Body != nil {
			r.Body.Close()
		}
		g.t.Errorf("%s request to %s%s leaves the base host %s", r.Method, r.URL.Host, r.URL.Path, g.host)
		return nil, fmt.Errorf("refused: %s is not the base host", r.URL.Host)
	}
	return g.next.RoundTrip(r)
}

// rig is a hostile issuer with a client for it that can only talk to it.
type rig struct {
	cl     *library.Client
	issuer *hostileIssuer
	host   string // host:port of the base URL
}

func newRig(t *testing.T, endpoint string, store library.TokenStore) *rig {
	t.Helper()
	h := &hostileIssuer{endpoint: endpoint, posts: map[string][]url.Values{}}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := &rig{cl: mustClient(t, srv.URL, store), issuer: h, host: srv.Listener.Addr().String()}
	r.cl.HTTP.Transport = &baseHostGuard{t: t, host: r.host, next: http.DefaultTransport}
	return r
}

// onHost reports whether raw is an http URL for host, with no userinfo in front of it.
func onHost(raw, host string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && u.Host == host && u.User == nil
}

// awaitedBrowser plays the user's browser as browser does, on a goroutine the subtest waits for: what it
// reports reaches the subtest, never a subtest that is already over (as account's inBrowser does).
func awaitedBrowser(t *testing.T) func(string) error {
	return func(u string) error {
		done := make(chan struct{})
		t.Cleanup(func() { <-done })
		go func() {
			defer close(done)
			resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(u)
			if err != nil {
				t.Errorf("browser: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

// The sign-in: the user is sent to an authorize URL on the base host, and the code and the verifier go to a
// token endpoint there.
func TestLoginPKCEStaysOnTheBaseHost(t *testing.T) {
	for _, c := range hostileEndpoints {
		t.Run(c.name, func(t *testing.T) {
			store := &memStore{}
			r := newRig(t, c.endpoint, store)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			open := awaitedBrowser(t)
			var shown string
			tok, err := r.cl.LoginPKCE(ctx, library.LoginOptions{
				OnURL: func(u string) { shown = u },
				Open: func(u string) error {
					if !onHost(u, r.host) {
						cancel() // the test's own browser stays on the base host too
						return nil
					}
					return open(u)
				},
				Timeout: 10 * time.Second,
			})
			if !onHost(shown, r.host) {
				noQuery, _, _ := strings.Cut(shown, "?")
				t.Fatalf("the authorize URL %q is not on the base host %s", noQuery, r.host)
			}
			if err != nil {
				t.Fatalf("LoginPKCE: %v", err)
			}
			if posts := r.issuer.got("authorization_code"); len(posts) != 1 || posts[0].Get("code") != "code-1" || posts[0].Get("code_verifier") == "" {
				t.Fatalf("the code and the verifier did not reach the base host's token endpoint (%d exchanges)", len(posts))
			}
			if tok.User == nil || tok.User.Username != "ada" || store.t == nil {
				t.Fatal("the login was not completed and stored")
			}
		})
	}
}

// The refresh: the stored refresh token goes to the base host's token endpoint. Token falls back to the
// stored login when a refresh fails, so what proves it is what the issuer received and what came back.
func TestRefreshStaysOnTheBaseHost(t *testing.T) {
	for _, c := range hostileEndpoints {
		t.Run(c.name, func(t *testing.T) {
			store := &memStore{t: &library.Token{AccessToken: "at-old", RefreshToken: "rt-stored", TokenType: "Bearer", Method: "pkce",
				ExpiresAt: time.Now().Add(-time.Hour)}}
			r := newRig(t, c.endpoint, store)
			tok, err := r.cl.Token(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if posts := r.issuer.got("refresh_token"); len(posts) != 1 || posts[0].Get("refresh_token") != "rt-stored" {
				t.Fatalf("the refresh token did not reach the base host's token endpoint (%d refreshes)", len(posts))
			}
			if tok == nil || tok.AccessToken != "at-new" || store.t == nil || store.t.AccessToken != "at-new" {
				t.Fatal("the refreshed login was not returned and stored")
			}
		})
	}
}

// The revocation: the stored token goes to the base host's revocation endpoint. Logout is best effort and
// says nothing of a failed revocation, so here too what proves it is what the issuer received.
func TestLogoutStaysOnTheBaseHost(t *testing.T) {
	for _, c := range hostileEndpoints {
		t.Run(c.name, func(t *testing.T) {
			store := &memStore{t: &library.Token{AccessToken: "at-live", RefreshToken: "rt-stored", TokenType: "Bearer", Method: "pkce",
				ExpiresAt: time.Now().Add(time.Hour)}}
			r := newRig(t, c.endpoint, store)
			if err := r.cl.Logout(context.Background()); err != nil {
				t.Fatal(err)
			}
			if posts := r.issuer.got("revoke"); len(posts) != 1 || posts[0].Get("token") != "rt-stored" {
				t.Fatalf("the stored token did not reach the base host's revocation endpoint (%d revocations)", len(posts))
			}
			if store.t != nil {
				t.Fatal("Logout kept the login")
			}
		})
	}
}
