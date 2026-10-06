package account_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// A key store that cannot open the refresh token must not erase what keeps a token in doubt
// from being presented (A24). That pass cannot present the token, but a key store failure
// recorded over unconfirmed, or over the last attempt that a clock gone back is measured
// against, leaves a token on disk that the next pass, with the key store back, presents:
// perhaps long after monoes.me's reuse window, which revokes every install of the account. So
// a pass that cannot read a token that must never be presented again drops it unread
// (os.Remove needs no key store). Inside the window nothing is decided yet: the marker stays
// and the failure is keyring_unavailable (TestAMarkerStaysWhileTheKeyStoreCannotOpenTheToken).

// keyStoreDown is a store whose key store cannot open the refresh token while down is set: a
// LoadRefresh that would have to ask it fails as a locked keychain does (a missing refresh.enc
// still reads as none: that needs no key store). Everything else goes to the store underneath,
// DeleteRefresh included, since os.Remove needs no key store.
type keyStoreDown struct {
	account.Store
	mu      sync.Mutex
	down    bool
	deletes int
	saved   []string // the last result of every session it saved, in order
}

func (s *keyStoreDown) LoadRefresh() (string, error) {
	rt, err := s.Store.LoadRefresh()
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down && err == nil && rt != "" {
		return "", fmt.Errorf("simulated: the key store does not answer: %w", account.ErrKeyringUnavailable)
	}
	return rt, err
}

func (s *keyStoreDown) DeleteRefresh() error {
	s.mu.Lock()
	s.deletes++
	s.mu.Unlock()
	return s.Store.DeleteRefresh()
}

func (s *keyStoreDown) Save(sess *account.Session) error {
	s.mu.Lock()
	s.saved = append(s.saved, sess.LastResult)
	s.mu.Unlock()
	return s.Store.Save(sess)
}

func (s *keyStoreDown) deleted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

func (s *keyStoreDown) savedResults() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.saved...)
}

// passWith is one command over store, a process of its own.
func (r *lostRig) passWith(store account.Store) (account.Status, error) {
	return r.e.guardWith(r.net, store).EnsureFresh(context.Background())
}

// neverPresentedAgain fails unless monoes.me has been presented rt-1 exactly times times, the
// presentations before the token came into doubt, and the account is whole.
func (r *lostRig) neverPresentedAgain(t *testing.T, times int) {
	t.Helper()
	if got := count(r.srv.presented(), "rt-1"); got != times || r.srv.isRevoked() {
		t.Fatalf("monoes.me was presented rt-1 %d times (revoked %t), want %d and the account whole: %v", got, r.srv.isRevoked(), times, r.srv.presented())
	}
}

// The probe of the finding: a drop completed, refresh.enc came back (a restored backup), and
// the key store did not answer at the next pass. That pass recorded keyring_unavailable over
// unconfirmed, and the pass after it, with the key store back, presented the token about 480 s
// after monoes.me had rotated it: every install of the account revoked.
func TestAKeyStoreFailureNeverRevivesATokenThatCameBackAfterTheDrop(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	r.e.f.Clock.Advance(241 * time.Second)
	if st, err := r.command(); err != nil || st.Reason != account.ReasonUnconfirmed || !r.refreshFileGone() {
		t.Fatalf("the drop = %s/%q, %v (token gone %t), want grace/unconfirmed and the token gone", st.State, st.Reason, err, r.refreshFileGone())
	}
	if err := r.e.store.SaveRefresh("rt-1"); err != nil { // a backup brings refresh.enc back
		t.Fatal(err)
	}
	r.e.f.Clock.Advance(2 * time.Minute) // past the negative cache
	down := &keyStoreDown{Store: account.OpenStore(r.e.dir, r.e.seal), down: true}
	if st, err := r.passWith(down); err != nil || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("the pass whose key store does not answer = %s/%q, %v, want grace/unconfirmed and no error", st.State, st.Reason, err)
	}
	if !r.refreshFileGone() || down.deleted() != 1 {
		t.Errorf("refresh token gone %t after %d removes, want it dropped unread, once", r.refreshFileGone(), down.deleted())
	}
	if got := down.savedResults(); !reflect.DeepEqual(got, []string{"unconfirmed"}) {
		t.Errorf("the pass saved the session with the results %q, want one write, of unconfirmed: a key store problem written first would stand if the next write were lost", got)
	}
	if sess := r.e.session(); sess.LastResult != "unconfirmed" || r.e.rawPending() != "" {
		t.Errorf("stored session = %s, want unconfirmed kept and no marker", describe(sess))
	}
	r.e.f.Clock.Advance(2 * time.Minute)
	if st, err := r.command(); err != nil || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("the pass with the key store back = %s/%q, %v, want grace/unconfirmed", st.State, st.Reason, err)
	}
	r.neverPresentedAgain(t, 1)
}

