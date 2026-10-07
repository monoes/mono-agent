package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// olderLogin is what a caller of Adopt hands over: one older login's refresh token.
func olderLogin(rt string) func() (string, *account.User, bool) {
	return func() (string, *account.User, bool) { return rt, &account.User{ID: "u-ada", Username: "ada"}, true }
}

// Spec D23: an older refresh token becomes the session when monoes.me honors it.
func TestAdoptStoresASessionFromAnOlderRefreshToken(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	var told []account.AdoptResult
	res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = append(told, r) })
	if err != nil || !res.Adopted || res.Status.State != account.StateOK || res.Status.User.Username != "ada" || len(told) != 1 || !told[0].Adopted {
		t.Fatalf("adopted %v, state %q, told %d times, %v", res.Adopted, res.Status.State, len(told), err)
	}
	if got, _ := store.LoadRefresh(); got == "" || got == rt {
		t.Fatal("the rotated refresh token was not stored")
	}
	before := fake.Refreshes
	again, err := c.Adopt(context.Background(), func() (string, *account.User, bool) {
		t.Error("a session exists: the older login must not even be read")
		return rt, nil, true
	}, nil)
	if err != nil || again.Adopted || again.Tokens != nil || fake.Refreshes != before || fake.Replays != 0 {
		t.Fatalf("a second adoption must not ask monoes.me anything (adopted %v, refreshes %d -> %d, replays %d, %v)", again.Adopted, before, fake.Refreshes, fake.Replays, err)
	}
}

// Whatever monoes.me answers to the attempt is "sign in once more": never a stored
// refusal (D23). invalid_grant says the older token is Dead, so the caller removes
// it; the tokens monoes.me did issue come back, so the older login survives.
func TestAdoptNeverReadsAnAnswerAsARefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		mode libraryfake.RefreshMode
		dead bool
	}{"invalid_grant": {libraryfake.RefreshInvalidGrant, true}, "http 500": {libraryfake.RefreshServerError, false},
		"dropped": {libraryfake.RefreshDrop, false}} {
		fake := libraryfake.New()
		c, store := newFakeClient(t, fake)
		_, rt := fake.NewGrant("ada")
		fake.SetRefreshMode(tc.mode)
		var told account.AdoptResult
		res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
		if err != nil || res.Adopted || res.Tokens != nil || res.Dead != tc.dead || told.Dead != tc.dead {
			t.Errorf("%s: adopted %v, tokens %v, dead %v, %v", name, res.Adopted, res.Tokens != nil, res.Dead, err)
		}
		if sess, _ := store.Load(); sess != nil {
			t.Errorf("%s: a session was stored (%s)", name, sessionSummary(sess))
		}
		fake.Close()
	}
	fake := libraryfake.New()
	defer fake.Close()
	fake.SetOpaqueTokens(true) // resource ignored: the exchange succeeds, the token cannot be a session
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	var told account.AdoptResult
	res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
	if err != nil || res.Adopted || res.Tokens == nil || res.Tokens.RefreshToken == "" || res.Tokens.RefreshToken == rt || told.Tokens == nil {
		t.Fatalf("opaque answer: adopted %v, tokens %v, told tokens %v, %v", res.Adopted, res.Tokens != nil, told.Tokens != nil, err)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session was stored (%s)", sessionSummary(sess))
	}
}

