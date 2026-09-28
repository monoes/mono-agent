package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oauthMeta is the part of the authorization server metadata (RFC 8414)
// MonoAgent uses.
type oauthMeta struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// Fallback endpoints (Better-Auth's oauth-provider defaults) when the
// server publishes no metadata.
const (
	defaultAuthorizePath = "/api/auth/oauth2/authorize"
	defaultTokenPath     = "/api/auth/oauth2/token"
	defaultRevokePath    = "/api/auth/oauth2/revoke"
)

// currentToken returns the stored token, refreshing it first when it is
// about to expire. nil, nil when logged out.
func (c *Client) currentToken(ctx context.Context) (*Token, error) {
	c.mu.Lock()
	if !c.loaded && c.Store != nil {
		t, err := c.Store.Load(ctx)
		if err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("read monoes.me login from the vault: %w", err)
		}
		c.token, c.loaded = t, true
	}
	t := c.token
	c.mu.Unlock()
	if t == nil || !t.expiring(c.Now(), time.Minute) || t.RefreshToken == "" {
		return t, nil
	}
	if nt, err := c.refresh(ctx, t); err == nil {
		return nt, nil
	}
	return t, nil // let the server decide; a 401 says "log in again"
}

// Token returns the current login (refreshed when due), or nil.
func (c *Client) Token(ctx context.Context) (*Token, error) { return c.currentToken(ctx) }

func (c *Client) setToken(ctx context.Context, t *Token) error {
	c.mu.Lock()
	c.token, c.loaded = t, true
	c.mu.Unlock()
	if c.Store == nil || t == nil {
		return nil
	}
	return c.Store.Save(ctx, t)
}

// endpoints discovers the authorization server's endpoints. An endpoint
// on another host than the base URL is used by path on the base URL, so a
// misconfigured issuer can never receive the code verifier or a token.
func (c *Client) endpoints(ctx context.Context) *oauthMeta {
	c.mu.Lock()
	m := c.oauth
	c.mu.Unlock()
	if m != nil {
		return m
	}
	m = &oauthMeta{}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/.well-known/oauth-authorization-server", nil)
	if resp, err := c.HTTP.Do(req); err == nil {
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(m)
		}
		resp.Body.Close()
	}
	m.AuthorizationEndpoint = c.onBase(m.AuthorizationEndpoint, defaultAuthorizePath)
	m.TokenEndpoint = c.onBase(m.TokenEndpoint, defaultTokenPath)
	m.RevocationEndpoint = c.onBase(m.RevocationEndpoint, defaultRevokePath)
	c.mu.Lock()
	c.oauth = m
	c.mu.Unlock()
	return m
}

func (c *Client) onBase(endpoint, fallback string) string {
	if endpoint == "" {
		return c.BaseURL + fallback
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Path == "" {
		return c.BaseURL + fallback
	}
	base, _ := url.Parse(c.BaseURL)
	if strings.EqualFold(u.Host, base.Host) && u.Scheme == base.Scheme {
		return endpoint
	}
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return c.BaseURL + p
}

// tokenResponse is an OAuth token endpoint (or email verify) answer.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *Client) postToken(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.readToken(req)
}

func (c *Client) readToken(req *http.Request) (*tokenResponse, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monoes.me unreachable (%s): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp)
	}
	if err := decodeJSON(resp.Body, &tr); err != nil {
		return nil, err
	}
	if tr.AccessToken == "" {
		return nil, &APIError{Status: http.StatusUnauthorized, Code: tr.Error, Message: nonEmpty(tr.ErrorDescription, "no access token in the response")}
	}
	return &tr, nil
}

func (c *Client) tokenFrom(tr *tokenResponse, method string, prev *Token) *Token {
	t := &Token{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, TokenType: tr.TokenType,
		Scope: tr.Scope, Method: method, BaseURL: c.BaseURL}
	if tr.ExpiresIn > 0 {
		t.ExpiresAt = c.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).UTC()
	}
	if prev != nil {
		if t.RefreshToken == "" {
			t.RefreshToken = prev.RefreshToken
		}
		if t.Scope == "" {
			t.Scope = prev.Scope
		}
		t.User = prev.User
	}
	return t
}

// refresh trades the refresh token for a new access token and stores it.
func (c *Client) refresh(ctx context.Context, t *Token) (*Token, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}, "client_id": {ClientID}}
	tr, err := c.postToken(ctx, c.endpoints(ctx).TokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	nt := c.tokenFrom(tr, t.Method, t)
	if err := c.setToken(ctx, nt); err != nil {
		return nil, err
	}
	return nt, nil
}

// LoginOptions controls LoginPKCE.
type LoginOptions struct {
	// Open opens the authorization URL (the system browser); nil = don't.
	Open func(string) error
	// OnURL is told the authorization URL before Open runs.
	OnURL   func(string)
	Timeout time.Duration // default 5 minutes
}

