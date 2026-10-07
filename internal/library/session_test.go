package library_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// accountSession is the session source of one process: a new guard over store.
func accountSession(fake *libraryfake.Server, store account.Store) *library.AccountSession {
	guard := account.NewGuard(account.GuardOptions{Store: store, Refresher: account.NewRefresher(fake.URL)})
	return &library.AccountSession{Guard: guard, Store: store}
}

// signedIn signs the fake's ada in as the machine session and returns the pieces
// a library client needs to use it.
func signedIn(t *testing.T, fake *libraryfake.Server) *library.AccountSession {
	t.Helper()
	libraryfake.TrustKey(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	_, err := account.NewClient(fake.URL, store).Login(context.Background(),
		account.LoginOptions{Open: browser(t), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("account sign-in: %v", err)
	}
	return accountSession(fake, store)
}

// backdate makes the stored session's last refresh attempt d older: a guard holds
// a second attempt off for a minute (spec §4.4), so a test that wants a new
// process to renew moves the file's clock.
func backdate(t *testing.T, store account.Store, d time.Duration) {
	t.Helper()
	sess, err := store.Load()
	if err != nil || sess == nil {
		t.Fatalf("no session to backdate: %v", err)
	}
	sess.LastAttempt = sess.LastAttempt.Add(-d)
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
}

// methodOf names how a login was made, for failure messages that must not print it.
func methodOf(t *library.Token) string {
	if t == nil {
		return "(no login)"
	}
	return t.Method
}

func sessionClient(t *testing.T, base string, s library.SessionSource, legacy library.TokenStore) *library.Client {
	t.Helper()
	c := mustClient(t, base, legacy)
	c.Session = s
	return c
}

// Spec D21 and index §3.4 item 10: the library reads with the machine session's
// token, and only at the host that issued it.
func TestSessionTokenIsSentOnlyToItsHost(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	elsewhere := libraryfake.New()
	defer elsewhere.Close()
	sess := signedIn(t, fake)
	ctx := context.Background()

	c := sessionClient(t, fake.URL, sess, nil)
	me, err := c.Me(ctx)
	if err != nil || me.User.Username != "ada" {
		t.Fatalf("Me with the session = %+v, %v", me, err)
	}
	if tok, _ := c.Token(ctx); tok == nil || tok.Method != "session" || tok.RefreshToken != "" {
		t.Fatalf("token: present %v, method %q, refresh token kept %v", tok != nil, methodOf(tok), tok != nil && tok.RefreshToken != "")
	}

	other := sessionClient(t, elsewhere.URL, sess, nil)
	if _, err := other.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me at another host = %v", err)
	}
	if n := elsewhere.Requests["GET /api/library/me"]; n != 0 {
		t.Fatalf("the session token went to another host (%d requests)", n)
	}
}

// Dormant releases must change nothing a user can see: a login made in a profile's
// vault before the session existed keeps serving reads until a session replaces it.
func TestAnOlderLoginServesReadsUntilASessionExists(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	empty := &library.AccountSession{
		Store: account.OpenStore(t.TempDir(), account.NewMemorySealer()),
		Guard: account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())}),
	}
	legacy := &memStore{}
	login(t, fake, legacy) // the older login, in the profile's own store
	ctx := context.Background()

	c := sessionClient(t, fake.URL, empty, legacy)
	if me, err := c.Me(ctx); err != nil || me.User.Username != "ada" {
		t.Fatalf("Me with only the older login = %+v, %v", me, err)
	}
	if tok, _ := c.Token(ctx); tok.Method != "pkce" {
		t.Fatalf("token method = %q, want the older login's", tok.Method)
	}

	c = sessionClient(t, fake.URL, signedIn(t, fake), legacy)
	if tok, _ := c.Token(ctx); tok == nil || tok.Method != "session" {
		t.Fatalf("with a session, token method %q", methodOf(tok))
	}
}

// sharedStore is a TokenStore two goroutines may use.
type sharedStore struct {
	mu sync.Mutex
	t  *library.Token
}

func (s *sharedStore) Load(context.Context) (*library.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.t, nil
}

func (s *sharedStore) Save(_ context.Context, t *library.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *t
	s.t = &c
	return nil
}

func (s *sharedStore) Delete(context.Context) error {
	return s.Save(context.Background(), &library.Token{})
}

