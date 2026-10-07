package account_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// deleteFailsStore is a store whose refresh token cannot be removed.
type deleteFailsStore struct{ account.Store }

func (deleteFailsStore) DeleteRefresh() error { return errors.New("simulated: permission denied") }

func refusedBy(description string) func(*fakeRefresher) {
	return func(r *fakeRefresher) { r.err = &account.RefusedError{Description: description} }
}

// A refresh changes the tokens and the clock fields of the session, and nothing
// that says whose it is or where it came from.
func TestARefreshKeepsTheUserAndTheHostOfTheSession(t *testing.T) {
	e := newEnv(t)
	sess := e.signIn(2*time.Hour, time.Hour)
	sess.Host = "https://monoes.example.test"
	sess.User = &account.User{ID: "user-1", Email: "u@example.test", Username: "uu"}
	e.save(sess)
	if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK {
		t.Fatalf("EnsureFresh = %s/%q, %v, want ok", st.State, st.Reason, err)
	}
	got := e.session()
	if got.Host != sess.Host || got.User == nil || *got.User != *sess.User {
		t.Fatalf("stored session = %s, want the host and the user it had", describe(got))
	}
}

// monoes.me rotates the refresh token on every use. An answer that names the very token
// that was presented says it was not rotated: a definitive answer, the session is stored and
// the token stays where it is. An answer that names NO refresh token cannot mean that: it
// says nothing about the one that was presented, which may be rotated, so its outcome is
// unknown (A24): nothing of it is stored, the marker and the old token stay, and the attempt
// is a server error (the retry inside the window gets the same answer; after it the token is
// dropped). Either way the key store, which cannot seal here, is not asked to.
func TestAServerThatAnswersWithoutANewRefreshTokenKeepsTheOldOne(t *testing.T) {
	cases := []struct {
		name, answer string
		state        account.State
		reason       account.Reason
		marker       bool
	}{
		{"no refresh token in the answer: an outcome that is unknown", "", account.StateGrace, account.ReasonServerError, true},
		{"the same refresh token in the answer: not rotated", "rt-1", account.StateOK, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour)
			fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true}
			fixed := refresherFunc(func(context.Context, string) (*account.TokenSet, error) {
				return &account.TokenSet{AccessToken: e.f.Token(accounttest.TokenOptions{}), RefreshToken: c.answer}, nil
			})
			g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: fixed, Now: e.f.Clock.Now})
			t.Cleanup(g.Close)
			st, err := g.EnsureFresh(context.Background())
			if err != nil || st.State != c.state || st.Reason != c.reason {
				t.Fatalf("EnsureFresh = %s/%q, %v, want %s/%q", st.State, st.Reason, err, c.state, c.reason)
			}
			if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "Save"}) {
				t.Fatalf("writes = %v, want the marker and then the session or the record of the attempt: a refresh token that did not change is not written", got)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatal("the refresh token the answer did not replace was lost")
			}
			if got := e.rawPending() != ""; got != c.marker {
				t.Fatalf("a marker on disk: %t, want %t (stored session %s)", got, c.marker, describe(e.session()))
			}
		})
	}
}

// An answer is a failure, never a crash, when it carries no token set at all,
// or when its errors are wrapped by the refresher that returned them.
func TestAnswersThatAreEmptyOrWrappedAreStillClassified(t *testing.T) {
	cases := []struct {
		name   string
		answer func() (*account.TokenSet, error)
		state  account.State
		reason account.Reason
	}{
		{"no token set at all", func() (*account.TokenSet, error) { return nil, nil }, account.StateGrace, account.ReasonServerError},
		{"a wrapped refusal", func() (*account.TokenSet, error) {
			return nil, fmt.Errorf("token endpoint: %w", &account.RefusedError{Description: "revoked"})
		}, account.StateLocked, account.ReasonRefused},
		{"a wrapped server error", func() (*account.TokenSet, error) {
			return nil, fmt.Errorf("token endpoint: %w", transient(account.ReasonServerError))
		}, account.StateGrace, account.ReasonServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour)
			answering := refresherFunc(func(context.Context, string) (*account.TokenSet, error) { return c.answer() })
			g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(e.dir, e.seal), Refresher: answering, Now: e.f.Clock.Now})
			t.Cleanup(g.Close)
			st, err := g.EnsureFresh(context.Background())
			if err != nil || st.State != c.state || st.Reason != c.reason {
				t.Fatalf("EnsureFresh = %s/%q, %v, want %s/%q", st.State, st.Reason, err, c.state, c.reason)
			}
		})
	}
}