// LoginPKCE runs the OAuth 2.1 authorization code flow with PKCE and a
// loopback redirect (RFC 8252): it listens on 127.0.0.1:<random>, sends the
// user to the authorize URL, waits for the redirect, exchanges the code and
// stores the token together with the account it belongs to.
func (c *Client) LoginPKCE(ctx context.Context, opts LoginOptions) (*Token, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("library login: listen on loopback: %w", err)
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	verifier, state := randomString(48), randomString(24)
	sum := sha256.Sum256([]byte(verifier))
	meta := c.endpoints(ctx)
	q := url.Values{
		"response_type": {"code"}, "client_id": {ClientID}, "redirect_uri": {redirect},
		"scope": {strings.Join(Scopes, " ")}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(meta.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	authURL := meta.AuthorizationEndpoint + sep + q.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		v := r.URL.Query()
		var res result
		switch {
		case v.Get("state") != state:
			res.err = errors.New("library login: the redirect's state does not match (ignored)")
			writeCallbackPage(w, false, "This sign-in link is not for this login attempt.")
			return // keep waiting for the real redirect
		case v.Get("error") != "":
			res.err = fmt.Errorf("library login: monoes.me refused: %s %s", v.Get("error"), v.Get("error_description"))
			writeCallbackPage(w, false, "monoes.me did not authorize MonoAgent: "+v.Get("error"))
		case v.Get("code") == "":
			res.err = errors.New("library login: the redirect carried no code")
			writeCallbackPage(w, false, "The sign-in redirect carried no code.")
		default:
			res.code = v.Get("code")
			writeCallbackPage(w, true, "")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if opts.OnURL != nil {
		opts.OnURL(authURL)
	}
	if opts.Open != nil {
		_ = opts.Open(authURL) // the URL was shown; the user can open it by hand
	}
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return nil, fmt.Errorf("library login: no answer from the browser within %s", opts.Timeout)
	}
	if res.err != nil {
		return nil, res.err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {res.code}, "redirect_uri": {redirect},
		"client_id": {ClientID}, "code_verifier": {verifier}}
	tr, err := c.postToken(ctx, meta.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("library login: exchange the code: %w", err)
	}
	return c.finishLogin(ctx, c.tokenFrom(tr, "pkce", nil))
}

// finishLogin stores t with the account /api/library/me reports for it.
func (c *Client) finishLogin(ctx context.Context, t *Token) (*Token, error) {
	c.mu.Lock()
	c.token, c.loaded = t, true
	c.mu.Unlock()
	me, err := c.Me(ctx)
	if err != nil {
		c.mu.Lock()
		c.token = nil
		c.mu.Unlock()
		return nil, fmt.Errorf("library login: check the new login: %w", err)
	}
	t.User = &me.User
	if len(me.Scopes) > 0 && t.Scope == "" {
		t.Scope = strings.Join(me.Scopes, " ")
	}
	if err := c.setToken(ctx, t); err != nil {
		return nil, fmt.Errorf("library login: save the login in the vault: %w", err)
	}
	return t, nil
}

func writeCallbackPage(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title, body := "Logged in to monoes.me", "MonoAgent is now connected to your monoes.me library. You can close this tab."
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		title, body = "Sign-in failed", msg
	}
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title><body style="font-family:system-ui;margin:3rem"><h1>%s</h1><p>%s</p>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}

// SendEmailCode asks monoes.me to email a login code (the headless
// fallback). The server answers the same whether or not the address has
// an account.
func (c *Client) SendEmailCode(ctx context.Context, email string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "client_id": ClientID, "scope": strings.Join(Scopes, " ")})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/auth/agent/claim", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("monoes.me unreachable (%s): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError(resp)
	}
	return nil
}

// VerifyEmailCode trades an emailed code for a token and stores it.
func (c *Client) VerifyEmailCode(ctx context.Context, email, code string) (*Token, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "code": code, "client_id": ClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/auth/agent/claim/verify", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	tr, err := c.readToken(req)
	if err != nil {
		return nil, fmt.Errorf("library login: %w", err)
	}
	return c.finishLogin(ctx, c.tokenFrom(tr, "email", nil))
}

// Logout revokes the token on monoes.me (best effort) and forgets it.
func (c *Client) Logout(ctx context.Context) error {
	t, _ := c.currentToken(ctx)
	if t != nil {
		tok := nonEmpty(t.RefreshToken, t.AccessToken)
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.endpoints(rctx).RevocationEndpoint,
			strings.NewReader(url.Values{"token": {tok}, "client_id": {ClientID}}.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if resp, err := c.HTTP.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		cancel()
	}
	c.mu.Lock()
	c.token, c.loaded = nil, true
	c.mu.Unlock()
	if c.Store == nil {
		return nil
	}
	return c.Store.Delete(ctx)
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
