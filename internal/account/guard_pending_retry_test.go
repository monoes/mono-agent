package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// What the guard does at the next attempt after a grant whose answer was lost
// (A24). monoes.me answers a refresh token that it rotated away with the same
// answer for 300 s and takes it for theft after that, so the guard presents the
// token again at once while the marker is younger than 240 s, and never after:
// the token is dropped, this machine signs in again, and no other install of the
// account is touched. The server is the windowServer of guard_refresh_lostanswer_test.go,
// which rotates on every use, repeats its answer inside the window and revokes
// every refresh token of the account when a rotated-away token comes back outside it.

// fate is what one grant meets between this machine and monoes.me.
type fate int

const (
	arrives    fate = iota // monoes.me processes the request and the answer comes back
	lost                   // monoes.me processes the request and the answer never arrives: an outcome that is unknown
	unsent                 // the request never leaves this machine (DNS, dial): settled, monoes.me is not reached
	status429              // monoes.me answers a complete HTTP 429 (a rate limit) and processes nothing: settled
	plain                  // monoes.me processes the request and the client gets an error that is not a *TransientError
	opaque                 // monoes.me processes the request and its answer holds a new refresh token and an access token this build cannot verify
	gateway504             // monoes.me processes the request and a gateway answers HTTP 504 in its place: an outcome that is unknown
)

// flakyNet is the network between a guard and the windowServer. Each grant takes the
// next fate of the script, and arrives when the script is empty.
type flakyNet struct {
	srv *windowServer

	mu     sync.Mutex
	script []fate
	sent   []string // the refresh token of every grant that left a guard, whatever became of it
}

func (n *flakyNet) then(fates ...fate) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.script = append(n.script, fates...)
}

func (n *flakyNet) grants() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sent)
}

func (n *flakyNet) Refresh(ctx context.Context, rt string) (*account.TokenSet, error) {
	n.mu.Lock()
	n.sent = append(n.sent, rt)
	f := arrives
	if len(n.script) > 0 {
		f, n.script = n.script[0], n.script[1:]
	}
	n.mu.Unlock()
	switch f {
	case unsent:
		return nil, &account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("dial tcp: lookup monoes.me: no such host")}
	case status429:
		return nil, &account.TransientError{Reason: account.ReasonServerError, Settled: true, Err: errors.New("the token endpoint answered HTTP 429 Too Many Requests")}
	}
	ts, err := n.srv.Refresh(ctx, rt)
	switch f {
	case lost:
		return nil, lostAnswer(account.ReasonUnreachable)
	case plain:
		return nil, errors.New("read tcp: connection reset by peer")
	case gateway504:
		return nil, &account.TransientError{Reason: account.ReasonServerError, Err: errors.New("the gateway answered HTTP 504 Gateway Timeout")}
	case opaque:
		if err == nil {
			return &account.TokenSet{AccessToken: "opaque-0123456789", RefreshToken: ts.RefreshToken}, nil
		}
	}
	return ts, err
}

// accept makes the server take rt as the current refresh token of one more install.
func (s *windowServer) accept(rt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current[rt] = true
}

// revokeAll is what the server does when a rotated-away token comes back after the window.
func (s *windowServer) revokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked, s.current = true, map[string]bool{}
}

// lostRig is one install of an account whose access token has four minutes left, so
// that a command refreshes it, over a windowServer that also knows a second install.
type lostRig struct {
	e   *env
	srv *windowServer
	net *flakyNet
	t0  time.Time
}

func newLostRig(t *testing.T) *lostRig {
	t.Helper()
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour)
	srv := newWindowServer(e.f, 0, "rt-1")
	return &lostRig{e: e, srv: srv, net: &flakyNet{srv: srv}, t0: e.f.Clock.Now()}
}

// command is one process: a guard of its own over the session, as every CLI call is.
func (r *lostRig) command() (account.Status, error) {
	return r.e.guardWith(r.net).EnsureFresh(context.Background())
}

