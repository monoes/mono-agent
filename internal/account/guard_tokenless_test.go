package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// `account logout` is an open command. A user whose account monoes.me has blocked
// must not be able to unlock the machine with it and a clock set back: the date is
// judged on max(now, hw), and hw lives only in session.json, so a logout that
// removed the file would take the mark with it. B1b's logout therefore saves a
// session with no token that keeps the mark, and these tests pin that B1a's side
// of it holds: a session with no token still judges the date by its mark, the
// guard keeps advancing that mark, never calls monoes.me for it and writes
// nothing for a session that is refused or missing.

// What a logout leaves: a host and a mark, no token, no refresh token.
func tokenless(hw time.Time) *account.Session {
	return &account.Session{V: 1, Host: account.HostURL, HW: hw}
}

func TestATokenlessSessionJudgesTheEnforcementDateByItsMark(t *testing.T) {
	accounttest.New(t) // enforced, from a day before the clock's start
	date := account.EnforceDate()
	cases := []struct {
		name     string
		hw, now  time.Time
		enforced bool
	}{
		{"a mark two days past the date, the clock a month before it", date.Add(48 * time.Hour), date.Add(-30 * 24 * time.Hour), true},
		{"a mark two days past the date, the clock an hour before it", date.Add(48 * time.Hour), date.Add(-time.Hour), true},
		{"a mark at the date, the clock before it", date, date.Add(-time.Hour), true},
		{"a mark a second before the date, the clock before it", date.Add(-time.Second), date.Add(-time.Hour), false},
		{"a mark before the date, the clock past it", date.Add(-48 * time.Hour), date.Add(time.Hour), true},
		{"no mark, the clock past the date", time.Time{}, date.Add(time.Hour), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := account.Evaluate(tokenless(c.hw), c.now)
			if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn {
				t.Fatalf("Evaluate = %s/%q, want locked/not_logged_in", st.State, st.Reason)
			}
			if st.Enforced != c.enforced || st.Allowed() == c.enforced || !st.EnforceFrom.Equal(date) {
				t.Fatalf("Evaluate: enforced=%t allowed=%t enforce_from=%v, want enforced=%t allowed=%t enforce_from=%v", st.Enforced, st.Allowed(), st.EnforceFrom, c.enforced, !c.enforced, date)
			}
		})
	}
}

// While the package is dormant nothing is enforced, whatever the mark says.
func TestATokenlessSessionIsNotEnforcedWhileThePackageIsDormant(t *testing.T) {
	f := accounttest.New(t)
	account.SetEnforceFromForTest(t, time.Time{})
	for _, hw := range []time.Time{{}, f.Clock.Now().Add(48 * time.Hour)} {
		st := account.Evaluate(tokenless(hw), f.Clock.Now().Add(-30*24*time.Hour))
		if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || st.Enforced || !st.Allowed() || !st.EnforceFrom.IsZero() {
			t.Fatalf("Evaluate while dormant (mark %v) = %s/%q enforced=%t allowed=%t enforce_from=%v, want locked/not_logged_in, not enforced, allowed", hw, st.State, st.Reason, st.Enforced, st.Allowed(), st.EnforceFrom)
		}
	}
}

// The same through a guard over the stored file: Status and Require.
func TestAGuardOverATokenlessSessionIsLockedWhileTheClockIsBeforeTheDate(t *testing.T) {
	ctx := context.Background()
	t.Run("enforced", func(t *testing.T) {
		e := newEnv(t)
		date := account.EnforceDate()
		sess := tokenless(date.Add(48 * time.Hour))
		sess.User = &account.User{ID: "user-1"}
		e.save(sess)
		e.f.Clock.Set(date.Add(-time.Hour))
		st := e.g.Status()
		if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced || st.Allowed() || !st.EnforceFrom.Equal(date) {
			t.Fatalf("Status = %s, want locked/not_logged_in, enforced from %v and not allowed", describeStatus(st), date)
		}
		err := e.g.Require(ctx)
		var required *account.LoginRequiredError
		if !errors.As(err, &required) || required.Status.Reason != account.ReasonNotLoggedIn {
			t.Fatalf("Require = %v, want a *LoginRequiredError for not_logged_in", err)
		}
	})
	t.Run("dormant", func(t *testing.T) {
		e := newEnv(t)
		date := account.EnforceDate()
		e.save(tokenless(date.Add(48 * time.Hour)))
		e.f.Clock.Set(date.Add(-time.Hour))
		account.SetEnforceFromForTest(t, time.Time{})
		st := e.g.Status()
		if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || st.Enforced || !st.Allowed() {
			t.Fatalf("Status while dormant = %s, want locked/not_logged_in, not enforced, allowed", describeStatus(st))
		}
		if err := e.g.Require(ctx); err != nil {
			t.Fatalf("Require while dormant = %v, want nil", err)
		}
	})
}

