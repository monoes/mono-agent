// Package libraryfake is an in-process fake of the monoes.me library API
// and its OAuth endpoints (PKCE authorization code with a loopback
// redirect, refresh, revoke, and the email-code fallback), for tests. Only
// tests import it; the binary never does.
package libraryfake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// User is an account on the fake.
type User struct {
	ID, Name, Username, Email string
	Admin                     bool
}

// Item is a stored library item.
type Item struct {
	ID, Kind, Slug, Name, Description, Version, Visibility string
	Tags                                                   []string
	Owner                                                  *User
	Data                                                   []byte
	Meta                                                   map[string]any
	Versions                                               []string
	Created, Updated                                       time.Time
}

func (it *Item) sha() string {
	s := sha256.Sum256(it.Data)
	return hex.EncodeToString(s[:])
}

type grant struct {
	user                  *User
	challenge, redirect   string
	accessExp             time.Time
	refresh               string
	scopes                []string
	revoked               bool
	accessToken, clientID string
	resource              string // the audience the client asked for; "" = none
	spent                 bool   // a refresh token that was rotated or revoked: presenting it again is a replay
}

// Server is the fake. Its fields may be changed between requests.
type Server struct {
	*httptest.Server

	mu     sync.Mutex
	users  map[string]*User
	items  map[string]*Item
	codes  map[string]*grant // authorization code → grant
	access map[string]*grant
	refr   map[string]*grant
	seq    int

	// BrowserUser is who the simulated browser is logged in as when it
	// hits the authorize endpoint.
	BrowserUser *User
	// AccessTTL is the access tokens' lifetime (default 1h).
	AccessTTL time.Duration
	// TamperArtifact makes artifact downloads send different bytes than
	// the X-Content-SHA256 header and the item say.
	TamperArtifact bool
	// EmailCode is the code the email flow accepts (default "123456").
	EmailCode string
	// AnonymousReads lets requests without a token list, show and
	// download public and official items, as monoes.me did before it
	// required a login for every library read. Off by default.
	AnonymousReads bool
	// Requests counts calls per "METHOD path" (no query).
	Requests map[string]int
	// Refreshes counts refresh_token grants.
	Refreshes int
	// Replays counts refresh tokens presented after they were rotated or revoked.
	// monoes.me answers that with invalid_grant and deletes every refresh token of
	// the account (spike S2), so a client must never cause one.
	Replays int

	cmu          sync.Mutex // guards clock and tokenHook
	clock        func() time.Time
	tokenHook    func() // runs when a token request arrives (OnToken)
	blocked      map[string]bool
	refreshMode  RefreshMode
	opaque       bool   // a server that ignores resource: access tokens stay opaque
	emailOpaque  bool   // claim/verify answers as today: an opaque token, no refresh token
	emailTrade   bool   // claim/verify answers an opaque token beside a refresh token to trade
	lastResource string // the resource of the latest token request
	tokenReqs    []TokenRequest
}

// New starts a fake with a "monoes" admin (the official publisher) and
// a regular user "ada", as whom the simulated browser is logged in.
func New() *Server {
	s := &Server{users: map[string]*User{}, items: map[string]*Item{}, codes: map[string]*grant{},
		access: map[string]*grant{}, refr: map[string]*grant{}, AccessTTL: time.Hour, EmailCode: "123456",
		Requests: map[string]int{}, blocked: map[string]bool{}}
	s.AddUser(&User{ID: "u-monoes", Name: "Monoes", Username: "monoes", Email: "team@monoes.me", Admin: true})
	s.BrowserUser = s.AddUser(&User{ID: "u-ada", Name: "Ada", Username: "ada", Email: "ada@example.com"})
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// AddUser registers u.
func (s *Server) AddUser(u *User) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = u
	return u
}

// User returns the user with username name.
func (s *Server) User(name string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Username == name {
			return u
		}
	}
	return nil
}

