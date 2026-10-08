package libraryfake_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

func postToken(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(base+"/api/auth/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func refresh(rt, resource string) url.Values {
	f := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {"monoagent"}}
	if resource != "" {
		f.Set("resource", resource)
	}
	return f
}

func verifies(token string) bool {
	_, err := account.Verify(token, time.Now())
	return err == nil
}

// Spec §4.1 and plan A's findings: a token is a JWT exactly when a resource is
// sent, only for the MonoAgent audience, and the audience sticks to its chain.
func TestAccessTokensAreJWTsOnlyWhenTheAudienceIsSent(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	_, rt := fake.NewGrant("ada")

	status, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	tok, _ := body["access_token"].(string)
	rec, err := account.Verify(tok, time.Now())
	if status != 200 || err != nil || rec.Sub != "u-ada" || rec.ExpiresAt.Sub(rec.IssuedAt) != time.Hour || fake.LastResource() != account.Audience {
		t.Fatalf("with the audience: HTTP %d, %v", status, err)
	}

	status, body = postToken(t, fake.URL, refresh(body["refresh_token"].(string), ""))
	if tok, _ = body["access_token"].(string); status != 200 || !verifies(tok) || fake.LastResource() != "" {
		t.Fatalf("a chain that has the audience keeps it: HTTP %d", status)
	}

	_, older := fake.NewGrant("ada")
	status, body = postToken(t, fake.URL, refresh(older, ""))
	if tok, _ = body["access_token"].(string); status != 200 || verifies(tok) {
		t.Fatalf("a chain that never had a resource stays opaque: HTTP %d", status)
	}

	status, body = postToken(t, fake.URL, refresh(body["refresh_token"].(string), "https://elsewhere.example"))
	if status != 400 || body["error"] != "invalid_target" {
		t.Fatalf("another audience: HTTP %d %v", status, body)
	}
}

// The clock dates the tokens; Block revokes everything the user holds, so the next
// refresh is invalid_grant and the library refuses the access token.
func TestClockDatesTokensAndBlockRevokesThem(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	fake.SetClock(func() time.Time { return at })
	_, rt := fake.NewGrant("ada")
	_, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	access, _ := body["access_token"].(string)
	if rec, err := account.Verify(access, at); err != nil || !rec.IssuedAt.Equal(at) {
		t.Fatalf("the token is not dated by the fake's clock: %v", err)
	}

	fake.Block("u-ada")
	if status, body := postToken(t, fake.URL, refresh(body["refresh_token"].(string), account.Audience)); status != 400 || body["error"] != "invalid_grant" || fake.Replays != 0 {
		t.Fatalf("refresh of a blocked user: HTTP %d %v (replays %d)", status, body, fake.Replays)
	}
	req, _ := http.NewRequest(http.MethodGet, fake.URL+"/api/library/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a blocked user's access token still works: HTTP %d", resp.StatusCode)
	}
}

// Plan A, spike S2: presenting a rotated refresh token again ends every refresh
// token of the account. The fake does the same and counts it, so a test can prove
// that a client never causes one.
func TestReplayingARotatedRefreshTokenRevokesTheAccount(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	_, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	newer := body["refresh_token"].(string)
	if status, _ := postToken(t, fake.URL, refresh(rt, account.Audience)); status != 400 || fake.Replays != 1 {
		t.Fatalf("the replay: HTTP %d, replays %d", status, fake.Replays)
	}
	if status, _ := postToken(t, fake.URL, refresh(newer, account.Audience)); status != 400 {
		t.Fatalf("the newer refresh token survived a replay: HTTP %d", status)
	}
}

// A24, the case this fake exists to show: monoes.me rotates the refresh token and the answer never
// arrives, so the client holds a token that monoes.me has already spent. (RefreshDrop hangs up before
// the token is looked at and leaves it good.) Presenting the spent token again is a replay.
func TestALostAnswerLeavesTheRefreshTokenSpent(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	_, rt := fake.NewGrant("ada")
	fake.SetRefreshMode(libraryfake.RefreshLost)
	resp, err := http.Post(fake.URL+"/api/auth/oauth2/token", "application/x-www-form-urlencoded", strings.NewReader(refresh(rt, account.Audience).Encode()))
	if err == nil {
		resp.Body.Close()
		t.Fatalf("a lost answer was answered: HTTP %d", resp.StatusCode)
	}
	fake.SetRefreshMode(libraryfake.RefreshOK) // takes the fake's lock, so what the lost request did is now visible here
	if fake.Refreshes != 1 || fake.Replays != 0 {
		t.Fatalf("refreshes %d, replays %d: the token must be rotated by the lost answer and not yet replayed", fake.Refreshes, fake.Replays)
	}
	if status, _ := postToken(t, fake.URL, refresh(rt, account.Audience)); status != 400 || fake.Replays != 1 {
		t.Fatalf("the spent token was presented again: HTTP %d, replays %d", status, fake.Replays)
	}
}