// loseTheFirstAnswer runs the command whose answer never arrives: monoes.me has
// rotated, the machine holds the dead token and the marker that says so.
func (r *lostRig) loseTheFirstAnswer(t *testing.T) {
	t.Helper()
	r.net.then(lost)
	st, err := r.command()
	if err != nil || st.State != account.StateOK || st.Reason != account.ReasonNone {
		t.Fatalf("the command whose answer is lost = %s/%q, %v, want ok (the token has four minutes left) and no error", st.State, st.Reason, err)
	}
	if !r.srv.isCurrent("rt-rotated-1") || r.srv.isCurrent("rt-1") {
		t.Fatal("the server did not rotate: this test cannot tell a lost answer from a request that never arrived")
	}
	if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" {
		t.Fatalf("refresh.enc holds %q after the lost answer, want the dead token the marker is about", rt)
	}
	if got := r.e.pendingOn(); !got.Equal(r.t0) {
		t.Fatalf("pending_since = %v after the lost answer, want %v", got, r.t0)
	}
}

// refreshFileGone reports whether refresh.enc has been removed.
func (r *lostRig) refreshFileGone() bool {
	_, err := os.Stat(filepath.Join(r.e.dir, "refresh.enc"))
	return errors.Is(err, os.ErrNotExist)
}

// anotherInstall signs a second install of the same account in (its own directory, its
// own refresh token) and returns a command of it.
func (r *lostRig) anotherInstall(t *testing.T) func() (account.Status, error) {
	t.Helper()
	store := account.OpenStore(filepath.Join(t.TempDir(), "other"), account.NewMemorySealer())
	signInInstall(t, r.e.f, store, 56*time.Minute, time.Hour, "rt-B") // four minutes left at t0
	r.srv.accept("rt-B")
	return func() (account.Status, error) {
		g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r.net, Now: r.e.f.Clock.Now})
		t.Cleanup(g.Close)
		return g.EnsureFresh(context.Background())
	}
}

// (a), (b): the answer is lost, and the next attempt comes soon enough that monoes.me
// repeats it. 240 s is the last second of the window, to the second.
func TestALostAnswerIsRetriedInsideTheWindowAndRecovers(t *testing.T) {
	for _, after := range []time.Duration{0, time.Second, time.Minute, 239 * time.Second, 240 * time.Second} {
		t.Run(after.String(), func(t *testing.T) {
			r := newLostRig(t)
			r.loseTheFirstAnswer(t)
			r.e.f.Clock.Advance(after)
			st, err := r.command()
			if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(r.t0.Add(time.Hour)) {
				t.Fatalf("the retry after %v = %s/%q valid until %v, %v, want ok with the answer monoes.me repeated (valid until %v)", after, st.State, st.Reason, st.ValidUntil, err, r.t0.Add(time.Hour))
			}
			if got := r.srv.presented(); !reflect.DeepEqual(got, []string{"rt-1", "rt-1"}) {
				t.Fatalf("monoes.me was presented %v, want the same token twice, inside its window", got)
			}
			if r.srv.isRevoked() {
				t.Fatal("the account was revoked")
			}
			if rt, _ := r.e.store.LoadRefresh(); rt != "rt-rotated-1" {
				t.Fatalf("refresh.enc holds %q, want the rotated token that the repeated answer brought", rt)
			}
			if got := r.e.rawPending(); got != "" {
				t.Fatalf("session.json still carries pending_since %s after the answer was stored", got)
			}
			if sess := r.e.session(); sess.LastResult != "ok" || !sess.LastAttempt.Equal(r.e.f.Clock.Now()) {
				t.Fatalf("stored session = %s, want ok at %v", describe(sess), r.e.f.Clock.Now())
			}
		})
	}
}

