package account

import (
	"context"
	"errors"
	"testing"
	"time"
)

// keepLastAttempt is what the refresher does when its clock went back while a marker is pending
// (A24): it raises the session's last attempt to the reading the clock showed before, under the
// lock, on the session read again there, and only ever raises it. A session with no marker is
// left alone, an unconfirmed one included, and a lock that cannot be taken writes nothing.
func TestKeepLastAttemptRaisesTheEvidenceOfAMarkerAndNothingElse(t *testing.T) {
	cases := []struct {
		name    string
		pending bool
		result  string
		last    time.Duration // the stored last attempt, from at
		written bool
	}{
		{"a marker, the last attempt before the reading", true, "unreachable", -time.Minute, true},
		{"a marker, the last attempt at the reading", true, "unreachable", 0, false},
		{"a marker, the last attempt after the reading", true, "unreachable", time.Minute, false},
		{"no marker", false, "unreachable", -time.Minute, false},
		{"unconfirmed, no marker", false, string(ReasonUnconfirmed), -time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			sess := r.signIn(10 * time.Minute)
			at := r.clock.Now()
			sess.LastAttempt, sess.LastResult = at.Add(c.last), c.result
			if c.pending {
				sess.PendingSince = at.Add(-2 * time.Minute)
			}
			if err := r.store.Save(sess); err != nil {
				t.Fatal(err)
			}
			ws := &orderStore{Store: r.store}
			g := NewGuard(GuardOptions{Store: ws, Refresher: r.srv, Now: r.clock.Now})
			t.Cleanup(g.Close)
			g.keepLastAttempt(at)
			if got := len(ws.writes()) == 1; got != c.written {
				t.Fatalf("writes = %v, want a write: %t", ws.writes(), c.written)
			}
			want := sess.LastAttempt
			if c.written {
				want = at
			}
			got, err := r.store.Load()
			if err != nil || !got.LastAttempt.Equal(want) || !got.PendingSince.Equal(sess.PendingSince) || got.LastResult != c.result || !got.HW.Equal(sess.HW) {
				t.Fatalf("stored session = %s (%v), want the last attempt at %v and nothing else changed", sessionFacts(got), err, want)
			}
			if c.written {
				if cached, _ := g.cached(); cached == nil || !cached.LastAttempt.Equal(at) {
					t.Fatal("the raised last attempt was not taken in")
				}
			}
		})
	}
}

// lockFailStore is a store whose lock cannot be taken.
type lockFailStore struct{ Store }

func (lockFailStore) Lock(context.Context) (func(), error) {
	return nil, errors.New("simulated: lock held")
}

func TestKeepLastAttemptWritesNothingWithoutTheLock(t *testing.T) {
	r := newRig(t)
	sess := r.signIn(10 * time.Minute)
	at := r.clock.Now()
	sess.PendingSince, sess.LastAttempt = at.Add(-2*time.Minute), at.Add(-time.Minute)
	if err := r.store.Save(sess); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(GuardOptions{Store: lockFailStore{r.store}, Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(g.Close)
	g.keepLastAttempt(at)
	if got, err := r.store.Load(); err != nil || !got.LastAttempt.Equal(sess.LastAttempt) {
		t.Fatalf("stored session = %s (%v), want it as it was: no write without the lock", sessionFacts(got), err)
	}
}
