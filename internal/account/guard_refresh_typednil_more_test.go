package account_test

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// A Refresher that returns a typed-nil error (an error that holds a nil
// *RefusedError or a nil *TransientError) has a bug, but it must not take a
// daemon down. The guard cannot tell what the failure was: it is no refusal (the
// account is not locked out for a bug of ours), so it is an ordinary failure, the
// grace applies and the refresh token stays where it is.
func TestATypedNilRefreshErrorIsAnOrdinaryFailureAndNeverARefusal(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"a nil *RefusedError", (*account.RefusedError)(nil)},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(56*time.Minute, time.Hour) // due: four minutes left
			e.ref.set(func(r *fakeRefresher) { r.err = c.err })

			var st account.Status
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("EnsureFresh panicked on %s: %v", c.name, r)
					}
				}()
				st, err = e.g.EnsureFresh(context.Background())
			}()
			if err != nil || st.State != account.StateOK {
				t.Fatalf("EnsureFresh = %s/%q, %v, want ok and no error: the token has four minutes left", st.State, st.Reason, err)
			}
			if n := e.ref.calls.Load(); n != 1 {
				t.Fatalf("%d network refreshes, want 1", n)
			}
			sess := e.session()
			if sess.State != "" || sess.LastResult != string(account.ReasonUnreachable) || sess.AccessToken == "" {
				t.Fatalf("stored session: %s, want it kept, not refused, with the attempt recorded as unreachable", describe(sess))
			}
			if rt, err := e.store.LoadRefresh(); err != nil || rt != "rt-1" {
				t.Fatalf("the refresh token is %q (err %v), want it untouched: a typed nil says nothing about the account", rt, err)
			}

			// An ordinary failure goes into the negative cache: no second call at once.
			e.f.Clock.Advance(time.Second)
			if _, err := e.g.EnsureFresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n := e.ref.calls.Load(); n != 1 {
				t.Fatalf("%d network refreshes after a second call a second later, want 1: the failure was not recorded", n)
			}
		})
	}
}
