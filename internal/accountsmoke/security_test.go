//go:build devaccount && !windows

package accountsmoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// setMark rewrites the high-water mark of the stored session: the highest time the machine has seen.
func (r *rig) setMark(at time.Time) {
	r.t.Helper()
	st := account.OpenStore(r.accountDir(), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil {
		r.t.Fatal("no session to move the mark of")
	}
	sess.HW = at
	must(r.t, st.Save(sess))
}

// agePending makes the stored marker of a refresh in flight (pending_since, A24) d older. A real binary
// has no clock to move, so the smoke ages what the clock is compared with, as backdate does for a token.
func (r *rig) agePending(d time.Duration) {
	r.t.Helper()
	st := account.OpenStore(r.accountDir(), account.NewMemorySealer())
	unlock, err := st.Lock(context.Background())
	must(r.t, err)
	defer unlock()
	sess, err := st.Load()
	must(r.t, err)
	if sess == nil || sess.PendingSince.IsZero() {
		r.t.Fatal("no refresh in flight to age")
	}
	sess.PendingSince = sess.PendingSince.Add(-d)
	must(r.t, st.Save(sess))
}

// killedMidRefresh signs in, expires the access token and kills the command that is in the middle of the
// refresh. monoes.me has rotated the refresh token (modeHold: the edge passes the grant to the fake at once
// and answers holdAnswer later), the answer is lost with the process, and the dead token is still on disk.
// What is left for the next command to find is the marker that the guard wrote before it sent the grant.
func (r *rig) killedMidRefresh() {
	r.t.Helper()
	r.signIn()
	r.backdate(2 * time.Hour) // the access token expired an hour ago: the next command asks for another
	r.edge.set(modeHold)
	p := r.start("account", "status", "--json")
	r.waitFor("the refresh grant to reach monoes.me", 30*time.Second, func() bool { return r.edge.refreshes() == 1 })
	if sess := r.session(); sess == nil || sess.PendingSince.IsZero() {
		r.t.Fatal("the grant was sent before pending_since was written (A24)")
	}
	must(r.t, p.cmd.Process.Kill()) // nothing can finish the grant, and the answer is lost with the process
	<-p.done
	r.edge.set(modePass)
}

// assertStillEnforcedWithTheClockSetBack is what a clock set back looks like to the guard: the date is
// ahead of the real clock and the stored high-water mark says that the machine has seen a time after it.
// A real binary has no clock to set, so the smoke moves the date and the mark. The machine must still be
// refused, and the same date on a machine with no record must be a warn period, in which nothing is.
func (r *rig) assertStillEnforcedWithTheClockSetBack() {
	r.t.Helper()
	date := time.Now().Add(24 * time.Hour).Truncate(time.Second) // the clock is now before this date
	r.setMark(date.Add(time.Hour))                               // and the machine has seen a time after it
	r.enforce = date
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
	if st, code := r.status(); st.State != "locked" || st.Reason != "not_logged_in" || !st.Enforced || code != 4 {
		r.t.Fatalf("account status with the clock set back: exit %d, state %q, reason %q, enforced %v, want locked(not_logged_in), enforced", code, st.State, st.Reason, st.Enforced)
	}
	// The same date and clock on a machine with no record are a warn period, and nothing is refused: the
	// case spec 4.8 accepts (a clock set back before the first check).
	mustExit(r.t, newRig(r.t, rigOptions{enforce: date}).run("workflow", "list"), 0)
}

// lockHeld says whether a process holds the machine's session lock right now.
func (r *rig) lockHeld() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	unlock, err := account.OpenStore(r.accountDir(), account.NewMemorySealer()).Lock(ctx)
	if err == nil {
		unlock()
	}
	return err != nil
}

// A20: a refresh grant, once sent, is always completed and stored. monoes.me rotates the refresh token
// when it answers, and the answer is the only copy of the new one: a command that gives up while the
// answer is on its way leaves the dead token on disk, and the next refresh would end every install of
// the account (spec A7). Here the answer is held back (modeHold) and the command is interrupted while it
// is on its way. The command finishes the grant before it ends, so the next refresh presents the new
// token and nothing is revoked. The edge keeps monoes.me's reuse window, so the test lets it pass
// (later) before the next command: a dead token presented after the window is a replay, which the fake
// punishes, and the scenario needs no wait.
func TestAnInterruptedRefreshIsCompletedAndNeverEndsTheAccount(t *testing.T) {
	for _, c := range []struct {
		name string
		sig  os.Signal
	}{{"Ctrl-C", syscall.SIGINT}, {"SIGTERM", syscall.SIGTERM}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, rigOptions{enforce: past})
			r.signIn()
			r.backdate(2 * time.Hour) // the access token expired an hour ago: the next command asks for another
			r.edge.set(modeHold)      // monoes.me rotates the refresh token at once and answers a few seconds later
			p := r.start("account", "status", "--json")
			r.waitFor("the refresh grant to reach monoes.me", 30*time.Second, func() bool { return r.edge.refreshes() == 1 })
			must(t, p.cmd.Process.Signal(c.sig)) // while the answer is on its way
			r.waitFor("the interrupted command to end", 40*time.Second, func() bool { return !p.alive() })

			r.edge.set(modePass)
			r.edge.later(reuseWindow + time.Second) // five minutes pass at monoes.me: it no longer repeats its answer
			r.backdate(2 * time.Hour)               // what the interrupted command stored is aged too: the next command asks again
			st, code := r.status()
			if st.State != "ok" || code != 0 || r.fake.Replays != 0 || r.edge.refreshes() != 2 {
				t.Fatalf("the refresh after the interruption: exit %d, state %q, reason %q, %d refreshes, %d replays: the interrupted grant was not completed and stored",
					code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
			}
		})
	}
}

