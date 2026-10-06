package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// pinSession sets session.json's modification time to a moment no write of a
// test can have, so that a later change of it shows a write.
func (e *env) pinSession() time.Time {
	e.t.Helper()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), old, old); err != nil {
		e.t.Fatal(err)
	}
	return old
}

// Another process refuses the session and leaves session.json with the
// modification time it had: a filesystem with coarse timestamps does that when
// the two writes fall in one tick. The guard's poll then finds an unchanged
// file, so the one read that can still show it the refusal is the one touchHW
// makes under the lock. It must take what it found in, not drop it.
func TestAGuardThatFindsARefusalWhileWritingTheHighWaterMarkTakesItIn(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, and its mark is ten minutes old: touchHW will try
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
	seen := mustMtime(t, e.store)
	e.refuse()
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil {
		t.Fatal(err)
	}
	if !mustMtime(t, e.store).Equal(seen) {
		t.Fatal("the test could not restore the file's modification time: it cannot tell a poll from the lock's read")
	}

	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused: the refusal was read under the lock", st.State, st.Reason, err)
	}
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("Status afterwards = %s/%q, want locked/refused", st.State, st.Reason)
	}
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d network refreshes, want none: a refused session never tries again", n)
	}
}

// The mark is written once it is a minute stale, to the second.
func TestTheHighWaterMarkIsWrittenOnceItIsAMinuteStale(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		written bool
	}{
		{"59 seconds old", 59 * time.Second, false},
		{"a minute old, to the second", time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(c.age, time.Hour) // healthy; its mark is the token's iat
			old := e.pinSession()
			if _, err := e.g.EnsureFresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if wrote := !mustMtime(t, e.store).Equal(old); wrote != c.written {
				t.Fatalf("session.json written: %t, want %t", wrote, c.written)
			}
			if c.written && !e.session().HW.Equal(e.f.Clock.Now()) {
				t.Fatalf("hw = %v, want now %v", e.session().HW, e.f.Clock.Now())
			}
		})
	}
}

// While dormant nothing is written on a caller's behalf, an explicit Refresh
// included: only a refresh that is due writes, and that is the caller's request.
func TestARefreshWhileDormantWritesNoHighWaterMark(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
	old := e.pinSession()
	account.SetEnforceFromForTest(t, time.Time{})
	if st, err := e.g.Refresh(context.Background()); err != nil || st.State != account.StateOK {
		t.Fatalf("Refresh = %s/%q, %v, want ok", st.State, st.Reason, err)
	}
	if !mustMtime(t, e.store).Equal(old) {
		t.Fatal("Refresh wrote the high-water mark while dormant")
	}
}

// A refused session has no mark worth writing: the guard does not even take the
// lock for it.
func TestARefusedSessionIsNeitherWrittenNorLocked(t *testing.T) {
	e := newEnv(t)
	e.refuse()
	spy := e.spy()
	g := e.guardOver(spy, 0)
	st, err := g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
	}
	if lock, save := spy.calls("Lock"), spy.calls("Save"); lock != 0 || save != 0 {
		t.Fatalf("%d locks and %d writes for a refused session, want none", lock, save)
	}
}

// A write of the mark that cannot be made is not reported, and is tried again
// at most once a minute: a lock that is held must not cost every call the wait.
func TestAHighWaterMarkWriteThatFailsIsTriedAgainOnlyAMinuteLater(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
	spy := e.spy()
	spy.lockErr = errors.New("simulated: the lock is held")
	g := e.guardOver(spy, 0)
	call := func(when string, want int) {
		t.Helper()
		if _, err := g.EnsureFresh(context.Background()); err != nil {
			t.Fatalf("%s: %v: a missed high-water write is not reported", when, err)
		}
		if n := spy.calls("Lock"); n != want {
			t.Fatalf("%s: %d tries for the lock so far, want %d", when, n, want)
		}
	}
	call("the first call", 1)
	call("straight after it", 1)
	e.f.Clock.Advance(59 * time.Second)
	call("59 seconds after it", 1)
	e.f.Clock.Advance(time.Second)
	call("a minute to the second after it", 2)
}

