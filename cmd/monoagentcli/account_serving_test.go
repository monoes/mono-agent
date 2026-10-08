package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// recordServingGuard replaces startServingGuard with a counter for one test.
func recordServingGuard(t *testing.T) *int {
	t.Helper()
	var started int
	old := startServingGuard
	startServingGuard = func(context.Context) { started++ }
	t.Cleanup(func() { startServingGuard = old })
	return &started
}

// TestServingCommandsStartTheGuardRefresher: the four commands that serve for
// as long as they run start the refresher before their RunE does anything.
func TestServingCommandsStartTheGuardRefresher(t *testing.T) {
	started := recordServingGuard(t)
	root := newRootCmd()
	for _, path := range [][]string{{"daemon"}, {"httpapi"}, {"mcp"}, {"extension", "serve"}} {
		name := strings.Join(path, " ")
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
			t.Fatalf("%s: Find = %v, %v", name, cmd, err)
		}
		if cmd.PreRunE != nil {
			t.Errorf("%s has a PreRunE, which would shadow the PreRun that starts the guard", name)
		}
		if cmd.PreRun == nil {
			t.Errorf("%s does not start the guard's refresher: no PreRun", name)
			continue
		}
		before := *started
		cmd.PreRun(cmd, nil)
		if *started != before+1 {
			t.Errorf("%s: its pre-run started the refresher %d times, want 1", name, *started-before)
		}
	}
}

// TestOrgServeStartsTheGuardOnlyWhenItServes: `org serve` without --foreground
// spawns a background process and exits, and --stop stops one; neither serves.
func TestOrgServeStartsTheGuardOnlyWhenItServes(t *testing.T) {
	started := recordServingGuard(t)
	cmd, _, err := newRootCmd().Find([]string{"org", "serve"})
	if err != nil || cmd == nil || cmd.PreRun == nil {
		t.Fatalf("org serve: Find = %v, %v (PreRun set: %v)", cmd, err, cmd != nil && cmd.PreRun != nil)
	}
	for _, c := range []struct {
		foreground, stop string
		want             int
	}{
		{"true", "false", 1},  // org serve --foreground
		{"false", "false", 0}, // org serve
		{"true", "true", 0},   // org serve --foreground --stop
		{"false", "true", 0},  // org serve --stop
	} {
		_ = cmd.Flags().Set("foreground", c.foreground)
		_ = cmd.Flags().Set("stop", c.stop)
		before := *started
		cmd.PreRun(cmd, nil)
		if got := *started - before; got != c.want {
			t.Errorf("foreground=%s stop=%s: started the refresher %d times, want %d", c.foreground, c.stop, got, c.want)
		}
	}
}

// countingRefresher counts the grants the guard sends and answers each with
// invalid_grant, so that nothing more happens to the session afterwards.
type countingRefresher struct{ grants atomic.Int32 }

func (r *countingRefresher) Refresh(context.Context, string) (*account.TokenSet, error) {
	r.grants.Add(1)
	return nil, &account.RefusedError{Description: "the account was blocked"}
}

// dueGuard installs a signed-in guard whose token is past half its life, so that the
// first pass of a refresher that has been started sends a grant at once, and returns
// the refresher that counts it. The enforcement date has passed (accounttest.New sets
// one), because StartRefresher does nothing while the gate is dormant.
func dueGuard(t *testing.T) *countingRefresher {
	t.Helper()
	fx := accounttest.New(t)
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	token := fx.Token(accounttest.TokenOptions{Sub: "u1", IssuedAt: fx.Clock.Now().Add(-40 * time.Minute)})
	if err := store.Save(&account.Session{V: 1, Host: account.HostURL, AccessToken: token, User: &account.User{ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefresh("refresh-1"); err != nil {
		t.Fatal(err)
	}
	r := &countingRefresher{}
	g := account.NewGuard(account.GuardOptions{Store: store, Refresher: r, Now: fx.Clock.Now})
	account.InstallForTest(t, g)
	t.Cleanup(g.Close) // registered last, so it runs first: the refresher has ended before the guard is uninstalled and the date and key are restored
	return r
}

// TestStartServingGuardStartsTheInstalledGuardsRefresher is the one test of the real
// startServingGuard (the tests above replace it): the guard that is installed gets
// its refresher, so a session that is due is sent its grant. It fails when the
// StartRefresher call is deleted, and when the no-guard rule is.
func TestStartServingGuardStartsTheInstalledGuardsRefresher(t *testing.T) {
	t.Run("a guard that is due", func(t *testing.T) {
		r := dueGuard(t)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		startServingGuard(ctx)

		for deadline := time.Now().Add(10 * time.Second); r.grants.Load() == 0; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("no grant was sent within 10s of startServingGuard: the guard's refresher was not started")
			}
		}
	})
	t.Run("no guard installed", func(t *testing.T) {
		account.InstallForTest(t, nil)
		account.SetEnforceFromForTest(t, time.Now().Add(-24*time.Hour)) // enforced: a dormant gate returns before it touches a nil guard

		startServingGuard(context.Background()) // there is nothing to start, and nothing panics
	})
}