// A24, the case A20 cannot close: a command that is killed (SIGKILL, a crash, a power cut) while monoes.me
// is answering cannot finish the grant, because the refresh token is rotated and the new one is lost with
// the process. The guard wrote pending_since before it sent the grant, so the next attempt knows that a
// grant may have been lost. Inside monoes.me's reuse window the retry is immediate and monoes.me repeats
// its answer: a kill costs nothing, and the account is whole.
func TestAKilledRefreshIsRetriedAtOnceInsideMonoesMesWindow(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.killedMidRefresh()
	st, code := r.status() // seconds later, inside the window
	if st.State != "ok" || code != 0 || r.edge.refreshes() != 2 || r.fake.Replays != 0 {
		t.Fatalf("the retry inside the window: exit %d, state %q, reason %q, %d refreshes, %d replays: the guard must present the token again at once, and monoes.me repeats its answer",
			code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
	}
	if sess := r.session(); sess == nil || !sess.PendingSince.IsZero() {
		t.Fatal("the answer to the retry must clear pending_since")
	}
	if _, err := os.Stat(r.refreshFile()); err != nil {
		t.Fatalf("the retry must store the refresh token it was answered with: %v", err)
	}
	r.backdate(2 * time.Hour) // and the account is whole: the next refresh presents the new token
	if st, code := r.status(); st.State != "ok" || code != 0 || r.edge.refreshes() != 3 || r.fake.Replays != 0 {
		t.Fatalf("the refresh after the retry: exit %d, state %q, reason %q, %d refreshes, %d replays", code, st.State, st.Reason, r.edge.refreshes(), r.fake.Replays)
	}
}

// A24, after the window: monoes.me no longer repeats its answer, so presenting the dead token would end
// every install of the account (A7). The guard does not present it: it deletes this machine's refresh
// token, makes no call, and says why (unconfirmed, a grace reason). One install signs in again; the
// account and the other install are untouched. Two installs of one account share monoes.me and the edge.
// The window passes at monoes.me (later) and, on this machine, in the marker (agePending), which is what
// the clock does to a real process.
func TestAKilledRefreshIsNeverPresentedAgainAfterTheWindow(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	other := newRig(t, rigOptions{enforce: past, sharing: r}) // a second install of the same account
	other.signIn()
	r.killedMidRefresh()
	r.edge.later(reuseWindow + 10*time.Second) // monoes.me no longer answers a repeat of the dead token
	r.agePending(reuseWindow + 10*time.Second) // and on this machine the wait is over too
	grants := r.edge.refreshes()

	st, code := r.status()
	if st.State != "grace" || st.Reason != "unconfirmed" || code != 0 || r.edge.refreshes() != grants || r.fake.Replays != 0 {
		t.Fatalf("the attempt after the window: exit %d, state %q, reason %q, %d new grants, %d replays: want grace(unconfirmed) and no call to monoes.me",
			code, st.State, st.Reason, r.edge.refreshes()-grants, r.fake.Replays)
	}
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("this machine must drop the refresh token it cannot vouch for: %v", err)
	}
	res := r.run("--json", "workflow", "list") // what the person sees: the work goes on, and the line says what to do
	mustExit(t, res, 0)
	if !strings.Contains(res.stderr, unconfirmedLine) || strings.Contains(res.stdout, unconfirmedLine) || !strings.HasPrefix(strings.TrimSpace(res.stdout), "[") {
		t.Fatalf("the unconfirmed grace is one line on stderr and never on stdout:\nstdout: %s\nstderr: %s", res.stdout, res.stderr)
	}

	other.backdate(2 * time.Hour) // the account was not revoked: the other install still refreshes
	if st, code := other.status(); st.State != "ok" || code != 0 || r.fake.Replays != 0 {
		t.Fatalf("the other install after this one dropped its token: exit %d, state %q, reason %q, %d replays: the account was ended", code, st.State, st.Reason, r.fake.Replays)
	}

	r.backdate(25 * time.Hour) // the grace is over: locked, with the reason that says why
	if st, code := r.status(); st.State != "locked" || st.Reason != "unconfirmed" || code != 4 {
		t.Fatalf("at the end of the grace: exit %d, state %q, reason %q, want locked(unconfirmed)", code, st.State, st.Reason)
	}
	r.assertLocked(r.run("--json", "workflow", "list"), "unconfirmed", true)
	r.signIn() // this machine signs in again
	mustExit(t, r.run("workflow", "list"), 0)
}

