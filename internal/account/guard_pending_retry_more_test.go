package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// The second half of guard_pending_retry_test.go: retries that are lost again, the answer to a
// retry, an answer that could not be stored, stray markers and the dormant package. It uses the
// rig, the fates and the helpers of the first half.

// The window runs from the FIRST send that monoes.me may have answered. A retry that is
// lost again keeps the stamp it found, so the retries stay inside the window together and
// the token is dropped 240 s after the first send, not 240 s after the last retry: with a
// stamp that moved, a token presented 450 s after the rotation would end every install.
func TestRetriesThatAreLostAgainKeepTheFirstStampAndTheWindowRunsFromIt(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	first := r.e.rawPending()
	if first == "" {
		t.Fatal("no stamp after the first send")
	}
	for _, step := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second} { // +30 s, +90 s, +210 s
		r.net.then(lost)
		r.e.f.Clock.Advance(step)
		if _, err := r.command(); err != nil {
			t.Fatalf("the retry at +%v: %v", r.e.f.Clock.Now().Sub(r.t0), err)
		}
		if got := r.e.rawPending(); got != first {
			t.Fatalf("pending_since = %q at +%v, want the stamp of the first send, %q, byte for byte", got, r.e.f.Clock.Now().Sub(r.t0), first)
		}
	}
	if n := r.net.grants(); n != 4 {
		t.Fatalf("%d grants, want the first one and three retries", n)
	}
	// 31 s later it is 241 s after the first send: the token goes, though the last retry was 31 s ago.
	r.e.f.Clock.Advance(31 * time.Second)
	st, err := r.command()
	if err != nil || st.Reason != account.ReasonUnconfirmed || r.net.grants() != 4 || !r.refreshFileGone() || r.srv.isRevoked() {
		t.Fatalf("the attempt 241 s after the first send = %s/%q, %v with %d grants (token gone %t, revoked %t), want grace/unconfirmed, no new grant and the token dropped",
			st.State, st.Reason, err, r.net.grants(), r.refreshFileGone(), r.srv.isRevoked())
	}
}

// The fourth retry of the loop's schedule (30, 60, 120, 240 s) falls at about +450 s after the
// first send, and what it judges is the first stamp, however many retries came before it.
func TestARetryAtAboutFourAndAHalfMinutesDropsTheTokenWhateverTheRetriesBefore(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	for _, step := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second} {
		r.net.then(lost)
		r.e.f.Clock.Advance(step)
		_, _ = r.command()
	}
	r.e.f.Clock.Advance(240 * time.Second) // +450 s
	st, err := r.command()
	if err != nil || st.Reason != account.ReasonUnconfirmed || r.net.grants() != 4 || !r.refreshFileGone() || r.srv.isRevoked() {
		t.Fatalf("the fourth retry = %s/%q, %v with %d grants (token gone %t, revoked %t), want the token dropped instead of presented 450 s after the rotation", st.State, st.Reason, err, r.net.grants(), r.refreshFileGone(), r.srv.isRevoked())
	}
}

// A retry that fails settled says nothing about the send before it, which may have been
// answered: the stamp stays, and the token is dropped when the window is over instead of
// being presented, at any time, as a token that nothing is known to have consumed.
func TestASettledRetryDoesNotEraseTheStampOfTheSendThatMayHaveRotated(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	first := r.e.rawPending()
	r.net.then(unsent)
	r.e.f.Clock.Advance(30 * time.Second)
	if _, err := r.command(); err != nil {
		t.Fatal(err)
	}
	if got := r.e.rawPending(); got != first {
		t.Fatalf("pending_since = %q after a settled retry, want the stamp of the first send, %q: the first send may have rotated the token", got, first)
	}
	if sess := r.e.session(); sess.LastResult != "unreachable" {
		t.Fatalf("the retry was not recorded: %s", describe(sess))
	}
	r.e.f.Clock.Advance(370 * time.Second) // +400 s: long past the window of the first send
	st, err := r.command()
	if err != nil || st.Reason != account.ReasonUnconfirmed || r.srv.isRevoked() || !r.refreshFileGone() || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1"}) {
		t.Fatalf("the attempt at +400 s = %s/%q, %v (revoked %t, token gone %t, presented %v), want grace/unconfirmed and a token that was never presented again", st.State, st.Reason, err, r.srv.isRevoked(), r.refreshFileGone(), r.srv.presented())
	}
}

