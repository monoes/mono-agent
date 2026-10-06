package account_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// A Refresher that returns a typed-nil error, an error that holds a nil
// *RefusedError or a nil *TransientError, has a bug (a `var terr *TransientError`
// that is set only on failure and returned always), but it must not take a daemon
// down or lock an account out. What it meant is no error: the guard reads it so.
// A typed nil that another error wraps (fmt.Errorf with %w) is not seen through,
// and is an ordinary failure: the nil-receiver and nil-target guards of the error
// types and of the switch in refreshUnderLock are what keep that one from
// panicking.

var typedNils = []struct {
	name string
	err  error
}{
	{"a nil *RefusedError", (*account.RefusedError)(nil)},
	{"a nil *TransientError", (*account.TransientError)(nil)},
}

// typedNilBesideAnAnswer is the fake monoes.me that makes that mistake: a success
// comes back with its token set and, beside it, a typed-nil error.
type typedNilBesideAnAnswer struct {
	inner  account.Refresher
	nilErr error
}

func (r typedNilBesideAnAnswer) Refresh(ctx context.Context, refreshToken string) (*account.TokenSet, error) {
	ts, err := r.inner.Refresh(ctx, refreshToken)
	if err != nil {
		return ts, err
	}
	return ts, r.nilErr
}

// The server rotates the refresh token on every use. A guard that dropped the set
// that came with a typed nil would keep presenting the old one, and monoes.me takes
// that for theft and revokes every refresh token of the account.
func TestATypedNilErrorBesideATokenSetIsTheSuccessItMeant(t *testing.T) {
	for _, c := range typedNils {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(56*time.Minute, time.Hour) // due: four minutes left
			t0 := e.f.Clock.Now()
			g := account.NewGuard(account.GuardOptions{
				Store: account.OpenStore(e.dir, e.seal), Refresher: typedNilBesideAnAnswer{inner: e.ref, nilErr: c.err}, Now: e.f.Clock.Now,
			})
			t.Cleanup(g.Close)
			ctx := context.Background()

			st, err := g.EnsureFresh(ctx)
			if err != nil || st.State != account.StateOK || !st.ValidUntil.Equal(t0.Add(time.Hour)) {
				t.Errorf("EnsureFresh = %s valid until t0%+v, %v, want ok with the new token (t0+1h): the answer was dropped", st.State, st.ValidUntil.Sub(t0), err)
			}
			var server string
			e.ref.set(func(r *fakeRefresher) { server = r.valid })
			if rt, err := e.store.LoadRefresh(); err != nil || rt != server {
				t.Errorf("the stored refresh token is the server's current one: %t (err %v): the server rotated it and it was not stored", rt == server, err)
			}

			// What dropping it costs: the token is near its end again, the guard presents
			// the refresh token it holds, and the server refuses a token it has retired.
			e.f.Clock.Advance(56 * time.Minute)
			st, err = g.EnsureFresh(ctx)
			if err != nil || st.State != account.StateOK {
				t.Fatalf("EnsureFresh an hour on = %s/%q, %v, want ok: the refresh token it presented was a retired one", st.State, st.Reason, err)
			}
			if n := e.ref.calls.Load(); n != 2 {
				t.Fatalf("%d network refreshes, want 2", n)
			}
		})
	}
}

// anOrdinaryFailure makes the fake monoes.me answer with err and no token set, and
// checks what the guard does with it: the session is kept and not refused, the
// attempt is recorded as want, the refresh token stays where it is, and the
// negative cache holds.
func anOrdinaryFailure(t *testing.T, err error, want account.Reason) {
	t.Helper()
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due: four minutes left
	e.ref.set(func(r *fakeRefresher) { r.err = err })

	var st account.Status
	var callErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("EnsureFresh panicked: %v", r)
			}
		}()
		st, callErr = e.g.EnsureFresh(context.Background())
	}()
	if callErr != nil || st.State != account.StateOK {
		t.Fatalf("EnsureFresh = %s/%q, %v, want ok and no error: the token has four minutes left", st.State, st.Reason, callErr)
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes, want 1", n)
	}
	sess := e.session()
	if sess.State != "" || sess.LastResult != string(want) || sess.AccessToken == "" {
		t.Fatalf("stored session: %s, want it kept, not refused, with the attempt recorded as %s", describe(sess), want)
	}
	if rt, err := e.store.LoadRefresh(); err != nil || rt != "rt-1" {
		t.Fatalf("the stored refresh token is still the first one: %t (err %v), want it untouched: it says nothing about the account", rt == "rt-1", err)
	}

	// A failure goes into the negative cache: no second call at once.
	e.f.Clock.Advance(time.Second)
	if _, err := e.g.EnsureFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.ref.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes after a second call a second later, want 1: the failure was not recorded", n)
	}
}

// With no token set it is an answer with nothing in it, which is the server's
// fault, as for any other answer with nothing in it (spec D27).
func TestATypedNilErrorWithNoTokenSetIsAnAnswerWithNothingInIt(t *testing.T) {
	for _, c := range typedNils {
		t.Run(c.name, func(t *testing.T) { anOrdinaryFailure(t, c.err, account.ReasonServerError) })
	}
}

// Wrapped, it is a failure like any other that is not a refusal: grace, unreachable.
func TestATypedNilErrorInsideAnotherErrorIsAnOrdinaryFailureAndNeverARefusal(t *testing.T) {
	for _, c := range typedNils {
		t.Run(c.name, func(t *testing.T) {
			anOrdinaryFailure(t, fmt.Errorf("refresh: %w", c.err), account.ReasonUnreachable)
		})
	}
}
