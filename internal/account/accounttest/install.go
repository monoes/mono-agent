package accounttest

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// Mode is the state Install puts the process guard in.
type Mode int

const (
	SignedIn      Mode = iota // enforced, a valid token, state ok
	InGrace                   // enforced, an expired token inside the 24 hours, state grace(unreachable)
	LockedNoLogin             // enforced, no session, locked(not_logged_in)
	LockedRefused             // enforced, session marked refused, locked(refused)
	Dormant                   // EnforceFrom is the zero time: nothing is enforced
)

// Install builds a guard in the given mode over a temporary store with a
// throwaway key, trusts that key, sets the enforcement date for the mode,
// installs the guard as the process guard with the test-binary exception
// switched off (strict), and restores all of it when the test ends. It returns
// the guard. A test builds at most one fixture (here, with InstallWithFixture or
// with New): the trusted key is process-global, so a second one replaces the
// first one's key. And it must not call t.Parallel(): the seams it uses panic if it does.
//
// The guard is real and the test strict, so a gate site's "refuses when locked"
// test cannot be let through by the test-binary exception (D24). What it cannot
// catch is a gate site that skips Require when Current() is nil: that takes a
// no-guard strict test (spec section 11), Install(t, LockedNoLogin) followed by
// account.Install(nil), which refuses (the date Install sets is already behind the
// real clock).
func Install(t testing.TB, m Mode) *account.Guard {
	t.Helper()
	g, _ := InstallWithFixture(t, m)
	return g
}

// InstallWithFixture is Install that also returns the Fixture, for a test that
// needs the key to mint another token or the Clock to move time. The same two
// rules hold: at most one fixture per test, and no t.Parallel() (it panics).
func InstallWithFixture(t testing.TB, m Mode) (*account.Guard, *Fixture) {
	t.Helper()
	f := New(t)
	if m == Dormant {
		account.SetEnforceFromForTest(t, time.Time{})
	}
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	now := f.Clock.Now()
	user := &account.User{ID: "user-1", Email: "user@example.test", Username: "user"}
	var sess *account.Session
	switch m {
	case SignedIn, InGrace:
		issued := now
		if m == InGrace {
			issued = now.Add(-2 * time.Hour) // a one-hour token, expired an hour ago
		}
		var err error
		if sess, err = account.NewSession(account.HostURL, f.Token(TokenOptions{IssuedAt: issued}), user, now); err != nil {
			t.Fatalf("accounttest: building the session: %v", err)
		}
		if m == InGrace {
			sess.LastResult = string(account.ReasonUnreachable)
		}
	case LockedRefused:
		sess = &account.Session{V: 1, Host: account.HostURL, User: user, HW: now, State: "refused", LastResult: "refused", LastAttempt: now}
	case LockedNoLogin, Dormant:
		// no session: nobody signed in
	default:
		t.Fatalf("accounttest: unknown mode %d", m)
	}
	if sess != nil {
		if err := store.Save(sess); err != nil {
			t.Fatalf("accounttest: saving the session: %v", err)
		}
	}
	g := account.NewGuard(account.GuardOptions{Store: store, Now: f.Clock.Now})
	t.Cleanup(g.Close)
	account.StrictForTest(t)
	account.InstallForTest(t, g)
	return g, f
}
