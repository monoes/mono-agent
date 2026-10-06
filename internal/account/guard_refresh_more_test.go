package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// refresherFunc adapts a function to a Refresher, so that a test can act inside
// the network call.
type refresherFunc func(ctx context.Context, refreshToken string) (*account.TokenSet, error)

func (f refresherFunc) Refresh(ctx context.Context, refreshToken string) (*account.TokenSet, error) {
	return f(ctx, refreshToken)
}

// spyStore is the counting store with hooks on Lock: onLock runs when the lock
// is asked for, before it is taken; lockErr makes the call fail at once;
// afterLock runs once the lock is held.
type spyStore struct {
	*countingStore
	mu        sync.Mutex
	onLock    func(ctx context.Context)
	lockErr   error
	afterLock func()
}

func (s *spyStore) Lock(ctx context.Context) (func(), error) {
	s.mu.Lock()
	on, lockErr, after := s.onLock, s.lockErr, s.afterLock
	s.mu.Unlock()
	if on != nil {
		on(ctx)
	}
	if lockErr != nil {
		s.count("Lock")
		return nil, lockErr
	}
	unlock, err := s.countingStore.Lock(ctx)
	if err == nil && after != nil {
		after()
	}
	return unlock, err
}

func (e *env) spy() *spyStore {
	e.t.Helper()
	return &spyStore{countingStore: newCountingStore(account.OpenStore(e.dir, e.seal))}
}

// remaining is how long a context has left, and whether it has a deadline at all.
func remaining(ctx context.Context) (time.Duration, bool) {
	d, ok := ctx.Deadline()
	return time.Until(d), ok
}

type callResult struct {
	st  account.Status
	err error
}

// callWhileAnotherProcessHoldsTheLock makes a guard over its own store handle
// call EnsureFresh and ask for the lock (it finds the session due), which this
// test holds as another process would. It lets go once during has run, and
// returns what EnsureFresh returned.
func (e *env) callWhileAnotherProcessHoldsTheLock(during func()) (account.Status, error) {
	e.t.Helper()
	asked := make(chan struct{})
	var once sync.Once
	spy := e.spy()
	spy.onLock = func(context.Context) { once.Do(func() { close(asked) }) }
	g := e.guardOver(spy, 0)
	unlock, err := account.OpenStore(e.dir, e.seal).Lock(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	defer unlock() // safe to call twice; it lets a stuck guard go if the test fails
	done := make(chan callResult, 1)
	go func() {
		st, err := g.EnsureFresh(context.Background())
		done <- callResult{st, err}
	}()
	select {
	case <-asked:
	case <-time.After(3 * time.Second):
		e.t.Fatal("the guard never asked for the lock")
	}
	during()
	unlock()
	select {
	case r := <-done:
		return r.st, r.err
	case <-time.After(3 * time.Second):
		e.t.Fatal("the guard did not come back after the lock was let go")
	}
	return account.Status{}, nil
}

// A guard that waited for the lock re-reads the session and decides again: what
// another process did meanwhile is taken in, and a refresh it made is not
// repeated (the server would take the second one for theft).
func TestWhatAnotherProcessDidWhileThisOneWaitedForTheLockIsTakenIn(t *testing.T) {
	t.Run("it refreshed: the refresh is not repeated", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(56*time.Minute, time.Hour) // due
		now := e.f.Clock.Now()
		st, err := e.callWhileAnotherProcessHoldsTheLock(func() {
			fresh, err := account.NewSession(account.HostURL, e.f.Token(accounttest.TokenOptions{Lifetime: 2 * time.Hour}), &account.User{ID: "user-1"}, now)
			if err != nil {
				t.Fatal(err)
			}
			e.save(fresh)
			if err := e.store.SaveRefresh("rt-2"); err != nil {
				t.Fatal(err)
			}
			e.ref.set(func(r *fakeRefresher) { r.valid = "rt-2" })
		})
		if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(2*time.Hour)) || e.ref.calls.Load() != 0 {
			t.Fatalf("EnsureFresh = %s valid until %v, %v with %d network refreshes, want the other process's token and no refresh", st.State, st.ValidUntil, err, e.ref.calls.Load())
		}
	})
	t.Run("it rotated the refresh token and could not write the session: this one refreshes with the new token", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(56*time.Minute, time.Hour) // due, and it stays due: the session is as it was
		st, err := e.callWhileAnotherProcessHoldsTheLock(func() {
			if err := e.store.SaveRefresh("rt-2"); err != nil { // the refresh token is written before the session
				t.Fatal(err)
			}
			e.ref.set(func(r *fakeRefresher) { r.valid, r.seq = "rt-2", 1 }) // the server has rotated once
		})
		if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
			t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want ok after one refresh with the token read under the lock", st.State, st.Reason, err, e.ref.calls.Load())
		}
		if rt, _ := e.store.LoadRefresh(); rt != "rt-3" {
			t.Fatal("the stored refresh token is not the one this refresh rotated to")
		}
	})
	t.Run("it was refused: the session is locked and nothing is tried", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(56*time.Minute, time.Hour) // due
		st, err := e.callWhileAnotherProcessHoldsTheLock(func() {
			e.refuse()
			if err := e.store.DeleteRefresh(); err != nil {
				t.Fatal(err)
			}
		})
		if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused || e.ref.calls.Load() != 0 {
			t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want locked/refused and none", st.State, st.Reason, err, e.ref.calls.Load())
		}
	})
	t.Run("it signed out: nothing is tried", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(56*time.Minute, time.Hour) // due
		st, err := e.callWhileAnotherProcessHoldsTheLock(func() {
			if err := os.Remove(filepath.Join(e.dir, "session.json")); err != nil {
				t.Fatal(err)
			}
			if err := e.store.DeleteRefresh(); err != nil {
				t.Fatal(err)
			}
		})
		if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || e.ref.calls.Load() != 0 {
			t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want locked/not_logged_in and none", st.State, st.Reason, err, e.ref.calls.Load())
		}
	})
}

