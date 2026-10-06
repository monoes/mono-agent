package account_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// The refresher and a grant whose answer was lost (A24). Its own lost answers are retried on
// its schedule of 30, 60, 120 and 240 s, which puts the retries at about +30, +90 and +210 s
// from the first send, inside the window of 240 s. But a marker that ANOTHER process left (a CLI
// whose answer was lost while the daemon held after a refresh or waited out a long backoff)
// would be looked at only after that hold or backoff, past the window, and dropped although
// it was recoverable: so a marker that this loop did not write ends a hold and caps the wait for
// the next attempt at 30 s. The loop tells its own marker from a foreign one by remembering the
// stamp of its own last unsettled attempt.

// waitForGrants waits, in real time, until the net has carried at least n grants.
func waitForGrants(t *testing.T, net *flakyNet, n int, what string) {
	t.Helper()
	eventually(t, what, func() bool { return net.grants() >= n })
}

// expectGrants waits quietFor and then fails unless the net has carried exactly n grants.
func expectGrants(t *testing.T, net *flakyNet, n int, what string) {
	t.Helper()
	quiet()
	if got := net.grants(); got != n {
		t.Fatalf("%s: %d grants, want %d", what, got, n)
	}
}

// loopMachine is an install whose token is age old (an hour of life) with a refresher running
// over the windowServer through the flakyNet.
func loopMachine(t *testing.T, age time.Duration) (*env, *windowServer, *flakyNet, *account.Guard) {
	t.Helper()
	e := newEnv(t)
	e.signIn(age, time.Hour)
	srv := newWindowServer(e.f, 0, "rt-1")
	net := &flakyNet{srv: srv}
	return e, srv, net, e.guardOn(net, loopPoll)
}

// anotherProcessLosesAnAnswer is a CLI that sent a grant with the refresh token on disk and lost the
// answer: monoes.me has rotated, and the marker is on disk. It does what that process does, under the
// session lock.
func anotherProcessLosesAnAnswer(t *testing.T, e *env, srv *windowServer) {
	t.Helper()
	e.underLock(func() {
		rt, err := e.store.LoadRefresh()
		if err != nil || rt == "" {
			t.Fatalf("the other process found no refresh token (%v)", err)
		}
		if _, err := srv.Refresh(context.Background(), rt); err != nil {
			t.Fatalf("monoes.me refused the other process: %v", err)
		}
		e.leavePending(e.f.Clock.Now())
	})
}

// The loop's own lost answers keep the schedule of the backoff: a cap would turn the retries at
// +31 s, +92 s and +213 s into one every 30 s, which is safe inside the window but not what the
// schedule says. The stamp is the first one throughout, and the third retry, 213 s after the first
// send, is inside the window and recovers.
func TestTheRefreshersOwnLostAnswersAreRetriedOnTheBackoffScheduleAndRecoverInsideTheWindow(t *testing.T) {
	e, srv, net, g := loopMachine(t, 2*time.Hour) // in grace: every pass is due
	net.then(lost, lost, lost)
	g.StartRefresher(context.Background())
	waitForGrants(t, net, 1, "the first attempt") // its answer is lost: the marker is written, the next attempt is 30 s away
	expectGrants(t, net, 1, "the first lost answer")
	first := e.rawPending()
	if first == "" {
		t.Fatal("no marker after the first lost answer")
	}

	e.f.Clock.Advance(29 * time.Second)
	expectGrants(t, net, 1, "29 seconds after the first attempt")
	e.f.Clock.Advance(2 * time.Second) // +31 s
	waitForGrants(t, net, 2, "the first retry")
	expectGrants(t, net, 2, "the first retry, lost again")
	e.f.Clock.Advance(30 * time.Second) // +61 s: a loop that capped its own marker would retry here
	expectGrants(t, net, 2, "61 seconds after the first attempt, 30 after the retry: the loop's own marker keeps the backoff of 60 seconds")
	e.f.Clock.Advance(31 * time.Second) // +92 s
	waitForGrants(t, net, 3, "the second retry")
	expectGrants(t, net, 3, "the second retry, lost again")
	e.f.Clock.Advance(119 * time.Second) // +211 s
	expectGrants(t, net, 3, "a second before the backoff of 120 seconds ends")
	e.f.Clock.Advance(2 * time.Second) // +213 s: inside the 240 s window
	waitForGrants(t, net, 4, "the third retry")
	eventually(t, "the third retry to be answered and stored", func() bool { return e.rawPending() == "" && e.session().LastResult == "ok" })
	if got := net.grants(); got != 4 || srv.isRevoked() || !reflect.DeepEqual(srv.presented(), []string{"rt-1", "rt-1", "rt-1", "rt-1"}) {
		t.Fatalf("%d grants (revoked %t, presented %v), want four presentations of the same token, all inside the window", got, srv.isRevoked(), srv.presented())
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-rotated-1" {
		t.Fatalf("refresh.enc holds %q, want the rotated token", rt)
	}
}