// Plan A, spike S2: monoes.me ends every refresh token of the account when a spent
// one is presented again. The library's refresh of a login of its own waits for the
// account lock, which an adoption in another process holds while it spends the
// token, and then refreshes what the vault holds, not the token it read earlier.
func TestOlderLoginRefreshWaitsForAnAdoptionAndUsesWhatItLeft(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	sess := &library.AccountSession{Store: store, Guard: account.NewGuard(account.GuardOptions{Store: store})}
	ctx := context.Background()
	unlock, err := store.Lock(ctx) // an adoption in another process holds the account lock
	if err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, fake.URL, sess, legacy)
	done := make(chan error, 1)
	go func() {
		_, err := c.Me(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the refresh did not wait for the account lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The adoption spends the refresh token, leaves the new tokens in the vault, and lets go.
	spent, err := account.NewRefresher(fake.URL).Refresh(ctx, old.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	fresh := old
	fresh.AccessToken, fresh.RefreshToken, fresh.ExpiresAt = spent.AccessToken, spent.RefreshToken, time.Time{}
	_ = legacy.Save(ctx, &fresh)
	unlock()
	if err := <-done; err != nil || fake.Replays != 0 {
		t.Fatalf("Me: %v (replays %d): the refresh presented a spent refresh token", err, fake.Replays)
	}
}

// A20: the refresh of the profile's own older login spends its refresh token like any other, so once
// the grant is sent it is completed and stored even if the caller gives up while monoes.me answers
// (Ctrl-C, a closing context): the vault keeps the refresh token that replaced the spent one, and
// nothing is presented twice.
func TestOlderLoginRefreshIsStoredWhenTheCallerGivesUp(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	c := mustClient(t, fake.URL, legacy)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.OnToken(cancel) // the caller gives up while monoes.me is answering
	tok, err := c.Token(ctx)
	kept, _ := legacy.Load(context.Background())
	if err != nil || tok == nil || kept == nil || kept.RefreshToken == old.RefreshToken || tok.RefreshToken != kept.RefreshToken || fake.Replays != 0 {
		t.Fatalf("token %v (%v), vault entry %v, replaced the spent refresh token %v, replays %d",
			tok != nil, err, kept != nil, kept != nil && kept.RefreshToken != old.RefreshToken, fake.Replays)
	}
}

// A24: the refresh of the profile's own older login spends its refresh token like any other. When the
// grant went out and the answer is lost (monoes.me rotated the token, the connection closed), the token
// may be spent, and presenting it again after monoes.me's 300-second reuse window would end every refresh
// token of the account, so the vault forgets the login and the client presents nothing more, not even the
// retry that the 401 of the revoked access token would trigger in the same call. The fake keeps no reuse
// window: a second presentation is a replay.
func TestOlderLoginRefreshDropsTheLoginWhenTheAnswerIsLost(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	c := mustClient(t, fake.URL, legacy)
	ctx := context.Background()
	fake.SetRefreshMode(libraryfake.RefreshLost)
	if _, err := c.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me after a lost answer = %v, want the login gone", err)
	}
	if kept, _ := legacy.Load(ctx); kept != nil && kept.RefreshToken != "" {
		t.Fatal("the vault still holds a refresh token that monoes.me may have spent")
	}
	if fake.Refreshes != 1 || fake.Replays != 0 {
		t.Fatalf("refreshes %d, replays %d: the token that the lost answer spent was presented again", fake.Refreshes, fake.Replays)
	}
	fake.SetRefreshMode(libraryfake.RefreshOK)
	if _, err := c.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) || fake.Refreshes != 1 {
		t.Fatalf("a later call = %v (refreshes %d): with the login dropped nothing may be presented", err, fake.Refreshes)
	}
}

// A24: the other outcomes the client cannot tell from a rotated token with a lost answer, a connection that
// closes before monoes.me looks at the token, a 200 that is not JSON, and a 500, which can come after the
// rotation was committed, drop the login, and nothing is presented again.
func TestOlderLoginRefreshDropsTheLoginWhenTheOutcomeIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode libraryfake.RefreshMode
	}{
		{"the connection closes after the request was read", libraryfake.RefreshDrop},
		{"an answer that is not JSON", libraryfake.RefreshMalformed},
		{"a 500", libraryfake.RefreshServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := libraryfake.New()
			defer fake.Close()
			seed := &memStore{}
			login(t, fake, seed)
			old := *seed.t
			old.ExpiresAt = time.Now().Add(-time.Minute)
			legacy := &sharedStore{t: &old}
			c := mustClient(t, fake.URL, legacy)
			ctx := context.Background()
			fake.SetRefreshMode(tc.mode)
			if _, err := c.Token(ctx); err != nil {
				t.Fatal(err)
			}
			if kept, _ := legacy.Load(ctx); kept != nil && kept.RefreshToken != "" {
				t.Fatal("the vault still holds a refresh token that monoes.me may have spent")
			}
			fake.SetRefreshMode(libraryfake.RefreshOK)
			asked := len(fake.TokenRequests())
			if tok, err := c.Token(ctx); err != nil || tok != nil || len(fake.TokenRequests()) != asked {
				t.Fatalf("a later call: token %v, %v, %d new token requests: with the login dropped nothing may be presented", tok != nil, err, len(fake.TokenRequests())-asked)
			}
		})
	}
}

