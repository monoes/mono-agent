package account_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/library/libraryfake"
)

// leftByLogout checks what logout leaves of a session (A23): no access token, no refresh token, and the
// machine's clock-guard record, the high-water mark. It returns that record.
func leftByLogout(t *testing.T, store account.Store) *account.Session {
	t.Helper()
	sess, err := store.Load()
	if err != nil || sess == nil || sess.AccessToken != "" || sess.State != "" || sess.HW.IsZero() {
		t.Fatalf("logout must leave a session with no token and a high-water mark, not %s (%v)", sessionSummary(sess), err)
	}
	if rt, _ := store.LoadRefresh(); rt != "" {
		t.Fatal("refresh token kept")
	}
	return sess
}

func TestLogoutRevokesAndForgetsEvenOffline(t *testing.T) {
	fake := libraryfake.New()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	rt, _ := store.LoadRefresh()
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	var refused *account.RefusedError
	if _, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt); !errors.As(err, &refused) {
		t.Fatalf("the refresh token was not revoked at monoes.me: %v", err)
	}
	leftByLogout(t, store) // the login is gone; the clock-guard record is not

	// Offline: monoes.me is gone, the local state still goes.
	signInAtFake(t, c)
	fake.Close()
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("offline logout: %v", err)
	}
	leftByLogout(t, store)
}

// An unreadable session.json is replaced like any other: its mark is lost with it, so the record
// starts from now (the file was there, so the machine had been checked).
func TestLogoutForgetsAnUnreadableSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := account.OpenStore(dir, account.NewMemorySealer())
	if err := account.NewClient("https://monoes.example", store).Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	leftByLogout(t, store)
}

// Spec 4.5 and D5, A23: a clock set back must not postpone the enforcement date, and an account that
// monoes.me has blocked must not get out from under that by logging out, an open command, first.
// Logout keeps the machine's high-water mark in a session with no token, so the date is still judged
// against it: with the clock set before the date the machine is not logged in, and enforced.
func TestLogoutKeepsTheClockGuardRecord(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	date := time.Now().Add(-time.Hour) // the date has passed: this machine is enforced
	account.SetEnforceFromForTest(t, date)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	sess := leftByLogout(t, store)
	if sess.HW.Before(date) {
		t.Fatalf("the high-water mark %v is older than the date %v that the machine has already seen", sess.HW, date)
	}
	setBack := date.Add(-48 * time.Hour) // the clock is set to before the date
	if st := account.Evaluate(sess, setBack); st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced {
		t.Fatalf("with the clock set back: %+v, want locked(not_logged_in), enforced", st)
	}
	// Without the record there is nothing to judge the date against: the hole logout used to open.
	if st := account.Evaluate(nil, setBack); st.Enforced {
		t.Fatalf("with no record the same clock is not enforced, and this test would prove nothing: %+v", st)
	}
}

// Logging out twice is the same as once: the second finds a record with no token and nothing to
// forget, and leaves it as it is (nothing is written, the mark does not move).
func TestASecondLogoutChangesNothing(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir(), "session.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); !bytes.Equal(first, second) {
		t.Fatal("a second logout rewrote the clock-guard record")
	}
}

// A new sign-in replaces the record that logout left: the session is whole again.
func TestLoginAfterLogoutReplacesTheRecord(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := signInAtFake(t, c); st.State != account.StateOK {
		t.Fatalf("status %+v", st)
	}
	sess, _ := store.Load()
	if rt, _ := store.LoadRefresh(); sess == nil || sess.AccessToken == "" || rt == "" {
		t.Fatalf("a sign-in after a logout did not make a whole session: %s (refresh token stored: %v)", sessionSummary(sess), rt != "")
	}
}

