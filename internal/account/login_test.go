package account_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// newFakeClient is a Client for the fake over a temp store, with the fake's key trusted.
func newFakeClient(t *testing.T, fake *libraryfake.Server) (*account.Client, account.Store) {
	t.Helper()
	libraryfake.TrustKey(t)
	st := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	return account.NewClient(fake.URL, st), st
}

// userBrowser plays the user's browser: it opens the authorize URL, which redirects
// back to the loopback listener, on a goroutine the test waits for (inBrowser).
func userBrowser(t *testing.T) func(string) error {
	return func(u string) error {
		inBrowser(t, func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Errorf("userBrowser: %v", err)
				return
			}
			resp.Body.Close()
		})
		return nil
	}
}

// sessionSummary describes a session without its tokens, for failure messages.
func sessionSummary(s *account.Session) string {
	if s == nil {
		return "no session"
	}
	return fmt.Sprintf("host %s, state %q, reason %q, access token of %d bytes", s.Host, s.State, s.Reason, len(s.AccessToken))
}

func signInAtFake(t *testing.T, c *account.Client) account.Status {
	t.Helper()
	st, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return st
}

func TestLoginEstablishesTheSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	var shown string
	st, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), OnURL: func(u string) { shown = u }, Timeout: 10 * time.Second})
	if err != nil || st.State != account.StateOK || st.User == nil || st.User.Username != "ada" || st.User.Email != "ada@example.com" {
		t.Fatalf("status %+v, %v", st, err)
	}
	if !strings.Contains(shown, "resource=https%3A%2F%2Fmonoes.me%2Fapi%2Fmonoagent") || fake.LastResource() != account.Audience {
		t.Fatalf("the audience was not sent (authorize %s, token %q)", shown, fake.LastResource())
	}
	sess, _ := store.Load()
	rt, _ := store.LoadRefresh()
	rec, verr := account.Verify(sess.AccessToken, time.Now())
	if sess.Host != fake.URL || rt == "" || verr != nil || !sess.HW.Equal(rec.IssuedAt) || sess.LastResult != "ok" || sess.State != "" {
		t.Fatalf("session: %s (refresh token stored: %v)", sessionSummary(sess), rt != "")
	}
	// The code exchange sends the grant, the code, the verifier and the audience, and nothing that names this machine.
	reqs := fake.TokenRequests()
	got := slices.Sorted(maps.Keys(reqs[0].Form))
	if want := []string{"client_id", "code", "code_verifier", "grant_type", "redirect_uri", "resource"}; !slices.Equal(got, want) {
		t.Fatalf("the code exchange sent the fields %v, want %v", got, want)
	}
}

func TestLoginReplacesARefusedSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	if err := store.Save(&account.Session{V: 1, Host: fake.URL, State: "refused", Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
	if st := signInAtFake(t, c); st.State != account.StateOK {
		t.Fatalf("status %+v", st)
	}
	if sess, _ := store.Load(); sess.State != "" || sess.Reason != "" {
		t.Fatalf("the refusal outlived the new sign-in: %s", sessionSummary(sess))
	}
}

// A monoes.me that does not mint audience-bound JWTs yet answers an opaque token;
// nothing may be stored from it.
func TestLoginAnOpaqueAnswerStoresNothing(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetOpaqueTokens(true)
	c, store := newFakeClient(t, fake)
	_, err := c.Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(store.Dir(), "session.json")); !os.IsNotExist(serr) {
		t.Fatalf("a session was written from an opaque token: %v", serr)
	}
}

type unavailableSealer struct{}

func (unavailableSealer) Seal([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }
func (unavailableSealer) Open([]byte) ([]byte, error) { return nil, account.ErrKeyringUnavailable }

// Spec §9: sign-in fails closed without a key store, and leaves no session behind.
func TestLoginWithoutAKeyStoreWritesNoSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	store := account.OpenStore(t.TempDir(), unavailableSealer{})
	_, err := account.NewClient(fake.URL, store).Login(context.Background(), account.LoginOptions{Open: userBrowser(t), Timeout: 10 * time.Second})
	if !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session without its refresh token: %s", sessionSummary(sess))
	}
}

func TestEmailSignInStartsASession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	ctx := context.Background()
	if err := c.SendEmailCode(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyEmailCode(ctx, "ada@example.com", "000000"); !errors.Is(err, account.ErrBadCode) {
		t.Fatalf("a wrong code: %v", err)
	}
	st, err := c.VerifyEmailCode(ctx, " Ada@Example.com ", "123456")
	if err != nil || st.State != account.StateOK || st.User.Username != "ada" {
		t.Fatalf("status %+v, %v", st, err)
	}
	if rt, _ := store.LoadRefresh(); rt == "" || fake.LastResource() != account.Audience || fake.Replays != 0 {
		t.Fatal("the code was verified without the audience, or the session has no refresh token, or a token was replayed")
	}
	if n := len(fake.TokenRequests()); n != 0 {
		t.Fatalf("%d token requests: a code that names the audience is answered with the signed session in one call", n)
	}
}

// A route that answers a refresh token beside an opaque access token, whatever the
// request asks (plan A's Task 7 does so for a request without the audience), is
// served by trading that refresh token at the token endpoint, with the audience,
// for the signed one. The refresh token that comes back is the one kept: the emailed
// one is spent and never stored.
func TestEmailSignInTradesARefreshTokenAnsweredBesideAnOpaqueToken(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetEmailTrade(true)
	c, store := newFakeClient(t, fake)
	before := len(fake.TokenRequests())
	st, err := c.VerifyEmailCode(context.Background(), "ada@example.com", "123456")
	if err != nil || st.State != account.StateOK || st.User.Username != "ada" {
		t.Fatalf("status %+v, %v", st, err)
	}
	reqs := fake.TokenRequests()
	if len(reqs) != before+1 || reqs[before].Form.Get("grant_type") != "refresh_token" || reqs[before].Form.Get("resource") != account.Audience {
		t.Fatalf("%d token requests: want one refresh grant that names the audience", len(reqs)-before)
	}
	if rt, _ := store.LoadRefresh(); rt == "" || rt == reqs[before].Form.Get("refresh_token") || fake.Replays != 0 {
		t.Fatal("the emailed refresh token was kept instead of the rotated one, or was replayed")
	}
}

// Today's claim endpoint ignores the audience and answers an opaque token with no
// refresh token. The sign-in says so, and stores nothing.
func TestEmailSignInAtAServerThatCannotSignTheSession(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetEmailOpaque(true)
	c, store := newFakeClient(t, fake)
	if _, err := c.VerifyEmailCode(context.Background(), "ada@example.com", "123456"); !errors.Is(err, account.ErrEmailSessionUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session was stored: %s", sessionSummary(sess))
	}
}
