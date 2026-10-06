package account_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// monoes.me rotates the refresh token by answering, so once it has answered the
// one on disk is dead. Presented again after the reuse window it is taken for
// theft and ends every refresh token of the account, which locks every install
// of it. So a refresh whose answer is lost to the client, because the caller's
// context ended (Ctrl-C, SIGTERM, a deadline, the guard closing) while the
// request was in flight, leaves exactly that dead token on disk. The guard
// therefore sends a grant with a context of its own, and these tests pin that
// what the server answered is always stored, whatever the caller does.

// reuseWindow is how long monoes.me answers a refresh token that was rotated
// away with the answer it gave the first time (plan A: 300 s).
const reuseWindow = 300 * time.Second

type usedGrant struct {
	at     time.Time
	answer *account.TokenSet
}

// windowServer is monoes.me's refresh grant as plan A configures it: the refresh
// token rotates on every use; the token that was rotated away is answered with
// the same answer inside the reuse window; after it, the provider takes it for
// theft and deletes every refresh token of the account, the current ones too.
// Like net/http, it delivers its answer only to a client whose context has not
// ended: a request that was sent and then abandoned has rotated the token and
// lost the answer.
type windowServer struct {
	f    *accounttest.Fixture
	life time.Duration // of the access tokens it mints; an hour when zero

	calls atomic.Int32

	mu      sync.Mutex
	current map[string]bool      // the refresh tokens it accepts, one per install
	used    map[string]usedGrant // the tokens it rotated away
	seq     int
	revoked bool     // a reuse after the window ended every refresh token of the account
	seen    []string // every refresh token presented, in order
	hook    func()   // runs once, after the server has rotated and before the client reads the answer
}

func newWindowServer(f *accounttest.Fixture, life time.Duration, current ...string) *windowServer {
	s := &windowServer{f: f, life: life, current: map[string]bool{}, used: map[string]usedGrant{}}
	for _, rt := range current {
		s.current[rt] = true
	}
	return s
}

// midCall makes fn run, once, while the next grant is in flight: the server has
// rotated, the client has not read the answer yet.
func (s *windowServer) midCall(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = fn
}

func (s *windowServer) Refresh(ctx context.Context, rt string) (*account.TokenSet, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.seen = append(s.seen, rt)
	now := s.f.Clock.Now()
	var answer *account.TokenSet
	used, wasUsed := s.used[rt]
	switch {
	case s.revoked:
		s.mu.Unlock()
		return nil, &account.RefusedError{Description: "session not found"}
	case s.current[rt]:
		s.seq++
		next := fmt.Sprintf("rt-rotated-%d", s.seq)
		answer = &account.TokenSet{AccessToken: s.f.Token(accounttest.TokenOptions{Lifetime: s.life}), RefreshToken: next}
		delete(s.current, rt)
		s.current[next] = true
		s.used[rt] = usedGrant{at: now, answer: answer}
	case wasUsed && now.Sub(used.at) <= reuseWindow:
		answer = used.answer // inside the window: the same answer again
	default:
		s.revoked, s.current = true, map[string]bool{} // a rotated-away token, after the window
		s.mu.Unlock()
		return nil, &account.RefusedError{Description: "refresh token reuse detected"}
	}
	hook := s.hook
	s.hook = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err := ctx.Err(); err != nil { // the client gave up: the request is aborted and the answer never read
		return nil, &account.TransientError{Reason: account.ReasonUnreachable, Err: err}
	}
	return answer, nil
}

func (s *windowServer) isCurrent(rt string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current[rt]
}

func (s *windowServer) isRevoked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked
}

func (s *windowServer) presented() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// signInInstall stores a login as `account login` would have: a token issued age
// ago for life, its mark at its iat, and rt sealed as the refresh token.
func signInInstall(t *testing.T, f *accounttest.Fixture, store account.Store, age, life time.Duration, rt string) {
	t.Helper()
	now := f.Clock.Now()
	token := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-age), Lifetime: life})
	sess, err := account.NewSession(account.HostURL, token, &account.User{ID: "user-1"}, now)
	if err != nil {
		t.Fatalf("signInInstall: %v", err)
	}
	sess.LastAttempt = now.Add(-age)
	if err := store.Save(sess); err != nil {
		t.Fatalf("signInInstall: %v", err)
	}
	if err := store.SaveRefresh(rt); err != nil {
		t.Fatalf("signInInstall: %v", err)
	}
}

