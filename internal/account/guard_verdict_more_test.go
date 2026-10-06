package account_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// Every way the stored session can be, in the verdict the guard gives and in
// what Require does with it, enforced and dormant. Status is the pure Evaluate
// of what is stored; the one thing it adds is that a file that cannot be read
// is invalid, not "not logged in".
func TestStatusIsTheVerdictOfTheStoredSessionAndRequireFollowsIt(t *testing.T) {
	later := func(e *env, d time.Duration) time.Time { return e.f.Clock.Now().Add(d) }
	cases := []struct {
		name   string
		setup  func(*env)
		state  account.State
		reason account.Reason
	}{
		{"a healthy login", func(e *env) { e.signIn(10*time.Minute, time.Hour) }, account.StateOK, ""},
		{"an expired token inside the grace", func(e *env) {
			e.signIn(10*time.Minute, time.Hour)
			e.f.Clock.Advance(time.Hour)
		}, account.StateGrace, account.ReasonUnreachable},
		{"a login past the grace", func(e *env) {
			e.signIn(10*time.Minute, time.Hour)
			e.f.Clock.Advance(24 * time.Hour)
		}, account.StateLocked, account.ReasonExpired},
		{"a refused login", func(e *env) { e.refuse() }, account.StateLocked, account.ReasonRefused},
		{"nothing stored", func(*env) {}, account.StateLocked, account.ReasonNotLoggedIn},
		{"a file that is not a session", func(e *env) { e.corrupt() }, account.StateLocked, account.ReasonInvalid},
		{"a token signed by a key this build does not pin", func(e *env) {
			e.storeToken(e.f.Token(accounttest.TokenOptions{KID: "rotated-key"}), e.f.Clock.Now())
		}, account.StateLocked, account.ReasonKeyUnknown},
		{"a token for another audience", func(e *env) {
			e.storeToken(e.f.Token(accounttest.TokenOptions{Audience: []string{"https://elsewhere.example"}}), e.f.Clock.Now())
		}, account.StateLocked, account.ReasonInvalid},
		{"a token from the future", func(e *env) {
			e.storeToken(e.f.Token(accounttest.TokenOptions{IssuedAt: later(e, 10*time.Minute)}), time.Time{})
		}, account.StateLocked, account.ReasonClockSkew},
		{"a clock set back", func(e *env) {
			sess := e.signIn(10*time.Minute, time.Hour)
			sess.HW = later(e, 2*time.Hour)
			e.save(sess)
		}, account.StateLocked, account.ReasonClockRollback},
	}
	for _, c := range cases {
		for _, dormant := range []bool{false, true} {
			name := c.name
			if dormant {
				name += ", dormant"
			}
			t.Run(name, func(t *testing.T) {
				e := newEnv(t)
				c.setup(e)
				if dormant {
					account.SetEnforceFromForTest(t, time.Time{})
				}
				st := e.g.Status()
				if st.State != c.state || st.Reason != c.reason {
					t.Fatalf("Status = %s/%q, want %s/%q", st.State, st.Reason, c.state, c.reason)
				}
				stored, err := e.store.Load() // no session, for nothing stored and for a file that cannot be read
				want := account.Evaluate(stored, e.f.Clock.Now())
				if err != nil {
					want.Reason = account.ReasonInvalid
				}
				if !reflect.DeepEqual(st, want) {
					t.Fatalf("Status = %s, want the verdict of the stored session, %s", describeStatus(st), describeStatus(want))
				}

				// An ended context changes nothing: Require makes no call that could use it.
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				err = e.g.Require(ctx)
				if allowed := dormant || c.state != account.StateLocked; allowed {
					if err != nil {
						t.Fatalf("Require = %v, want nil: the work may run", err)
					}
					return
				}
				var lr *account.LoginRequiredError
				if !errors.As(err, &lr) || !account.IsLoginRequired(err) || !reflect.DeepEqual(lr.Status, st) {
					t.Fatalf("Require = %v, want a LoginRequiredError carrying %s", err, describeStatus(st))
				}
			})
		}
	}
}
