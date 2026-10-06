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

// failingSaveStore is a Store whose Save can be made to fail, as a full disk or a
// directory that has gone read-only does, and that counts the attempts.
type failingSaveStore struct {
	account.Store
	mu    sync.Mutex
	fail  bool
	tries int
}

func (s *failingSaveStore) Save(sess *account.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tries++
	if s.fail {
		return errors.New("simulated: the disk is full")
	}
	return s.Store.Save(sess)
}

func (s *failingSaveStore) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tries
}

// writeInTheSameTick stores sess as another process would, and puts session.json's
// modification time back to seen, as a filesystem with coarse timestamps would
// leave it: a guard that polls by modification time finds nothing new, so the only
// read that can show it the write is the one touchHW makes under the lock.
func (e *env) writeInTheSameTick(sess *account.Session, seen time.Time) {
	e.t.Helper()
	if err := e.store.Save(sess); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil {
		e.t.Fatal(err)
	}
	if !mustMtime(e.t, e.store).Equal(seen) {
		e.t.Fatal("the test could not restore the file's modification time: it cannot tell a poll from the lock's read")
	}
}

// heldSession is what aLaterSessionWithAStaleMark sets up.
type heldSession struct {
	e    *env
	g    *account.Guard
	fs   *failingSaveStore
	now  time.Time
	seen time.Time
}

// aLaterSessionWithAStaleMark is the scene of two tests: a guard that holds the
// session of a login ten minutes old (valid for 50 more minutes) over a store whose
// Save fails from now on; and on disk a later session that another process wrote
// within the file's modification-time tick, valid for two hours, whose mark is
// stale as well (it is the token's iat, ten minutes ago), so that the guard's
// touchHW wants to raise it.
func aLaterSessionWithAStaleMark(t *testing.T) heldSession {
	t.Helper()
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // its mark is ten minutes old, so touchHW will try
	fs := &failingSaveStore{Store: account.OpenStore(e.dir, e.seal)}
	g := e.guardOver(fs, 0)
	now := e.f.Clock.Now()
	if st := g.Status(); st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(50*time.Minute)) {
		t.Fatalf("Status = %s valid until %v, want the stored token (50 minutes left)", st.State, st.ValidUntil)
	}
	seen := mustMtime(t, e.store)
	later, err := account.NewSession(account.HostURL, e.f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Minute), Lifetime: 2 * time.Hour}), &account.User{ID: "user-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	e.writeInTheSameTick(later, seen)
	fs.fail = true
	return heldSession{e: e, g: g, fs: fs, now: now, seen: seen}
}

// touchHW reads the session again under the lock, and what it reads is, as a rule,
// the newest thing this process has seen: another process may have written it
// within the file's modification-time tick, so the poll that compares times finds
// nothing new. When the high-water mark write that follows fails, the guard must
// still take that session in, not drop it until some later write succeeds.
func TestAGuardWhoseHighWaterWriteFailsStillTakesInTheSessionItReadUnderTheLock(t *testing.T) {
	s := aLaterSessionWithAStaleMark(t)
	st, err := s.g.EnsureFresh(context.Background())
	if err != nil {
		t.Fatalf("EnsureFresh = %v: a high-water write that fails is not reported", err)
	}
	if n := s.fs.attempts(); n != 1 {
		t.Fatalf("%d writes of the mark, want 1: this test cannot tell without the failing write", n)
	}
	if !st.ValidUntil.Equal(s.now.Add(110 * time.Minute)) {
		t.Fatalf("EnsureFresh = %s valid until %v, want the other process's token (110 minutes left)", st.State, st.ValidUntil)
	}
	if st := s.g.Status(); st.State != account.StateOK || !st.ValidUntil.Equal(s.now.Add(110*time.Minute)) {
		t.Fatalf("Status afterwards = %s valid until %v, want the other process's token", st.State, st.ValidUntil)
	}
	if !mustMtime(t, s.e.store).Equal(s.seen) {
		t.Fatal("the file was written although the write was made to fail")
	}
}

