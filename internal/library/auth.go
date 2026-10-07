package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/account"
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

// endpoints discovers the authorization server's endpoints, once per client.
func (c *Client) endpoints(ctx context.Context) *account.OAuthEndpoints {
	c.mu.Lock()
	m := c.oauth
	c.mu.Unlock()
	if m != nil {
		return m
	}
	m, _ = account.DiscoverEndpoints(ctx, c.HTTP, c.BaseURL)
	c.mu.Lock()
	c.oauth = m
	c.mu.Unlock()
	return m
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

// LoginPKCE runs the OAuth 2.1 authorization code flow with PKCE and a loopback
// redirect (RFC 8252): it sends the user to the authorize URL, waits for the
// redirect, exchanges the code and stores the token together with the account it
// belongs to.
func (c *Client) LoginPKCE(ctx context.Context, opts LoginOptions) (*Token, error) {
	meta := c.endpoints(ctx)
	res, err := account.AuthorizeInBrowser(ctx, meta.AuthorizationEndpoint,
		url.Values{"client_id": {ClientID}, "scope": {strings.Join(Scopes, " ")}},
		account.AuthorizeOptions{Open: opts.Open, OnURL: opts.OnURL, Timeout: opts.Timeout, Label: "library login",
			SuccessText: "MonoAgent is now connected to your monoes.me library. You can close this tab."})
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {res.Code}, "redirect_uri": {res.Redirect},
		"client_id": {ClientID}, "code_verifier": {res.Verifier}}
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

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
