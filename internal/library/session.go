package library

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// refreshGrantTimeout bounds the refresh of the profile's own login from the moment its grant is
// sent: the grant runs on a context the caller's cancellation does not reach (A20), so it needs a
// deadline of its own. It is the guard's backstop on one grant.
const refreshGrantTimeout = 20 * time.Second

// refreshGrant sends the refresh-token grant of the profile's own login, as readToken answers any
// token call, and says whether the outcome is settled (A24) by the rule the machine session's
// refresher uses, account.GrantSettled: true when the token cannot have been spent, because the
// request was never written or monoes.me answered with a complete 4xx (it processed nothing); false
// for every other failure, because monoes.me may have rotated the token and the answer that held its
// successor is lost: a reset or a timeout after the write, a 5xx whatever its body, a 3xx, a body cut
// short, and a 2xx that names no access token or no refresh token (monoes.me rotates at every use, so
// a success names the token that replaces this one; tokenFrom's fallback to the old one would keep a
// spent token). httptrace's WroteRequest says when the request has been written; it fires once the
// transport has handed the request to the connection, so a connection that dies in that instant
// counts as unknown, which is the safe side.
func (c *Client) refreshGrant(ctx context.Context, endpoint, refreshToken string) (*tokenResponse, bool, error) {
	var written atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(i httptrace.WroteRequestInfo) {
		if i.Err == nil {
			written.Store(true)
		}
	}})
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, true, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, account.GrantSettled(written.Load(), 0, false), fmt.Errorf("monoes.me unreachable (%s): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, account.GrantSettled(written.Load(), resp.StatusCode, false), fmt.Errorf("monoes.me: the answer was cut short: %w", err)
	}
	settled := account.GrantSettled(written.Load(), resp.StatusCode, true)
	if resp.StatusCode/100 != 2 {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return nil, settled, apiError(resp)
	}
	var tr tokenResponse
	if err := decodeJSON(bytes.NewReader(body), &tr); err != nil {
		return nil, settled, err
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, settled, &APIError{Status: http.StatusUnauthorized, Code: tr.Error, Message: nonEmpty(tr.ErrorDescription, "no access token or no refresh token in the response")}
	}
	return &tr, true, nil
}

// dropLogin forgets the profile's own login without revoking it, after a refresh whose outcome is
// unknown (A24: monoes.me may have rotated its refresh token and the answer is lost), one that
// monoes.me answered with invalid_grant (the token is dead), or one whose answer the vault did not
// take (A24(d): the token is spent and its successor is lost). Presenting such a token again, after
// the 300-second reuse window, would end every refresh token of the account. The user logs in again,
// and `library login` makes the machine session.
func (c *Client) dropLogin(ctx context.Context) {
	c.mu.Lock()
	c.token, c.loaded = nil, true
	c.mu.Unlock()
	if c.Store != nil {
		_ = c.Store.Delete(ctx)
	}
}

// SessionSource supplies the machine-wide monoes.me session (spec D21): the one
// login that `account login` and `library login` both make and every library
// call uses.
type SessionSource interface {
	// Token returns the session's access token as a login for host, refreshed when
	// it is due, or nil, nil when there is no usable session for that host.
	Token(ctx context.Context, host string) (*Token, error)
}

// AccountSession is the SessionSource over internal/account: the Guard refreshes
// the session and the Store holds its token.
type AccountSession struct {
	Guard   *account.Guard
	Store   account.Store
	Offline bool // report the saved session without asking monoes.me to renew it
}

// Token implements SessionSource. A session issued for another host than the
// library's (MONOES_BASE_URL pointed elsewhere) is never handed out, so its token
// cannot reach a server that did not issue it. A locked session is not handed out
// either: the caller says "log in first".
func (s *AccountSession) Token(ctx context.Context, host string) (*Token, error) {
	sess, err := s.Store.Load()
	if err != nil || sess == nil || sess.Host != host {
		return nil, err
	}
	st := s.Guard.Status()
	if !s.Offline {
		st, _ = s.Guard.Refresh(ctx) // when due; if it cannot, the call itself finds out
	}
	if st.State == account.StateLocked {
		return nil, nil
	}
	if sess, err = s.Store.Load(); err != nil || sess == nil || sess.AccessToken == "" {
		return nil, err // the refresh may have replaced the token
	}
	t := &Token{AccessToken: sess.AccessToken, TokenType: "Bearer", Method: "session", BaseURL: sess.Host, ExpiresAt: st.ValidUntil}
	if u := sess.User; u != nil {
		t.User = &User{ID: u.ID, Email: u.Email, Username: u.Username}
	}
	return t, nil
}

// Lock takes the account store's exclusive lock. The library holds it while it
// refreshes a login of its own, so that never overlaps an adoption in another
// process (see Client.refresh).
func (s *AccountSession) Lock(ctx context.Context) (unlock func(), err error) {
	return s.Store.Lock(ctx)
}

// sessionToken is the machine session's login for this client's host, asked for
// again once it is within a minute of expiring.
func (c *Client) sessionToken(ctx context.Context) (*Token, error) {
	c.mu.Lock()
	t := c.session
	c.mu.Unlock()
	if t != nil && !t.expiring(c.Now(), time.Minute) {
		return t, nil
	}
	t, err := c.Session.Token(ctx, c.BaseURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.session = t
	c.mu.Unlock()
	return t, nil
}