// A refusal keeps the user for the message the CLI prints, records the attempt,
// raises the high-water mark to now (a refused session keeps the enforcement
// evidence), and keeps at most 200 bytes of what the server said.
func TestARefusalKeepsWhatTheMessageNeedsAndTruncatesTheReason(t *testing.T) {
	for _, n := range []int{0, 199, 200, 201, 250} {
		t.Run(fmt.Sprintf("a reason of %d bytes", n), func(t *testing.T) {
			e := newEnv(t)
			sess := e.signIn(2*time.Hour, time.Hour)
			sess.User = &account.User{ID: "user-1", Email: "u@example.test", Username: "uu"}
			e.save(sess)
			reason := strings.Repeat("x", n)
			e.ref.set(refusedBy(reason))
			now := e.f.Clock.Now()
			if _, err := e.g.EnsureFresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := reason
			if n > 200 {
				want = reason[:200]
			}
			got := e.session()
			if got.Reason != want || got.User == nil || *got.User != *sess.User || got.Host != sess.Host || !got.LastAttempt.Equal(now) || !got.HW.Equal(now) {
				t.Fatalf("stored session = %s (reason %d bytes), want %d bytes of reason, the user, the host, the mark raised to now %v (A24: a refused session keeps the enforcement evidence) and the attempt %v", describe(got), len(got.Reason), len(want), now, now)
			}
		})
	}
}

// The marker is written before the refresh token is deleted: a crash in between
// leaves a marker beside a dead token, still locked, never a live-looking
// session. And the token is deleted only once the marker is saved: a marker that
// cannot be written leaves the refused token where it is, so that the next
// process learns the refusal again.
func TestARefusalWritesTheMarkerBeforeItDeletesTheRefreshToken(t *testing.T) {
	ctx := context.Background()
	t.Run("the order of the two writes", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		e.ref.set(refusedBy("revoked"))
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal)}
		g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		if _, err := g.EnsureFresh(ctx); err != nil {
			t.Fatal(err)
		}
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "Save", "DeleteRefresh"}) {
			t.Fatalf("writes = %v, want the pending marker of the grant, then the refusal's marker, then the delete", got)
		}
	})
	t.Run("a marker that cannot be written is reported and the refresh token stays", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		e.ref.set(refusedBy("revoked"))
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true, okSaves: 1} // the grant's marker is written, the refusal's is not
		g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		st, err := g.EnsureFresh(ctx)
		if err == nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused and the write error", st.State, st.Reason, err)
		}
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "Save"}) {
			t.Fatalf("writes = %v, want the grant's marker and the refusal's marker that failed: the refresh token goes once the refusal is saved", got)
		}
		if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
			t.Fatal("the refresh token was deleted although the marker was not saved: the disk then holds a live-looking session with no token")
		}
	})
	t.Run("a refresh token that cannot be deleted is reported, the marker stays", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		e.ref.set(refusedBy("revoked"))
		g := account.NewGuard(account.GuardOptions{Store: deleteFailsStore{account.OpenStore(e.dir, e.seal)}, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		st, err := g.EnsureFresh(ctx)
		if err == nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused and the delete error", st.State, st.Reason, err)
		}
		if e.session().State != "refused" {
			t.Fatal("the marker was not written")
		}
	})
}

// A refusal whose marker cannot be saved (a full disk, a read-only session.json)
// must not leave the other processes working. The disk still holds the old
// session: with the refresh token gone as well, the next process would find a due
// session with no token, record keyring_unavailable and keep the grace for up to
// 24 hours, and the process that was refused would forget the refusal too once
// any process wrote session.json. With the token kept, the next process presents
// it, is refused again (nothing is left to revoke) and writes the marker.
func TestARefusalWhoseMarkerCouldNotBeSavedIsLearnedAgainByTheNextProcess(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // due, with 22 hours of grace left
	e.ref.set(refusedBy("revoked"))
	// The disk fills up for this process after the marker of its grant (A24 writes one
	// before it sends, and a grant whose marker cannot be written is not sent), so it is
	// the refusal's marker that cannot be saved.
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true, okSaves: 1}
	a := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(a.Close)

	// Process A is refused and cannot write the marker.
	st, err := a.EnsureFresh(ctx)
	if err == nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("process A = %s/%q, %v, want locked/refused and the write error", st.State, st.Reason, err)
	}
	if got := e.session().State; got != "" {
		t.Fatalf("the stored session is marked %q: the write was meant to fail", got)
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
		t.Errorf("process A deleted the refresh token (now %q) although the marker was not saved", rt)
	}

	// Process B (a CLI command, the daemon) reads the old session. It is due, so it
	// presents the token, is refused and, with a disk that works, writes the marker.
	b := e.newGuard(0)
	st, err = b.EnsureFresh(ctx)
	if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Errorf("process B = %s/%q, %v, want locked/refused: the refusal must not be lost with the marker", st.State, st.Reason, err)
	}
	if got := e.session().State; got != "refused" {
		t.Errorf("the stored session is marked %q after process B, want refused", got)
	}
	e.touch() // the modification time that tells A the file changed

	// Process A reads the file again at its next poll and stays refused.
	e.f.Clock.Advance(account.PollInterval)
	if st := a.Status(); st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Errorf("process A after reading what B wrote = %s/%q, want locked/refused", st.State, st.Reason)
	}
}