// A24: a failure that leaves no doubt that the older refresh token was not spent (nothing was sent, or
// monoes.me answered with a complete 4xx: it processed nothing) leaves the older login as it was, and the
// result says nothing. A request that went out and got any other answer, or none, may have spent it (a 500
// can come after the rotation was committed): the result is Unconfirmed and the caller drops its copy,
// because presenting it again after monoes.me's 300-second reuse window would end every refresh token of
// the account.
func TestAdoptTellsAnUnknownOutcomeFromAKnownOne(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        libraryfake.RefreshMode
		gone        bool // monoes.me is not there at all
		unconfirmed bool
	}{
		{"nobody answers", libraryfake.RefreshOK, true, false},
		{"invalid_client", libraryfake.RefreshInvalidClient, false, false},
		{"invalid_target", libraryfake.RefreshInvalidTarget, false, false},
		{"http 500", libraryfake.RefreshServerError, false, true},
		{"the connection closes after the request was read", libraryfake.RefreshDrop, false, true},
		{"an answer that is not a token set", libraryfake.RefreshMalformed, false, true},
		{"the token is rotated and the answer is lost", libraryfake.RefreshLost, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := libraryfake.New()
			defer fake.Close()
			c, store := newFakeClient(t, fake)
			_, rt := fake.NewGrant("ada")
			fake.SetRefreshMode(tc.mode)
			if tc.gone {
				fake.Close()
			}
			var told account.AdoptResult
			res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
			if err != nil || res.Adopted || res.Dead || res.Tokens != nil || res.Unconfirmed != tc.unconfirmed || told.Unconfirmed != tc.unconfirmed {
				t.Fatalf("adopted %v, dead %v, tokens %v, unconfirmed %v (told %v), want unconfirmed %v: %v",
					res.Adopted, res.Dead, res.Tokens != nil, res.Unconfirmed, told.Unconfirmed, tc.unconfirmed, err)
			}
			if sess, _ := store.Load(); sess != nil {
				t.Fatalf("a session was stored (%s)", sessionSummary(sess))
			}
		})
	}
}

// A24(d): the exchange was answered, so the older refresh token is spent, but the key store did not take the new
// one. The guard carries a marker for such a case and recovers the answer; an adoption has none, so it is an
// unknown outcome too: the result is Unconfirmed (the caller drops its copy of the older token, which must not be
// presented again), no tokens are handed back for it to keep somewhere else, nothing is stored, and the error says
// what failed.
func TestAdoptTreatsAnAnswerThatCouldNotBeStoredAsUnconfirmed(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	store := account.OpenStore(t.TempDir(), unavailableSealer{})
	c := account.NewClient(fake.URL, store)
	_, rt := fake.NewGrant("ada")
	var told account.AdoptResult
	res, err := c.Adopt(context.Background(), olderLogin(rt), func(r account.AdoptResult) { told = r })
	if !errors.Is(err, account.ErrKeyringUnavailable) {
		t.Fatalf("err = %v, want the key store's failure", err)
	}
	if res.Adopted || res.Dead || res.Tokens != nil || !res.Unconfirmed || told.Tokens != nil || !told.Unconfirmed {
		t.Fatalf("adopted %v, dead %v, tokens %v, unconfirmed %v (told: tokens %v, unconfirmed %v): want Unconfirmed alone",
			res.Adopted, res.Dead, res.Tokens != nil, res.Unconfirmed, told.Tokens != nil, told.Unconfirmed)
	}
	if fake.Refreshes != 1 || fake.Replays != 0 {
		t.Fatalf("%d exchanges, %d replays: the older token is presented once and never again", fake.Refreshes, fake.Replays)
	}
	if sess, _ := store.Load(); sess != nil {
		t.Fatalf("a session was stored (%s)", sessionSummary(sess))
	}
}

