package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// These tests are about a clock that runs ahead of monoes.me and is corrected
// while the refresher holds. The holding loop keeps the high-water mark current,
// so the mark is at the old, fast clock, and a clock corrected by more than the
// skew lies before it: locked(clock_rollback). The corrected clock agrees with
// monoes.me, so one refresh repairs the lock at once, and the hold must not stand
// in its way: it ends when the clock goes back before the refresh, which this
// clock does not (it is still after the refresh), so the verdict has to end it.

// holdingOnAFastClock is a refresher that refreshed on a clock lag ahead of
// monoes.me for a token of life, and has held for elapsed since, keeping the mark
// current: the mark in session.json is at the old clock.
func holdingOnAFastClock(t *testing.T, life, lag, elapsed time.Duration) (*env, *loopServer, *account.Guard) {
	t.Helper()
	e := newEnv(t)
	srv := newLoopServer(e)
	srv.set(func(s *loopServer) { s.lag, s.life = lag, life })
	srv.signIn(2*time.Hour, time.Hour) // expired: the first pass refreshes
	g := e.guardOn(srv, loopPoll)
	g.StartRefresher(context.Background())
	waitForCalls(t, srv, 1, "the first refresh")
	expectCalls(t, srv, 1, "the hold")
	start := e.f.Clock.Now()
	waitForHW(t, e, start, "the first pass that holds to write the mark") // the token's iat is lag behind: the mark is stale
	e.f.Clock.Advance(elapsed)
	waitForHW(t, e, start.Add(elapsed), "the refresher that holds to keep the mark current")
	return e, srv, g
}

func TestAClockCorrectedWhileTheRefresherHoldsIsRepairedByOneRefresh(t *testing.T) {
	cases := []struct {
		name               string
		life, lag, elapsed time.Duration
	}{
		{"a token of one hour", time.Hour, 25 * time.Minute, 29 * time.Minute},
		{"the longest token", account.MaxTokenLife, 11*time.Hour + 50*time.Minute, 11*time.Hour + 55*time.Minute},
		{"a clock ten minutes ahead", time.Hour, 10 * time.Minute, 12 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, srv, g := holdingOnAFastClock(t, c.life, c.lag, c.elapsed)
			corrected := e.f.Clock.Now().Add(-c.lag)
			// The set-up is what it must be: the stored session, judged by the corrected
			// clock, is locked, and without the loop's help only the clock coming round to
			// the mark would unlock it.
			if st := account.Evaluate(e.session(), corrected); st.State != account.StateLocked || st.Reason != account.ReasonClockRollback {
				t.Fatalf("the stored session at the corrected time is %s/%q, want locked/clock_rollback: the test sets nothing up", st.State, st.Reason)
			}
			srv.set(func(s *loopServer) { s.lag = 0 }) // monoes.me and the corrected clock agree
			e.f.Clock.Set(corrected)
			eventually(t, "the refresher to repair the clock rollback", func() bool { return g.Status().State == account.StateOK })
			expectCalls(t, srv, 2, "one refresh repairs it, and the next hold begins")
		})
	}
}

func TestAClockCorrectedByLessThanTheSkewIsNoRollbackAndTheHoldGoesOn(t *testing.T) {
	e, srv, g := holdingOnAFastClock(t, time.Hour, 4*time.Minute, 10*time.Minute)
	corrected := e.f.Clock.Now().Add(-4 * time.Minute)
	if st := account.Evaluate(e.session(), corrected); st.State != account.StateOK {
		t.Fatalf("the stored session at the corrected time is %s/%q, want ok: a mark 4 minutes ahead is within the skew", st.State, st.Reason)
	}
	srv.set(func(s *loopServer) { s.lag = 0 })
	e.f.Clock.Set(corrected)
	expectCalls(t, srv, 1, "a clock corrected by less than the skew leaves the hold alone")
	if st := g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
}

func TestTheRefresherStillHoldsWhenTheClockIsMoreThanTheGraceWindowAhead(t *testing.T) {
	// A local clock 25 hours ahead of monoes.me makes every token it is given older
	// than the grace window: the verdict is locked(expired), not clock_rollback, and
	// only a clock that went back ends the hold early. The refresher must still not
	// refresh on every pass.
	e := newEnv(t)
	srv := newLoopServer(e)
	srv.set(func(s *loopServer) { s.lag = 25 * time.Hour })
	srv.signIn(2*time.Hour, time.Hour) // expired: the first pass refreshes
	g := e.guardOn(srv, loopPoll)
	g.StartRefresher(context.Background())
	waitForCalls(t, srv, 1, "the first refresh")
	expectCalls(t, srv, 1, "the hold")
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonExpired {
		t.Fatalf("Status = %s/%q, want locked/expired: the test sets nothing up", st.State, st.Reason)
	}
	e.f.Clock.Advance(30*time.Minute - time.Second)
	expectCalls(t, srv, 1, "a second before the half-life")
	e.f.Clock.Advance(2 * time.Second)
	waitForCalls(t, srv, 2, "the refresh at the half-life")
	expectCalls(t, srv, 2, "once per half-life")
}
