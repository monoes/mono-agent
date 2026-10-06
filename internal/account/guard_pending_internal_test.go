package account

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests are in package account for what is not exported: the constant that
// bounds a retry (A24), and the helpers of the refresh that is in flight.

// monoes.me answers a refresh token that it rotated away with the same answer for
// 300 seconds, and takes it for theft after that. A retry is only safe inside that
// window, and it must leave room for the call's own duration (up to
// refreshCallTimeout, 20 s), the wait for the key store (keyStoreTimeout, 10 s) and
// two clocks that do not run at the same rate: 240 s is the value of the contract.
func TestThePendingRetryWindowIsFourMinutesAndLeavesRoomInTheServersFive(t *testing.T) {
	const serversReuseWindow = 300 * time.Second
	if pendingRetryWindow != 240*time.Second {
		t.Fatalf("pendingRetryWindow = %v, want 240 s (index §3.2)", pendingRetryWindow)
	}
	if room := serversReuseWindow - pendingRetryWindow; room < refreshCallTimeout+keyStoreTimeout {
		t.Fatalf("a retry made at the end of the window reaches the server %v before its reuse window ends, want at least a call (%v) and a key store wait (%v)",
			room, refreshCallTimeout, keyStoreTimeout)
	}
}

// Whether the outcome of a grant is known is a fact about the failure, and the
// fail-safe reading is the zero value: a Refresher that never heard of the field
// reports an outcome that is unknown, which the guard treats as a refresh token
// that may have been rotated.
func TestATransientErrorIsUnsettledUnlessItSaysOtherwise(t *testing.T) {
	var zero TransientError
	if zero.Settled {
		t.Fatal("the zero value of TransientError is settled: a Refresher that does not set the field would be read as one whose outcome is known")
	}
	if (&TransientError{Reason: ReasonUnreachable, Err: errors.New("connection reset")}).Settled {
		t.Fatal("a TransientError built without Settled is settled")
	}
	// Settled says what happened to the token, not what to tell a person: the text of the error is the same.
	plain := &TransientError{Reason: ReasonUnreachable, Err: errors.New("no route")}
	settled := &TransientError{Reason: ReasonUnreachable, Err: errors.New("no route"), Settled: true}
	if plain.Error() != settled.Error() || !strings.Contains(plain.Error(), "no route") {
		t.Fatalf("Settled changes the text of the error: %q and %q", plain.Error(), settled.Error())
	}
	if !errors.Is(settled, settled.Err) || errors.Unwrap(settled) != settled.Err {
		t.Fatal("a settled TransientError no longer wraps its cause")
	}
}