// A gated command on a machine that has logged out: the guard never calls
// monoes.me for a session with no token, even with a refresh token left on disk
// that the server would accept, and it keeps the mark moving with the clock, at
// most once a minute, so that a clock set back afterwards is still caught.
func TestATokenlessSessionCallsNoOneAndItsMarkFollowsTheClock(t *testing.T) {
	ctx := context.Background()
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			date := account.EnforceDate()
			start := e.f.Clock.Now() // a day past the date
			e.save(tokenless(start))
			if err := e.store.SaveRefresh("rt-1"); err != nil {
				t.Fatal(err)
			}
			e.ref.set(func(r *fakeRefresher) { r.valid = "rt-1" })

			e.f.Clock.Advance(2 * time.Hour)
			st, err := ep.call(e.g, ctx)
			if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || e.ref.calls.Load() != 0 {
				t.Fatalf("%s = %s, %v with %d network refreshes, want locked/not_logged_in and none: there is no token to refresh", ep.name, describeStatus(st), err, e.ref.calls.Load())
			}
			if hw := e.session().HW; !hw.Equal(e.f.Clock.Now()) {
				t.Fatalf("the stored mark is %v after two hours, want the clock %v", hw, e.f.Clock.Now())
			}

			// At most once a minute.
			old := e.pinSession()
			e.f.Clock.Advance(59 * time.Second)
			if _, err := ep.call(e.g, ctx); err != nil {
				t.Fatal(err)
			}
			if !mustMtime(t, e.store).Equal(old) {
				t.Fatal("the mark was written again inside the minute")
			}
			e.f.Clock.Advance(2 * time.Second)
			if _, err := ep.call(e.g, ctx); err != nil {
				t.Fatal(err)
			}
			if mustMtime(t, e.store).Equal(old) || !e.session().HW.Equal(e.f.Clock.Now()) {
				t.Fatalf("the mark was not written after the minute: %v, want %v", e.session().HW, e.f.Clock.Now())
			}
			if n := e.ref.calls.Load(); n != 0 {
				t.Fatalf("%d network refreshes over the three calls, want none", n)
			}

			// The clock is set back to before the date: the mark keeps the gate enforced.
			e.f.Clock.Set(date.Add(-time.Hour))
			st = e.g.Status()
			if st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced || st.Allowed() {
				t.Fatalf("Status with the clock before the date = %s, want locked/not_logged_in, enforced", describeStatus(st))
			}
			if err := e.g.Require(ctx); !account.IsLoginRequired(err) {
				t.Fatalf("Require with the clock before the date = %v, want a login required error", err)
			}
		})
	}
}

// A session with no token that is refused has no mark worth writing: the guard
// writes nothing and calls no one. (A machine with no session at all keeps the record
// once the enforcement date has been reached, A25: guard_hwrecord_test.go.)
func TestARefusedTokenlessSessionWritesNothing(t *testing.T) {
	ctx := context.Background()
	for _, ep := range entryPoints {
		t.Run(ep.name+"/refused", func(t *testing.T) {
			e := newEnv(t)
			start := e.f.Clock.Now()
			refused := tokenless(start)
			refused.State = "refused"
			e.save(refused)
			old := e.pinSession()
			spy := e.spy()
			g := e.guardOver(spy, 0)
			e.f.Clock.Advance(2 * time.Hour)
			st, err := ep.call(g, ctx)
			if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused || e.ref.calls.Load() != 0 {
				t.Fatalf("%s = %s, %v with %d network refreshes, want locked/refused and none", ep.name, describeStatus(st), err, e.ref.calls.Load())
			}
			if lock, save := spy.calls("Lock"), spy.calls("Save"); lock != 0 || save != 0 {
				t.Fatalf("%d locks and %d writes for a refused session, want none", lock, save)
			}
			if !mustMtime(t, e.store).Equal(old) || !e.session().HW.Equal(start) {
				t.Fatalf("the refused session was written: mark %v, want %v untouched", e.session().HW, start)
			}
		})
	}
}
