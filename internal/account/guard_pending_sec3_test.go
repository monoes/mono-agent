package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// Regression tests from the third security review (sec3): an account directory that
// stops taking writes must never make the guard present a refresh token that monoes.me
// has rotated away, which ends every refresh token of the account, the other installs'
// included. Before A24 the first grant rotated rt-1, its successor could not be saved
// and the dead rt-1 could not be deleted, and every later command presented it again
// until one came after the 300 s reuse window.

// sec3Machine signs this machine in with rt-1 (two minutes left on its token, so a
// command refreshes) next to another install of the same account (rt-other), and
// takes the lock once so that session.lock exists, as after any earlier refresh.
func sec3Machine(t *testing.T) (*accounttest.Fixture, string, account.Sealer, *windowServer) {
	t.Helper()
	f := accounttest.New(t)
	dir := filepath.Join(t.TempDir(), "account")
	seal := account.NewMemorySealer()
	store := account.OpenStore(dir, seal)
	signInInstall(t, f, store, 58*time.Minute, time.Hour, "rt-1")
	unlock, err := store.Lock(context.Background())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	unlock()
	return f, dir, seal, newWindowServer(f, 0, "rt-1", "rt-other")
}

// sec3Command is one CLI command: a guard of its own over store, as a new process has.
func sec3Command(t *testing.T, f *accounttest.Fixture, store account.Store, srv *windowServer) (account.Status, error) {
	t.Helper()
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: srv, Now: f.Clock.Now})
	defer g.Close()
	return g.EnsureFresh(context.Background())
}

// count is how many times monoes.me was presented rt.
func count(presented []string, rt string) int {
	n := 0
	for _, p := range presented {
		if p == rt {
			n++
		}
	}
	return n
}

// Control: with a writable directory one command a minute stores the rotated token
// and revokes nothing.
func TestSec3ControlAWritableDirectoryRevokesNothing(t *testing.T) {
	f, dir, seal, srv := sec3Machine(t)
	for i := 0; i < 8; i++ {
		if _, err := sec3Command(t, f, account.OpenStore(dir, seal), srv); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
		f.Clock.Advance(61 * time.Second)
	}
	if srv.isRevoked() || !srv.isCurrent("rt-other") || count(srv.presented(), "rt-1") != 1 {
		t.Fatalf("monoes.me was presented %v (revoked %t), want rt-1 once and the account whole", srv.presented(), srv.isRevoked())
	}
}

// The account directory loses its write permission after the sign-in (chmod, a restore
// with the wrong mode): session.lock still opens and refresh.enc still reads, but no
// file can be created or removed in it. The marker cannot be saved, so no grant is
// ever sent: nothing is rotated, and nothing can be replayed.
func TestSec3AnUnwritableDirectoryReplaysTheDeadTokenUntilTheAccountIsRevoked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory permissions")
	}
	f, dir, seal, srv := sec3Machine(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if probe, err := os.CreateTemp(dir, "probe-*"); err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("the directory still takes writes (a superuser): nothing to test")
	}
	for i := 0; i < 8; i++ {
		st, err := sec3Command(t, f, account.OpenStore(dir, seal), srv)
		if err == nil || st.Reason == account.ReasonRefused {
			t.Fatalf("command %d = %s/%q, %v, want the write error and never a refusal", i, st.State, st.Reason, err)
		}
		f.Clock.Advance(61 * time.Second)
	}
	if got := srv.presented(); len(got) != 0 {
		t.Fatalf("monoes.me was presented %v, want nothing: a grant whose marker cannot be saved is not sent", got)
	}
	if srv.isRevoked() || !srv.isCurrent("rt-other") {
		t.Fatal("the account was revoked, the other install's token included")
	}
}

// refresh.enc can be read but neither replaced nor removed, and session.json stays
// writable. The first grant is answered and its successor cannot be saved (A24(d)): the
// marker stays, and the retries inside 240 s present rt-1 again, which monoes.me answers
// from its reuse window, and fail to store it again. After 240 s the token is dropped and
// the remove fails: it stays on disk and is never presented again, whatever the commands
// that follow. A sign-in, once the file can be replaced, puts everything right.
func TestSec3AnUndeletableRefreshFileReplaysTheDeadTokenUntilTheAccountIsRevoked(t *testing.T) {
	f, dir, seal, srv := sec3Machine(t)
	stuck := &stuckRefreshFile{Store: account.OpenStore(dir, seal), stuck: true}
	t0 := f.Clock.Now()
	for i := 0; i < 8; i++ {
		st, err := sec3Command(t, f, stuck, srv)
		at := f.Clock.Now().Sub(t0)
		want := 4 // the first grant, then the retries at +61 s, +122 s and +183 s
		if at < 240*time.Second {
			want = i + 1
		}
		if n := count(srv.presented(), "rt-1"); n != want {
			t.Fatalf("after the command at +%v monoes.me was presented rt-1 %d times, want %d: %v", at, n, want, srv.presented())
		}
		if !errors.Is(err, os.ErrPermission) || st.Reason == account.ReasonRefused {
			t.Fatalf("the command at +%v = %s/%q, %v, want the error of the stuck file and never a refusal", at, st.State, st.Reason, err)
		}
		f.Clock.Advance(61 * time.Second)
	}
	if srv.isRevoked() || !srv.isCurrent("rt-other") {
		t.Fatalf("the account was revoked (presented %v)", srv.presented())
	}
	store := account.OpenStore(dir, seal)
	if rt, _ := store.LoadRefresh(); rt != "rt-1" {
		t.Fatalf("refresh.enc holds %q, want the dead rt-1 that could not be removed", rt)
	}
	if sess, err := store.Load(); err != nil || sess.LastResult != "unconfirmed" {
		t.Fatalf("stored session = %s (%v), want unconfirmed", describe(sess), err)
	}

	// The file can be replaced again and the user signs in: the login replaces the dead
	// token and the session, and the next refresh presents the new token.
	stuck.unstick()
	srv.accept("rt-login")
	signInInstall(t, f, store, 58*time.Minute, time.Hour, "rt-login")
	st, err := sec3Command(t, f, stuck, srv)
	if err != nil || st.State != account.StateOK || count(srv.presented(), "rt-login") != 1 {
		t.Fatalf("the command after the sign-in = %s/%q, %v (presented %v), want ok after presenting the new token", st.State, st.Reason, err, srv.presented())
	}
	if count(srv.presented(), "rt-1") != 4 || srv.isRevoked() || !srv.isCurrent("rt-other") {
		t.Fatalf("monoes.me was presented %v (revoked %t), want rt-1 never again and the account whole", srv.presented(), srv.isRevoked())
	}
}
