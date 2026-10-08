package account_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// recTB is a testing.TB that keeps the cleanups it is given instead of queueing
// them on the real test, so a test can end "a test" when it chooses, and that
// says it has failed when told to. Whatever it does not override is the real
// test's. One recTB belongs to one goroutine.
type recTB struct {
	testing.TB
	failed   bool
	cleanups []func()
}

func (r *recTB) Helper()          {}
func (r *recTB) Failed() bool     { return r.failed }
func (r *recTB) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }

// Setenv does nothing: the seams mark the test with t.Setenv, and the real test's
// Setenv is not for goroutines that run at once, which these recorders are used from.
// The marker is tried on the real test by TestEveryGlobalStateSeamPanicsWhenMixedWithTParallel.
func (r *recTB) Setenv(key, value string) {}

// end runs what the "test" registered, the last registered first, as the
// testing package does. It is safe to call twice.
func (r *recTB) end() {
	for len(r.cleanups) > 0 {
		r.pop()
	}
}

// pop runs the cleanup registered last.
func (r *recTB) pop() {
	f := r.cleanups[len(r.cleanups)-1]
	r.cleanups = r.cleanups[:len(r.cleanups)-1]
	f()
}

func bareGuard(t *testing.T) *account.Guard {
	t.Helper()
	g := account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer())})
	t.Cleanup(g.Close)
	return g
}

// The test-binary exception of Require (D24) is invisible while the package is
// dormant, which is what the ambient date is: nothing locks, exception or not.
// So the date is set here, and the verdict that Require lets through is the one
// CurrentStatus still reports, because the exception belongs to Require alone.
func TestATestBinaryWithNoGuardIsLetThroughEvenWhenTheGateIsEnforced(t *testing.T) {
	account.InstallForTest(t, nil)
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
	ctx := context.Background()
	if st := account.CurrentStatus(); !st.Enforced || st.Allowed() || st.State != account.StateLocked {
		t.Fatalf("the premise does not hold: CurrentStatus = %+v, want an enforced, locked verdict", st)
	}
	if err := account.Require(ctx); err != nil {
		t.Fatalf("Require in a test binary with no guard and no strictness = %v, want nil (D24)", err)
	}
	account.StrictForTest(t)
	if err := account.Require(ctx); !account.IsLoginRequired(err) {
		t.Fatalf("Require once the test is strict = %v, want a *LoginRequiredError", err)
	}
}

// The exception is for the process with no guard. A guard that is installed
// speaks for the process, strict or not: a test that installs its own guard
// with InstallForTest, and nothing else, gets that guard's verdict.
func TestAnInstalledGuardIsObeyedWhetherOrNotTheTestIsStrict(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		setup  func(e *env)
		reason account.Reason // empty: allowed
	}{
		{"nobody signed in", func(e *env) {}, account.ReasonNotLoggedIn},
		{"signed in", func(e *env) { e.signIn(0, time.Hour) }, account.ReasonNone},
		{"refused", func(e *env) { e.save(refusedSession()) }, account.ReasonRefused},
	}
	for _, strict := range []bool{false, true} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("strict=%v, %s", strict, c.name), func(t *testing.T) {
				account.InstallForTest(t, nil)
				e := newEnv(t) // the fixture trusts its key and enforces the gate
				c.setup(e)
				account.InstallForTest(t, e.newGuard(0))
				if strict {
					account.StrictForTest(t)
				}
				err := account.Require(ctx)
				st := account.CurrentStatus()
				if c.reason == account.ReasonNone {
					if err != nil || st.State != account.StateOK {
						t.Fatalf("Require = %v, CurrentStatus = %s/%q, want the signed-in guard's verdict", err, st.State, st.Reason)
					}
					return
				}
				var lr *account.LoginRequiredError
				if !errors.As(err, &lr) || lr.Status.State != account.StateLocked || lr.Status.Reason != c.reason {
					t.Fatalf("Require = %v, want the guard's locked/%s", err, c.reason)
				}
				if st.State != account.StateLocked || st.Reason != c.reason {
					t.Fatalf("CurrentStatus = %s/%q, want the guard's locked/%s", st.State, st.Reason, c.reason)
				}
			})
		}
	}
}

// With no guard installed the process is judged by the real clock against the
// enforcement date, to the second: a date that has just passed locks, a date
// that is still ahead does not, for Require and CurrentStatus alike.
func TestNoGuardFollowsTheEnforcementDateToTheSecond(t *testing.T) {
	account.InstallForTest(t, nil)
	account.StrictForTest(t)
	ctx := context.Background()
	for _, c := range []struct {
		name     string
		date     time.Duration // from now
		enforced bool
	}{
		{"the date passed five seconds ago", -5 * time.Second, true},
		{"the date is five seconds ahead", 5 * time.Second, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			date := time.Now().Add(c.date)
			account.SetEnforceFromForTest(t, date)
			err := account.Require(ctx)
			if c.enforced != account.IsLoginRequired(err) || (!c.enforced && err != nil) {
				t.Fatalf("Require = %v, want a refusal: %v", err, c.enforced)
			}
			st := account.CurrentStatus()
			if st.Enforced != c.enforced || st.Allowed() == c.enforced || !st.EnforceFrom.Equal(date) ||
				st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || st.V != 1 {
				t.Fatalf("CurrentStatus = %+v, want locked/not_logged_in, enforced=%v, EnforceFrom=%v", st, c.enforced, date)
			}
		})
	}
}