// What the guard holds after a failed write of the mark is what the file holds: the
// raised mark was never written, so it is not remembered in memory either. This
// is a pin, not a regression: it keeps the session that was read apart from the
// copy of it that was meant to be written. The mark in the file is ten minutes
// old, so a clock set back 6 minutes is no rollback by it, and would be by the
// raised one (more than the 5 minute allowance behind it).
func TestAMarkThatCouldNotBeWrittenIsNotKeptInMemoryEither(t *testing.T) {
	s := aLaterSessionWithAStaleMark(t)
	if _, err := s.g.EnsureFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := s.fs.attempts(); n != 1 {
		t.Fatalf("%d writes of the mark, want 1: this test cannot tell without the failing write", n)
	}
	s.e.f.Clock.Set(s.now.Add(-6 * time.Minute))
	if st := s.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status after a 6 minute set-back = %s/%q, want ok: the mark that was not written stands in memory", st.State, st.Reason)
	}
}

// Another process's session can be older than this process's own. A refresh worked,
// the write of its session failed (the write of the refresh token did not), and so
// only this process holds the new session. A failed write of the mark must not put
// the older file back in its place: the guard would go back to the old token, find
// it due and refresh again, and again at every call.
func TestAFailedHighWaterWriteDoesNotRevertANewerSessionOnlyThisProcessHolds(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due: four minutes left
	fs := &failingSaveStore{Store: account.OpenStore(e.dir, e.seal)}
	g := e.guardOver(fs, 0)
	fs.fail = true
	t0 := e.f.Clock.Now()
	ctx := context.Background()
	if _, err := g.EnsureFresh(ctx); err == nil {
		t.Fatal("the session write was made to fail and EnsureFresh reported nothing: this test cannot tell")
	}
	for i := 1; i <= 6; i++ {
		e.f.Clock.Advance(61 * time.Second) // the mark of the new session is a minute stale again
		st, _ := g.EnsureFresh(ctx)
		if st.State != account.StateOK || !st.ValidUntil.Equal(t0.Add(time.Hour)) {
			t.Fatalf("call %d: %s valid until t0%+v, want ok with the new token (t0+1h): the older file was put back", i, st.State, st.ValidUntil.Sub(t0))
		}
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes, want 1", n)
	}
}

// The same when the older file already has a current mark: a peer wrote it, so
// touchHW has nothing to raise and takes the file's session in, which is the older
// one.
func TestAMarkAPeerWroteOnTheOlderFileDoesNotRevertANewerSessionOnlyThisProcessHolds(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due: four minutes left
	fs := &failingSaveStore{Store: account.OpenStore(e.dir, e.seal)}
	g := e.guardOver(fs, 0)
	fs.fail = true
	t0 := e.f.Clock.Now()
	ctx := context.Background()
	if _, err := g.EnsureFresh(ctx); err == nil {
		t.Fatal("the session write was made to fail and EnsureFresh reported nothing: this test cannot tell")
	}
	seen := mustMtime(t, e.store)
	e.f.Clock.Advance(61 * time.Second) // the mark of the new session is a minute stale; nothing is due
	peer := e.session()                 // the older session: it is what the file still holds
	peer.HW = e.f.Clock.Now().Add(-10 * time.Second)
	e.writeInTheSameTick(peer, seen)
	st, err := g.EnsureFresh(ctx)
	if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(t0.Add(time.Hour)) {
		t.Fatalf("EnsureFresh = %s valid until t0%+v, %v, want ok with the new token (t0+1h): the older file was put back", st.State, st.ValidUntil.Sub(t0), err)
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes, want 1", n)
	}
}

// The boundary of that rule: a file that is as new as the guard's copy is taken
// in. Here a peer raised the mark of the very session this process holds, so the
// two have the same LastAttempt, and the peer's mark is the one to keep: it shows
// when the clock is set back, past its allowance, by 6 minutes.
func TestAMarkAnotherProcessWroteOnTheSessionThisProcessHoldsIsTakenIn(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy; its mark is ten minutes old, so touchHW will try
	now := e.f.Clock.Now()
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
	seen := mustMtime(t, e.store)
	peer := e.session()
	peer.HW = now.Add(-10 * time.Second)
	e.writeInTheSameTick(peer, seen)
	if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK {
		t.Fatalf("EnsureFresh = %s/%q, %v, want ok", st.State, st.Reason, err)
	}
	e.f.Clock.Set(now.Add(-6 * time.Minute))
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonClockRollback {
		t.Fatalf("Status after a 6 minute set-back = %s/%q, want locked/clock_rollback: the peer's mark was not taken in", st.State, st.Reason)
	}
}
