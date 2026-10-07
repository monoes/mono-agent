package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// refreshIfDue and the decision it makes again under the lock each read the
// Status and then the cached session, one after the other. A poll in another
// goroutine can swap the cache between the two reads, and a session.json that
// another process rewrote with a token this build cannot verify (a key it does
// not pin yet, in a roll-out) is a session with a token and no receipt: not ok,
// so due. It must be refreshed, never dereferenced. In these tests the clock the
// guard reads between the two stands for the other goroutine.

// swapOnSecondRead returns a clock for the guard under test that, once armed,
// swaps the cache at its second read: it advances time, stores a session with a
// token that does not verify and polls, as another goroutine's Status would.
// The guard and the flag are passed in because the clock is part of the guard.
func (e *env) swapOnSecondRead(g **account.Guard, armed *bool) func() time.Time {
	reads := 0
	return func() time.Time {
		if *armed {
			reads++
			if reads == 2 {
				*armed = false
				e.f.Clock.Advance(time.Second)
				e.storeToken("opaque-0123456789", e.f.Clock.Now().Add(-time.Minute))
				(*g).Status() // the other goroutine's poll reloads the changed file
			}
		}
		return e.f.Clock.Now()
	}
}

func TestARefreshDoesNotCrashWhenAPollSwapsTheCacheBeforeTheLock(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy: on its own, nothing is due
	var g *account.Guard
	armed := false
	g = account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Refresher: e.ref, Now: e.swapOnSecondRead(&g, &armed), Poll: time.Nanosecond})
	t.Cleanup(g.Close)
	if st := g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
	e.f.Clock.Advance(time.Second) // a poll is due
	armed = true                   // the second clock read of EnsureFresh is the one between its Status and its read of the cache
	st, err := g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want ok after one refresh that replaced the token", st.State, st.Reason, err, e.ref.calls.Load())
	}
}

func TestARefreshDoesNotCrashWhenAPollSwapsTheCacheAfterTheLock(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due: four minutes left
	spy := e.spy()
	var g *account.Guard
	armed := false
	g = account.NewGuard(account.GuardOptions{Store: spy, Refresher: e.ref, Now: e.swapOnSecondRead(&g, &armed), Poll: time.Nanosecond})
	t.Cleanup(g.Close)
	// While this guard waited for the lock another process left a healthy session;
	// the guard reads it under the lock, and a poll then swaps the cache once more.
	// The hook runs once the guard holds the file lock, so the write below rewrites
	// session.json under it, which only a writer that ignores the lock can do: this
	// swap after the lock is not an interleaving between processes that honor it.
	// It is the cache swap that the test is about.
	spy.afterLock = func() {
		healthy, err := account.NewSession(account.HostURL, e.f.Token(accounttest.TokenOptions{Lifetime: 2 * time.Hour}), &account.User{ID: "user-1"}, e.f.Clock.Now())
		if err != nil {
			t.Error(err)
			return
		}
		e.save(healthy)
		armed = true // the second clock read after this is the one between the Status and the cache read of the decision under the lock
	}
	st, err := g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want ok after one refresh that replaced the token", st.State, st.Reason, err, e.ref.calls.Load())
	}
}
