package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save"}) {
			t.Fatalf("writes = %v, want the attempt only: a refresh token that did not change is not written", got)
		}
		fs.failSaveRefresh = false // the key store works again: the next attempt rotates the token and writes the new one
		e.f.Clock.Advance(time.Minute)
		if st, err := g.EnsureFresh(ctx); err != nil || st.State != account.StateOK {
			t.Fatalf("the next attempt = %s/%q, %v, want ok with the token that was never rotated", st.State, st.Reason, err)
		}
	})
	t.Run("a new refresh token that cannot be written removes the dead one", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true}
		answers := 0
		g := e.answeringFirstWith(fs, &account.TokenSet{RefreshToken: "rt-2"}, true, &answers)
		st, err := g.EnsureFresh(ctx)
		if !errors.Is(err, account.ErrKeyringUnavailable) || st.State != account.StateGrace || st.Reason != account.ReasonKeyringUnavailable {
			t.Fatalf("EnsureFresh = %s/%q, %v, want grace/keyring_unavailable and the write error", st.State, st.Reason, err)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "refresh.enc")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the dead refresh token is still on disk (stat err %v)", err)
		}
		for i := 0; i < 3; i++ {
			e.f.Clock.Advance(2 * time.Minute)
			if st, _ := g.EnsureFresh(ctx); st.State != account.StateGrace || st.Reason == account.ReasonRefused {
				t.Fatalf("Status = %s/%q, want grace and never refused", st.State, st.Reason)
			}
		}
		if answers != 1 || e.ref.calls.Load() != 0 {
			t.Fatalf("%d answers and %d network refreshes after the first: the dead refresh token was presented again", answers, e.ref.calls.Load())
		}
	})
}