// A refresh.enc that can be neither replaced nor removed: the drop could not take it out, so it
// kept the marker and recorded unconfirmed. Then the key store does not answer while the clock is
// set back into the window, and a pass after that, with the key store back, finds a marker whose
// age looks young. Nothing of that may make the token presentable.
func TestAKeyStoreFailureAndAClockSetBackIntoTheWindowNeverPresentAStuckToken(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	stuck := &stuckRefreshFile{Store: account.OpenStore(r.e.dir, r.e.seal), stuck: true}
	r.e.f.Clock.Advance(241 * time.Second)
	if st, err := r.passWith(stuck); err == nil || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("the drop that cannot remove the token = %s/%q, %v, want grace/unconfirmed and the error of the remove", st.State, st.Reason, err)
	}
	if sess := r.e.session(); sess.LastResult != "unconfirmed" || !sess.PendingSince.Equal(r.t0) {
		t.Fatalf("stored session = %s, want unconfirmed and the marker kept", describe(sess))
	}
	r.e.f.Clock.Set(r.t0.Add(100 * time.Second)) // set back into the window
	if _, err := r.passWith(&keyStoreDown{Store: stuck, down: true}); err == nil {
		t.Error("the pass whose key store does not answer reported nothing, want the error of the remove that failed again")
	}
	if sess := r.e.session(); sess.LastResult != "unconfirmed" || !sess.PendingSince.Equal(r.t0) {
		t.Errorf("stored session = %s, want unconfirmed and the marker kept: a key store failure must not overwrite them", describe(sess))
	}
	r.e.f.Clock.Set(r.t0.Add(110 * time.Second))
	if st, _ := r.passWith(stuck); st.Reason == account.ReasonRefused {
		t.Fatalf("the pass with the key store back = %s/%q, want never a refusal", st.State, st.Reason)
	}
	r.neverPresentedAgain(t, 1)
	if rt, _ := r.e.store.LoadRefresh(); rt != "rt-1" {
		t.Fatal("the test removed the stuck token itself: it cannot tell")
	}
}

// Retries at +30, +90 and +210 s, the clock set back to +100 s, and a key store that does not
// answer at that pass. Recording the failure there put the last attempt at +100 s, before the
// clock again, so the pass at +110 s with the key store back read the marker as 110 s old and
// presented the token, although at least 210 s had passed since monoes.me rotated it.
func TestAKeyStoreFailureOnAClockThatWentBackNeverPresentsTheToken(t *testing.T) {
	r := retriedThreeTimes(t)
	r.e.f.Clock.Set(r.t0.Add(100 * time.Second))
	down := &keyStoreDown{Store: account.OpenStore(r.e.dir, r.e.seal), down: true}
	if _, err := r.passWith(down); err != nil {
		t.Fatalf("the pass whose key store does not answer: %v", err)
	}
	if !r.refreshFileGone() || down.deleted() != 1 || r.e.rawPending() != "" || r.e.session().LastResult != "unconfirmed" {
		t.Errorf("refresh token gone %t after %d removes, stored session %s: want the token dropped unread, unconfirmed and no marker", r.refreshFileGone(), down.deleted(), describe(r.e.session()))
	}
	r.e.f.Clock.Set(r.t0.Add(110 * time.Second))
	if _, err := r.command(); err != nil {
		t.Fatalf("the pass at +110 s: %v", err)
	}
	if n := r.net.grants(); n != 4 {
		t.Fatalf("%d grants, want the four of before and no more", n)
	}
	r.neverPresentedAgain(t, 4)
}

// The clock that judges a key store failure is read after the session, under the lock: another
// process may have recorded an attempt while this one waited for it, and a reading from before
// the wait would lie before that attempt and look like a clock gone back, dropping a token that a
// retry still recovers.
func TestAKeyStoreFailureAfterAnotherProcessRecordedAnAttemptIsNoClockGoingBack(t *testing.T) {
	r := newLostRig(t)
	r.loseTheFirstAnswer(t)
	r.e.f.Clock.Advance(50 * time.Second)
	spy := r.e.spy()
	var once sync.Once
	spy.afterLock = func() {
		once.Do(func() {
			// Meanwhile another process retried at +55 s and lost the answer again, and the
			// clock moved on to +60 s.
			sess := r.e.session()
			sess.LastAttempt, sess.LastResult = r.t0.Add(55*time.Second), "unreachable"
			r.e.save(sess)
			r.e.f.Clock.Set(r.t0.Add(60 * time.Second))
		})
	}
	if _, err := r.passWith(&keyStoreDown{Store: spy, down: true}); err != nil {
		t.Fatalf("the pass whose key store does not answer: %v", err)
	}
	if sess := r.e.session(); sess.LastResult != "keyring_unavailable" || !sess.PendingSince.Equal(r.t0) || r.refreshFileGone() {
		t.Fatalf("stored session = %s (token gone %t), want keyring_unavailable, the marker and the token kept for a retry", describe(sess), r.refreshFileGone())
	}
	r.e.f.Clock.Advance(10 * time.Second)
	if st, err := r.command(); err != nil || st.State != account.StateOK || r.srv.isRevoked() {
		t.Fatalf("the retry with the key store back = %s/%q, %v (revoked %t), want ok", st.State, st.Reason, err, r.srv.isRevoked())
	}
}
