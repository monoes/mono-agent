package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// loginRequiredLine is the first line of every refusal (index §2), pinned here
// on purpose: a change to the shared text must be a decision.
const loginRequiredLine = "Log in to monoes.me first: monoagentcli account login"

// gate runs the CLI gate on args against the installed guard.
func gate(args ...string) (stderr string, err error) {
	root := newRootCmd()
	applyClassification(root)
	var buf bytes.Buffer
	err = gateCommand(context.Background(), root, args, account.Current(), &buf)
	return buf.String(), err
}

// fakeRefresher answers every refresh with fail, or else with a fresh token.
type fakeRefresher struct {
	calls atomic.Int32
	fail  error
	next  func() string
}

func (r *fakeRefresher) Refresh(context.Context, string) (*account.TokenSet, error) {
	r.calls.Add(1)
	if r.fail != nil {
		return nil, r.fail
	}
	return &account.TokenSet{AccessToken: r.next(), RefreshToken: "refresh-test-2"}, nil
}

// installExpiredSession installs a guard whose access token ran out an hour
// ago (inside the 24 hours), with a refresh token and a refresher that
// answers fail.
func installExpiredSession(t *testing.T, fail error) *fakeRefresher {
	t.Helper()
	f := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	expired := f.Token(accounttest.TokenOptions{IssuedAt: f.Clock.Now().Add(-2 * time.Hour)})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: expired, User: &account.User{ID: "user-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh("refresh-test-1"); err != nil {
		t.Fatal(err)
	}
	r := &fakeRefresher{fail: fail, next: func() string { return f.Token(accounttest.TokenOptions{}) }}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r, Now: f.Clock.Now})
	account.InstallForTest(t, g)
	t.Cleanup(g.Close)
	return r
}

// installUnconfirmedSession installs a guard whose access token ran out an hour ago (inside the 24
// hours), with no refresh token and last_result "unconfirmed": what a refresh whose answer never
// arrived leaves once the guard has dropped the token (A24). The refresher counts what is asked of it.
func installUnconfirmedSession(t *testing.T) *fakeRefresher {
	t.Helper()
	f := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	expired := f.Token(accounttest.TokenOptions{IssuedAt: f.Clock.Now().Add(-2 * time.Hour)})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: expired, User: &account.User{ID: "user-1"},
		LastAttempt: f.Clock.Now().Add(-time.Hour), LastResult: string(account.ReasonUnconfirmed)}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRefresher{next: func() string { return f.Token(accounttest.TokenOptions{}) }}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r, Now: f.Clock.Now})
	account.InstallForTest(t, g)
	t.Cleanup(g.Close)
	return r
}

// A refusal is the text of account.LoginRequiredError: the fixed first line
// and, for the five reasons of spec §6.1 and for unconfirmed (A24), where signing
// in again is not the whole story, a second one. It exits 4 and is a
// login-required error.
func TestRefusalTextPerReason(t *testing.T) {
	for _, r := range []account.Reason{account.ReasonExpired, account.ReasonRefused, account.ReasonClockRollback,
		account.ReasonClockSkew, account.ReasonKeyUnknown, account.ReasonUnconfirmed} {
		err := newGateRefusal(account.Status{State: account.StateLocked, Reason: r, Enforced: true})
		lines := strings.Split(err.Error(), "\n")
		if len(lines) != 2 || lines[0] != loginRequiredLine || lines[1] == "" || exitCodeFor(err) != 4 || !isLoginRequired(err) {
			t.Errorf("%s: message %q, exit %d", r, err, exitCodeFor(err))
		}
	}
	if got := newGateRefusal(account.Status{State: account.StateLocked, Reason: account.ReasonNotLoggedIn}).Error(); got != loginRequiredLine {
		t.Errorf("not logged in: message %q, want the first line alone", got)
	}
}

// A refusal that comes out of a command (the engine, an agent turn, a browser
// action) exits 4 as well, not 1, wrapped or not.
func TestAnyLoginRequiredErrorExitsFour(t *testing.T) {
	err := fmt.Errorf("agent turn: %w", &account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: account.ReasonRefused}})
	if exitCodeFor(err) != 4 || jsonErrorCode(err) != "auth_or_connection" {
		t.Errorf("exit %d, code %q", exitCodeFor(err), jsonErrorCode(err))
	}
	if exitCodeFor(errors.New("other")) != 1 {
		t.Error("an ordinary error must keep exit 1")
	}
}

