package account_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// stuckRefreshFile is a refresh.enc that can be read but neither replaced nor removed
// while stuck is set (a macOS user-immutable flag, a Windows process that holds it open
// without FILE_SHARE_DELETE), over a session.json that stays writable.
type stuckRefreshFile struct {
	account.Store
	mu    sync.Mutex
	stuck bool
}

func (s *stuckRefreshFile) isStuck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stuck
}

func (s *stuckRefreshFile) unstick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stuck = false
}

func (s *stuckRefreshFile) SaveRefresh(token string) error {
	if s.isStuck() {
		return fmt.Errorf("simulated: replacing refresh.enc: %w", os.ErrPermission)
	}
	return s.Store.SaveRefresh(token)
}

func (s *stuckRefreshFile) DeleteRefresh() error {
	if s.isStuck() {
		return fmt.Errorf("simulated: removing refresh.enc: %w", os.ErrPermission)
	}
	return s.Store.DeleteRefresh()
}

// The drop of a refresh token that monoes.me may have rotated (A24) takes the token
// out first and then writes down why, so a process that stops between the two steps,
// or a write that fails, leaves a session that still carries the marker and no
// refresh token. The next pass finishes that drop: it records the reason the drop
// would have recorded and clears the marker, with no call. A missing token is not a
// key store problem then, and a marker that stayed would keep every later pass due.

// interruptedDrop is a session in grace whose marker says a grant went out pending ago,
// with last as its last result and no refresh token on disk.
func interruptedDrop(t *testing.T, pending time.Duration, last string) *env {
	t.Helper()
	e := newEnv(t)
	sess := e.signIn(2*time.Hour, time.Hour) // in grace: every pass is due
	sess.PendingSince = e.f.Clock.Now().Add(-pending)
	sess.LastAttempt = sess.PendingSince
	sess.LastResult = last
	e.save(sess)
	if err := e.store.DeleteRefresh(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAPassThatFindsTheMarkerAndNoRefreshTokenFinishesTheDrop(t *testing.T) {
	cases := []struct {
		name    string
		pending time.Duration
		last    string
	}{
		{"a drop that stopped between the delete and the record", 5 * time.Minute, "unreachable"},
		{"a marker inside the window whose refresh token is gone", 30 * time.Second, "unreachable"},
		{"a drop that could not delete the token, which is gone since", 5 * time.Minute, "unconfirmed"},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := interruptedDrop(t, c.pending, c.last)
				st, err := ep.call(e.g, context.Background())
				if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed || e.ref.calls.Load() != 0 {
					t.Fatalf("%s = %s/%q, %v with %d network refreshes, want grace/unconfirmed, no error and none", ep.name, st.State, st.Reason, err, e.ref.calls.Load())
				}
				sess := e.session()
				if sess.LastResult != "unconfirmed" || !sess.PendingSince.IsZero() || !sess.LastAttempt.Equal(e.f.Clock.Now()) || sess.AccessToken == "" {
					t.Fatalf("stored session = %s, want the drop finished: unconfirmed at %v, no marker, the access token kept", describe(sess), e.f.Clock.Now())
				}
				// Finished, it is a drop like any other: later passes record nothing.
				finished := describe(sess)
				e.f.Clock.Advance(2 * time.Minute) // past the negative cache
				if st, err := ep.call(e.newGuard(0), context.Background()); err != nil || st.Reason != account.ReasonUnconfirmed || e.ref.calls.Load() != 0 {
					t.Fatalf("the next pass = %s/%q, %v with %d network refreshes, want grace/unconfirmed and none", st.State, st.Reason, err, e.ref.calls.Load())
				}
				if got := describe(e.session()); got != finished {
					t.Fatalf("the next pass changed the session: %s, was %s", got, finished)
				}
			})
		}
	}
}

// Once a drop has recorded unconfirmed, a refresh token on disk is never presented
// (ruling 5): it is dropped again. Whatever brought it back, a restore of the file or a
// delete that did not happen, it is the token that monoes.me may have rotated, and the
// age of the marker cannot vouch for it any more.
func TestATokenThatComesBackAfterTheDropIsDroppedAgainNotPresented(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	r.e.f.Clock.Advance(241 * time.Second)
	if st, err := r.command(); err != nil || st.Reason != account.ReasonUnconfirmed || !r.refreshFileGone() {
		t.Fatalf("the drop = %s/%q, %v (token gone %t), want grace/unconfirmed and the token gone", st.State, st.Reason, err, r.refreshFileGone())
	}
	if err := r.e.store.SaveRefresh("rt-1"); err != nil { // a backup brings refresh.enc back
		t.Fatal(err)
	}
	r.e.f.Clock.Advance(2 * time.Minute) // past the negative cache; the access token has run out
	st, err := r.command()
	if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("the pass that finds the token again = %s/%q, %v, want grace/unconfirmed", st.State, st.Reason, err)
	}
	if n := r.net.grants(); n != 1 || r.srv.isRevoked() {
		t.Fatalf("%d grants (revoked %t), want only the lost one: a token that a drop gave up is never presented", n, r.srv.isRevoked())
	}
	if !r.refreshFileGone() {
		t.Fatal("refresh.enc is still on disk: the next pass would face it again")
	}
	if sess := r.e.session(); sess.LastResult != "unconfirmed" || !sess.PendingSince.IsZero() || !sess.LastAttempt.Equal(r.e.f.Clock.Now()) {
		t.Fatalf("stored session = %s, want the drop recorded again at %v with no marker", describe(sess), r.e.f.Clock.Now())
	}
}

