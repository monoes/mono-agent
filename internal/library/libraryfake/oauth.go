package libraryfake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// The fake's OAuth side: authorize (as a browser already logged in as
// BrowserUser that consents at once), token (code + PKCE, refresh with
// rotation) and the email-code verify. A request that carries the resource
// indicator of the MonoAgent audience gets a signed JWT access token (spec
// §4.1), one without gets an opaque one, as monoes.me does.

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	ru, err := url.Parse(redirect)
	if q.Get("client_id") != "monoagent" || err != nil || ru.Scheme != "http" || ru.Hostname() != "127.0.0.1" || ru.Path != "/callback" {
		http.Error(w, "bad client or redirect_uri", 400)
		return
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("response_type") != "code" {
		http.Error(w, "PKCE S256 required", 400)
		return
	}
	if res := q.Get("resource"); res != "" && res != account.Audience {
		http.Error(w, "invalid_target", 400)
		return
	}
	s.mu.Lock()
	if s.blocked[s.BrowserUser.ID] {
		s.mu.Unlock()
		http.Redirect(w, r, redirect+"?"+url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
		return
	}
	s.seq++
	code := fmt.Sprintf("code-%d", s.seq)
	s.codes[code] = &grant{user: s.BrowserUser, challenge: q.Get("code_challenge"), redirect: redirect,
		scopes: strings.Fields(q.Get("scope")), clientID: "monoagent", resource: q.Get("resource")}
	s.mu.Unlock()
	http.Redirect(w, r, redirect+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
}

// issue mints the tokens of g; the caller holds s.mu.
func (s *Server) issue(g *grant) map[string]any {
	s.seq++
	now := s.now()
	g.accessToken = fmt.Sprintf("at-%d", s.seq)
	if g.resource != "" && !s.opaque {
		g.accessToken = s.signJWT(g, now, s.seq)
	}
	g.refresh = fmt.Sprintf("rt-%d", s.seq)
	g.accessExp = now.Add(s.AccessTTL)
	s.access[g.accessToken] = g
	s.refr[g.refresh] = g
	return map[string]any{"access_token": g.accessToken, "refresh_token": g.refresh, "token_type": "Bearer",
		"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	if hook := s.onToken(); hook != nil {
		hook() // a test acts here, while the request is in flight and nothing is locked
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResource = f.Get("resource")
	hdr := r.Header.Clone()
	if _, sent := hdr["Authorization"]; sent {
		hdr.Set("Authorization", "redacted") // that one was sent, never what it held
	}
	s.tokenReqs = append(s.tokenReqs, TokenRequest{Form: url.Values(f), Header: hdr})
	switch f.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[f.Get("code")]
		delete(s.codes, f.Get("code"))
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if !ok || g.redirect != f.Get("redirect_uri") || f.Get("client_id") != "monoagent" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "bad code or verifier"})
			return
		}
		if g.user != nil && s.blocked[g.user.ID] {
			// A code issued before Block is a sign-in Block ends, like every token the user holds.
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "the account is blocked"})
			return
		}
		if res := f.Get("resource"); res != "" {
			g.resource = res
		}
		if g.resource != "" && g.resource != account.Audience {
			writeJSON(w, 400, map[string]string{"error": "invalid_target"})
			return
		}
		writeJSON(w, 200, s.issue(g))
	case "refresh_token":
		if s.refuse(w) {
			return
		}
		old, ok := s.refr[f.Get("refresh_token")]
		if ok && old.spent {
			// A rotated or revoked token again: monoes.me deletes every refresh token of the account.
			s.Replays++
			s.revokeUser(old.user.ID)
		}
		if !ok || old.revoked {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		res := f.Get("resource")
		if res != "" && res != account.Audience {
			writeJSON(w, 400, map[string]string{"error": "invalid_target"})
			return
		}
		if res == "" {
			res = old.resource // the audience sticks to a chain that has one (spike S2)
		}
		s.Refreshes++
		old.revoked, old.spent = true, true // rotate
		ng := &grant{user: old.user, scopes: old.scopes, clientID: old.clientID, resource: res}
		answer := s.issue(ng)
		if s.refreshMode == RefreshLost {
			hangUp(w) // the token is rotated and monoes.me's answer never arrives (A24)
			return
		}
		writeJSON(w, 200, answer)
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

// verifyEmail answers the emailed code. By default as monoes.me does once plan A's
// Task 7 is in: a request that asks for the MonoAgent audience gets the
// token endpoint's own answer, a signed access token and a refresh token; another
// audience is invalid_target before the code is looked at; a request without one
// gets an opaque access token and a refresh token, which the client trades at the
// token endpoint. SetEmailTrade and SetEmailOpaque make it answer the other two
// shapes whatever the request asks.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		ClientID string `json:"client_id"`
		Resource string `json:"resource"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	u := s.userByEmail(body.Email)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResource = body.Resource
	signed := body.Resource != "" && !s.emailOpaque && !s.emailTrade
	if signed && (body.Resource != account.Audience || body.ClientID != "monoagent") {
		writeJSON(w, 400, map[string]string{"error": "invalid_target"})
		return
	}
	if u == nil || s.blocked[u.ID] || body.Code != s.EmailCode {
		writeJSON(w, 400, map[string]string{"error": "invalid_or_expired_code"})
		return
	}
	g := &grant{user: u, clientID: "monoagent", resource: body.Resource,
		scopes: []string{"openid", "profile", "email", "offline_access", "library:read", "library:write"}}
	if signed {
		writeJSON(w, 200, s.issue(g)) // the token endpoint's own answer
		return
	}
	g.resource = "" // the answer is opaque: the audience is asked for at the trade
	if s.emailOpaque {
		s.seq++
		g.accessToken, g.accessExp = fmt.Sprintf("at-email-%d", s.seq), s.now().Add(s.AccessTTL)
		s.access[g.accessToken] = g
		writeJSON(w, 200, map[string]any{"access_token": g.accessToken, "token_type": "Bearer",
			"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")})
		return
	}
	writeJSON(w, 200, s.issue(g)) // an opaque access token (no resource) beside a refresh token
}

func (s *Server) userByEmail(email string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Email, email) {
			return u
		}
	}
	return nil
}