// (b): a second after the window the answer can no longer be recovered, and the
// token is not presented: it is dropped, with no call, and this machine alone signs
// in again. The grace applies and then locks with the reason that names the cause.
func TestALostAnswerIsDroppedNotPresentedAfterTheWindow(t *testing.T) {
	for _, after := range []time.Duration{241 * time.Second, 300 * time.Second, 301 * time.Second, time.Hour} {
		t.Run(after.String(), func(t *testing.T) {
			r := newLostRig(t)
			otherInstall := r.anotherInstall(t)
			r.loseTheFirstAnswer(t)
			r.e.f.Clock.Advance(after)
			st, err := r.command()
			if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed {
				t.Fatalf("the attempt after %v = %s/%q, %v, want grace/unconfirmed", after, st.State, st.Reason, err)
			}
			if n := r.net.grants(); n != 1 {
				t.Fatalf("%d grants, want only the one that was lost: the attempt that drops the token sends nothing", n)
			}
			if !r.refreshFileGone() {
				t.Fatal("refresh.enc is still on disk: the dead token would be presented at the next attempt")
			}
			sess := r.e.session()
			if sess.LastResult != "unconfirmed" || !sess.LastAttempt.Equal(r.e.f.Clock.Now()) || sess.AccessToken == "" || !sess.PendingSince.IsZero() || r.e.rawPending() != "" {
				t.Fatalf("stored session = %s with pending %v, want unconfirmed at %v, the access token kept and no marker", describe(sess), sess.PendingSince, r.e.f.Clock.Now())
			}
			if r.srv.isRevoked() {
				t.Fatal("the account was revoked: the dead token was presented")
			}
			// Nothing but a sign-in repairs it, and no other install is touched.
			if st, err := otherInstall(); err != nil || st.State != account.StateOK {
				t.Fatalf("the other install = %s/%q, %v, want ok: one machine pays, not every install", st.State, st.Reason, err)
			}
			if r.srv.isRevoked() {
				t.Fatal("the account was revoked by the other install")
			}
		})
	}
}

// (c): a stamp that lies after the clock means the clock went back. The age cannot be
// trusted, so the token is not presented.
func TestAClockSetBackAfterALostAnswerDropsTheToken(t *testing.T) {
	for _, back := range []time.Duration{time.Nanosecond, time.Second, time.Minute, time.Hour} {
		t.Run(back.String(), func(t *testing.T) {
			r := newLostRig(t)
			r.loseTheFirstAnswer(t)
			r.e.f.Clock.Advance(-back)
			_, err := r.command()
			if err != nil {
				t.Fatalf("the attempt with the clock %v back: %v", back, err)
			}
			if n := r.net.grants(); n != 1 {
				t.Fatalf("%d grants, want only the lost one: a stamp in the future is not an age", n)
			}
			if !r.refreshFileGone() || r.e.session().LastResult != "unconfirmed" || r.e.rawPending() != "" {
				t.Fatalf("stored session = %s, refresh token gone: %t, want unconfirmed, no marker and no refresh token", describe(r.e.session()), r.refreshFileGone())
			}
			if r.srv.isRevoked() {
				t.Fatal("the account was revoked")
			}
			// Once the clock is right again the verdict names the cause.
			r.e.f.Clock.Set(r.t0.Add(10 * time.Minute))
			if st := r.e.newGuard(0).Status(); st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed {
				t.Fatalf("Status with the clock repaired = %s/%q, want grace/unconfirmed", st.State, st.Reason)
			}
		})
	}
}