// What a refusal with no guard carries is the whole verdict that CurrentStatus
// gives, and nothing else: no user, no token times, and the message and JSON
// fields of a not-logged-in refusal.
func TestTheRefusalWithNoGuardCarriesTheWholeStatus(t *testing.T) {
	account.InstallForTest(t, nil)
	account.StrictForTest(t)
	date := time.Now().Add(-time.Hour)
	account.SetEnforceFromForTest(t, date)
	err := account.Require(context.Background())
	var lr *account.LoginRequiredError
	if !errors.As(err, &lr) {
		t.Fatalf("Require = %v, want a *LoginRequiredError", err)
	}
	st := lr.Status
	if st.V != 1 || st.State != account.StateLocked || st.Reason != account.ReasonNotLoggedIn || !st.Enforced || !st.EnforceFrom.Equal(date) {
		t.Fatalf("the refusal carries %+v, want locked/not_logged_in, enforced since %v, version 1", st, date)
	}
	if st.User != nil || st.Plan != "" || !st.IssuedAt.IsZero() || !st.ValidUntil.IsZero() || !st.GraceUntil.IsZero() {
		t.Fatalf("the refusal carries more than a verdict with no session: %+v", st)
	}
	if err.Error() != account.LoginRequiredMessage {
		t.Fatalf("the message is %q, want exactly %q", err.Error(), account.LoginRequiredMessage)
	}
	fields := lr.JSONErrorFields()
	acct, _ := fields["account"].(map[string]any)
	if fields["login_required"] != true || fields["code"] != "auth_or_connection" || acct["state"] != "locked" || acct["reason"] != "not_logged_in" {
		t.Fatalf("JSONErrorFields = %v", fields)
	}
}

// InstallForTest puts back what was installed before it, when the test ends,
// whatever the test did: fail, install a guard of its own, or install twice.
func TestInstallForTestPutsBackTheGuardThatWasThere(t *testing.T) {
	account.InstallForTest(t, nil) // whatever the test binary had comes back at the end of this test
	g1, g2, g3 := bareGuard(t), bareGuard(t), bareGuard(t)
	expect := func(t *testing.T, what string, want *account.Guard) {
		t.Helper()
		if got := account.Current(); got != want {
			t.Fatalf("%s: the installed guard is %p, want %p", what, got, want)
		}
	}
	t.Run("the test failed", func(t *testing.T) {
		account.Install(g1)
		rec := &recTB{TB: t, failed: true}
		t.Cleanup(rec.end)
		account.InstallForTest(rec, g2)
		expect(t, "while the test runs", g2)
		rec.end()
		expect(t, "after a failed test", g1)
	})
	t.Run("the test failed and nothing was installed before", func(t *testing.T) {
		account.Install(nil)
		rec := &recTB{TB: t, failed: true}
		t.Cleanup(rec.end)
		account.InstallForTest(rec, g2)
		rec.end()
		expect(t, "after a failed test", nil)
	})
	t.Run("the test installed a guard of its own", func(t *testing.T) {
		account.Install(g1)
		rec := &recTB{TB: t}
		t.Cleanup(rec.end)
		account.InstallForTest(rec, g2)
		account.Install(g3)
		rec.end()
		expect(t, "after the test installed another", g1)
	})
	t.Run("two installs end in reverse order", func(t *testing.T) {
		account.Install(g1)
		rec := &recTB{TB: t}
		t.Cleanup(rec.end)
		account.InstallForTest(rec, g2)
		account.InstallForTest(rec, nil)
		expect(t, "after the second install", nil)
		rec.pop()
		expect(t, "after the second cleanup", g2)
		rec.pop()
		expect(t, "after the first cleanup", g1)
	})
}