// A drop that could not remove the token keeps the marker and records unconfirmed. If the
// clock then goes back into the window, the age of the marker says retry, but the session
// says the token was given up: it is not presented, it is dropped again.
func TestADropThatCouldNotRemoveTheTokenIsNotUndoneByAClockSetBackIntoTheWindow(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	stuck := &stuckRefreshFile{Store: account.OpenStore(r.e.dir, r.e.seal), stuck: true}
	r.e.f.Clock.Advance(241 * time.Second)
	if st, err := r.e.guardWith(r.net, stuck).EnsureFresh(context.Background()); err == nil || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("the drop that cannot remove the token = %s/%q, %v, want grace/unconfirmed and the error of the remove", st.State, st.Reason, err)
	}
	if sess := r.e.session(); sess.LastResult != "unconfirmed" || !sess.PendingSince.Equal(r.t0) {
		t.Fatalf("stored session = %s, want unconfirmed with the marker kept", describe(sess))
	}
	r.e.f.Clock.Set(r.t0.Add(100 * time.Second)) // the clock goes back: the marker looks 100 s old
	if _, err := r.command(); err != nil {
		t.Fatalf("the pass with the clock set back: %v", err)
	}
	if got := r.srv.presented(); len(got) != 1 {
		t.Fatalf("monoes.me was presented %v, want the lost grant only: the token was given up", got)
	}
	if !r.refreshFileGone() || r.e.session().LastResult != "unconfirmed" || r.e.rawPending() != "" {
		t.Fatalf("stored session = %s (token gone %t), want the drop done this time", describe(r.e.session()), r.refreshFileGone())
	}
}

// A key store that cannot open the refresh token is not a missing token: the token is still
// on disk and may be the one monoes.me rotated. The marker stays and the attempt is a key
// store failure, so that once the token can be read it is retried inside the window, or
// dropped after it, and never presented as a token that nothing marks.
func TestAMarkerStaysWhileTheKeyStoreCannotOpenTheToken(t *testing.T) {
	for _, c := range []struct {
		name     string
		after    time.Duration // from the lost send to the pass whose key store works again
		recovers bool
	}{
		{"the key store answers again inside the window", time.Minute, true},
		{"the key store answers again after it", 241 * time.Second, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLostRig(t)
			r.loseTheFirstAnswer(t)
			r.e.f.Clock.Advance(30 * time.Second)
			if _, err := r.e.guardWith(r.net, account.OpenStore(r.e.dir, brokenSealer{})).EnsureFresh(context.Background()); err != nil || r.net.grants() != 1 {
				t.Fatalf("the pass whose key store fails: %v with %d grants, want no error and none beyond the lost one", err, r.net.grants())
			}
			if sess := r.e.session(); sess.LastResult != "keyring_unavailable" || !sess.PendingSince.Equal(r.t0) {
				t.Fatalf("stored session = %s, want keyring_unavailable and the marker of the lost send, %v", describe(sess), r.t0)
			}
			if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatalf("refresh.enc holds %q, want the token that could not be read, untouched", rt)
			}
			r.e.f.Clock.Set(r.t0.Add(c.after))
			st, err := r.command()
			if c.recovers {
				if err != nil || st.State != account.StateOK || r.e.rawPending() != "" || !equalTokens(r.srv.presented(), "rt-1", "rt-1") || r.srv.isRevoked() {
					t.Fatalf("the pass inside the window = %s/%q, %v (presented %v), want ok after the retry and no marker", st.State, st.Reason, err, r.srv.presented())
				}
				return
			}
			if err != nil || st.Reason != account.ReasonUnconfirmed || !r.refreshFileGone() || !equalTokens(r.srv.presented(), "rt-1") || r.srv.isRevoked() {
				t.Fatalf("the pass after the window = %s/%q, %v (presented %v, token gone %t), want grace/unconfirmed and the token dropped, not presented", st.State, st.Reason, err, r.srv.presented(), r.refreshFileGone())
			}
		})
	}
}

// equalTokens reports whether got is exactly want, in order.
func equalTokens(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