// (d): a failure that is settled leaves the token alone: nothing was consumed, so the
// marker is taken back and the token is presented later, however much later, with no
// drop. The window does not apply to a token that was not rotated.
func TestASettledFailureLeavesTheTokenToBePresentedLaterWheneverThatIs(t *testing.T) {
	for _, c := range []struct {
		name string
		f    fate
		last string
	}{{"a request that never left", unsent, "unreachable"}, {"an HTTP 429", status429, "server_error"}} {
		t.Run(c.name, func(t *testing.T) {
			r := newLostRig(t)
			r.net.then(c.f)
			st, err := r.command()
			if err != nil || st.State != account.StateOK {
				t.Fatalf("the failed command = %s/%q, %v, want ok: the token has four minutes left", st.State, st.Reason, err)
			}
			if sess := r.e.session(); sess.LastResult != c.last || r.e.rawPending() != "" {
				t.Fatalf("stored session = %s, pending %q, want %q and no marker", describe(sess), r.e.rawPending(), c.last)
			}
			if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatalf("refresh.enc holds %q, want the token that was never consumed", rt)
			}
			r.e.f.Clock.Advance(10 * time.Minute) // far beyond any window
			st, err = r.command()
			if err != nil || st.State != account.StateOK {
				t.Fatalf("the command ten minutes later = %s/%q, %v, want ok: the token was presented, not dropped", st.State, st.Reason, err)
			}
			if got := r.srv.presented(); !reflect.DeepEqual(got, []string{"rt-1"}) {
				t.Fatalf("monoes.me was presented %v, want the old token once, at the second attempt", got)
			}
			if r.srv.isRevoked() {
				t.Fatal("the account was revoked")
			}
		})
	}
}

// A 5xx is never a known outcome: a gateway that gives up (a 502, 504 or 524), or a 500
// raised after the commit, can come after monoes.me really rotated the token. The
// Refresher reports it unsettled, and the guard treats it as a lost answer: the marker
// stays, the retry inside the window gets monoes.me's answer again, and after the window
// the token is dropped, never presented. (The guard trusts the flag as reported: a
// transport that called such an answer settled would have the marker taken back.)
func TestAGatewayErrorAfterTheRotationIsAnUnknownOutcome(t *testing.T) {
	gatewayError := func(t *testing.T) *lostRig {
		t.Helper()
		r := newLostRig(t)
		r.net.then(gateway504)
		if st, err := r.command(); err != nil || st.State != account.StateOK {
			t.Fatalf("the command that met the gateway = %s/%q, %v, want ok: the token has four minutes left", st.State, st.Reason, err)
		}
		if !r.srv.isCurrent("rt-rotated-1") {
			t.Fatal("monoes.me did not rotate: this test cannot tell a gateway error after the rotation from one before it")
		}
		if sess := r.e.session(); !sess.PendingSince.Equal(r.t0) || sess.LastResult != "server_error" {
			t.Fatalf("stored session = %s, want the marker of the send kept and server_error recorded", describe(sess))
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" {
			t.Fatalf("refresh.enc holds %q, want the token the marker is about", rt)
		}
		return r
	}
	t.Run("the retry inside the window recovers", func(t *testing.T) {
		r := gatewayError(t)
		r.e.f.Clock.Advance(time.Minute)
		if st, err := r.command(); err != nil || st.State != account.StateOK || r.srv.isRevoked() {
			t.Fatalf("the retry = %s/%q, %v (revoked %t), want ok", st.State, st.Reason, err, r.srv.isRevoked())
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-rotated-1" || r.e.rawPending() != "" {
			t.Fatalf("refresh.enc holds %q with pending %q, want the rotated token that the repeated answer brought and no marker", rt, r.e.rawPending())
		}
	})
	t.Run("after the window the token is dropped, not presented", func(t *testing.T) {
		r := gatewayError(t)
		r.e.f.Clock.Advance(10 * time.Minute)
		st, err := r.command()
		if err != nil || st.Reason != account.ReasonUnconfirmed || r.net.grants() != 1 || !r.refreshFileGone() || r.srv.isRevoked() {
			t.Fatalf("the command ten minutes later = %s/%q, %v with %d grants (token gone %t, revoked %t), want grace/unconfirmed and the dead token never presented", st.State, st.Reason, err, r.net.grants(), r.refreshFileGone(), r.srv.isRevoked())
		}
	})
}