// A write of the mark waits two seconds for the lock at most, however long the
// caller's context would let it.
func TestAHighWaterMarkWriteNeverWaitsLongForTheLock(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
	var left time.Duration
	var set bool
	spy := e.spy()
	spy.onLock = func(ctx context.Context) {
		if !set {
			left, set = remaining(ctx)
		}
	}
	if _, err := e.guardOver(spy, 0).EnsureFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !set || left <= time.Second || left > 2*time.Second {
		t.Fatalf("the wait for the lock has %v (a deadline: %t), want 2 s", left, set)
	}
}

// A mark that cannot be written because the session is gone, or cannot be read,
// is not an error and leaves the file as it is.
func TestAHighWaterMarkWriteThatFindsNoUsableSessionDoesNothing(t *testing.T) {
	cached := func(t *testing.T) *env {
		t.Helper()
		e := newEnv(t)
		e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
		if st := e.g.Status(); st.State != account.StateOK {
			t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
		}
		return e
	}
	ensureFresh := func(t *testing.T, e *env) {
		t.Helper()
		if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK {
			t.Fatalf("EnsureFresh = %s/%q, %v, want ok and no error: the cached session still stands", st.State, st.Reason, err)
		}
	}
	t.Run("the session was removed", func(t *testing.T) {
		e := cached(t)
		if err := os.Remove(filepath.Join(e.dir, "session.json")); err != nil {
			t.Fatal(err)
		}
		ensureFresh(t, e)
		if _, err := os.Stat(filepath.Join(e.dir, "session.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a high-water write brought session.json back (stat err %v)", err)
		}
	})
	t.Run("the session cannot be read", func(t *testing.T) {
		e := cached(t)
		damaged := e.corrupt()
		ensureFresh(t, e)
		if !mustMtime(t, e.store).Equal(damaged) {
			t.Fatal("a high-water write repaired a file it could not read")
		}
	})
}

// What another process has written is taken in, never undone: its newer session
// is adopted without a write when it already has a current mark, and a mark that
// is ahead of this guard's clock is not lowered to its stale view of it.
func TestAMarkAnotherProcessWroteIsTakenInAndNeverLowered(t *testing.T) {
	t.Run("a session with a current mark is adopted, not rewritten", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
		if st := e.g.Status(); st.State != account.StateOK {
			t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
		}
		seen := mustMtime(t, e.store)
		now := e.f.Clock.Now()
		other, err := account.NewSession(account.HostURL, e.f.Token(accounttest.TokenOptions{Lifetime: 2 * time.Hour}), &account.User{ID: "user-1"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Save(other); err != nil { // the other process's refresh: mark = the new token's iat = now
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil { // within one tick: the poll sees nothing
			t.Fatal(err)
		}
		st, err := e.g.EnsureFresh(context.Background())
		if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(2*time.Hour)) {
			t.Fatalf("EnsureFresh = %s valid until %v, %v, want the other process's token", st.State, st.ValidUntil, err)
		}
		if !mustMtime(t, e.store).Equal(seen) {
			t.Fatal("a mark that was already current was written again")
		}
	})
	t.Run("a mark ahead of the clock is not lowered", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
		if st := e.g.Status(); st.State != account.StateOK {
			t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
		}
		seen := mustMtime(t, e.store)
		ahead := e.f.Clock.Now().Add(time.Minute) // inside the 5 minute allowance
		other := e.session()
		other.HW = ahead
		if err := e.store.Save(other); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil {
			t.Fatal(err)
		}
		if _, err := e.g.EnsureFresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !mustMtime(t, e.store).Equal(seen) || !e.session().HW.Equal(ahead) {
			t.Fatalf("a mark ahead of the clock was rewritten: hw = %v, want %v", e.session().HW, ahead)
		}
	})
}

// The guard knows the file it has just written: a poll that finds it unchanged
// reads nothing, so a write of the mark does not cost a read five seconds later.
func TestTheGuardDoesNotReReadTheMarkItJustWrote(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, with a stale mark
	cs := newCountingStore(account.OpenStore(e.dir, e.seal))
	g := e.guardOver(cs, 0)
	old := e.pinSession()
	if _, err := g.EnsureFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mustMtime(t, e.store).Equal(old) {
		t.Fatal("the mark was not written: this test cannot tell")
	}
	reads := cs.calls("Load")
	e.f.Clock.Advance(account.PollInterval)
	if st := g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
	if n := cs.calls("Load"); n != reads {
		t.Fatalf("%d reads after the poll, %d before: the guard read the file it had just written", n, reads)
	}
}