// (h): the retry's answer is whatever monoes.me says, and each ends the doubt.
func TestAnAnswerToTheRetryEndsTheDoubtWhateverItSays(t *testing.T) {
	t.Run("monoes.me refuses the token", func(t *testing.T) {
		r := newLostRig(t)
		r.loseTheFirstAnswer(t)
		r.srv.revokeAll() // the account was ended meanwhile, by another install or by its owner
		r.e.f.Clock.Advance(time.Minute)
		st, err := r.command()
		if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("the retry = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
		}
		sess := r.e.session()
		if sess.State != "refused" || r.e.rawPending() != "" || !sess.HW.Equal(r.e.f.Clock.Now()) || !r.refreshFileGone() {
			t.Fatalf("stored session = %s, pending %q, refresh token gone %t, want refused, no marker, the mark raised to now and the refused token deleted", describe(sess), r.e.rawPending(), r.refreshFileGone())
		}
	})
	t.Run("the answer holds an access token this build cannot verify", func(t *testing.T) {
		r := newLostRig(t)
		r.loseTheFirstAnswer(t)
		r.net.then(opaque)
		r.e.f.Clock.Advance(time.Minute)
		if _, err := r.command(); err != nil {
			t.Fatal(err)
		}
		if got := r.e.rawPending(); got != "" {
			t.Fatalf("pending_since = %q: the answer arrived and its rotated refresh token is saved", got)
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-rotated-1" {
			t.Fatalf("refresh.enc holds %q, want the rotated token", rt)
		}
		if sess := r.e.session(); sess.LastResult != "server_error" {
			t.Fatalf("stored session = %s, want the attempt recorded as server_error", describe(sess))
		}
	})
}

// RULING A24(d), end to end: the answer arrived and its rotated refresh token could not
// be saved. It is a lost answer: inside the window the retry gets the same answer and, with
// a key store that works again, stores it; after the window the token is dropped.
func TestAnAnswerThatCouldNotBeStoredIsRecoveredInsideTheWindowOrDroppedAfterIt(t *testing.T) {
	failingFirst := func(r *lostRig) *account.Guard {
		fs := &failingStore{Store: account.OpenStore(r.e.dir, r.e.seal), failSaveRefresh: true}
		return r.e.guardWith(r.net, fs)
	}
	t.Run("a retry inside the window stores the answer", func(t *testing.T) {
		r := newLostRig(t)
		st, err := failingFirst(r).EnsureFresh(context.Background())
		if !errors.Is(err, account.ErrKeyringUnavailable) || st.State != account.StateOK {
			t.Fatalf("the command = %s/%q, %v, want ok and the write error", st.State, st.Reason, err)
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" || !r.e.pendingOn().Equal(r.t0) {
			t.Fatalf("refresh.enc holds %q with pending %v, want the old token and the marker", rt, r.e.pendingOn())
		}
		r.e.f.Clock.Advance(time.Minute)
		if st, err := r.command(); err != nil || st.State != account.StateOK || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1", "rt-1"}) || r.srv.isRevoked() {
			t.Fatalf("the retry = %s/%q, %v (presented %v, revoked %t), want ok after the same token was presented again inside the window", st.State, st.Reason, err, r.srv.presented(), r.srv.isRevoked())
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-rotated-1" || r.e.rawPending() != "" {
			t.Fatalf("refresh.enc holds %q with pending %q, want the rotated token and no marker", rt, r.e.rawPending())
		}
	})
	t.Run("a retry that cannot store it either keeps the first stamp", func(t *testing.T) {
		r := newLostRig(t)
		g := failingFirst(r)
		if _, err := g.EnsureFresh(context.Background()); !errors.Is(err, account.ErrKeyringUnavailable) {
			t.Fatalf("the first command: %v", err)
		}
		first := r.e.rawPending()
		r.e.f.Clock.Advance(time.Minute)
		if _, err := g.EnsureFresh(context.Background()); !errors.Is(err, account.ErrKeyringUnavailable) {
			t.Fatalf("the retry that cannot store the answer: %v", err)
		}
		if got := r.e.rawPending(); got != first {
			t.Fatalf("pending_since = %q, want the first stamp %q", got, first)
		}
		r.e.f.Clock.Advance(181 * time.Second) // +241 s from the first send
		st, err := g.EnsureFresh(context.Background())
		if err != nil || st.Reason != account.ReasonUnconfirmed || !r.refreshFileGone() || r.srv.isRevoked() || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1", "rt-1"}) {
			t.Fatalf("the attempt after the window = %s/%q, %v (token gone %t, revoked %t, presented %v), want grace/unconfirmed and no third presentation", st.State, st.Reason, err, r.refreshFileGone(), r.srv.isRevoked(), r.srv.presented())
		}
	})
	t.Run("after the window the token is dropped without being presented", func(t *testing.T) {
		r := newLostRig(t)
		if _, err := failingFirst(r).EnsureFresh(context.Background()); !errors.Is(err, account.ErrKeyringUnavailable) {
			t.Fatalf("the command: %v", err)
		}
		r.e.f.Clock.Advance(241 * time.Second)
		st, err := r.command()
		if err != nil || st.Reason != account.ReasonUnconfirmed || !r.refreshFileGone() || r.srv.isRevoked() || !reflect.DeepEqual(r.srv.presented(), []string{"rt-1"}) {
			t.Fatalf("the attempt after the window = %s/%q, %v (token gone %t, revoked %t, presented %v), want grace/unconfirmed and the dead token never presented again", st.State, st.Reason, err, r.refreshFileGone(), r.srv.isRevoked(), r.srv.presented())
		}
	})
}