// A24: a 200 answer that holds no token set is monoes.me's answer to the grant too, and it may have
// rotated the token and lost the new one with it: the login is dropped like any other unknown outcome.
// So is a 200 that names no refresh token: monoes.me rotates at every use, so the token presented is spent
// and its successor is not in the answer, and keeping the old one would present a dead token later. And so
// is an answer whose body is cut short, a 4xx's too: only a complete 4xx says that nothing was processed.
func TestOlderLoginRefreshDropsTheLoginWhenTheAnswerHoldsNoTokenSet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		cut    bool // promise more bytes than are sent, then end the connection
	}{
		{"no access token", 200, `{"token_type":"Bearer"}`, false},
		{"no refresh token", 200, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`, false},
		{"a 4xx whose body is cut off", 429, `{"error":"slow`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/oauth2/token" {
					http.NotFound(w, r) // the discovery: the client uses the default endpoints
					return
				}
				if tc.cut {
					w.Header().Set("Content-Length", "400")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			legacy := &sharedStore{t: &library.Token{AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer",
				Method: "pkce", BaseURL: srv.URL, ExpiresAt: time.Now().Add(-time.Minute)}}
			c := mustClient(t, srv.URL, legacy)
			ctx := context.Background()
			if _, err := c.Token(ctx); err != nil {
				t.Fatal(err)
			}
			if kept, _ := legacy.Load(ctx); kept != nil && kept.RefreshToken != "" {
				t.Fatal("the vault still holds a refresh token that monoes.me may have spent")
			}
		})
	}
}

// A24: a refresh that never left this machine, or that monoes.me answered with a complete 4xx (it processed
// nothing), cannot have spent the token: the login stays in the vault, and a later call presents it again.
func TestOlderLoginRefreshKeepsTheLoginAfterAKnownFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode libraryfake.RefreshMode
		gone bool // monoes.me is not there at all
	}{
		{"invalid_client", libraryfake.RefreshInvalidClient, false},
		{"invalid_target", libraryfake.RefreshInvalidTarget, false},
		{"nobody answers", libraryfake.RefreshOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := libraryfake.New()
			defer fake.Close()
			seed := &memStore{}
			login(t, fake, seed)
			old := *seed.t
			old.ExpiresAt = time.Now().Add(-time.Minute)
			legacy := &sharedStore{t: &old}
			c := mustClient(t, fake.URL, legacy)
			ctx := context.Background()
			fake.SetRefreshMode(tc.mode)
			if tc.gone {
				fake.Close()
			}
			if tok, err := c.Token(ctx); err != nil || tok == nil || tok.RefreshToken != old.RefreshToken {
				t.Fatalf("after a failed refresh the login must be as it was: %v", err)
			}
			if kept, _ := legacy.Load(ctx); kept == nil || kept.RefreshToken != old.RefreshToken {
				t.Fatal("a refresh that cannot have spent the token removed the login")
			}
			if tc.gone {
				return
			}
			fake.SetRefreshMode(libraryfake.RefreshOK)
			if tok, err := c.Token(ctx); err != nil || tok == nil || tok.RefreshToken == old.RefreshToken || fake.Replays != 0 {
				t.Fatalf("a later refresh: %v (replays %d)", err, fake.Replays)
			}
		})
	}
}