// The accepted residual of guard_pending.go, pinned as it is documented and not because it is
// wanted: the record of a refusal could not be saved, so the disk still holds the marker of the
// grant beside the refresh token, which is what a lost answer leaves. A process that comes inside
// the window learns the refusal again (above); one that comes after it cannot tell the two
// apart and drops the token, presenting nothing, and the machine shows grace and then
// locked(unconfirmed) instead of locked(refused). If the guard ever learns to tell them apart,
// this test is to change with it.
func TestARefusalWhoseRecordWasLostIsDroppedAsUnconfirmedAfterTheWindow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	sess := e.signIn(2*time.Hour, time.Hour) // due, with 22 hours of grace left
	e.ref.set(refusedBy("revoked"))
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true, okSaves: 1} // the grant's marker is written, the refusal is not
	a := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(a.Close)
	if st, err := a.EnsureFresh(ctx); err == nil || st.Reason != account.ReasonRefused {
		t.Fatalf("process A = %s/%q, %v, want locked/refused and the write error", st.State, st.Reason, err)
	}
	if got := e.session(); got.State != "" || got.PendingSince.IsZero() {
		t.Fatalf("stored session = %s, want the grant's marker and no refusal: the write was meant to fail", describe(got))
	}

	e.f.Clock.Advance(241 * time.Second)
	st, err := e.newGuard(0).EnsureFresh(ctx)
	if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed || e.ref.calls.Load() != 1 {
		t.Fatalf("process B after the window = %s/%q, %v with %d grants, want grace/unconfirmed and only process A's grant", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if rt, _ := e.store.LoadRefresh(); rt != "" {
		t.Fatalf("refresh.enc holds %q, want it dropped", rt)
	}
	e.f.Clock.Set(sess.HW.Add(24 * time.Hour)) // the grace of the token ends at its iat + 24h
	if st := e.newGuard(0).Status(); st.State != account.StateLocked || st.Reason != account.ReasonUnconfirmed {
		t.Fatalf("Status once the grace is over = %s/%q, want locked/unconfirmed", st.State, st.Reason)
	}
}

// An attempt that failed moves a stale mark forward to now (the write is being
// made anyway), keeps one that is less than a minute old, and never lowers one
// that is ahead of the clock.
func TestAFailedAttemptMovesTheMarkForwardAndNeverBack(t *testing.T) {
	cases := []struct {
		name  string
		hw    time.Duration // the stored mark, relative to now
		moved bool
	}{
		{"two hours old", -2 * time.Hour, true},
		{"exactly a minute old", -time.Minute, true},
		{"59 seconds old", -59 * time.Second, false},
		{"a minute ahead of the clock", time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			sess := e.signIn(2*time.Hour, time.Hour) // expired: due
			now := e.f.Clock.Now()
			sess.HW = now.Add(c.hw)
			e.save(sess)
			e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) })
			if _, err := e.g.EnsureFresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := now.Add(c.hw)
			if c.moved {
				want = now
			}
			if got := e.session().HW; !got.Equal(want) {
				t.Fatalf("hw = %v after a failed attempt, want %v", got, want)
			}
		})
	}
}

// A failed attempt that cannot be written is reported, and this process still
// shows why the session is in grace.
func TestAFailedWriteOfAnAttemptIsReportedAndItsReasonStillShows(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonServerError) })
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true, okSaves: 1} // the marker of the grant is written, the record of the attempt is not
	g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(g.Close)
	st, err := g.EnsureFresh(context.Background())
	if err == nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
		t.Fatalf("EnsureFresh = %s/%q, %v, want grace/server_error and the write error", st.State, st.Reason, err)
	}
}