// strayMarker is what another process leaves while this one is not looking: a marker on a
// session whose token is healthy, and a monoes.me that has rotated at the time it names.
func strayMarker(t *testing.T, age time.Duration) (*env, *windowServer, *flakyNet) {
	t.Helper()
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // 50 minutes left: nothing is due by the margin rule
	srv := newWindowServer(e.f, 0, "rt-1")
	e.f.Clock.Advance(-age)
	if _, err := srv.Refresh(context.Background(), "rt-1"); err != nil { // the other process's grant: monoes.me rotates, the answer is lost
		t.Fatal(err)
	}
	e.leavePending(e.f.Clock.Now())
	e.f.Clock.Advance(age)
	return e, srv, &flakyNet{srv: srv}
}

// (1b): a pending session is due at once, in both modes, whatever the margin and the
// negative cache say: only the age decides what happens, and it decides in the lock.
func TestAPendingSessionIsRetriedAtOnceWhateverTheMarginAndTheNegativeCacheSay(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name+"/a healthy token", func(t *testing.T) {
			e, srv, net := strayMarker(t, 30*time.Second)
			st, err := ep.call(e.guardWith(net), context.Background())
			if err != nil || st.State != account.StateOK || net.grants() != 1 || e.rawPending() != "" || srv.isRevoked() {
				t.Fatalf("%s = %s/%q, %v with %d grants (pending %q, revoked %t), want the marker retried at once although 50 minutes are left", ep.name, st.State, st.Reason, err, net.grants(), e.rawPending(), srv.isRevoked())
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-rotated-1" {
				t.Fatalf("refresh.enc holds %q, want the rotated token", rt)
			}
		})
		t.Run(ep.name+"/an expired token tried 10 seconds ago", func(t *testing.T) {
			e := newEnv(t)
			sess := e.signIn(2*time.Hour, time.Hour) // in grace
			srv := newWindowServer(e.f, 0, "rt-1")
			if _, err := srv.Refresh(context.Background(), "rt-1"); err != nil {
				t.Fatal(err)
			}
			sess.LastAttempt = e.f.Clock.Now().Add(-10 * time.Second) // the negative cache holds a plain failure off for a minute
			sess.PendingSince = e.f.Clock.Now().Add(-10 * time.Second)
			e.save(sess)
			net := &flakyNet{srv: srv}
			st, err := ep.call(e.guardWith(net), context.Background())
			if err != nil || st.State != account.StateOK || net.grants() != 1 {
				t.Fatalf("%s = %s/%q, %v with %d grants, want the marker retried although the last attempt was 10 seconds ago", ep.name, st.State, st.Reason, err, net.grants())
			}
		})
	}
}