// Add stores an item owned by owner (a username) and returns its id.
func (s *Server) Add(owner, kind, slug, name, visibility, version string, data []byte, meta map[string]any) string {
	u := s.User(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("it-%d", s.seq)
	now := time.Now().UTC()
	s.items[id] = &Item{ID: id, Kind: kind, Slug: slug, Name: name, Version: version, Visibility: visibility,
		Owner: u, Data: data, Meta: meta, Versions: []string{version}, Created: now, Updated: now, Tags: []string{}}
	return id
}

// SetArtifact replaces an item's artifact with a new version.
func (s *Server) SetArtifact(id, version string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.items[id]
	it.Data, it.Version, it.Updated = data, version, time.Now().UTC()
	it.Versions = append(it.Versions, version)
}

// Get returns a copy of a stored item.
func (s *Server) Get(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

// RevokeAll revokes every access and refresh token, so a refresh fails
// too and only a new login helps.
func (s *Server) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.access {
		g.revoked = true
	}
	for _, g := range s.refr {
		g.revoked = true
	}
}

// ExpireAccessTokens makes every issued access token expired at the fake: it refuses them. A signed
// token still carries the exp it was minted with, so a client that checks exp itself sees it live;
// a test that needs a client's own exp check to fire sets the clock of the client past the token's exp instead.
func (s *Server) ExpireAccessTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.access {
		g.accessExp = s.now().Add(-time.Minute)
	}
}

func (s *Server) itemJSON(it *Item) map[string]any {
	owner := map[string]any{"id": "", "username": "", "name": ""}
	if it.Owner != nil {
		owner = map[string]any{"id": it.Owner.ID, "username": it.Owner.Username, "name": it.Owner.Name}
	}
	meta := it.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	return map[string]any{"id": it.ID, "kind": it.Kind, "slug": it.Slug, "name": it.Name, "description": it.Description,
		"version": it.Version, "visibility": it.Visibility, "tags": it.Tags, "owner": owner, "sha256": it.sha(),
		"size": len(it.Data), "meta": meta, "created_at": it.Created.Format(time.RFC3339),
		"updated_at": it.Updated.Format(time.RFC3339), "url": s.URL + "/library/" + it.Kind + "s/" + it.Slug,
		"artifact_url": s.URL + "/api/library/items/" + it.ID + "/artifact"}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// caller is the user behind the request's bearer token; bad is true when
// a token was sent but is unknown or expired.
func (s *Server) caller(r *http.Request) (u *User, scopes []string, bad bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, nil, false
	}
	tok := strings.TrimPrefix(h, "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.access[tok]
	if !ok || g.revoked || s.now().After(g.accessExp) {
		return nil, nil, true
	}
	return g.user, g.scopes, false
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests[r.Method+" "+r.URL.Path]++
	s.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/.well-known/oauth-authorization-server":
		writeJSON(w, 200, map[string]any{"issuer": s.URL, "authorization_endpoint": s.URL + "/api/auth/oauth2/authorize",
			"token_endpoint": s.URL + "/api/auth/oauth2/token", "revocation_endpoint": s.URL + "/api/auth/oauth2/revoke"})
	case p == "/api/auth/oauth2/authorize":
		s.authorize(w, r)
	case p == "/api/auth/oauth2/token":
		s.token(w, r)
	case p == "/api/auth/oauth2/revoke":
		_ = r.ParseForm()
		s.mu.Lock()
		if g, ok := s.refr[r.PostForm.Get("token")]; ok {
			g.revoked, g.spent = true, true
		}
		if g, ok := s.access[r.PostForm.Get("token")]; ok {
			g.revoked = true
		}
		s.mu.Unlock()
		w.WriteHeader(200)
	case p == "/api/auth/agent/claim":
		writeJSON(w, 200, map[string]string{"message": "If this email is registered, a verification code has been sent."})
	case p == "/api/auth/agent/claim/verify":
		s.verifyEmail(w, r)
	case p == "/api/library/me":
		u, scopes, _ := s.caller(r)
		if u == nil {
			apiErr(w, 401, "unauthorized", "login required")
			return
		}
		writeJSON(w, 200, map[string]any{"user": map[string]any{"id": u.ID, "name": u.Name, "username": u.Username,
			"email": u.Email, "image": ""}, "scopes": scopes})
	case strings.HasPrefix(p, "/api/library/items"):
		s.items_(w, r, strings.Trim(strings.TrimPrefix(p, "/api/library/items"), "/"))
	default:
		http.NotFound(w, r)
	}
}