// A marker that another process left during the hold after a refresh is retried within 30 s of
// the loop learning of it, not when the hold (half an hour) ends.
func TestAMarkerAnotherProcessLeftDuringTheHoldIsRetriedWithinThirtySecondsAndRecovers(t *testing.T) {
	e, srv, net, g := loopMachine(t, 40*time.Minute) // past its half-life: the first pass refreshes
	g.StartRefresher(context.Background())
	waitForGrants(t, net, 1, "the first refresh")
	expectGrants(t, net, 1, "the hold")
	waitForHW(t, e, e.f.Clock.Now(), "the first pass that holds to write the mark") // a write of the loop must not race the test's

	anotherProcessLosesAnAnswer(t, e, srv)
	e.f.Clock.Advance(30 * time.Second) // the hold has 30 minutes to go
	waitForGrants(t, net, 2, "the retry of the marker the other process left")
	eventually(t, "the retry to be answered and stored", func() bool { return e.rawPending() == "" })
	if srv.isRevoked() || !reflect.DeepEqual(srv.presented(), []string{"rt-1", "rt-rotated-1", "rt-rotated-1"}) {
		t.Fatalf("monoes.me was presented %v (revoked %t), want the token the other process presented, once more inside the window", srv.presented(), srv.isRevoked())
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-rotated-2" {
		t.Fatalf("refresh.enc holds %q, want the token the repeated answer brought", rt)
	}
}

// The same during a long backoff: the loop that waits 240 s for its next attempt (four failures that
// are settled, so with no marker of its own) is not left waiting out the window.
func TestAMarkerAnotherProcessLeftDuringALongBackoffIsRetriedWithinThirtySecondsAndRecovers(t *testing.T) {
	e, srv, net, g := loopMachine(t, 2*time.Hour)
	net.then(unsent, unsent, unsent, unsent)
	g.StartRefresher(context.Background())
	waitForGrants(t, net, 1, "the first attempt")
	expectGrants(t, net, 1, "the first failure") // the next is 30 s away
	e.f.Clock.Advance(31 * time.Second)
	waitForGrants(t, net, 2, "the second attempt")
	expectGrants(t, net, 2, "the second failure") // 60 s
	e.f.Clock.Advance(61 * time.Second)
	waitForGrants(t, net, 3, "the third attempt")
	expectGrants(t, net, 3, "the third failure") // 120 s
	e.f.Clock.Advance(121 * time.Second)
	waitForGrants(t, net, 4, "the fourth attempt")
	expectGrants(t, net, 4, "the fourth failure") // 240 s: the next attempt is 4 minutes away
	if e.rawPending() != "" {
		t.Fatal("settled failures left a marker")
	}

	anotherProcessLosesAnAnswer(t, e, srv)
	e.f.Clock.Advance(30 * time.Second) // 210 s of the backoff to go
	waitForGrants(t, net, 5, "the retry of the marker the other process left")
	eventually(t, "the retry to be answered and stored", func() bool { return e.rawPending() == "" && e.session().LastResult == "ok" })
	if srv.isRevoked() || !reflect.DeepEqual(srv.presented(), []string{"rt-1", "rt-1"}) {
		t.Fatalf("monoes.me was presented %v (revoked %t), want the token the other process presented, once more inside the window", srv.presented(), srv.isRevoked())
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-rotated-1" {
		t.Fatalf("refresh.enc holds %q, want the token the repeated answer brought", rt)
	}
}

