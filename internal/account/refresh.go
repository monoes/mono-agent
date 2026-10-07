package account

// The refresh-token grant (spec §4.4, D27). This is the one place that decides
// what monoes.me's answer means: only invalid_grant answered to a refresh-token
// grant is a refusal, and everything else is transient. A transient failure also
// says whether monoes.me can have rotated the token (A24).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const maxAnswerBytes = 1 << 20

type refresher struct {
	host string
	hc   *http.Client
}

// NewRefresher returns the Refresher for host: the refresh-token grant with resource=Audience.
func NewRefresher(host string) Refresher {
	return &refresher{host: strings.TrimRight(host, "/"), hc: newHTTPClient(refreshCallTimeout)}
}

// GrantSettled is TransientError.Settled for a refresh-token grant that brought no token set (A24): status 0
// means no answer came. Known only when the request was never written, or the answer is a complete 4xx.
func GrantSettled(written bool, status int, complete bool) bool {
	if !written {
		return true
	}
	return status >= 400 && status < 500 && complete
}

// newHTTPClient connects within ConnectTimeout, gives up after overall and follows no redirect.
func newHTTPClient(overall time.Duration) *http.Client {
	return &http.Client{
		Timeout: overall,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: ConnectTimeout}).DialContext,
			TLSHandshakeTimeout: ConnectTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// checkHost accepts https, or http to a loopback host (tests and local servers), and nothing else.
func checkHost(host string) error {
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return fmt.Errorf("account: host %q is not a URL", host)
	}
	if u.Scheme == "https" {
		return nil
	}
	h := u.Hostname()
	if ip := net.ParseIP(h); u.Scheme == "http" && (h == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("account: host %s must be https", u.Host)
}

// oauthCode returns code when it is a plain OAuth error code (lowercase letters and underscores, short), else "".
func oauthCode(code string) string {
	if code == "" || len(code) > 64 {
		return ""
	}
	for _, c := range code {
		if (c < 'a' || c > 'z') && c != '_' {
			return ""
		}
	}
	return code
}

func (r *refresher) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	if err := checkHost(r.host); err != nil {
		return nil, &TransientError{Reason: ReasonServerError, Settled: true, Err: err}
	}
	// Nothing has been sent while the endpoints are discovered, so a failure here is settled.
	ep, err := DiscoverEndpoints(ctx, r.hc, r.host)
	if err != nil {
		return nil, &TransientError{Reason: ReasonUnreachable, Settled: true, Err: errors.New("monoes.me is not reachable")}
	}
	form := url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
		"client_id": {ClientID}, "resource": {Audience},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &TransientError{Reason: ReasonServerError, Settled: true, Err: errors.New("the refresh request could not be built")}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var wrote atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				wrote.Store(true)
			}
		},
	}))
	resp, err := r.hc.Do(req)
	if err != nil {
		// url.Error carries the URL, never the body, so the token cannot leak through it.
		return nil, &TransientError{Reason: ReasonUnreachable, Settled: GrantSettled(wrote.Load(), 0, false), Err: errors.New("no answer from monoes.me")}
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if rerr != nil {
		return nil, &TransientError{Reason: ReasonUnreachable, Settled: GrantSettled(true, resp.StatusCode, false), Err: errors.New("the answer of monoes.me was cut short")}
	}
	var ans struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	jerr := json.Unmarshal(body, &ans)
	if resp.StatusCode == http.StatusOK {
		if jerr != nil || ans.AccessToken == "" || ans.RefreshToken == "" {
			// monoes.me rotates at every use: an answer without both tokens spent this one.
			return nil, &TransientError{Reason: ReasonServerError, Err: errors.New("monoes.me answered without a usable token set")}
		}
		return &TokenSet{AccessToken: ans.AccessToken, RefreshToken: ans.RefreshToken}, nil
	}
	code := ""
	if jerr == nil {
		code = oauthCode(ans.Error)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && code == "invalid_grant" {
		return nil, &RefusedError{}
	}
	msg := fmt.Sprintf("monoes.me answered HTTP %d", resp.StatusCode)
	if code != "" {
		msg += " " + code
	}
	return nil, &TransientError{Reason: ReasonServerError, Settled: GrantSettled(true, resp.StatusCode, true), Err: errors.New(msg)}
}