// A refresh is decided from the cache: a session that is healthy, with a mark
// fresh enough, costs no lock, no write, no read of the refresh token and no
// read of the file beyond the guard's first.
func TestAHealthySessionIsJudgedFromTheCacheWithoutTouchingTheDisk(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(30*time.Second, time.Hour) // healthy; its mark is the token's iat, 30 seconds old
			spy := e.spy()
			g := e.guardOver(spy, 0)
			if st := g.Status(); st.State != account.StateOK {
				t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
			}
			stats, reads := spy.calls("Mtime"), spy.calls("Load")
			for i := 0; i < 3; i++ {
				if st, err := ep.call(g, context.Background()); err != nil || st.State != account.StateOK {
					t.Fatalf("%s = %s/%q, %v, want ok", ep.name, st.State, st.Reason, err)
				}
			}
			for _, name := range []string{"Lock", "Save", "LoadRefresh", "SaveRefresh", "DeleteRefresh"} {
				if n := spy.calls(name); n != 0 {
					t.Fatalf("a healthy session made %d calls to %s, want none", n, name)
				}
			}
			if m, l := spy.calls("Mtime"), spy.calls("Load"); m != stats || l != reads {
				t.Fatalf("%d stats and %d reads, want the %d and %d of the first Status only", m, l, stats, reads)
			}
			if n := e.ref.calls.Load(); n != 0 {
				t.Fatalf("%d network refreshes, want none", n)
			}
		})
	}
}

// A session.json that cannot be read under the lock says nothing about the
// account: the guard keeps what it has, reports the failure and tries nothing.
func TestAFileThatCannotBeReadUnderTheLockLeavesTheCacheAlone(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due
	if st := e.g.Status(); st.State != account.StateGrace {
		t.Fatalf("Status = %s/%q, want grace", st.State, st.Reason)
	}
	e.corrupt()
	st, err := e.g.EnsureFresh(context.Background())
	if err == nil || st.State != account.StateGrace || e.ref.calls.Load() != 0 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want grace, an error and none", st.State, st.Reason, err, e.ref.calls.Load())
	}
}

// A caller whose context has ended starts nothing: it does not even take the
// lock or look at the refresh token.
func TestACallerThatHasGivenUpTouchesNeitherTheDiskNorTheServer(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // due
			spy := e.spy()
			g := e.guardOver(spy, 0)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for i := 0; i < 40; i++ { // a select between "free" and "ended" picks at random: ask often
				if _, err := ep.call(g, ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("%s with an ended context: %v, want context.Canceled", ep.name, err)
				}
			}
			for _, name := range []string{"Lock", "Save", "LoadRefresh", "SaveRefresh", "DeleteRefresh"} {
				if n := spy.calls(name); n != 0 {
					t.Fatalf("an ended context made %d calls to %s, want none", n, name)
				}
			}
			if n := e.ref.calls.Load(); n != 0 {
				t.Fatalf("%d network refreshes, want none", n)
			}
		})
	}
}

// A caller that gives up just as it gets the lock starts no refresh: it
// returns its context's error, calls nothing and writes nothing.
func TestACallerThatGivesUpWhileItWaitsForTheLockStartsNoRefresh(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due
	before := describe(e.session())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spy := e.spy()
	spy.afterLock = cancel // the context ends as the lock is taken
	g := e.guardOver(spy, 0)
	st, err := g.EnsureFresh(ctx)
	if !errors.Is(err, context.Canceled) || st.State != account.StateGrace || e.ref.calls.Load() != 0 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d network refreshes, want grace, context.Canceled and none", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if after := describe(e.session()); after != before {
		t.Fatalf("an ended context changed the session: %s, was %s", after, before)
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
		t.Fatal("an ended context changed the refresh token")
	}
}

