package libraryfake

import (
	"net/http"
	"net/url"
	"time"
)

// RefreshMode is how the fake answers a refresh_token grant.
type RefreshMode int

const (
	RefreshOK            RefreshMode = iota // rotate and issue, as monoes.me does
	RefreshInvalidGrant                     // 400 invalid_grant: the one answer that is a refusal (spec D27)
	RefreshInvalidClient                    // 401 invalid_client
	RefreshInvalidTarget                    // 400 invalid_target
	RefreshServerError                      // 500 with a plain body
	RefreshMalformed                        // 200 with a body that is not JSON
	RefreshDrop                             // closes the connection without answering, before it looks at the token: the token stays good
	RefreshLost                             // rotates the token, then closes the connection without answering: the answer is lost (A24)
)

// now is the fake's clock: it dates tokens and decides when they expire.
func (s *Server) now() time.Time {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// SetClock sets the fake's clock (nil: the real one).
func (s *Server) SetClock(now func() time.Time) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.clock = now
}

// OnToken registers fn to run when a request to the token endpoint arrives: in that request's own
// goroutine, before the fake looks at the request, with no lock held, and the fake answers it
// afterwards. A test cancels a caller's context in it to see what the caller does when it gives up
// while monoes.me is answering. nil removes the hook.
func (s *Server) OnToken(fn func()) {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.tokenHook = fn
}

// onToken is the registered hook, read under its lock.
func (s *Server) onToken() func() {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return s.tokenHook
}

// SetRefreshMode picks how refresh_token grants are answered from now on.
func (s *Server) SetRefreshMode(m RefreshMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshMode = m
}

// SetOpaqueTokens makes the fake ignore the resource indicator everywhere: access
// tokens stay opaque, as at a monoes.me that does not mint audience-bound JWTs yet.
func (s *Server) SetOpaqueTokens(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opaque = on
}

// SetEmailOpaque makes claim/verify answer as monoes.me does today: an opaque
// access token and no refresh token, whatever the request carries.
func (s *Server) SetEmailOpaque(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailOpaque = on
}

// SetEmailTrade makes claim/verify answer an opaque access token beside a refresh
// token whatever the request carries, so the client has to trade the refresh token
// at the token endpoint for the signed one. (By default a request that asks for the
// MonoAgent audience gets the token endpoint's own answer, as in plan A's Task 7.)
func (s *Server) SetEmailTrade(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailTrade = on
}

// Block blocks a user as monoes.me's admin route does: every token the user holds
// is deleted, so a refresh answers invalid_grant (without counting as a replay),
// and no new sign-in succeeds.
func (s *Server) Block(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[userID] = true
	s.revokeUser(userID)
}

// revokeUser revokes every token of a user; the caller holds s.mu.
func (s *Server) revokeUser(userID string) {
	for _, g := range s.access {
		if g.user != nil && g.user.ID == userID {
			g.revoked = true
		}
	}
	for _, g := range s.refr {
		if g.user != nil && g.user.ID == userID {
			g.revoked = true
		}
	}
}

// Unblock lets the user sign in again. Tokens revoked by Block stay revoked.
func (s *Server) Unblock(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocked, userID)
}

// TokenRequest is one request to the token endpoint as the fake received it.
type TokenRequest struct {
	Form   url.Values  // the form fields, refresh tokens and codes included: never print them
	Header http.Header // an Authorization header reads "redacted": that it was sent, never what it held
}

// TokenRequests is every request the token endpoint has received, in order. A test
// pins the exact set of fields a sign-in and a refresh send.
func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TokenRequest(nil), s.tokenReqs...)
}

// LastResource is the resource indicator of the latest token or emailed-code
// request, "" if it carried none.
func (s *Server) LastResource() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResource
}

// NewGrant is a login of username made before the machine session existed: an
// opaque access token and a refresh token, as the token endpoint answers a client
// that sends no resource.
func (s *Server) NewGrant(username string) (access, refresh string) {
	u := s.User(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	g := &grant{user: u, clientID: "monoagent",
		scopes: []string{"openid", "profile", "email", "offline_access", "library:read", "library:write"}}
	s.issue(g)
	return g.accessToken, g.refresh
}

// refuse answers a refresh with the failure refreshMode asks for and says whether
// it did; the caller holds s.mu.
func (s *Server) refuse(w http.ResponseWriter) bool {
	switch s.refreshMode {
	case RefreshInvalidGrant:
		writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "the refresh token was revoked"})
	case RefreshInvalidClient:
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
	case RefreshInvalidTarget:
		writeJSON(w, 400, map[string]string{"error": "invalid_target"})
	case RefreshServerError:
		http.Error(w, "internal error", http.StatusInternalServerError)
	case RefreshMalformed:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("<html>not json"))
	case RefreshDrop:
		hangUp(w)
	default:
		return false
	}
	return true
}

// hangUp closes the connection without answering.
func hangUp(w http.ResponseWriter) {
	if h, ok := w.(http.Hijacker); ok {
		if conn, _, err := h.Hijack(); err == nil {
			conn.Close()
		}
	}
}
