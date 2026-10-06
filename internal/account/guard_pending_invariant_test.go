package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// Two invariants that keep a refresh token in doubt from ever being presented (A24), checked
// over every kind of pass, on a clock that reads before the session's last attempt (set back)
// and on one after it:
//
//   - no write of the guard moves LastAttempt back while the session it leaves has a marker or
//     is unconfirmed: a clock that reads before it is the evidence that the clock went back, and
//     the age of a marker cannot be told then (pendingExpired);
//   - unconfirmed is left only by a sign-in (NewSession): it is what keeps a token that a drop
//     gave up from being presented when that token is still, or again, on disk.

// doubt is a session with a refresh token in doubt, built by what the guard did, and the last
// attempt it records.
type doubt struct {
	name  string
	build func(t *testing.T) (r *lostRig, last time.Time)
	// unconfirmed: the session is unconfirmed, so no grant may go out whatever the clock says
	unconfirmed bool
}

var doubts = []doubt{
	{"a marker after three retries", func(t *testing.T) (*lostRig, time.Time) {
		r := retriedThreeTimes(t)
		return r, r.t0.Add(210 * time.Second)
	}, false},
	{"a marker and unconfirmed, the token stuck on disk", func(t *testing.T) (*lostRig, time.Time) {
		r := newLostRig(t)
		r.loseTheFirstAnswer(t)
		r.e.f.Clock.Advance(241 * time.Second)
		_, _ = r.passWith(&stuckRefreshFile{Store: account.OpenStore(r.e.dir, r.e.seal), stuck: true})
		if sess := r.e.session(); sess.LastResult != "unconfirmed" || sess.PendingSince.IsZero() {
			t.Fatalf("setup: %s, want unconfirmed with the marker", describe(sess))
		}
		return r, r.t0.Add(241 * time.Second)
	}, true},
	{"unconfirmed, the token gone", func(t *testing.T) (*lostRig, time.Time) {
		r := newLostRig(t)
		r.loseTheFirstAnswer(t)
		r.e.f.Clock.Advance(241 * time.Second)
		_, _ = r.command()
		if sess := r.e.session(); sess.LastResult != "unconfirmed" || !r.refreshFileGone() {
			t.Fatalf("setup: %s, want unconfirmed and no refresh token", describe(sess))
		}
		return r, r.t0.Add(241 * time.Second)
	}, true},
	{"unconfirmed, the token back on disk", func(t *testing.T) (*lostRig, time.Time) {
		r := newLostRig(t)
		r.loseTheFirstAnswer(t)
		r.e.f.Clock.Advance(241 * time.Second)
		_, _ = r.command()
		if err := r.e.store.SaveRefresh("rt-1"); err != nil {
			t.Fatal(err)
		}
		return r, r.t0.Add(241 * time.Second)
	}, true},
	{"unconfirmed, a healthy access token", func(t *testing.T) (*lostRig, time.Time) {
		e, srv, net := strayMarker(t, 241*time.Second) // the access token has 50 minutes left
		r := &lostRig{e: e, srv: srv, net: net, t0: e.f.Clock.Now()}
		_, _ = r.command()
		if sess := r.e.session(); sess.LastResult != "unconfirmed" || sess.AccessToken == "" {
			t.Fatalf("setup: %s, want unconfirmed with the access token kept", describe(sess))
		}
		return r, r.t0
	}, true},
}

// passKind is one pass of another process: the store it sees and what monoes.me does.
var passKinds = []struct {
	name  string
	store func(r *lostRig) account.Store
	fate  []fate
}{
	{"the key store does not answer", func(r *lostRig) account.Store {
		return &keyStoreDown{Store: account.OpenStore(r.e.dir, r.e.seal), down: true}
	}, nil},
	{"refresh.enc can be neither replaced nor removed", func(r *lostRig) account.Store {
		return &stuckRefreshFile{Store: account.OpenStore(r.e.dir, r.e.seal), stuck: true}
	}, nil},
	{"monoes.me answers", func(r *lostRig) account.Store { return account.OpenStore(r.e.dir, r.e.seal) }, []fate{arrives}},
	{"the answer is lost", func(r *lostRig) account.Store { return account.OpenStore(r.e.dir, r.e.seal) }, []fate{lost}},
	{"the request never leaves", func(r *lostRig) account.Store { return account.OpenStore(r.e.dir, r.e.seal) }, []fate{unsent}},
	{"an error of no known type", func(r *lostRig) account.Store { return account.OpenStore(r.e.dir, r.e.seal) }, []fate{plain}},
}

