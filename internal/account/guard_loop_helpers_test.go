package account_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// loopServer is monoes.me for the refresher tests. Like the real one it accepts
// only the newest refresh token and rotates it on every use, so a refresh made
// twice shows as a refusal and a lockout, not only as a count of two. Its tokens
// live for life and carry an iat that lags the guard's clock by lag: a local
// clock that runs ahead of monoes.me.
type loopServer struct {
	e     *env
	calls atomic.Int32

	mu    sync.Mutex
	valid string        // the one refresh token it accepts
	seq   int           // how many it has issued
	lag   time.Duration // the iat of what it mints is this far behind the guard's clock
	life  time.Duration // an hour when zero
	err   error         // answered instead of tokens
	takes time.Duration // the fixture clock moves by this inside each call, so a call is slow
}

func newLoopServer(e *env) *loopServer { return &loopServer{e: e, valid: "rt-1"} }

func (s *loopServer) set(fn func(*loopServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// signIn stores a session as a login would have (see env.signIn) and has the
// server accept the refresh token that login stored.
func (s *loopServer) signIn(age, life time.Duration) {
	s.e.t.Helper()
	s.e.signIn(age, life)
	s.set(func(s *loopServer) { s.valid = "rt-1" })
}

func (s *loopServer) Refresh(ctx context.Context, refreshToken string) (*account.TokenSet, error) {
	s.calls.Add(1)
	s.mu.Lock()
	takes, err := s.takes, s.err
	s.mu.Unlock()
	if takes > 0 {
		s.e.f.Clock.Advance(takes)
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if refreshToken != s.valid {
		return nil, &account.RefusedError{Description: "refresh token already used"}
	}
	life := s.life
	if life == 0 {
		life = time.Hour
	}
	s.seq++
	s.valid = fmt.Sprintf("rt-%d", s.seq+1)
	access := s.e.f.Token(accounttest.TokenOptions{IssuedAt: s.e.f.Clock.Now().Add(-s.lag), Lifetime: life})
	return &account.TokenSet{AccessToken: access, RefreshToken: s.valid}, nil
}

// guardOn is a guard over the env's session that refreshes through ref and
// wakes every poll. It is closed when the test ends, and a Close that does not
// return within 5 seconds fails the test instead of hanging the test binary.
func (e *env) guardOn(ref account.Refresher, poll time.Duration) *account.Guard {
	e.t.Helper()
	g := account.NewGuard(account.GuardOptions{
		Store: account.OpenStore(e.dir, e.seal), Refresher: ref, Now: e.f.Clock.Now, Poll: poll,
	})
	t := e.t
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			g.Close()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Close did not return")
		}
	})
	return g
}

// refusedSession is what another process writes when monoes.me refused it.
func refusedSession() *account.Session {
	return &account.Session{V: 1, Host: account.HostURL, User: &account.User{ID: "user-1"}, State: "refused"}
}

// waitForCalls waits, in real time, until the server has been called at least n times.
func waitForCalls(t *testing.T, srv *loopServer, n int32, what string) {
	t.Helper()
	eventually(t, what, func() bool { return srv.calls.Load() >= n })
}

// expectCalls waits quietFor (thirty ticks of the refresher at loopPoll) and then
// fails unless the server has been called exactly n times: the refresher did
// what it should and nothing more.
func expectCalls(t *testing.T, srv *loopServer, n int32, what string) {
	t.Helper()
	quiet()
	if got := srv.calls.Load(); got != n {
		t.Fatalf("%s: %d calls, want %d", what, got, n)
	}
}
