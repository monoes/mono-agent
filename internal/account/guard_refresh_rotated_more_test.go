package account_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// monoes.me rotates the refresh token by answering, so the one on disk is dead
// as soon as the server has answered, whatever the answer holds. An answer with
// a refresh token and no access token is a server error for the session, but the
// refresh token in it must still be kept: the next attempt would otherwise
// present the dead one, which the server takes for theft and answers by revoking
// every refresh token of the account.

// answeringFirstWith makes a guard over store whose first answer is first, and
// whose later answers are the fake server's. A server that rotates has rotated
// by the time the first answer is given.
func (e *env) answeringFirstWith(store account.Store, first *account.TokenSet, rotates bool, answers *int) *account.Guard {
	e.t.Helper()
	broken := refresherFunc(func(ctx context.Context, rt string) (*account.TokenSet, error) {
		*answers++
		if *answers > 1 {
			if rotates && rt == "rt-1" {
				return first, nil // a retry inside the reuse window: monoes.me repeats the answer it gave
			}
			return e.ref.Refresh(ctx, rt)
		}
		if rotates {
			e.ref.set(func(r *fakeRefresher) { r.valid, r.seq = "rt-2", 1 })
		}
		return first, nil
	})
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: broken, Now: e.f.Clock.Now})
	e.t.Cleanup(g.Close)
	return g
}

func TestAnAnswerWithARefreshTokenAndNoAccessToken(t *testing.T) {
	ctx := context.Background()
	t.Run("a new refresh token is kept, and the next attempt presents it", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour) // expired: due
		answers := 0
		g := e.answeringFirstWith(account.OpenStore(e.dir, e.seal), &account.TokenSet{RefreshToken: "rt-2"}, true, &answers)
		st, err := g.EnsureFresh(ctx)
		if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
			t.Fatalf("EnsureFresh = %s/%q, %v, want grace/server_error", st.State, st.Reason, err)
		}
		if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
			t.Fatal("the rotated refresh token was dropped: the one on disk is dead")
		}
		e.f.Clock.Advance(time.Minute) // the negative cache is over
		st, err = g.EnsureFresh(ctx)
		if err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
			t.Fatalf("the next attempt = %s/%q, %v with %d network refreshes, want ok: it must present the rotated token, not the dead one", st.State, st.Reason, err, e.ref.calls.Load())
		}
	})
	t.Run("the same refresh token is not written again", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true} // a write of the token would fail, and then delete the good one
		answers := 0
		g := e.answeringFirstWith(fs, &account.TokenSet{RefreshToken: "rt-1"}, false, &answers)
		st, err := g.EnsureFresh(ctx)
		if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
			t.Fatalf("EnsureFresh = %s/%q, %v, want grace/server_error", st.State, st.Reason, err)
		}
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "Save"}) {
			t.Fatalf("writes = %v, want the marker and the attempt only: a refresh token that did not change is not written", got)
		}
		fs.failSaveRefresh = false // the key store works again: the next attempt rotates the token and writes the new one
		e.f.Clock.Advance(time.Minute)
		if st, err := g.EnsureFresh(ctx); err != nil || st.State != account.StateOK {
			t.Fatalf("the next attempt = %s/%q, %v, want ok with the token that was never rotated", st.State, st.Reason, err)
		}
	})
	t.Run("a new refresh token that cannot be written keeps the old one and the marker", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		start := e.f.Clock.Now()
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true}
		answers := 0
		g := e.answeringFirstWith(fs, &account.TokenSet{RefreshToken: "rt-2"}, true, &answers)
		st, err := g.EnsureFresh(ctx)
		if !errors.Is(err, account.ErrKeyringUnavailable) || st.State != account.StateGrace || st.Reason != account.ReasonKeyringUnavailable {
			t.Fatalf("EnsureFresh = %s/%q, %v, want grace/keyring_unavailable and the write error", st.State, st.Reason, err)
		}
		// The answer was received but its refresh token was not stored: a lost answer
		// (A24, ruling d). The old token stays, with the marker that says it may be dead.
		if rt, err := e.store.LoadRefresh(); err != nil || rt != "rt-1" {
			t.Fatalf("refresh.enc holds %q (%v), want the old token: the marker covers it and a retry inside the window needs it", rt, err)
		}
		if got := e.pendingOn(); !got.Equal(start) {
			t.Fatalf("pending_since = %v, want %v", got, start)
		}
		// The key store works again and a minute has passed: the retry presents the old token,
		// monoes.me repeats its answer, and the rotated token is stored at last, so the attempt
		// after that presents it and not the dead one.
		fs.mu.Lock()
		fs.failSaveRefresh = false
		fs.mu.Unlock()
		e.f.Clock.Advance(time.Minute)
		if st, err := g.EnsureFresh(ctx); err != nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
			t.Fatalf("the retry = %s/%q, %v, want grace/server_error (the answer has no access token) and no error", st.State, st.Reason, err)
		}
		if rt, _ := e.store.LoadRefresh(); rt != "rt-2" || e.rawPending() != "" {
			t.Fatalf("refresh.enc holds %q with pending %q, want the rotated token and no marker", rt, e.rawPending())
		}
		e.f.Clock.Advance(time.Minute)
		if st, err := g.EnsureFresh(ctx); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 || answers != 3 {
			t.Fatalf("the next attempt = %s/%q, %v with %d answers and %d network refreshes, want ok with the rotated token presented (the dead one is not)", st.State, st.Reason, err, answers, e.ref.calls.Load())
		}
	})
}