func TestNoPassMovesTheLastAttemptBackOrLeavesUnconfirmedButASignIn(t *testing.T) {
	for _, d := range doubts {
		for _, p := range passKinds {
			for _, c := range []struct {
				name string
				at   time.Duration // from the last attempt
			}{{"the clock set back before the last attempt", -time.Minute}, {"the clock after it", 30 * time.Second}} {
				t.Run(d.name+"/"+p.name+"/"+c.name, func(t *testing.T) {
					r, last := d.build(t)
					grants := r.net.grants()
					r.e.f.Clock.Set(last.Add(c.at))
					r.net.then(p.fate...)
					_, _ = r.passWith(p.store(r))
					sess := r.e.session()
					if (!sess.PendingSince.IsZero() || sess.LastResult == "unconfirmed") && sess.LastAttempt.Before(last) {
						t.Errorf("stored session = %s: the last attempt went back from %v while the session is in doubt", describe(sess), last)
					}
					if d.unconfirmed && sess.LastResult != "unconfirmed" {
						t.Errorf("stored session = %s: unconfirmed was left without a sign-in", describe(sess))
					}
					if (d.unconfirmed || c.at < 0) && r.net.grants() != grants {
						t.Errorf("%d grants, want the %d of before: a token in doubt was presented", r.net.grants(), grants)
					}
					if r.srv.isRevoked() {
						t.Error("the account was revoked")
					}
				})
			}
		}
	}
}

// A sign-in is what leaves unconfirmed: a new session from a token monoes.me issued, saved with
// its refresh token, replaces it whatever the dropped session held.
func TestOnlyASignInLeavesUnconfirmed(t *testing.T) {
	for _, d := range doubts {
		if !d.unconfirmed {
			continue
		}
		t.Run(d.name, func(t *testing.T) {
			r, _ := d.build(t)
			login := r.e.f.Token(accounttest.TokenOptions{Lifetime: time.Hour})
			sess, err := account.NewSession(account.HostURL, login, &account.User{ID: "user-1"}, r.e.f.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.e.store.SaveRefresh("rt-login"); err != nil {
				t.Fatal(err)
			}
			r.e.save(sess)
			r.srv.accept("rt-login")
			r.e.f.Clock.Advance(56 * time.Minute) // due by the CLI's margin
			st, err := r.command()
			if err != nil || st.State != account.StateOK || r.e.session().LastResult != "ok" || r.e.rawPending() != "" {
				t.Fatalf("after the sign-in = %s/%q, %v, stored %s, want ok", st.State, st.Reason, err, describe(r.e.session()))
			}
			if got := count(r.srv.presented(), "rt-login"); got != 1 || r.srv.isRevoked() {
				t.Fatalf("monoes.me was presented the new token %d times (revoked %t), want once", got, r.srv.isRevoked())
			}
		})
	}
}

// The rule is for a session in doubt only. With nothing in doubt a failure recorded on a clock
// that went back is the attempt the negative cache counts from, as it always was.
func TestWithNothingInDoubtTheLastAttemptFollowsTheClockBack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // in grace: due
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
	if _, err := e.g.EnsureFresh(ctx); err != nil {
		t.Fatal(err)
	}
	back := e.session().LastAttempt.Add(-10 * time.Minute)
	e.f.Clock.Set(back)
	if _, err := e.newGuard(0).EnsureFresh(ctx); err != nil { // due: a stored time after the clock holds nothing off
		t.Fatal(err)
	}
	if sess := e.session(); !sess.LastAttempt.Equal(back) || e.rawPending() != "" || e.ref.calls.Load() != 2 {
		t.Fatalf("stored session = %s after %d calls, want the second attempt recorded on the clock that went back, %v, and no marker", describe(sess), e.ref.calls.Load(), back)
	}
	e.f.Clock.Advance(10 * time.Second)
	if _, err := e.newGuard(0).EnsureFresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.ref.calls.Load(); n != 2 {
		t.Fatalf("%d calls, want 2: the negative cache counts from the attempt on the clock that went back", n)
	}
}