// A marker that nothing has retried by the end of the window is dropped by whoever comes
// next, however healthy the access token still is: the refresh token is what cannot be
// trusted, and the access token has an hour to run.
func TestAStrayMarkerOlderThanTheWindowIsDroppedEvenOnAHealthyToken(t *testing.T) {
	e, srv, net := strayMarker(t, 241*time.Second)
	st, err := e.guardWith(net).EnsureFresh(context.Background())
	if err != nil || st.State != account.StateOK || net.grants() != 0 || srv.isRevoked() {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d grants (revoked %t), want ok on the access token that is left and no grant", st.State, st.Reason, err, net.grants(), srv.isRevoked())
	}
	if _, statErr := os.Stat(filepath.Join(e.dir, "refresh.enc")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refresh.enc survived (stat err %v)", statErr)
	}
	if sess := e.session(); sess.LastResult != "unconfirmed" || e.rawPending() != "" {
		t.Fatalf("stored session = %s, want unconfirmed and no marker", describe(sess))
	}
}

// (1d): once the token is dropped nothing is left to present, and no later pass says
// anything else about it: the grace keeps saying why it is not renewed. Recording the key
// store problem that a missing refresh token usually means would overwrite the reason, and
// the end of the grace would then say expired instead of unconfirmed.
func TestAfterTheDropAPassThatFindsNoRefreshTokenRecordsNothing(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			r := newLostRig(t)
			r.loseTheFirstAnswer(t)
			r.e.f.Clock.Advance(241 * time.Second)
			if _, err := ep.call(r.e.guardWith(r.net), context.Background()); err != nil { // the drop
				t.Fatal(err)
			}
			dropped := describe(r.e.session())
			for i := 0; i < 3; i++ {
				r.e.f.Clock.Advance(2 * time.Minute) // past the negative cache each time
				st, err := ep.call(r.e.guardWith(r.net), context.Background())
				if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed {
					t.Fatalf("pass %d = %s/%q, %v, want grace/unconfirmed to go on saying why", i+1, st.State, st.Reason, err)
				}
			}
			after := r.e.session()
			if got := describe(after); got != dropped {
				t.Fatalf("the passes changed the stored session: %s, was %s", got, dropped)
			}
			if r.net.grants() != 1 {
				t.Fatalf("%d grants, want only the lost one", r.net.grants())
			}
			// The grace ends at iat+24h: the verdict names the cause, and a sign-in replaces the session.
			r.e.f.Clock.Set(r.t0.Add(-56*time.Minute + 24*time.Hour))
			if st := r.e.newGuard(0).Status(); st.State != account.StateLocked || st.Reason != account.ReasonUnconfirmed {
				t.Fatalf("Status once the grace is over = %s/%q, want locked/unconfirmed", st.State, st.Reason)
			}
			login := r.e.f.Token(accounttest.TokenOptions{Lifetime: time.Hour})
			fresh, err := account.NewSession(account.HostURL, login, &account.User{ID: "user-1"}, r.e.f.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			r.e.save(fresh)
			if err := r.e.store.SaveRefresh("rt-new"); err != nil {
				t.Fatal(err)
			}
			if st := r.e.newGuard(0).Status(); st.State != account.StateOK || r.e.session().LastResult != "ok" || r.e.rawPending() != "" {
				t.Fatalf("Status after a new sign-in = %s/%q, want ok: a login replaces the dropped session", st.State, st.Reason)
			}
		})
	}
}