// (i): a session with a marker is due in both modes, whatever the token, the margin and
// the negative cache say: a grant that may have been answered is retried at once or
// dropped, and the lock path tells which from the age of the marker. What can never be
// refreshed stays so: refused, with no token, or no session.
func TestDueForRefreshIsAlwaysTrueForAPendingSession(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	receipt := func(age time.Duration) *Receipt {
		iat := now.Add(-age)
		return &Receipt{Sub: "u-1", IssuedAt: iat, ExpiresAt: iat.Add(time.Hour)}
	}
	ok := Status{State: StateOK}
	grace := Status{State: StateGrace}
	marked := func(stamp time.Time, lastAttempt time.Time) *Session {
		return &Session{V: 1, AccessToken: "token", LastAttempt: lastAttempt, PendingSince: stamp}
	}
	cases := []struct {
		name string
		sess *Session
		st   Status
		rcpt *Receipt
	}{
		{"a token with 50 minutes left", marked(now.Add(-time.Minute), time.Time{}), ok, receipt(10 * time.Minute)},
		{"a token with 50 minutes left, tried 10 seconds ago", marked(now.Add(-10*time.Second), now.Add(-10*time.Second)), ok, receipt(10 * time.Minute)},
		{"a token past its half-life, outside the CLI margin", marked(now.Add(-time.Minute), now.Add(-time.Second)), ok, receipt(35 * time.Minute)},
		{"a session in grace, tried a second ago", marked(now.Add(-time.Second), now.Add(-time.Second)), grace, receipt(2 * time.Hour)},
		{"a token this build cannot verify", marked(now.Add(-time.Second), now.Add(-time.Second)), Status{State: StateLocked, Reason: ReasonKeyUnknown}, nil},
		{"a marker that is older than the window", marked(now.Add(-time.Hour), time.Time{}), ok, receipt(10 * time.Minute)},
		{"a marker that lies after now", marked(now.Add(time.Hour), time.Time{}), ok, receipt(10 * time.Minute)},
	}
	for _, mode := range []refreshMode{modeCLI, modeBackground} {
		for _, c := range cases {
			if !dueForRefresh(c.sess, c.st, c.rcpt, now, mode) {
				t.Errorf("mode %d, %s: not due, want due: the marker is a grant in doubt", mode, c.name)
			}
		}
		for _, c := range []struct {
			name string
			sess *Session
			st   Status
		}{
			{"no session", nil, Status{State: StateLocked, Reason: ReasonNotLoggedIn}},
			{"a refused session that still holds a marker", &Session{V: 1, AccessToken: "token", State: stateRefused, PendingSince: now.Add(-time.Second)}, Status{State: StateLocked, Reason: ReasonRefused}},
			{"no token", &Session{V: 1, PendingSince: now.Add(-time.Second)}, Status{State: StateLocked, Reason: ReasonNotLoggedIn}},
		} {
			if dueForRefresh(c.sess, c.st, nil, now, mode) {
				t.Errorf("mode %d, %s: due, want never: there is nothing to refresh with", mode, c.name)
			}
		}
	}
}

// The age of a marker decides between the retry and the drop, in the lock path: the
// retry holds for exactly pendingRetryWindow, a clock that went back is no age at all.
func TestPendingExpiredJudgesTheAgeOfTheMarker(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		stamp time.Time
		want  bool
	}{
		{"no marker", time.Time{}, false},
		{"written just now", now, false},
		{"a second old", now.Add(-time.Second), false},
		{"a minute old", now.Add(-time.Minute), false},
		{"one nanosecond short of the window", now.Add(-pendingRetryWindow + time.Nanosecond), false},
		{"exactly the window old", now.Add(-pendingRetryWindow), false},
		{"one nanosecond past the window", now.Add(-pendingRetryWindow - time.Nanosecond), true},
		{"a second past the window", now.Add(-pendingRetryWindow - time.Second), true},
		{"the server's whole window old", now.Add(-300 * time.Second), true},
		{"a day old", now.Add(-24 * time.Hour), true},
		{"one nanosecond ahead of the clock", now.Add(time.Nanosecond), true},
		{"a second ahead of the clock", now.Add(time.Second), true},
		{"an hour ahead of the clock", now.Add(time.Hour), true},
	}
	for _, c := range cases {
		if got := pendingExpired(&Session{PendingSince: c.stamp}, now); got != c.want {
			t.Errorf("%s: pendingExpired = %t, want %t", c.name, got, c.want)
		}
	}
}

// The last attempt the session records is written from this machine's clock too, so a
// clock that reads before it has gone back, and a marker's age read on it cannot be
// trusted, however young it looks. Only for a marker: with no grant in doubt it is no
// reason to drop anything.
func TestPendingExpiredTakesAClockBeforeTheLastAttemptForAClockThatWentBack(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	stamp := now.Add(-100 * time.Second) // well inside the window
	cases := []struct {
		name  string
		stamp time.Time
		last  time.Time
		want  bool
	}{
		{"no last attempt", stamp, time.Time{}, false},
		{"a last attempt at the stamp", stamp, stamp, false},
		{"a last attempt 50 s ago", stamp, now.Add(-50 * time.Second), false},
		{"a last attempt at this very instant", stamp, now, false},
		{"a last attempt a nanosecond ahead of the clock", stamp, now.Add(time.Nanosecond), true},
		{"a last attempt 110 s ahead of the clock", stamp, now.Add(110 * time.Second), true},
		{"no marker, a last attempt an hour ahead of the clock", time.Time{}, now.Add(time.Hour), false},
	}
	for _, c := range cases {
		if got := pendingExpired(&Session{PendingSince: c.stamp, LastAttempt: c.last}, now); got != c.want {
			t.Errorf("%s: pendingExpired = %t, want %t", c.name, got, c.want)
		}
	}
}