// Install, Current, Require, CurrentStatus and InstallForTest are called from
// many goroutines at once (the doors, the gate, the refresher's callbacks). Run
// under -race: every access to the installed guard, to the strict flag, to the
// enforcement date and to the trusted keys goes through globalsMu, in the seams that
// write them as in the accessors that read them, and nobody holds it while asking
// the guard, which takes it again to read the enforcement date: a writer waiting in
// between would deadlock a reader that did.
func TestTheProcessGuardIsSafeFromManyGoroutines(t *testing.T) {
	account.InstallForTest(t, nil)
	e := newEnv(t) // the fixture enforces the gate
	date, key := account.EnforceDate(), e.f.Key
	e.signIn(0, time.Hour)
	signedIn := e.newGuard(0)
	locked := account.NewGuard(account.GuardOptions{Store: account.OpenStore(t.TempDir(), account.NewMemorySealer()), Now: e.f.Clock.Now})
	t.Cleanup(locked.Close)
	ctx := context.Background()
	const rounds = 1500

	problems := make(chan string, 16)
	report := func(format string, args ...any) {
		select {
		case problems <- fmt.Sprintf(format, args...):
		default:
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	spawn := func(n int, fn func()) {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				fn()
			}()
		}
	}
	spawn(4, func() { // readers
		for i := 0; i < rounds; i++ {
			if g := account.Current(); g != nil && g != signedIn && g != locked {
				report("Current returned a guard nobody installed")
			}
			if err := account.Require(ctx); err != nil && !account.IsLoginRequired(err) {
				report("Require = %v", err)
			}
			if st := account.CurrentStatus(); st.V != 1 || (st.State != account.StateOK && st.State != account.StateLocked) {
				report("CurrentStatus = %+v", st)
			}
			if got := account.EnforceDate(); !got.Equal(date) {
				report("EnforceDate = %v, want %v", got, date)
			}
			if got := account.TrustedKeys(); len(got) != 1 || got[0].KID != key.KID {
				report("TrustedKeys has %d keys, want the fixture's one", len(got))
			}
		}
	})
	spawn(2, func() { // writers
		for i := 0; i < rounds; i++ {
			account.Install(signedIn)
			account.Install(locked)
			account.Install(nil)
		}
	})
	spawn(1, func() { // a test seam, with a test of its own
		for i := 0; i < rounds/3; i++ {
			rec := &recTB{TB: t}
			account.InstallForTest(rec, signedIn)
			rec.end()
		}
	})
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)

	// The watchdog is a timer, not a branch of the loop below: the main goroutine
	// takes globalsMu too, so in a deadlock it is stuck with the others. The lock
	// is then unusable, and every cleanup of a test seam takes it, so t.Fatal
	// would hang in them: the dump of every goroutine's stack that a panic prints
	// is the failure.
	watchdog := time.AfterFunc(30*time.Second, func() {
		panic("the goroutines did not finish in 30 seconds: a deadlock on globalsMu")
	})
	defer watchdog.Stop()
	// The main goroutine flips the strict flag, and sets the date and the keys to what they
	// already are (what the seams lock is under test here, not what they set), while the
	// others run.
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
			rec := &recTB{TB: t}
			account.StrictForTest(rec)
			account.SetEnforceFromForTest(rec, date)
			account.SetTrustedKeysForTest(rec, []account.Key{key})
			rec.end()
		}
	}
	account.Install(nil)
	close(problems)
	for p := range problems {
		t.Error(p)
	}
}

// Require and CurrentStatus are asked on every request, so they call no one and
// write nothing, even for a session that is due for a refresh and whose
// high-water mark is two hours stale: only EnsureFresh, Refresh and the
// refresher refresh, and only they move the mark.
func TestRequireAndCurrentStatusCallNoOneAndWriteNothing(t *testing.T) {
	account.InstallForTest(t, nil)
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour) // an expired token inside its grace
	account.InstallForTest(t, e.newGuard(0))
	before := snapshotDir(t, e.dir)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		e.f.Clock.Advance(time.Minute)
		if err := account.Require(ctx); err != nil {
			t.Fatalf("Require = %v, want the grace to allow", err)
		}
		if st := account.CurrentStatus(); st.State != account.StateGrace {
			t.Fatalf("CurrentStatus = %s/%q, want grace", st.State, st.Reason)
		}
	}
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d calls to monoes.me from Require and CurrentStatus", n)
	}
	if after := snapshotDir(t, e.dir); !sameFiles(before, after) {
		t.Fatalf("Require and CurrentStatus changed the store:\nbefore %v\nafter  %v", describeFiles(before), describeFiles(after))
	}
}

type fileState struct {
	size  int64
	mtime time.Time
	data  string
}

// snapshotDir records every file of dir: its name, size, modification time and content.
func snapshotDir(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fileState{}
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[de.Name()] = fileState{size: info.Size(), mtime: info.ModTime(), data: string(data)}
	}
	return out
}

func sameFiles(a, b map[string]fileState) bool {
	if len(a) != len(b) {
		return false
	}
	for name, x := range a {
		y, ok := b[name]
		if !ok || x.size != y.size || !x.mtime.Equal(y.mtime) || x.data != y.data {
			return false
		}
	}
	return true
}

// describeFiles names the files and sizes of a snapshot, never their content: a
// session file holds a token.
func describeFiles(s map[string]fileState) string {
	parts := []string{}
	for name, f := range s {
		parts = append(parts, fmt.Sprintf("%s(%d bytes, %s)", name, f.size, f.mtime.Format(time.RFC3339Nano)))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}
