package library_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/library"
)

// A token endpoint that redirects a POST must not take the refresh token, the code verifier or the
// token to revoke along: Go's client re-sends the body of a 307 or 308 to whatever host the Location
// names. The library follows no redirect of a request that carries a secret in its body.
func TestSecretsInARequestBodyFollowNoRedirect(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		var stolen atomic.Int32
		elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				stolen.Add(1)
			}
		}))
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				http.Redirect(w, r, elsewhere.URL+"/collect", status)
				return
			}
			http.NotFound(w, r)
		}))
		store := &memStore{t: &library.Token{AccessToken: "at-old", RefreshToken: "rt-secret", TokenType: "Bearer", Method: "pkce",
			ExpiresAt: time.Now().Add(-time.Hour)}}
		c := mustClient(t, issuer.URL, store)
		_, _ = c.Token(context.Background()) // a refresh: the login is expired
		_ = c.Logout(context.Background())   // a revocation of the token
		if n := stolen.Load(); n != 0 {
			t.Errorf("HTTP %d: %d requests with a body followed the redirect to another host", status, n)
		}
		issuer.Close()
		elsewhere.Close()
	}
}