func visible(it *Item, u *User) bool {
	return it.Visibility != "private" || (u != nil && it.Owner != nil && it.Owner.ID == u.ID)
}

func (s *Server) items_(w http.ResponseWriter, r *http.Request, rest string) {
	u, scopes, bad := s.caller(r)
	if bad {
		apiErr(w, 401, "invalid_token", "token expired or unknown")
		return
	}
	s.mu.Lock()
	anon := s.AnonymousReads
	s.mu.Unlock()
	if u == nil && !anon {
		apiErr(w, 401, "unauthorized", "login required")
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		s.list(w, r, u)
	case rest == "" && r.Method == http.MethodPost:
		s.create(w, r, u, scopes)
	case strings.HasSuffix(rest, "/artifact") && r.Method == http.MethodGet:
		it := s.find(strings.TrimSuffix(rest, "/artifact"))
		if it == nil || !visible(it, u) {
			apiErr(w, 404, "not_found", "no such item")
			return
		}
		data := it.Data
		if s.TamperArtifact {
			data = append(append([]byte{}, data...), ' ')
		}
		w.Header().Set("X-Content-SHA256", it.sha())
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", it.Slug))
		_, _ = w.Write(data)
	case strings.HasSuffix(rest, "/artifact") && r.Method == http.MethodPut:
		it := s.find(strings.TrimSuffix(rest, "/artifact"))
		if u == nil {
			apiErr(w, 401, "unauthorized", "login required")
			return
		}
		if it == nil || !visible(it, u) {
			apiErr(w, 404, "not_found", "no such item")
			return
		}
		if it.Owner == nil || it.Owner.ID != u.ID {
			apiErr(w, 403, "forbidden", "not your item")
			return
		}
		data, fields, err := readUpload(r)
		if err != nil {
			apiErr(w, 400, "bad_request", err.Error())
			return
		}
		v := fields["version"]
		if v == "" {
			v = it.Version
		}
		s.SetArtifact(it.ID, v, data)
		writeJSON(w, 200, s.itemJSON(s.find(it.ID)))
	case r.Method == http.MethodPatch:
		s.patch(w, r, u, rest)
	case r.Method == http.MethodGet:
		it := s.find(rest)
		if it == nil || !visible(it, u) {
			apiErr(w, 404, "not_found", "no such item")
			return
		}
		writeJSON(w, 200, s.itemJSON(it))
	default:
		apiErr(w, 405, "method_not_allowed", r.Method)
	}
}

// find resolves an id or "<kind>/<slug>".
func (s *Server) find(ref string) *Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.items[ref]; ok {
		return it
	}
	kind, slug, ok := strings.Cut(ref, "/")
	if !ok {
		return nil
	}
	for _, it := range s.items {
		if it.Kind == kind && it.Slug == slug {
			return it
		}
	}
	return nil
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope == "" {
		scope = "public"
	}
	if scope == "mine" && u == nil {
		apiErr(w, 401, "unauthorized", "login required")
		return
	}
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	if perPage <= 0 {
		perPage = 20
	}
	if perPage > 100 {
		apiErr(w, 400, "bad_request", "per_page is at most 100")
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	s.mu.Lock()
	var all []*Item
	for _, it := range s.items {
		ok := false
		switch scope {
		case "mine":
			ok = it.Owner != nil && it.Owner.ID == u.ID
		case "official":
			ok = it.Visibility == "official"
		default:
			ok = it.Visibility == "public" || it.Visibility == "official"
		}
		if k := q.Get("kind"); k != "" && it.Kind != k {
			ok = false
		}
		if t := q.Get("q"); t != "" && !strings.Contains(strings.ToLower(it.Name+" "+it.Description+" "+it.Slug), strings.ToLower(t)) {
			ok = false
		}
		if ok {
			all = append(all, it)
		}
	}
	s.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	out := []map[string]any{}
	for i := (page - 1) * perPage; i < len(all) && i < page*perPage; i++ {
		out = append(out, s.itemJSON(all[i]))
	}
	writeJSON(w, 200, map[string]any{"items": out, "page": page, "per_page": perPage, "total": len(all)})
}
