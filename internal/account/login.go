package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// loginScopes are what a sign-in asks for: the community scopes, the two library
// scopes (the machine session serves the library too, spec D21) and offline_access
// for the refresh token.
const loginScopes = "openid profile email offline_access library:read library:write"

// identityPath is where the signed-in user's id, email and username are read: the
// library's own "who am I", which every MonoAgent token already works at.
const identityPath = "/api/library/me"

var (
	// ErrBadCode is monoes.me refusing an emailed code.
	ErrBadCode = errors.New("the code is wrong or expired")
	// ErrEmailSessionUnavailable ends an email sign-in whose answer holds no signed
	// token with a refresh token: no session can be kept from it.
	ErrEmailSessionUnavailable = errors.New("monoes.me's email sign-in cannot start a machine session yet; sign in with the browser instead: monoagentcli account login")
)

// LoginOptions controls Login.
type LoginOptions struct {
	Open    func(url string) error // opens the browser; nil does not
	OnURL   func(url string)       // told the URL before Open runs
	Timeout time.Duration          // default 5 minutes
}

// Client talks to one monoes.me host: the sign-in flows, logout and adoption. The
// package functions Login, Logout and the rest use the production host and store.
type Client struct {
	Host  string // base URL without a trailing slash
	HTTP  *http.Client
	Store Store
	Now   func() time.Time
}

// NewClient returns a Client for host over store.
func NewClient(host string, store Store) *Client {
	return &Client{Host: strings.TrimRight(host, "/"), HTTP: newHTTPClient(30 * time.Second), Store: store, Now: time.Now}
}

// answerError is a monoes.me answer that is not a success.
type answerError struct {
	status int
	msg    string
}

func (e *answerError) Error() string { return e.msg }

// unusableError is a sign-in monoes.me answered with something no session can be
// made of: a token that does not verify, or no refresh token.
type unusableError struct{ err error }

func (e *unusableError) Unwrap() error { return e.err }
func (e *unusableError) Error() string {
	var ve *VerifyError
	if errors.As(e.err, &ve) {
		switch ve.Reason {
		case ReasonKeyUnknown:
			return "monoes.me signed the session with a key this version does not know: run `monoagentcli update`, then sign in again"
		case ReasonClockSkew:
			return "this machine's clock is more than 5 minutes behind monoes.me's: correct the date and time, then sign in again"
		}
		return "monoes.me returned a session this version cannot verify: run `monoagentcli update`, then sign in again"
	}
	return "monoes.me did not return a usable session: " + e.err.Error()
}

type tokenAnswer struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// readTokens sends req and returns the tokens of a 200 answer.
func (c *Client) readTokens(req *http.Request) (*TokenSet, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var a tokenAnswer
	_ = json.Unmarshal(b, &a)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("monoes.me answered HTTP %d", resp.StatusCode)
		if oauthCode(a.Error) != "" {
			msg += " " + oauthCode(a.Error)
		}
		return nil, &answerError{status: resp.StatusCode, msg: msg}
	}
	if a.AccessToken == "" {
		return nil, errors.New("monoes.me's answer carried no access token")
	}
	return &TokenSet{AccessToken: a.AccessToken, RefreshToken: a.RefreshToken}, nil
}

func (c *Client) exchange(ctx context.Context, endpoint string, form url.Values) (*TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.readTokens(req)
}

// Login runs the browser sign-in (OAuth 2.1, PKCE, a loopback redirect) for the
// MonoAgent audience and stores the session it returns.
func (c *Client) Login(ctx context.Context, o LoginOptions) (Status, error) {
	if err := checkHost(c.Host); err != nil {
		return Status{}, err
	}
	ep, err := DiscoverEndpoints(ctx, c.HTTP, c.Host)
	if err != nil {
		return Status{}, fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	res, err := AuthorizeInBrowser(ctx, ep.AuthorizationEndpoint,
		url.Values{"client_id": {ClientID}, "scope": {loginScopes}, "resource": {Audience}},
		AuthorizeOptions{Open: o.Open, OnURL: o.OnURL, Timeout: o.Timeout, Label: "account login",
			SuccessText: "MonoAgent is now signed in to monoes.me. You can close this tab."})
	if err != nil {
		return Status{}, err
	}
	// A code is single use and rotates nothing: a call abandoned here leaves no refresh token on disk
	// that could be presented again, so this exchange needs no completion guarantee (A20).
	ts, err := c.exchange(ctx, ep.TokenEndpoint, url.Values{"grant_type": {"authorization_code"}, "code": {res.Code},
		"redirect_uri": {res.Redirect}, "client_id": {ClientID}, "code_verifier": {res.Verifier}, "resource": {Audience}})
	if err != nil {
		return Status{}, fmt.Errorf("account login: exchange the code: %w", err)
	}
	return c.establish(ctx, ts, nil)
}

// SendEmailCode asks monoes.me to email a sign-in code. It answers the same
// whether or not the address has an account.
func (c *Client) SendEmailCode(ctx context.Context, email string) error {
	if err := checkHost(c.Host); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"email": email, "client_id": ClientID, "scope": loginScopes})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+"/api/auth/agent/claim", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("monoes.me unreachable (%s): %w", c.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("monoes.me answered HTTP %d to the code request", resp.StatusCode)
	}
	return nil
}