// While the package is dormant EnsureFresh contacts no one and changes nothing, a marker
// included: the drop and the retry wait for the date, or for an explicit Refresh, which
// runs while dormant as every other refresh does.
func TestADormantEnsureFreshNeitherRetriesNorDropsButRefreshDoes(t *testing.T) {
	t.Run("a marker inside the window", func(t *testing.T) {
		e, srv, net := strayMarker(t, time.Minute)
		account.SetEnforceFromForTest(t, time.Time{})
		before := describe(e.session())
		if st, err := e.guardWith(net).EnsureFresh(context.Background()); err != nil || net.grants() != 0 || describe(e.session()) != before {
			t.Fatalf("EnsureFresh while dormant = %s, %v with %d grants (session changed: %t), want nothing", st.State, err, net.grants(), describe(e.session()) != before)
		}
		if st, err := e.guardWith(net).Refresh(context.Background()); err != nil || st.State != account.StateOK || net.grants() != 1 || srv.isRevoked() || e.rawPending() != "" {
			t.Fatalf("Refresh while dormant = %s/%q, %v with %d grants (revoked %t, pending %q), want the marker retried", st.State, st.Reason, err, net.grants(), srv.isRevoked(), e.rawPending())
		}
	})
	t.Run("a marker past the window", func(t *testing.T) {
		e, _, net := strayMarker(t, 241*time.Second)
		account.SetEnforceFromForTest(t, time.Time{})
		before := describe(e.session())
		if _, err := e.guardWith(net).EnsureFresh(context.Background()); err != nil || describe(e.session()) != before {
			t.Fatalf("EnsureFresh while dormant changed the session: %v, %s", err, describe(e.session()))
		}
		if _, err := e.guardWith(net).Refresh(context.Background()); err != nil || net.grants() != 0 || e.session().LastResult != "unconfirmed" {
			t.Fatalf("Refresh while dormant: %v with %d grants, session %s, want the token dropped with no grant", err, net.grants(), describe(e.session()))
		}
	})
}

// monoes.me rotates the refresh token on every use, so an answer that names none cannot mean
// that the one presented still holds: it may be rotated, and its successor is lost. That is an
// outcome that is unknown, whatever access token comes with it: the marker stays, the retry
// inside the window gets monoes.me's whole answer, and after the window the token is dropped
// instead of being presented when the access token that came with the answer runs out.
func TestAnAnswerWithoutARefreshTokenIsAnUnknownOutcome(t *testing.T) {
	stripped := func(t *testing.T) *lostRig {
		t.Helper()
		r := newLostRig(t)
		r.net.then(noRefreshToken)
		if _, err := r.command(); err != nil {
			t.Fatal(err)
		}
		if !r.srv.isCurrent("rt-rotated-1") {
			t.Fatal("monoes.me did not rotate: the test cannot tell")
		}
		if sess := r.e.session(); !sess.PendingSince.Equal(r.t0) || sess.LastResult != "server_error" {
			t.Errorf("stored session = %s, want the marker kept and server_error recorded", describe(sess))
		}
		return r
	}
	t.Run("the retry inside the window recovers", func(t *testing.T) {
		r := stripped(t)
		r.e.f.Clock.Advance(time.Minute)
		if st, err := r.command(); err != nil || st.State != account.StateOK || r.srv.isRevoked() {
			t.Fatalf("the retry = %s/%q, %v (revoked %t), want ok", st.State, st.Reason, err, r.srv.isRevoked())
		}
		if rt, _ := r.e.store.LoadRefresh(); rt != "rt-rotated-1" || r.e.rawPending() != "" {
			t.Fatalf("refresh.enc holds %q with pending %q, want the rotated token and no marker", rt, r.e.rawPending())
		}
	})
	t.Run("after the window the token is never presented", func(t *testing.T) {
		r := stripped(t)
		r.e.f.Clock.Advance(56 * time.Minute) // the access token of the answer has run down: due by the CLI's margin
		if _, err := r.command(); err != nil {
			t.Fatal(err)
		}
		if got := count(r.srv.presented(), "rt-1"); got != 1 || r.srv.isRevoked() {
			t.Fatalf("monoes.me was presented rt-1 %d times (revoked %t), want once: the token whose successor was lost is never presented again", got, r.srv.isRevoked())
		}
	})
}
