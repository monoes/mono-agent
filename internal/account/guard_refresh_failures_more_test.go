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

// A server that does not rotate the refresh token leaves the working one where
// it is. Here the key store cannot seal, so a write of it would fail and then
// delete the good one.
func TestAServerThatAnswersWithoutANewRefreshTokenKeepsTheOldOne(t *testing.T) {
	cases := []struct{ name, answer string }{
		{"no refresh token in the answer", ""},
		{"the same refresh token in the answer", "rt-1"},
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
			if err != nil || st.State != account.StateOK {
				t.Fatalf("EnsureFresh = %s/%q, %v, want ok", st.State, st.Reason, err)
			}
			if got := fs.order(); !reflect.DeepEqual(got, []string{"Save"}) {
				t.Fatalf("writes = %v, want the session only: a refresh token that did not change is not written", got)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatal("the refresh token the server did not change was lost")
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

// A refusal keeps the user and the mark for the message the CLI prints, records
// the attempt, and keeps at most 200 bytes of what the server said.
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
			if got.Reason != want || got.User == nil || *got.User != *sess.User || got.Host != sess.Host || !got.LastAttempt.Equal(now) || !got.HW.Equal(sess.HW) {
				t.Fatalf("stored session = %s (reason %d bytes), want %d bytes of reason, the user, the host, the mark %v and the attempt %v", describe(got), len(got.Reason), len(want), sess.HW, now)
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
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "DeleteRefresh"}) {
			t.Fatalf("writes = %v, want the marker before the delete", got)
		}
	})
	t.Run("a marker that cannot be written is reported and the refresh token stays", func(t *testing.T) {
		e := newEnv(t)
		e.signIn(2*time.Hour, time.Hour)
		e.ref.set(refusedBy("revoked"))
		fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true}
		g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
		t.Cleanup(g.Close)
		st, err := g.EnsureFresh(ctx)
		if err == nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
			t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused and the write error", st.State, st.Reason, err)
		}
		if got := fs.order(); !reflect.DeepEqual(got, []string{"Save"}) {
			t.Fatalf("writes = %v, want the marker only: the refresh token goes once the marker is saved", got)
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
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true} // the disk is full for this process
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
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true}
	g := account.NewGuard(account.GuardOptions{Store: fs, Refresher: e.ref, Now: e.f.Clock.Now})
	t.Cleanup(g.Close)
	st, err := g.EnsureFresh(context.Background())
	if err == nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
		t.Fatalf("EnsureFresh = %s/%q, %v, want grace/server_error and the write error", st.State, st.Reason, err)
	}
}