// Plan A, spike S2: a refresh token that monoes.me answers with invalid_grant is dead (rotated away by
// another binary, revoked, expired), and presenting a rotated-away token again ends every refresh token of
// the account. The login is dropped, and nothing presents it again.
func TestOlderLoginRefreshDropsADeadLogin(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &sharedStore{t: &old}
	c := mustClient(t, fake.URL, legacy)
	ctx := context.Background()
	fake.SetRefreshMode(libraryfake.RefreshInvalidGrant)
	if _, err := c.Token(ctx); err != nil {
		t.Fatal(err)
	}
	if kept, _ := legacy.Load(ctx); kept != nil && kept.RefreshToken != "" {
		t.Fatal("the vault still holds a refresh token that monoes.me answered invalid_grant: an older binary would present it again")
	}
	fake.SetRefreshMode(libraryfake.RefreshOK)
	asked := len(fake.TokenRequests())
	if tok, err := c.Token(ctx); err != nil || tok != nil || len(fake.TokenRequests()) != asked {
		t.Fatalf("a later call: token %v, %v, %d new token requests: a dead login may not be presented again", tok != nil, err, len(fake.TokenRequests())-asked)
	}
}

// unwritableStore is a vault that can be read and emptied but takes no new login: a locked keychain
// or a busy database at the moment a refresh's answer is to be saved.
type unwritableStore struct{ sharedStore }

func (s *unwritableStore) Save(context.Context, *library.Token) error {
	return errors.New("the vault cannot be written")
}

// A24(d): monoes.me answered the grant, so the refresh token presented is spent, but the vault did not
// take the one that replaced it. The spent token must not be presented again: the login is dropped as
// after an unknown outcome, the retry that the 401 of the old access token triggers in the same call
// presents nothing, and the vault no longer holds the token for the next command.
func TestOlderLoginRefreshDropsTheLoginWhenTheVaultFailsAfterTheAnswer(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	seed := &memStore{}
	login(t, fake, seed)
	old := *seed.t
	old.ExpiresAt = time.Now().Add(-time.Minute) // its access token has expired: a refresh is due
	legacy := &unwritableStore{sharedStore{t: &old}}
	c := mustClient(t, fake.URL, legacy)
	ctx := context.Background()
	asked := len(fake.TokenRequests())
	if _, err := c.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me after an answer the vault did not take = %v, want the login gone", err)
	}
	if n := len(fake.TokenRequests()) - asked; n != 1 || fake.Replays != 0 {
		t.Fatalf("%d token requests, replays %d: the refresh token that the answer spent was presented again", n, fake.Replays)
	}
	if kept, _ := legacy.Load(ctx); kept != nil && kept.RefreshToken != "" {
		t.Fatal("the vault still holds the refresh token that the answer spent: the next command would present it")
	}
}

// The account guard refreshes the session before a call when it is due. The client
// never refreshes it itself: a 401 is "log in first", and a refused session stops a
// new process before it asks monoes.me anything.
func TestSessionIsRefreshedByTheGuardAndNeverByTheClient(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	fake.AccessTTL = 2 * time.Minute // inside the 5 minute refresh margin: a refresh is due
	sess := signedIn(t, fake)
	backdate(t, sess.Store, 2*time.Minute)
	c := sessionClient(t, fake.URL, sess, nil)
	ctx := context.Background()
	if _, err := c.Me(ctx); err != nil || fake.Refreshes != 1 {
		t.Fatalf("Me: %v (refreshes %d, want the guard's one)", err, fake.Refreshes)
	}

	fake.RevokeAll() // monoes.me revoked everything
	if _, err := c.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) || fake.Refreshes != 1 {
		t.Fatalf("a 401 with a session: %v (refreshes %d, want no client refresh)", err, fake.Refreshes)
	}
	backdate(t, sess.Store, 2*time.Minute)
	before := fake.Requests["GET /api/library/me"]
	fresh := sessionClient(t, fake.URL, accountSession(fake, sess.Store), nil) // a new process, a new guard: its refresh is refused
	if _, err := fresh.Me(ctx); !errors.Is(err, library.ErrNotLoggedIn) || fake.Requests["GET /api/library/me"] != before {
		t.Fatalf("a refused session: %v (%d library requests went out)", err, fake.Requests["GET /api/library/me"]-before)
	}
}

func TestLibraryLogoutLegacyRevokesAndForgetsTheOlderLogin(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	legacy := &memStore{}
	c := login(t, fake, legacy)
	if err := c.LogoutLegacy(context.Background()); err != nil || legacy.t != nil {
		t.Fatalf("LogoutLegacy: %v (kept %v)", err, legacy.t != nil)
	}
	if _, err := c.Me(context.Background()); !errors.Is(err, library.ErrNotLoggedIn) {
		t.Fatalf("Me after LogoutLegacy = %v", err)
	}
	if err := (&library.Client{}).LogoutLegacy(context.Background()); err != nil {
		t.Fatalf("no store: %v", err)
	}
}