// Locked: a gated command is refused, a long-running one starts and says why
// on stderr, an open one passes in silence, whatever flags come first. Only
// the gate runs here: the serving commands would serve for real.
func TestGateWhenLocked(t *testing.T) {
	accounttest.Install(t, accounttest.LockedRefused)
	why := (&account.LoginRequiredError{Status: account.CurrentStatus()}).Error()

	for _, args := range [][]string{{"workflow", "list"}, {"--profile", "work", "person", "list"}, {"daemon", "restart"},
		{"daemon", "install"}, {"extension", "status"}, {"org", "run"}, {"org", "serve"}, {"org", "serve", "--foreground"},
		{"workflow", "list", "--help=false"}} {
		if stderr, err := gate(args...); err == nil || err.Error() != why || stderr != "" {
			t.Errorf("gated %q: %v, stderr %q", args, err, stderr)
		}
	}
	for _, args := range [][]string{{"daemon"}, {"httpapi"}, {"mcp"}, {"bridge", "serve"}, {"--profile", "work", "extension", "serve"}} {
		if stderr, err := gate(args...); err != nil || stderr != why+"\n" {
			t.Errorf("serving %q: %v, stderr %q", args, err, stderr)
		}
	}
	for _, args := range [][]string{{"doctor"}, {"doctor", "fix", "x"}, {"account", "login"}, {"--profile", "work", "account", "status"},
		{"update", "--app"}, {"setup"}, {"version"}, {"library", "login"}, {"library", "status", "--json"}, {"ref", "node", "http.request"},
		{"workflow", "list", "--help"}, {"daemon", "--help"}, {"help"}, {"completion", "bash"}, {"__complete", "work"}} {
		if stderr, err := gate(args...); err != nil || stderr != "" {
			t.Errorf("open %q: %v, stderr %q", args, err, stderr)
		}
	}
}

// A gated command renews a session that is due before it is judged, so a
// blocked account is found out and a renewable one never reaches the grace
// line; an open command asks nothing of monoes.me.
func TestGateRefreshesBeforeGatedCommandsOnly(t *testing.T) {
	r := installExpiredSession(t, nil)
	for _, args := range [][]string{{"completion", "bash"}, {"doctor"}, {"account", "login"}} {
		if _, err := gate(args...); err != nil || r.calls.Load() != 0 {
			t.Fatalf("%q: %v, %d refreshes: an open command must not refresh", args, err, r.calls.Load())
		}
	}
	if stderr, err := gate("workflow", "list"); err != nil || stderr != "" || r.calls.Load() != 1 {
		t.Errorf("gated: %v, stderr %q, %d refreshes, want a silent pass after 1", err, stderr, r.calls.Load())
	}
}

// The one line an allowed command owes the user goes to stderr; open commands
// and a dormant gate stay silent.
func TestGateAnnouncesGraceAndWarning(t *testing.T) {
	t.Run("grace", func(t *testing.T) {
		installExpiredSession(t, &account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("offline")})
		stderr, err := gate("workflow", "list")
		st := account.CurrentStatus()
		want := "monoes.me is unreachable; this login works offline until " + st.GraceUntil.Local().Format(time.RFC3339) + "\n"
		if err != nil || st.State != account.StateGrace || stderr != want {
			t.Errorf("state %s: %v, stderr %q, want %q", st.State, err, stderr, want)
		}
		if stderr, _ := gate("doctor"); stderr != "" {
			t.Errorf("an open command printed %q", stderr)
		}
	})
	// A24: monoes.me is not the problem and the refresh token is gone, so the line does not say "unreachable":
	// it says that the login cannot be renewed on this machine, until when it works and what to do. Nothing is
	// asked of monoes.me: there is no refresh token to present.
	t.Run("grace after a refresh whose answer never arrived", func(t *testing.T) {
		r := installUnconfirmedSession(t)
		stderr, err := gate("workflow", "list")
		st := account.CurrentStatus()
		want := "This login can no longer be renewed on this machine and works until " + st.GraceUntil.Local().Format(time.RFC3339) +
			". Sign in again: monoagentcli account login\n"
		if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnconfirmed || stderr != want || r.calls.Load() != 0 {
			t.Errorf("state %s/%s: %v, stderr %q, want %q, %d refresh calls", st.State, st.Reason, err, stderr, want, r.calls.Load())
		}
	})
	t.Run("warn period", func(t *testing.T) {
		accounttest.Install(t, accounttest.LockedNoLogin)
		date := time.Now().Add(72 * time.Hour)
		account.SetEnforceFromForTest(t, date)
		want := "A monoes.me login will be required from " + date.Local().Format("2006-01-02") + ": monoagentcli account login\n"
		if stderr, err := gate("workflow", "list"); err != nil || stderr != want {
			t.Errorf("%v, stderr %q, want %q", err, stderr, want)
		}
	})
	t.Run("dormant", func(t *testing.T) {
		accounttest.Install(t, accounttest.Dormant)
		if stderr, err := gate("workflow", "list"); err != nil || stderr != "" {
			t.Errorf("%v, stderr %q, want silence", err, stderr)
		}
	})
}