// (e): a failure that is not a *TransientError at all is an outcome that is unknown:
// the request may have been processed, so it is retried inside the window and dropped
// after it, like a lost answer.
func TestAnErrorOfNoKnownTypeIsRetriedInsideTheWindowAndDroppedAfterIt(t *testing.T) {
	t.Run("inside the window", func(t *testing.T) {
		r := newLostRig(t)
		r.net.then(plain)
		if _, err := r.command(); err != nil {
			t.Fatal(err)
		}
		if got := r.e.pendingOn(); !got.Equal(r.t0) {
			t.Fatalf("pending_since = %v, want %v: an error of no known type keeps the marker", got, r.t0)
		}
		r.e.f.Clock.Advance(time.Minute)
		if st, err := r.command(); err != nil || st.State != account.StateOK || r.srv.isRevoked() || r.e.rawPending() != "" {
			t.Fatalf("the retry = %s/%q, %v (revoked %t, pending %q), want ok and no marker", st.State, st.Reason, err, r.srv.isRevoked(), r.e.rawPending())
		}
	})
	t.Run("after the window", func(t *testing.T) {
		r := newLostRig(t)
		r.net.then(plain)
		if _, err := r.command(); err != nil {
			t.Fatal(err)
		}
		r.e.f.Clock.Advance(241 * time.Second)
		if st, err := r.command(); err != nil || st.Reason != account.ReasonUnconfirmed || r.net.grants() != 1 || !r.refreshFileGone() || r.srv.isRevoked() {
			t.Fatalf("the attempt after the window = %s/%q, %v with %d grants (token gone %t, revoked %t), want grace/unconfirmed, one grant and the token dropped", st.State, st.Reason, err, r.net.grants(), r.refreshFileGone(), r.srv.isRevoked())
		}
	})
}

// (g): a process that is killed while its grant is in flight is a lost answer: monoes.me
// has rotated, nothing came back, and what is on disk is all that the next process has.
// The model: the server rotates, the call is released with an outcome that is unknown,
// and a new guard starts over the same store. (A guard whose call is blocked is not
// abandoned: it keeps the session lock until its call ends.)
func TestAProcessKilledMidCallLeavesTheMarkerForTheNextOne(t *testing.T) {
	for _, c := range []struct {
		name     string
		after    time.Duration
		recovers bool
	}{{"the next process comes within the window", time.Minute, true}, {"the next process comes after it", 241 * time.Second, false}} {
		t.Run(c.name, func(t *testing.T) {
			r := newLostRig(t)
			rotated, release := make(chan struct{}), make(chan struct{})
			dying := refresherFunc(func(ctx context.Context, rt string) (*account.TokenSet, error) {
				if _, err := r.srv.Refresh(ctx, rt); err != nil { // monoes.me rotates
					t.Errorf("the server refused the first grant: %v", err)
				}
				close(rotated)
				<-release // the process dies here: the answer never reaches the code that would store it
				return nil, lostAnswer(account.ReasonUnreachable)
			})
			g := r.e.guardWith(dying)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = g.EnsureFresh(context.Background())
			}()
			select {
			case <-rotated:
			case <-time.After(3 * time.Second):
				t.Fatal("the grant never went out")
			}
			if got := r.e.pendingOn(); !got.Equal(r.t0) {
				t.Fatalf("pending_since = %v while the grant is in flight, want %v: the marker is the only thing a process that dies leaves behind", got, r.t0)
			}
			close(release)
			<-done

			r.e.f.Clock.Advance(c.after)
			st, err := r.command() // a new guard over the same store: another process
			if c.recovers {
				if err != nil || st.State != account.StateOK || r.srv.isRevoked() || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1", "rt-1"}) {
					t.Fatalf("the next process = %s/%q, %v (revoked %t, presented %v), want ok after presenting the token again inside the window", st.State, st.Reason, err, r.srv.isRevoked(), r.srv.presented())
				}
				return
			}
			if err != nil || st.Reason != account.ReasonUnconfirmed || r.srv.isRevoked() || !r.refreshFileGone() || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1"}) {
				t.Fatalf("the next process = %s/%q, %v (revoked %t, token gone %t, presented %v), want grace/unconfirmed and the dead token never presented again", st.State, st.Reason, err, r.srv.isRevoked(), r.refreshFileGone(), r.srv.presented())
			}
		})
	}
}