// monoes.me ends every refresh token of an account when a rotated one is presented again, and a
// refresh in another process rotates the one that logout would have read first. So logout reads the
// refresh token it revokes under the lock: the live one, not a rotated-away one. Here another
// process refreshes while logout waits for the lock.
func TestLogoutRevokesTheRefreshTokenThatIsOnDiskWhenItHoldsTheLock(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	rt, _ := store.LoadRefresh()
	ctx := context.Background()
	unlock, err := store.Lock(ctx) // another process holds the lock, in the middle of a refresh
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Logout(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("logout did not wait for the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	rotated, err := account.NewRefresher(fake.URL).Refresh(ctx, rt) // the other process's refresh: rt is spent now
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh(rotated.RefreshToken); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var refused *account.RefusedError
	if _, err := account.NewRefresher(fake.URL).Refresh(ctx, rotated.RefreshToken); !errors.As(err, &refused) {
		t.Fatalf("the refresh token that was on disk when logout held the lock was not revoked: %v", err)
	}
}

// A24: while a refresh grant's outcome is in doubt (the guard has saved pending_since and has not learned the
// answer) the refresh token on disk may already be spent. Presenting a spent token anywhere is what A24
// forbids, the revoke endpoint included, and revoking it would not touch the successor that monoes.me issued
// and this machine never received: logout forgets the login locally and calls nobody.
func TestLogoutWhileARefreshIsInDoubtOnlyForgetsLocally(t *testing.T) {
	fake := libraryfake.New()
	defer fake.Close()
	c, store := newFakeClient(t, fake)
	signInAtFake(t, c)
	sess, err := store.Load()
	if err != nil || sess == nil {
		t.Fatalf("no session to mark: %v", err)
	}
	rt, _ := store.LoadRefresh()
	sess.PendingSince = time.Now()
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := fake.Requests["POST /api/auth/oauth2/revoke"]; n != 0 {
		t.Fatalf("%d revocations went out while a refresh was in doubt", n)
	}
	if left := leftByLogout(t, store); !left.PendingSince.IsZero() { // the login is gone; the clock-guard record is not
		t.Fatal("the record that logout leaves still holds the marker of a refresh that is no longer there")
	}
	// monoes.me was not asked: the token that was on disk still works there.
	if _, err := account.NewRefresher(fake.URL).Refresh(context.Background(), rt); err != nil {
		t.Fatalf("the refresh token was revoked at monoes.me: %v", err)
	}
}

func TestLogoutWithNothingToForgetLeavesNoFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	c := account.NewClient("https://monoes.example", account.OpenStore(dir, account.NewMemorySealer()))
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("logout made %s: %v", dir, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A refresh token never travels in the clear: nothing is sent to a remote host over plain http, and the
// local copy goes all the same. With a session the guard would refresh, the plain-http rule is all that
// keeps the token from the revoke endpoint. A refresh token with no session behind it (a sign-in that
// stopped between its two writes) leaves no clock-guard record to keep: none is made.
func TestLogoutNeverRevokesOverPlainHTTP(t *testing.T) {
	for _, withSession := range []bool{true, false} {
		store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
		if err := store.SaveRefresh("a-refresh-token"); err != nil {
			t.Fatal(err)
		}
		if withSession {
			if err := store.Save(&account.Session{V: 1, Host: "http://monoes.example", AccessToken: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		c := account.NewClient("http://monoes.example", store)
		c.HTTP = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("offline")
		})}
		if err := c.Logout(context.Background()); err != nil {
			t.Fatal(err)
		}
		if rt, _ := store.LoadRefresh(); calls != 0 || rt != "" {
			t.Fatalf("with a session %v: %d requests went out, refresh token kept: %v", withSession, calls, rt != "")
		}
		if sess, _ := store.Load(); !withSession && sess != nil {
			t.Fatalf("logout made a session out of nothing: %s", sessionSummary(sess))
		}
	}
}

// A24: logout presents the refresh token to the revoke endpoint only when it knows that the guard would
// present that token itself: a session that was read, with no pending_since marker, no unconfirmed drop and
// no refusal. Any other token may have been rotated away already, or its state cannot be read here, and
// revoking a rotated-away token might count as its reuse and sign out every install of the account, so it is
// forgotten on this machine and monoes.me is not called. Each case starts from a real sign-in at the fake and
// then changes what is on disk; the refresh token stays there, as after a delete that failed.
func TestLogoutRevokesOnlyATokenTheGuardWouldPresent(t *testing.T) {
	write := func(body string) func(*testing.T, account.Store) {
		return func(t *testing.T, store account.Store) {
			if err := os.WriteFile(filepath.Join(store.Dir(), "session.json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	change := func(edit func(*account.Session)) func(*testing.T, account.Store) {
		return func(t *testing.T, store account.Store) {
			sess, err := store.Load()
			if err != nil || sess == nil {
				t.Fatalf("no session to change: %v", err)
			}
			edit(sess)
			if err := store.Save(sess); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, account.Store)
		revokes int
	}{
		{"a session the guard would refresh", func(*testing.T, account.Store) {}, 1},
		{"an unreadable session.json", write("{not json"), 0},
		{"a version this build does not read", write(`{"v":2,"host":"https://monoes.me"}`), 0},
		{"a refresh in doubt", change(func(s *account.Session) { s.PendingSince = time.Now() }), 0},
		{"a token a drop gave up, with no marker", change(func(s *account.Session) { s.LastResult = string(account.ReasonUnconfirmed) }), 0},
		{"a refused session", change(func(s *account.Session) { s.State, s.LastResult = "refused", "refused" }), 0},
		{"no session.json beside the refresh token", func(t *testing.T, store account.Store) {
			if err := os.Remove(filepath.Join(store.Dir(), "session.json")); err != nil {
				t.Fatal(err)
			}
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := libraryfake.New()
			defer fake.Close()
			c, store := newFakeClient(t, fake)
			signInAtFake(t, c)
			tc.prepare(t, store)
			if err := c.Logout(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n := fake.Requests["POST /api/auth/oauth2/revoke"]; n != tc.revokes {
				t.Fatalf("%d revocations went out, want %d", n, tc.revokes)
			}
			if rt, _ := store.LoadRefresh(); rt != "" {
				t.Fatal("the refresh token was kept on this machine")
			}
		})
	}
}
