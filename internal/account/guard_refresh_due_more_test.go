package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// entryPoints are the two ways a foreground caller refreshes. They differ only
// while the package is dormant, so every rule of when a refresh is due is
// pinned for both.
var entryPoints = []struct {
	name string
	call func(*account.Guard, context.Context) (account.Status, error)
}{
	{"EnsureFresh", (*account.Guard).EnsureFresh},
	{"Refresh", (*account.Guard).Refresh},
}

// A foreground call refreshes under five minutes left, and not before: past a
// token's half-life it is the background refresher's turn, not the CLI's.
func TestAForegroundCallRefreshesOnlyUnderFiveMinutesLeft(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration // of a token that lives an hour
		due  bool
	}{
		{"35 minutes old, 25 left: past its half-life, not due", 35 * time.Minute, false},
		{"exactly 5 minutes left", 55 * time.Minute, false},
		{"4 minutes 59 seconds left", 55*time.Minute + time.Second, true},
		{"1 minute left", 59 * time.Minute, true},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.signIn(c.age, time.Hour)
				var want int32
				if c.due {
					want = 1
				}
				st, err := ep.call(e.g, context.Background())
				if err != nil || st.State != account.StateOK || e.ref.calls.Load() != want {
					t.Fatalf("%s = %s/%q, %v with %d network refreshes, want ok and %d", ep.name, st.State, st.Reason, err, e.ref.calls.Load(), want)
				}
			})
		}
	}
}

// The negative cache holds off every foreground call, whichever way it is made,
// for a minute after an attempt.
func TestTheNegativeCacheHoldsOffEveryForegroundCallForAMinute(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // expired
			e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
			attempts := func(when string, want int32) {
				t.Helper()
				if _, err := ep.call(e.g, context.Background()); err != nil {
					t.Fatalf("%s: %v", when, err)
				}
				if n := e.ref.calls.Load(); n != want {
					t.Fatalf("%s: %d network attempts so far, want %d", when, n, want)
				}
			}
			attempts("the first call", 1)
			attempts("straight after it", 1)
			e.f.Clock.Advance(59 * time.Second)
			attempts("59 seconds after it", 1)
			e.f.Clock.Advance(time.Second)
			attempts("a minute to the second after it", 2)
		})
	}
}

// The negative cache counts from the last attempt the session records, not from
// its high-water mark: the two differ whenever a login is older than its last try.
func TestTheNegativeCacheCountsFromTheLastAttemptAndNotFromTheMark(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			sess := e.signIn(2*time.Hour, time.Hour) // expired, and its mark is two hours old
			sess.LastAttempt = e.f.Clock.Now().Add(-30 * time.Second)
			e.save(sess)
			if _, err := ep.call(e.g, context.Background()); err != nil || e.ref.calls.Load() != 0 {
				t.Fatalf("%s with an attempt 30 seconds old: %v with %d network attempts, want none", ep.name, err, e.ref.calls.Load())
			}
			e.f.Clock.Advance(30 * time.Second) // the attempt is a minute old now
			if st, err := ep.call(e.g, context.Background()); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
				t.Fatalf("%s with an attempt a minute old: %s/%q, %v with %d network attempts, want ok after one", ep.name, st.State, st.Reason, err, e.ref.calls.Load())
			}
		})
	}
}

// A session with nothing to refresh with is never due, even when the server
// would answer: one monoes.me refused (a token left in it changes nothing), and
// one that holds no token.
func TestASessionThatCannotBeRefreshedIsNeverDue(t *testing.T) {
	cases := []struct {
		name   string
		sess   func(token string) *account.Session
		reason account.Reason
	}{
		{"refused, though a token is still in it", func(token string) *account.Session {
			return &account.Session{V: 1, Host: account.HostURL, AccessToken: token, User: &account.User{ID: "user-1"}, State: "refused"}
		}, account.ReasonRefused},
		{"not refused, but no token", func(string) *account.Session {
			return &account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "user-1"}}
		}, account.ReasonNotLoggedIn},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.save(c.sess(e.f.Token(accounttest.TokenOptions{IssuedAt: e.f.Clock.Now().Add(-2 * time.Hour)})))
				if err := e.store.SaveRefresh("rt-1"); err != nil {
					t.Fatal(err)
				}
				st, err := ep.call(e.g, context.Background())
				if err != nil || st.State != account.StateLocked || st.Reason != c.reason || e.ref.calls.Load() != 0 {
					t.Fatalf("%s = %s/%q, %v with %d network refreshes, want locked/%q and none", ep.name, st.State, st.Reason, err, e.ref.calls.Load(), c.reason)
				}
			})
		}
	}
}