// A25, and spec 4.5: a machine that is refused after the date keeps the clock-guard record from that
// moment, so setting its clock back before the date does not un-enforce it while the record is left in
// place (spec 4.8). It never signed in, and it still needs to.
func TestARefusedMachineStaysEnforcedWhenItsClockIsSetBack(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.assertLocked(r.run("workflow", "list"), "not_logged_in", false) // the machine runs after the date: the refusal leaves the record
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.HW.IsZero() {
		t.Fatal("a refusal after the date must leave the clock-guard record: a session with no token and a high-water mark")
	}
	r.assertStillEnforcedWithTheClockSetBack()
}

// A23, and spec 4.5: the high-water mark is what makes setting the clock back worthless to a user who
// leaves the account folder alone (spec 4.8), and it lives in session.json, so logging out, an open
// command, must not erase it. The mark says the machine ran after the date, and the date is then put
// ahead of the real clock, which is what a clock set back looks like to the guard.
func TestLoggingOutDoesNotUnlockAMachineWhoseClockIsSetBack(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	mustExit(t, r.run("workflow", "list"), 0) // the machine runs, after the date
	mustExit(t, r.run("account", "logout"), 0)
	if sess := r.session(); sess == nil || sess.AccessToken != "" || sess.HW.IsZero() {
		t.Fatal("logging out must leave the clock-guard record: a session with no token and a high-water mark")
	}
	if _, err := os.Stat(r.refreshFile()); !os.IsNotExist(err) {
		t.Fatalf("logging out deletes the refresh token: %v", err)
	}
	r.assertStillEnforcedWithTheClockSetBack()
}

// A22: a key store that waits for ever (a locked keychain, an unlock dialog nobody answers) must not hold
// the machine's session lock, and with it every other process's refresh, for ever. A refresh reads the
// refresh token, and so calls the key store, while it holds the lock; every such call is bounded to 10
// seconds and a timeout is keyring_unavailable (grace). The key store here is the rig's file keyring
// (MONOAGENT_ALLOW_FILE_KEYRING, which the quiet read of the key honors on every OS), and its passphrase
// file is a named pipe that nobody writes to for the one process that is given that path: opening it
// blocks, as an unanswered unlock dialog would.
func TestABlockedKeyStoreDoesNotHoldUpAnotherProcess(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	r.backdate(2 * time.Hour) // the access token expired an hour ago: both commands below are due for a refresh
	pipe := filepath.Join(r.dir, "passphrase.pipe")
	must(t, syscall.Mkfifo(pipe, 0o600))

	stuck := r.startWith([]string{"MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE=" + pipe}, "account", "status", "--json")
	r.waitFor("the blocked command to hold the session lock or to end", 20*time.Second, func() bool { return !stuck.alive() || r.lockHeld() })
	if !stuck.alive() {
		t.Skip("the key store answered without reading the passphrase file, so the pipe blocks nothing here")
	}
	began := time.Now()
	res := r.run("account", "status", "--json") // another process, whose key store answers
	waited := time.Since(began)
	var doc accountStatus
	mustJSON(t, res.stdout, &doc)
	if waited > 20*time.Second || res.code != 0 || (doc.State != "ok" && doc.State != "grace") {
		t.Fatalf("another process behind a blocked key store: waited %v, exit %d, state %q: its wait must stay bounded (about 10 seconds) and account status must still answer",
			waited, res.code, doc.State)
	}
	r.waitFor("the blocked command to give up", 20*time.Second, func() bool { return !stuck.alive() })
	if stuck.err != nil || !strings.Contains(r.read("account.log"), "keyring_unavailable") {
		t.Fatalf("the blocked command must end by itself, in grace with reason keyring_unavailable: %v\n%s", stuck.err, r.read("account.log"))
	}

	// Nothing was lost: with the key store answering, the next refresh works.
	r.backdate(2 * time.Hour)
	if st, code := r.status(); st.State != "ok" || code != 0 {
		t.Fatalf("after the key store answered again: exit %d, state %q, reason %q", code, st.State, st.Reason)
	}
}