// VerifyEmailCode trades an emailed code for a session and stores it. The request
// names the MonoAgent audience. monoes.me answers as its token endpoint does, a
// signed access token and a refresh token (plan A, Task 7), and that is the session;
// or it answers a refresh token beside an opaque access token, and the refresh token
// is traded at the token endpoint, with the audience, for the signed one. A monoes.me
// that answers no refresh token, as the route does today, cannot start a machine
// session: the result is ErrEmailSessionUnavailable and nothing is stored. The
// emailed refresh token is presented once at most and never stored.
func (c *Client) VerifyEmailCode(ctx context.Context, email, code string) (Status, error) {
	if err := checkHost(c.Host); err != nil {
		return Status{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	body, _ := json.Marshal(map[string]string{"email": email, "code": code, "client_id": ClientID, "resource": Audience})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+"/api/auth/agent/claim/verify", bytes.NewReader(body))
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	ts, err := c.readTokens(req)
	var ae *answerError
	if errors.As(err, &ae) && ae.status == http.StatusBadRequest {
		return Status{}, ErrBadCode
	}
	if err != nil {
		return Status{}, err
	}
	if ts.RefreshToken == "" {
		return Status{}, ErrEmailSessionUnavailable
	}
	// The trade spends the emailed refresh token, which is never stored: a trade the caller abandons
	// leaves no refresh token on disk that could be presented again (A20), and a new code starts over.
	if strings.Count(ts.AccessToken, ".") != 2 { // opaque, not a JWS: the signed one is asked for at the trade
		if ts, err = NewRefresher(c.Host).Refresh(ctx, ts.RefreshToken); err != nil {
			return Status{}, fmt.Errorf("account login: trade the emailed code's refresh token: %w", err)
		}
	}
	st, err := c.establish(ctx, ts, &User{Email: email})
	var ue *unusableError
	var ve *VerifyError
	if errors.As(err, &ue) && errors.As(ue, &ve) && ve.Reason == ReasonInvalid {
		return Status{}, ErrEmailSessionUnavailable // an opaque access token beside a refresh token
	}
	return st, err
}

// session turns ts into the Session to store: it verifies the access token, looks
// up who the user is, and builds the session as B1a does everywhere else
// (NewSession, which starts the clock guard at the token's iat). It reads and
// writes no store.
func (c *Client) session(ctx context.Context, ts *TokenSet, hint *User) (*Session, error) {
	now := c.Now()
	rec, err := Verify(ts.AccessToken, now)
	if err != nil {
		return nil, &unusableError{err}
	}
	if ts.RefreshToken == "" {
		return nil, &unusableError{errors.New("it came without a refresh token")}
	}
	sess, err := NewSession(c.Host, ts.AccessToken, c.lookupUser(ctx, ts.AccessToken, rec.Sub, hint), now)
	if err != nil {
		return nil, &unusableError{err}
	}
	return sess, nil
}

// commit writes the refresh token, then the session, so a session never exists
// without the means to renew it. The caller holds the store lock.
func (c *Client) commit(sess *Session, refresh string) error {
	if err := c.Store.SaveRefresh(refresh); err != nil {
		return err
	}
	return c.Store.Save(sess)
}

// establish stores ts as the machine session, replacing whatever was there
// (including a refusal), and returns its verdict.
func (c *Client) establish(ctx context.Context, ts *TokenSet, hint *User) (Status, error) {
	sess, err := c.session(ctx, ts, hint)
	if err != nil {
		return Status{}, err
	}
	unlock, err := c.Store.Lock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	if err := c.commit(sess, ts.RefreshToken); err != nil {
		return Status{}, err
	}
	return Evaluate(sess, c.Now()), nil
}

// lookupUser names the signed-in user: the token's subject, and the id, email and
// username monoes.me reports for it when it answers and agrees on the subject.
func (c *Client) lookupUser(ctx context.Context, token, sub string, hint *User) *User {
	u := &User{ID: sub}
	if hint != nil {
		u.Email, u.Username = hint.Email, hint.Username
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.Host+identityPath, nil)
	if err != nil {
		return u
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return u
	}
	defer resp.Body.Close()
	var me struct {
		User User `json:"user"`
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me) == nil && me.User.ID == sub {
		return &me.User
	}
	return u
}
