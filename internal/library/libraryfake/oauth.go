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
)

// The fake's OAuth side: authorize (as a browser already logged in as
// BrowserUser that consents at once), token (code + PKCE, refresh with
// rotation) and the email-code verify.

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
	s.mu.Lock()
	s.seq++
	code := fmt.Sprintf("code-%d", s.seq)
	s.codes[code] = &grant{user: s.BrowserUser, challenge: q.Get("code_challenge"), redirect: redirect,
		scopes: strings.Fields(q.Get("scope")), clientID: "monoagent"}
	s.mu.Unlock()
	http.Redirect(w, r, redirect+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
}

func (s *Server) issue(g *grant) map[string]any {
	s.seq++
	g.accessToken = fmt.Sprintf("at-%d", s.seq)
	g.refresh = fmt.Sprintf("rt-%d", s.seq)
	g.accessExp = time.Now().Add(s.AccessTTL)
	s.access[g.accessToken] = g
	s.refr[g.refresh] = g
	return map[string]any{"access_token": g.accessToken, "refresh_token": g.refresh, "token_type": "Bearer",
		"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	s.mu.Lock()
	defer s.mu.Unlock()
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
		writeJSON(w, 200, s.issue(g))
	case "refresh_token":
		old, ok := s.refr[f.Get("refresh_token")]
		if !ok || old.revoked {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		s.Refreshes++
		old.revoked = true // rotate
		ng := &grant{user: old.user, scopes: old.scopes, clientID: old.clientID}
		writeJSON(w, 200, s.issue(ng))
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email, Code, ClientID string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	u := s.userByEmail(body.Email)
	if u == nil || body.Code != s.EmailCode {
		writeJSON(w, 400, map[string]string{"error": "invalid_or_expired_code"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := &grant{user: u, scopes: []string{"openid", "profile", "email", "library:read", "library:write"}}
	s.seq++
	g.accessToken, g.accessExp = fmt.Sprintf("at-email-%d", s.seq), time.Now().Add(s.AccessTTL)
	s.access[g.accessToken] = g
	writeJSON(w, 200, map[string]any{"access_token": g.accessToken, "token_type": "Bearer",
		"expires_in": int(s.AccessTTL / time.Second), "scope": strings.Join(g.scopes, " ")})
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