// (j): the loop's own lost answers end, after the schedule, in the drop: no call at the dropping
// pass, the dead token never presented, the account not revoked. A loop that goes on running after the
// drop finds no refresh token on every pass and must record nothing, so that the grace keeps saying
// unconfirmed (and the end of it says locked(unconfirmed), not expired); a new sign-in replaces it all.
func TestTheRefresherDropsATokenItCouldNotConfirmAndTheGraceKeepsSayingWhy(t *testing.T) {
	e, srv, net, g := loopMachine(t, 2*time.Hour) // in grace; its grace ends 22 hours from now
	net.then(lost, lost, lost, lost)
	g.StartRefresher(context.Background())
	start := e.f.Clock.Now()
	waitForGrants(t, net, 1, "the first attempt")
	expectGrants(t, net, 1, "the first lost answer")
	for _, step := range []struct {
		advance time.Duration
		grants  int
	}{{31 * time.Second, 2}, {61 * time.Second, 3}, {121 * time.Second, 4}} { // +31 s, +92 s, +213 s
		e.f.Clock.Advance(step.advance)
		waitForGrants(t, net, step.grants, "the retry")
		expectGrants(t, net, step.grants, "the retry, lost again")
	}
	e.f.Clock.Advance(241 * time.Second) // +454 s: the fourth retry of the schedule
	eventually(t, "the drop", func() bool { return e.session().LastResult == "unconfirmed" })
	quiet()
	if got := net.grants(); got != 4 || srv.isRevoked() {
		t.Fatalf("%d grants (revoked %t), want the four that were lost and no more: the pass that drops the token sends nothing", got, srv.isRevoked())
	}
	if rt, _ := e.store.LoadRefresh(); rt != "" {
		t.Fatalf("refresh.enc holds %q after the drop", rt)
	}
	if st := g.Status(); st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("Status = %s/%q, want grace/unconfirmed", st.State, st.Reason)
	}

	// The loop goes on: every pass finds no token, and none writes anything.
	dropped := describe(e.session())
	for i := 0; i < 3; i++ {
		e.f.Clock.Advance(6 * time.Minute) // past its backoff
		quiet()
	}
	if got := describe(e.session()); got != dropped {
		t.Fatalf("a pass of the loop changed the session after the drop: %s, was %s", got, dropped)
	}
	if st := g.Status(); st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("Status = %s/%q, want the reason to stay unconfirmed", st.State, st.Reason)
	}
	// The end of the grace says why.
	e.f.Clock.Set(start.Add(-2*time.Hour + 24*time.Hour))
	if st := g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("Status once the grace is over = %s/%q, want locked/unconfirmed", st.State, st.Reason)
	}
	// A new sign-in replaces the dropped session.
	e.underLock(func() { e.signIn(10*time.Minute, time.Hour) })
	e.f.Clock.Advance(loopPoll)
	eventually(t, "the sign-in", func() bool { return g.Status().State == account.StateOK })
	if sess := e.session(); sess.LastResult != "ok" || !sess.PendingSince.IsZero() {
		t.Fatalf("stored session = %s, want the login's session", describe(sess))
	}
}

// A stamp that the loop once wrote itself is not remembered for ever: when its marker is gone, a
// marker of another process that happens to carry the very same stamp is a foreign one again.
func TestAForeignMarkerWithTheStampOfAnEarlierOwnMarkerIsStillForeign(t *testing.T) {
	e, srv, net, g := loopMachine(t, 2*time.Hour) // in grace: the first pass is due
	net.then(lost)
	stamp := e.f.Clock.Now()
	g.StartRefresher(context.Background())
	waitForGrants(t, net, 1, "the first attempt") // its answer is lost: the marker, with the stamp of this instant, is the loop's own
	expectGrants(t, net, 1, "the first lost answer")
	e.f.Clock.Advance(31 * time.Second)
	waitForGrants(t, net, 2, "the retry")
	eventually(t, "the retry to be answered and stored", func() bool { return e.rawPending() == "" && e.session().LastResult == "ok" })
	expectGrants(t, net, 2, "the hold after the refresh") // the loop has noticed that the marker is gone

	e.underLock(func() { e.leavePending(stamp) }) // another process, whose grant went out at the very instant of the loop's first one
	e.f.Clock.Advance(30 * time.Second)
	waitForGrants(t, net, 3, "the retry of the marker the other process left")
	if srv.isRevoked() {
		t.Fatal("the account was revoked")
	}
}
