package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

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