// What the verdicts below rest on: the model answers a token that was rotated
// away again inside the window and takes it for theft after it.
func TestTheWindowServerAnswersARotatedTokenInsideTheWindowAndRevokesAfterIt(t *testing.T) {
	f := accounttest.New(t)
	srv := newWindowServer(f, 0, "rt-1")
	ctx := context.Background()
	first, err := srv.Refresh(ctx, "rt-1")
	if err != nil || first.RefreshToken != "rt-rotated-1" {
		t.Fatalf("the first grant = %+v, %v, want the token rt-rotated-1", first, err)
	}
	f.Clock.Advance(reuseWindow)
	if again, err := srv.Refresh(ctx, "rt-1"); err != nil || again != first {
		t.Fatalf("the same token %v after the first grant = %+v, %v, want the same answer again", reuseWindow, again, err)
	}
	f.Clock.Advance(time.Second)
	var refused *account.RefusedError
	if _, err := srv.Refresh(ctx, "rt-1"); !errors.As(err, &refused) || !srv.isRevoked() {
		t.Fatalf("the same token a second after the window = %v (revoked %v), want a refusal and the account revoked", err, srv.isRevoked())
	}
	if _, err := srv.Refresh(ctx, "rt-rotated-1"); !errors.As(err, &refused) || srv.isCurrent("rt-rotated-1") {
		t.Fatalf("the current token after a revocation = %v, want it refused too", err)
	}
}

// The user presses Ctrl-C while the grant is in flight. The answer is still
// stored, so the next attempt, minutes later and past the window, presents the
// rotated token and the account stays whole, the other installs included.
func TestACallerThatGivesUpMidGrantStillStoresTheAnswerAndNoInstallIsRevoked(t *testing.T) {
	f := accounttest.New(t)
	seal := account.NewMemorySealer()
	dirA, dirB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	storeA, storeB := account.OpenStore(dirA, seal), account.OpenStore(dirB, seal)
	signInInstall(t, f, storeA, 56*time.Minute, time.Hour, "rt-A") // four minutes left: a command refreshes
	signInInstall(t, f, storeB, 10*time.Minute, time.Hour, "rt-B") // another install of the same account
	srv := newWindowServer(f, 10*time.Minute, "rt-A", "rt-B")      // its access tokens live ten minutes
	guard := func(store account.Store) *account.Guard {
		g := account.NewGuard(account.GuardOptions{Store: store, Refresher: srv, Now: f.Clock.Now})
		t.Cleanup(g.Close)
		return g
	}

	// Install A: the gate of a command refreshes, and Ctrl-C comes mid-grant.
	ctx, ctrlC := context.WithCancel(context.Background())
	defer ctrlC()
	srv.midCall(ctrlC)
	st, err := guard(account.OpenStore(dirA, seal)).EnsureFresh(ctx)
	if err != nil || st.State != account.StateOK {
		t.Errorf("EnsureFresh after Ctrl-C mid-grant = %s/%q, %v, want ok: the answer arrived and is stored", st.State, st.Reason, err)
	}
	if rt, _ := storeA.LoadRefresh(); !srv.isCurrent(rt) {
		t.Errorf("after Ctrl-C mid-grant refresh.enc holds %q, which the server has rotated away: the next attempt presents a dead token", rt)
	}

	// The next command, six minutes later and so past the window. Both the
	// stored session of the old code (expired) and the new one (four minutes
	// left) are due, so every version of the guard presents its refresh token.
	f.Clock.Advance(6 * time.Minute)
	st, err = guard(account.OpenStore(dirA, seal)).EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK {
		t.Errorf("the next command on install A = %s/%q, %v, want ok", st.State, st.Reason, err)
	}

	// Install B did nothing wrong, and refreshes at its next due time.
	f.Clock.Advance(50 * time.Minute)
	st, err = guard(storeB).EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK {
		t.Errorf("install B = %s/%q, %v, want ok", st.State, st.Reason, err)
	}
	if srv.isRevoked() {
		t.Error("monoes.me took a refresh token that was rotated away for theft and ended every refresh token of the account")
	}
	if got, want := srv.presented(), []string{"rt-A", "rt-rotated-1", "rt-B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("refresh tokens presented = %v, want %v: the second grant of A must present the token the first one rotated to", got, want)
	}
}