// A23 and A25: a session with no token that monoes.me did not refuse is the clock-guard record that
// logout, or the guard on a machine that never signed in, leaves. It is nobody's login, so an older
// login is still adopted, and the session replaces the record.
func TestAdoptIgnoresTheClockGuardRecordOfAMachineThatNeverSignedIn(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	if err := store.Save(&account.Session{V: 1, Host: fake.URL, HW: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, rt := fake.NewGrant("ada")
	res, err := c.Adopt(context.Background(), olderLogin(rt), nil)
	if err != nil || !res.Adopted {
		t.Fatalf("adopted %v, %v", res.Adopted, err)
	}
	if sess, _ := store.Load(); sess == nil || sess.AccessToken == "" {
		t.Fatalf("the record was not replaced by the session: %s", sessionSummary(sess))
	}
}

// A session that monoes.me refused is a login that ended, not a record: adoption leaves it alone and
// asks monoes.me nothing.
func TestAdoptStopsAtASessionThatMonoesMeRefused(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	if err := store.Save(&account.Session{V: 1, Host: fake.URL, State: "refused", Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
	_, rt := fake.NewGrant("ada")
	res, err := c.Adopt(context.Background(), func() (string, *account.User, bool) {
		t.Error("a refused session exists: the older login must not even be read")
		return rt, nil, true
	}, nil)
	if err != nil || res.Adopted || fake.Refreshes != 0 {
		t.Fatalf("adopted %v, refreshes %d, %v", res.Adopted, fake.Refreshes, err)
	}
}

// monoes.me ends every refresh token of an account when a rotated one is presented
// again (plan A, spike S2), so the older login is read and updated while the store
// lock is held: no other process can present the token between the read and the
// exchange, or read it between the exchange and its update.
func TestAdoptReadsAndUpdatesTheOlderLoginUnderTheStoreLock(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	held := func(when string) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		if unlock, err := store.Lock(ctx); err == nil {
			unlock()
			t.Errorf("the store lock was free %s", when)
		}
	}
	_, err := c.Adopt(context.Background(), func() (string, *account.User, bool) {
		held("while the older login was read")
		return rt, nil, true
	}, func(account.AdoptResult) { held("while the older login was updated") })
	if err != nil {
		t.Fatal(err)
	}
}

// A20: the exchange spends the older refresh token, and its answer holds the only copy of the one that
// replaces it, so once it is sent it is completed and stored even if the caller gives up while
// monoes.me is answering (Ctrl-C, a closing context). The caller's context is cancelled at the moment
// the fake has the request; the session is stored all the same, and nothing is presented twice.
func TestAdoptStoresTheAnswerWhenTheCallerGivesUpMidCall(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	_, rt := fake.NewGrant("ada")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.OnToken(cancel)
	res, err := c.Adopt(ctx, olderLogin(rt), nil)
	if err != nil || !res.Adopted || fake.Refreshes != 1 || fake.Replays != 0 {
		t.Fatalf("adopted %v, refreshes %d, replays %d, %v", res.Adopted, fake.Refreshes, fake.Replays, err)
	}
	if got, _ := store.LoadRefresh(); got == "" || got == rt {
		t.Fatal("the rotated refresh token was not stored: the older one is spent and would be presented again")
	}
}

// lockCheckedStore reports every call into the refresh token's key store (LoadRefresh, SaveRefresh) that is made
// while session.lock is free. B1a bounds each such call to ten seconds and lets at most one that outlived its bound
// stay parked; the bound, and the SaveRefresh that follows a grant, are safe only while the lock serializes the calls.
type lockCheckedStore struct {
	account.Store
	t *testing.T
}

func (s lockCheckedStore) held(call string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if unlock, err := s.Store.Lock(ctx); err == nil {
		unlock()
		s.t.Errorf("%s ran without session.lock", call)
	}
}

func (s lockCheckedStore) LoadRefresh() (string, error) {
	s.held("LoadRefresh")
	return s.Store.LoadRefresh()
}

func (s lockCheckedStore) SaveRefresh(token string) error {
	s.held("SaveRefresh")
	return s.Store.SaveRefresh(token)
}

// Every call the client makes into the key store holds the store lock: the sign-in's commit, the refresh token that
// logout reads for its revocation, and the commit after the adoption exchange.
func TestEveryKeyStoreCallOfTheClientHoldsTheStoreLock(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	libraryfake.TrustKey(t)
	c := account.NewClient(fake.URL, lockCheckedStore{account.OpenStore(t.TempDir(), account.NewMemorySealer()), t})
	ctx := context.Background()
	signInAtFake(t, c)
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	_, rt := fake.NewGrant("ada")
	if res, err := c.Adopt(ctx, olderLogin(rt), nil); err != nil || !res.Adopted {
		t.Fatalf("adopted %v, %v", res.Adopted, err)
	}
}
