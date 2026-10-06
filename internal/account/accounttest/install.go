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
// the guard. A test that uses it must not call t.Parallel().
func Install(t testing.TB, m Mode) *account.Guard {
	t.Helper()
	g, _ := InstallWithFixture(t, m)
	return g
}

// InstallWithFixture is Install that also returns the Fixture, for a test that
// needs the key to mint another token or the Clock to move time.
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