// OnToken runs when a token request arrives, in the request's own goroutine, and the fake still
// answers the request afterwards: a test that cancels a caller there sees what the caller does when
// it gives up while monoes.me is answering. nil removes the hook.
func TestOnTokenRunsWhenATokenRequestArrivesAndTheFakeStillAnswers(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	var calls atomic.Int32
	fake.OnToken(func() { calls.Add(1) })
	_, rt := fake.NewGrant("ada")
	status, body := postToken(t, fake.URL, refresh(rt, account.Audience))
	if next, _ := body["refresh_token"].(string); status != 200 || next == "" || calls.Load() != 1 {
		t.Fatalf("HTTP %d, the hook ran %d times, want 200 and once", status, calls.Load())
	}
	fake.OnToken(nil)
	_, other := fake.NewGrant("ada")
	if status, _ := postToken(t, fake.URL, refresh(other, account.Audience)); status != 200 || calls.Load() != 1 {
		t.Fatalf("HTTP %d, the removed hook ran again (%d calls)", status, calls.Load())
	}
}

func postVerify(t *testing.T, base string, body map[string]string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+"/api/auth/agent/claim/verify", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// Plan A, Task 7: the emailed code answers like the token endpoint when the
// MonoAgent audience is asked for, with an opaque access token and a refresh token
// when it is not; SetEmailTrade and SetEmailOpaque give the other two shapes a client
// must survive.
func TestEmailVerifyAnswersLikeTheTokenEndpointWhenTheAudienceIsAsked(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	verify := func(extra map[string]string) (int, map[string]any) {
		body := map[string]string{"email": "ada@example.com", "code": "123456", "client_id": "monoagent"}
		for k, v := range extra {
			body[k] = v
		}
		return postVerify(t, fake.URL, body)
	}
	shape := func(body map[string]any) (signed, refresh bool) {
		tok, _ := body["access_token"].(string)
		rt, _ := body["refresh_token"].(string)
		return verifies(tok), rt != ""
	}

	status, body := verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || !signed || !refresh {
		t.Fatalf("with the audience: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	status, body = verify(nil)
	if signed, refresh := shape(body); status != 200 || signed || !refresh {
		t.Fatalf("without the audience: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	if status, body = verify(map[string]string{"resource": "https://elsewhere.example"}); status != 400 || body["error"] != "invalid_target" {
		t.Fatalf("another audience: HTTP %d %v", status, body)
	}
	if status, body = verify(map[string]string{"resource": account.Audience, "code": "000000"}); status != 400 || body["error"] != "invalid_or_expired_code" {
		t.Fatalf("a wrong code: HTTP %d %v", status, body)
	}

	fake.SetEmailTrade(true)
	status, body = verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || signed || !refresh {
		t.Fatalf("a route that answers a refresh token to trade: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
	fake.SetEmailOpaque(true)
	status, body = verify(map[string]string{"resource": account.Audience})
	if signed, refresh := shape(body); status != 200 || signed || refresh {
		t.Fatalf("the route of today: HTTP %d, signed token %v, refresh token %v", status, signed, refresh)
	}
}

// Block ends the user's sign-ins, and a code that was issued before the block is a sign-in too:
// exchanging it afterwards must not mint tokens for a blocked user.
func TestBlockRefusesTheExchangeOfACodeIssuedBeforeIt(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	verifier := "a-verifier-of-the-test"
	sum := sha256.Sum256([]byte(verifier))
	redirect := "http://127.0.0.1:1/callback"
	q := url.Values{"client_id": {"monoagent"}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {"s"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {account.Audience}}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(fake.URL + "/api/auth/oauth2/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("code") == "" {
		t.Fatalf("no code issued: HTTP %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	fake.Block("u-ada")
	status, body := postToken(t, fake.URL, url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {redirect}, "client_id": {"monoagent"}, "code_verifier": {verifier}, "resource": {account.Audience}})
	if status != 400 || body["error"] != "invalid_grant" || body["access_token"] != nil {
		t.Fatalf("a blocked user's code was exchanged: HTTP %d, token issued %v", status, body["access_token"] != nil)
	}
}
