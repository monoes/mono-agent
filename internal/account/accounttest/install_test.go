package accounttest

import (
	"context"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

func TestInstallModes(t *testing.T) {
	guardBefore, dateBefore := account.Current(), account.EnforceDate()
	cases := []struct {
		name     string
		mode     Mode
		state    account.State
		reason   account.Reason
		enforced bool
		allowed  bool
	}{
		{"SignedIn", SignedIn, account.StateOK, account.ReasonNone, true, true},
		{"InGrace", InGrace, account.StateGrace, account.ReasonUnreachable, true, true},
		{"LockedNoLogin", LockedNoLogin, account.StateLocked, account.ReasonNotLoggedIn, true, false},
		{"LockedRefused", LockedRefused, account.StateLocked, account.ReasonRefused, true, false},
		{"Dormant", Dormant, account.StateLocked, account.ReasonNotLoggedIn, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := Install(t, c.mode)
			st := g.Status()
			if st.State != c.state || st.Reason != c.reason || st.Enforced != c.enforced || st.Allowed() != c.allowed {
				t.Fatalf("Status = %s/%q enforced=%v allowed=%v, want %s/%q enforced=%v allowed=%v",
					st.State, st.Reason, st.Enforced, st.Allowed(), c.state, c.reason, c.enforced, c.allowed)
			}
			if account.Current() != g {
				t.Fatal("Install must make the guard the process guard")
			}
			if got := account.CurrentStatus(); got.State != c.state || got.Reason != c.reason {
				t.Fatalf("CurrentStatus = %s/%q, want the guard's", got.State, got.Reason)
			}
			err := account.Require(context.Background())
			if c.allowed != (err == nil) || (err != nil && !account.IsLoginRequired(err)) {
				t.Fatalf("Require = %v, want allowed=%v", err, c.allowed)
			}
		})
	}
	if account.Current() != guardBefore || !account.EnforceDate().Equal(dateBefore) {
		t.Fatalf("Install leaked out of its test: guard %v, date %v (was %v)", account.Current(), account.EnforceDate(), dateBefore)
	}
}

func TestInstallStrictRefusesWhenTheGuardIsGone(t *testing.T) {
	Install(t, LockedNoLogin)
	account.Install(nil)                                         // the guard is gone; strictness remains
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour)) // with no guard Require judges by the real clock
	if err := account.Require(context.Background()); !account.IsLoginRequired(err) {
		t.Fatalf("Require with no guard under Install's strictness = %v, want a LoginRequiredError", err)
	}
}

func TestInstallWithFixtureLetsATestMoveTime(t *testing.T) {
	g, f := InstallWithFixture(t, SignedIn)
	f.Clock.Advance(90 * time.Minute) // the one-hour token has expired
	if st := g.Status(); st.State != account.StateGrace {
		t.Fatalf("after the clock moved, Status = %s, want grace", st.State)
	}
}