// A caller that waits behind a refresh already running in this process gives up
// with its own context; and a refresh that ends because its caller gave up
// records nothing, since that says nothing about the account.
func TestACallerBehindARunningRefreshGivesUpWithItsContext(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due
	before := describe(e.session())
	e.ref.set(func(r *fakeRefresher) { r.block = true }) // the first call hangs until its context ends
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	running := make(chan callResult, 1)
	go func() {
		st, err := e.g.EnsureFresh(first)
		running <- callResult{st, err}
	}()
	eventually(t, "the first refresh to reach the server", func() bool { return e.ref.calls.Load() == 1 })

	second, cancelSecond := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelSecond()
	waited := make(chan callResult, 1)
	go func() {
		st, err := e.g.EnsureFresh(second)
		waited <- callResult{st, err}
	}()
	select {
	case r := <-waited:
		if !errors.Is(r.err, context.DeadlineExceeded) || r.st.State != account.StateGrace || e.ref.calls.Load() != 1 {
			t.Fatalf("the caller that waited: %s/%q, %v with %d network refreshes, want grace, the deadline and the first call only", r.st.State, r.st.Reason, r.err, e.ref.calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a caller behind a running refresh did not give up when its context ended")
	}

	cancelFirst()
	select {
	case r := <-running:
		if !errors.Is(r.err, context.Canceled) || r.st.State != account.StateGrace {
			t.Fatalf("the refresh whose caller gave up: %s/%q, %v, want grace and context.Canceled", r.st.State, r.st.Reason, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the refresh did not end with its context")
	}
	if after := describe(e.session()); after != before {
		t.Fatalf("a refresh that ended with its caller changed the session: %s, was %s", after, before)
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
		t.Fatal("a refresh that ended with its caller changed the refresh token")
	}
}

// What the server answered is recorded even when the caller has given up by the
// time it arrives: the refresh token was rotated and the old one is dead, or
// the server ended the login.
func TestAnAnswerThatArrivesAfterTheCallerGaveUpIsStillRecorded(t *testing.T) {
	guardAnswering := func(e *env, answer func(context.Context, string) (*account.TokenSet, error), cancel context.CancelFunc) *account.Guard {
		late := refresherFunc(func(ctx context.Context, rt string) (*account.TokenSet, error) {
			ts, err := answer(ctx, rt)
			cancel() // the caller gives up while the answer is on its way
			return ts, err
		})
		g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Refresher: late, Now: e.f.Clock.Now})
		e.t.Cleanup(g.Close)
		return g
	}
	t.Run("new tokens", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := guardAnswering(e, e.ref.Refresh, cancel)
		st, err := g.EnsureFresh(ctx)
		if err != nil || st.State != account.StateOK {
			t.Fatalf("EnsureFresh = %s/%q, %v, want ok", st.State, st.Reason, err)
		}
		if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
			t.Fatal("the rotated refresh token was lost: the one on disk is dead")
		}
		if sess := e.session(); sess.LastResult != "ok" || !sess.LastAttempt.Equal(e.f.Clock.Now()) {
			t.Fatalf("stored session = %s, want the refresh recorded", describe(sess))
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := guardAnswering(e, func(context.Context, string) (*account.TokenSet, error) {
			return nil, &account.RefusedError{Description: "revoked"}
		}, cancel)
		st, err := g.EnsureFresh(ctx)
		if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "refresh.enc")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refresh.enc survived a refusal (stat err %v)", err)
		}
	})
}

// The lock wait and the network call have budgets of their own, and the
// waiter's outlasts the call's: whoever holds the lock for a whole call must not
// make the next caller give up before the answer is stored.
func TestTheLockWaitAndTheNetworkCallHaveTheirOwnBudgets(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due
	var lockLeft, callLeft time.Duration
	var lockSet, callSet bool
	spy := e.spy()
	spy.onLock = func(ctx context.Context) {
		if !lockSet {
			lockLeft, lockSet = remaining(ctx)
		}
	}
	spied := refresherFunc(func(ctx context.Context, rt string) (*account.TokenSet, error) {
		callLeft, callSet = remaining(ctx)
		return e.ref.Refresh(ctx, rt)
	})
	g := account.NewGuard(account.GuardOptions{Store: spy, Refresher: spied, Now: e.f.Clock.Now})
	t.Cleanup(g.Close)
	if _, err := g.EnsureFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lockSet || lockLeft <= 22*time.Second || lockLeft > 25*time.Second {
		t.Fatalf("the wait for the lock has %v (a deadline: %t), want 25 s", lockLeft, lockSet)
	}
	if !callSet || callLeft <= 17*time.Second || callLeft > 20*time.Second {
		t.Fatalf("the network call has %v (a deadline: %t), want 20 s", callLeft, callSet)
	}
}

// A lock that cannot be taken in time is an advisory error from either entry
// point, and the Status that comes with it stays usable.
func TestBothEntryPointsReportALockThatCannotBeTakenInTime(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(56*time.Minute, time.Hour) // due
			unlock, err := account.OpenStore(e.dir, e.seal).Lock(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			st, err := ep.call(e.g, ctx)
			if !errors.Is(err, context.DeadlineExceeded) || st.State != account.StateOK || e.ref.calls.Load() != 0 {
				t.Fatalf("%s = %s, %v with %d network refreshes, want ok, the deadline and none", ep.name, st.State, err, e.ref.calls.Load())
			}
		})
	}
}