// orderStore records the writes of a pass in order and fails the ones a test names.
type orderStore struct {
	Store
	mu                    sync.Mutex
	log                   []string
	failSave, failDelete  bool
	sawRefreshOnSave      []bool // for each Save: whether refresh.enc was still on disk at that moment
	refreshFileOnDiskFunc func() bool
}

func (s *orderStore) writes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

func (s *orderStore) Save(sess *Session) error {
	s.mu.Lock()
	s.log = append(s.log, "Save")
	s.sawRefreshOnSave = append(s.sawRefreshOnSave, s.refreshFileOnDiskFunc != nil && s.refreshFileOnDiskFunc())
	fail := s.failSave
	s.mu.Unlock()
	if fail {
		return errors.New("simulated: disk full")
	}
	return s.Store.Save(sess)
}

func (s *orderStore) DeleteRefresh() error {
	s.mu.Lock()
	s.log = append(s.log, "DeleteRefresh")
	fail := s.failDelete
	s.mu.Unlock()
	if fail {
		return errors.New("simulated: permission denied")
	}
	return s.Store.DeleteRefresh()
}

// The drop takes the dead token out before anything else is written. A process that
// stops between the two steps then leaves a missing token and the old marker, which the next
// pass reads as a key store problem: never a token that nothing marks, which would be
// presented, perhaps outside the window, with every install of the account revoked.
func TestTheDropRemovesTheDeadTokenBeforeItWritesAnything(t *testing.T) {
	r := newRig(t)
	sess := r.signIn(2 * time.Hour)
	sess.PendingSince = r.clock.Now().Add(-time.Hour)
	if err := r.store.Save(sess); err != nil {
		t.Fatal(err)
	}
	ws := &orderStore{Store: r.store}
	ws.refreshFileOnDiskFunc = func() bool { rt, _ := r.store.LoadRefresh(); return rt != "" }
	g := NewGuard(GuardOptions{Store: ws, Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(g.Close)
	_, got, err := g.refreshIfDue(context.Background(), modeCLI)
	if err != nil || got != outcomeFailed || r.srv.calls.Load() != 0 {
		t.Fatalf("refreshIfDue = %s, %v with %d network refreshes, want failed, no error and none: a drop sends nothing", outcomeNames[got], err, r.srv.calls.Load())
	}
	if want := []string{"DeleteRefresh", "Save"}; !equalStrings(ws.writes(), want) {
		t.Fatalf("writes = %v, want %v: the dead token goes before the record of why", ws.writes(), want)
	}
	if len(ws.sawRefreshOnSave) != 1 || ws.sawRefreshOnSave[0] {
		t.Fatalf("refresh.enc was still on disk when the session was saved: %v", ws.sawRefreshOnSave)
	}
	if saved, err := r.store.Load(); err != nil || saved.LastResult != string(ReasonUnconfirmed) || !saved.PendingSince.IsZero() || !saved.LastAttempt.Equal(r.clock.Now()) {
		t.Fatalf("stored session = %+v (%v), want unconfirmed at %v with no marker", saved, err, r.clock.Now())
	}
}

// A token that cannot be removed is never presented: the marker stays, so that the next pass
// drops it again, and the error is reported.
func TestADropThatCannotRemoveTheTokenKeepsTheMarkerAndNeverPresentsIt(t *testing.T) {
	r := newRig(t)
	sess := r.signIn(2 * time.Hour)
	stamp := r.clock.Now().Add(-time.Hour)
	sess.PendingSince = stamp
	if err := r.store.Save(sess); err != nil {
		t.Fatal(err)
	}
	ws := &orderStore{Store: r.store, failDelete: true}
	g := NewGuard(GuardOptions{Store: ws, Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(g.Close)
	for pass := 1; pass <= 3; pass++ {
		r.clock.Advance(2 * time.Minute) // past the negative cache each time
		_, got, err := g.refreshIfDue(context.Background(), modeCLI)
		if err == nil || got != outcomeFailed || r.srv.calls.Load() != 0 {
			t.Fatalf("pass %d: refreshIfDue = %s, %v with %d network refreshes, want failed, the delete error and no network refresh", pass, outcomeNames[got], err, r.srv.calls.Load())
		}
		saved, err := r.store.Load()
		if err != nil || !saved.PendingSince.Equal(stamp) || saved.LastResult != string(ReasonUnconfirmed) {
			t.Fatalf("pass %d: stored session = %+v (%v), want the marker kept (%v) so that the token is dropped again", pass, saved, err, stamp)
		}
	}
	if rt, _ := r.store.LoadRefresh(); rt != "rt-1" {
		t.Fatal("the test removed the token itself: it cannot tell")
	}
	// The disk lets it go at last: the next pass takes the token out and clears the marker.
	ws.mu.Lock()
	ws.failDelete = false
	ws.mu.Unlock()
	r.clock.Advance(2 * time.Minute)
	if _, got, err := g.refreshIfDue(context.Background(), modeCLI); err != nil || got != outcomeFailed {
		t.Fatalf("the pass that can remove it = %s, %v", outcomeNames[got], err)
	}
	if rt, _ := r.store.LoadRefresh(); rt != "" {
		t.Fatalf("refresh.enc still holds %q", rt)
	}
	if saved, err := r.store.Load(); err != nil || !saved.PendingSince.IsZero() {
		t.Fatalf("stored session = %+v (%v), want the marker cleared once the token is gone", saved, err)
	}
}

// A record that cannot be saved still drops the token: the dead one must not be presented
// whether or not the reason can be written down. The pass reports the error, and the next
// process, which finds no token and the old marker, finishes the drop: it records what the
// drop would have, with no call (rule 1d).
func TestADropWhoseRecordCannotBeSavedStillRemovesTheToken(t *testing.T) {
	r := newRig(t)
	sess := r.signIn(2 * time.Hour)
	sess.PendingSince = r.clock.Now().Add(-time.Hour)
	if err := r.store.Save(sess); err != nil {
		t.Fatal(err)
	}
	ws := &orderStore{Store: r.store, failSave: true}
	g := NewGuard(GuardOptions{Store: ws, Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(g.Close)
	st, got, err := g.refreshIfDue(context.Background(), modeCLI)
	if err == nil || got != outcomeFailed || r.srv.calls.Load() != 0 {
		t.Fatalf("refreshIfDue = %s, %v with %d network refreshes, want failed, the write error and none", outcomeNames[got], err, r.srv.calls.Load())
	}
	if rt, _ := r.store.LoadRefresh(); rt != "" {
		t.Fatalf("refresh.enc still holds %q: a record that cannot be saved must not keep the dead token on disk", rt)
	}
	if st.State != StateGrace || st.Reason != ReasonUnconfirmed {
		t.Fatalf("Status = %s/%q, want grace/unconfirmed: this process knows why", st.State, st.Reason)
	}
	// Another process finds the old session and no token.
	other := NewGuard(GuardOptions{Store: OpenStore(r.dir, r.seal), Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(other.Close)
	r.clock.Advance(2 * time.Minute)
	st, got, err = other.refreshIfDue(context.Background(), modeCLI)
	if err != nil || got != outcomeFailed || st.State != StateGrace || st.Reason != ReasonUnconfirmed || r.srv.calls.Load() != 0 {
		t.Fatalf("the other process = %s/%q, %s, %v with %d network refreshes, want grace/unconfirmed, failed, no error and none", st.State, st.Reason, outcomeNames[got], err, r.srv.calls.Load())
	}
	if saved, err := r.store.Load(); err != nil || saved.LastResult != string(ReasonUnconfirmed) || !saved.PendingSince.IsZero() || !saved.LastAttempt.Equal(r.clock.Now()) {
		t.Fatalf("stored session = %+v (%v), want the drop finished: unconfirmed at %v and no marker", saved, err, r.clock.Now())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