// A grant that was sent is not cancelled by the caller: the Refresher gets a
// context of its own that ends at refreshCallTimeout, whatever deadline the
// caller has, and that stays alive when the caller gives up.
func TestTheGrantHasItsOwnContextWhateverTheCallerDoes(t *testing.T) {
	cases := []struct {
		name   string
		caller func() (context.Context, context.CancelFunc)
	}{
		{"a caller with no deadline", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }},
		{"a caller with a deadline of 10 seconds, far from over", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 10*time.Second)
		}},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.signIn(2*time.Hour, time.Hour) // due
				ctx, cancel := c.caller()
				defer cancel()
				var left time.Duration
				var hasDeadline bool
				var endedWhenCallerGaveUp error
				spied := refresherFunc(func(grant context.Context, rt string) (*account.TokenSet, error) {
					left, hasDeadline = remaining(grant)
					cancel() // the caller gives up mid-call
					endedWhenCallerGaveUp = grant.Err()
					return e.ref.Refresh(grant, rt)
				})
				g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Refresher: spied, Now: e.f.Clock.Now})
				t.Cleanup(g.Close)
				st, err := ep.call(g, ctx)
				if !hasDeadline || left <= 17*time.Second || left > 20*time.Second {
					t.Errorf("the grant has %v left (a deadline: %t), want refreshCallTimeout (20 s) whatever the caller's deadline", left, hasDeadline)
				}
				if endedWhenCallerGaveUp != nil {
					t.Errorf("the grant's context ended when the caller gave up (%v): the answer would be lost", endedWhenCallerGaveUp)
				}
				if err != nil || st.State != account.StateOK {
					t.Errorf("%s = %s/%q, %v, want ok: the answer is stored although the caller gave up", ep.name, st.State, st.Reason, err)
				}
				if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
					t.Errorf("refresh.enc holds %q after the caller gave up mid-call, want the rotated token rt-2", rt)
				}
			})
		}
	}
}

// A failure that happens while the caller has given up is a real failure: the
// grant was not cancelled by the caller, so it says something about monoes.me
// (the network, the server) and is recorded like any other, with its reason.
func TestAFailureThatHappensAfterTheCallerGaveUpIsRecordedLikeAnyOther(t *testing.T) {
	cases := []struct {
		name string
		fail error
		want account.Reason
	}{
		{"an error of no known type", errors.New("connection reset by peer"), account.ReasonUnreachable},
		{"an unreachable server", transient(account.ReasonUnreachable), account.ReasonUnreachable},
		{"a server error", transient(account.ReasonServerError), account.ReasonServerError},
		{"a call that ran into its deadline", context.DeadlineExceeded, account.ReasonUnreachable},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.signIn(2*time.Hour, time.Hour) // due
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failing := refresherFunc(func(context.Context, string) (*account.TokenSet, error) {
					cancel() // the caller gives up mid-call, and then the call fails
					return nil, c.fail
				})
				g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Refresher: failing, Now: e.f.Clock.Now})
				t.Cleanup(g.Close)
				st, err := ep.call(g, ctx)
				if err != nil || st.State != account.StateGrace || st.Reason != c.want {
					t.Fatalf("%s = %s/%q, %v, want grace/%q and no error: the failure is the call's, not the caller's", ep.name, st.State, st.Reason, err, c.want)
				}
				if sess := e.session(); sess.LastResult != string(c.want) || !sess.LastAttempt.Equal(e.f.Clock.Now()) {
					t.Fatalf("stored session = %s, want the failure %q recorded at %v", describe(sess), c.want, e.f.Clock.Now())
				}
				if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
					t.Fatal("a failed attempt changed the refresh token")
				}
			})
		}
	}
}

// cancelAfterRefreshRead cancels the caller's context once the refresh token has
// been read, as a Ctrl-C does while a slow key store is answering.
type cancelAfterRefreshRead struct {
	*countingStore
	cancel context.CancelFunc
}

func (s cancelAfterRefreshRead) LoadRefresh() (string, error) {
	token, err := s.countingStore.LoadRefresh()
	s.cancel()
	return token, err
}

// The last moment at which giving up means that nothing is sent is the moment
// before the grant: the key store may take seconds, and a caller that gives up
// during them has not asked monoes.me for anything.
func TestACallerThatGivesUpWhileTheKeyStoreIsReadSendsNothing(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // due
			before := describe(e.session())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			spy := &cancelAfterRefreshRead{countingStore: newCountingStore(account.OpenStore(e.dir, e.seal)), cancel: cancel}
			g := e.guardOver(spy, 0)
			st, err := ep.call(g, ctx)
			if !errors.Is(err, context.Canceled) || st.State != account.StateGrace || e.ref.calls.Load() != 0 {
				t.Fatalf("%s = %s/%q, %v with %d network refreshes, want grace, context.Canceled and none", ep.name, st.State, st.Reason, err, e.ref.calls.Load())
			}
			if n := spy.calls("LoadRefresh"); n != 1 {
				t.Fatalf("%d reads of the refresh token, want the one that the caller gave up during", n)
			}
			if after := describe(e.session()); after != before {
				t.Fatalf("a caller that gave up changed the session: %s, was %s", after, before)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatal("a caller that gave up changed the refresh token")
			}
		})
	}
}
