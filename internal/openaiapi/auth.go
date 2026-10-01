package openaiapi

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/monoes/mono-agent/internal/apikeys"
)

// Principal is who a verified request acts as. The profile comes from the
// key row, never from anything the client sends.
type Principal struct {
	KeyID     string
	ProfileID string
	// Context is true when the key adds the profile's knowledge to requests.
	Context bool
	// RequestID is the id the response carries as X-Request-Id and the
	// server log carries on the request's line.
	RequestID string
}

// policyFor is the policy a request is held to: the listener's, capped at the
// context maximum for a key created with --context.
func policyFor(p Policy, pr Principal) Policy {
	if pr.Context {
		return p.ForContextKey()
	}
	return p
}

const bearerPrefix = "Bearer "

// bearer returns the credential of an `Authorization: Bearer …` header.
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) <= len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(h[len(bearerPrefix):])
}

// auth runs next only for a request that carries a valid, unrevoked key. It
// never consults the vault or the legacy HTTP API token: these are separate
// credentials for separate routes. Every response, an error included, carries
// the request's X-Request-Id.
func (g *Gateway) auth(next func(w http.ResponseWriter, r *http.Request, p Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID("req_")
		w.Header().Set("X-Request-Id", id)
		key, err := g.deps.Keys.Authenticate(r.Context(), bearer(r))
		switch {
		case errors.Is(err, apikeys.ErrInvalidKey):
			// The caller's address, never the key it sent, so attempts are
			// visible. A request that sent no credential at all (`api status`
			// probing a listener) is not an attempt and is not logged.
			if r.Header.Get("Authorization") != "" {
				g.deps.Logf("req=%s status=401 remote=%s", id, remoteHost(r))
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="monoagentcli-api"`)
			writeError(w, errAuth())
			return
		case err != nil:
			g.deps.Logf("req=%s verifying an api key failed: %v", id, err)
			writeError(w, errInternal("The API key could not be verified. "+quoteRequestID))
			return
		}
		next(w, r, Principal{KeyID: key.ID, ProfileID: key.ProfileID, Context: key.Context, RequestID: id})
	})
}

// remoteHost is the caller's address without its port.
func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// newRequestID returns an id such as req_k3j2h1g4f5d6s7a8, for X-Request-Id
// and the chat completion id.
func newRequestID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}
