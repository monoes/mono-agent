package account_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// monoes.me rotates the refresh token as it answers a grant. When the answer is
// lost (a connection that dies after the request was written, a timeout, a kill,
// a laptop that sleeps mid-call) the client still holds the dead token, and
// presenting it after the 300 s reuse window ends every refresh token of the
// account (A24). So the guard writes down, under the session lock and before it
// sends a grant, that one is about to go out: pending_since. These tests are about
// the life of that marker: when it is written, and what clears it or keeps it. The
// rule of thumb they pin: the marker survives exactly when monoes.me may have
// rotated the token and the client does not hold the answer.

// pendingOn is the pending_since of the session on disk, the zero time when it has none.
func (e *env) pendingOn() time.Time {
	e.t.Helper()
	return e.session().PendingSince
}

// rawPending is the text of pending_since in session.json, "" when the file has no such key.
func (e *env) rawPending() string {
	e.t.Helper()
	data, err := readReplaced(filepath.Join(e.dir, "session.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(data), `"pending_since": "`)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(rest, `"`)
	return value
}

// The marker is on disk when the request goes out, and the old refresh token is
// still the one on disk: the marker is the only thing that says it may be dead.
func TestTheMarkerIsWrittenBeforeTheGrantIsSent(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // due
			if !e.pendingOn().IsZero() {
				t.Fatal("the signed-in session already carries a marker")
			}
			start := e.f.Clock.Now()
			var duringTheCall time.Time
			var tokenDuringTheCall string
			spied := refresherFunc(func(ctx context.Context, rt string) (*account.TokenSet, error) {
				duringTheCall = e.pendingOn()
				tokenDuringTheCall, _ = e.store.LoadRefresh()
				return e.ref.Refresh(ctx, rt)
			})
			g := e.guardWith(spied)
			if st, err := ep.call(g, context.Background()); err != nil || st.State != account.StateOK {
				t.Fatalf("%s = %s/%q, %v, want ok", ep.name, st.State, st.Reason, err)
			}
			if !duringTheCall.Equal(start) {
				t.Fatalf("pending_since during the call = %v, want the time the attempt began, %v: the marker must be on disk before the grant is sent", duringTheCall, start)
			}
			if tokenDuringTheCall != "rt-1" {
				t.Fatalf("refresh.enc held %q during the call, want the token that is being presented", tokenDuringTheCall)
			}
		})
	}
}

// A grant whose outcome cannot be recorded must not be started: with no marker on
// disk a lost answer would leave a rotated-away token that looks live.
func TestAMarkerThatCannotBeWrittenSendsNothing(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // due
			before := describe(e.session())
			fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSave: true}
			g := e.guardWith(e.ref, fs)
			st, err := ep.call(g, context.Background())
			if n := e.ref.calls.Load(); n != 0 {
				t.Fatalf("%d grants sent although the marker could not be written", n)
			}
			if err == nil || st.State != account.StateGrace {
				t.Fatalf("%s = %s/%q, %v, want grace and the write error (advisory)", ep.name, st.State, st.Reason, err)
			}
			if got := fs.order(); !reflect.DeepEqual(got, []string{"Save"}) {
				t.Fatalf("writes = %v, want only the marker that failed: nothing else is recorded", got)
			}
			if after := describe(e.session()); after != before || !e.pendingOn().IsZero() {
				t.Fatalf("a refresh that did not start changed the session: %s, was %s", after, before)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
				t.Fatalf("refresh.enc holds %q, want the token that was never presented", rt)
			}
			// The disk works again: nothing was held against the next attempt.
			g2 := e.newGuard(0)
			if st, err := ep.call(g2, context.Background()); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
				t.Fatalf("the next attempt = %s/%q, %v with %d grants, want ok after one", st.State, st.Reason, err, e.ref.calls.Load())
			}
		})
	}
}

// The marker is written once, before the grant, and the answer rewrites the session
// without it: a marker that outlived the answer would be dropped 240 s later.
func TestAnAnswerClearsTheMarkerAndStoresTheTokens(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour)
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal)}
	g := e.guardWith(e.ref, fs)
	if st, err := g.EnsureFresh(context.Background()); err != nil || st.State != account.StateOK {
		t.Fatalf("EnsureFresh = %s/%q, %v, want ok", st.State, st.Reason, err)
	}
	if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "SaveRefresh", "Save"}) {
		t.Fatalf("writes = %v, want the marker, the new refresh token and the new session, in that order", got)
	}
	if got := e.rawPending(); got != "" {
		t.Fatalf("session.json still carries pending_since %s after the answer was stored", got)
	}
	if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
		t.Fatalf("refresh.enc holds %q, want the rotated token", rt)
	}
}

// monoes.me answered invalid_grant: nothing is in doubt any more, and the refused
// session keeps what the gate needs. It also keeps the enforcement evidence: a
// refused session whose mark is stale would let a clock set back un-enforce the date.
func TestARefusalClearsTheMarkerAndRaisesTheHighWaterMark(t *testing.T) {
	cases := []struct {
		name string
		hw   time.Duration // the stored mark, relative to now
		want time.Duration // after the refusal, relative to now
	}{
		{"a mark two hours old", -2 * time.Hour, 0},
		{"a mark 30 seconds old", -30 * time.Second, 0},
		{"a mark ahead of the clock is not lowered", 2 * time.Minute, 2 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			sess := e.signIn(2*time.Hour, time.Hour)
			now := e.f.Clock.Now()
			sess.HW = now.Add(c.hw)
			e.save(sess)
			e.ref.set(refusedBy("revoked"))
			if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.Reason != account.ReasonRefused {
				t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused", st.State, st.Reason, err)
			}
			got := e.session()
			if got.State != "refused" || !got.PendingSince.IsZero() || e.rawPending() != "" {
				t.Fatalf("stored session = %s, pending %v, want refused and no marker", describe(got), got.PendingSince)
			}
			if !got.HW.Equal(now.Add(c.want)) {
				t.Fatalf("hw = %v, want %v: a refusal keeps the enforcement evidence and never lowers it", got.HW, now.Add(c.want))
			}
		})
	}
}

// An answer whose access token this build cannot verify is still an answer: the
// server rotated the refresh token and the new one is saved, so nothing is in doubt.
func TestAnAnswerThatDoesNotVerifyClearsTheMarker(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeRefresher)
	}{
		{"an opaque token", func(r *fakeRefresher) { r.badAccess = "opaque-0123456789" }},
		{"a key this build does not pin", func(r *fakeRefresher) { r.kid = "rotated-key" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour)
			e.ref.set(c.setup)
			if st, err := e.g.EnsureFresh(context.Background()); err != nil || st.State != account.StateGrace || st.Reason != account.ReasonServerError {
				t.Fatalf("EnsureFresh = %s/%q, %v, want grace/server_error", st.State, st.Reason, err)
			}
			if got := e.rawPending(); got != "" {
				t.Fatalf("session.json carries pending_since %s: the server answered and its new refresh token is saved", got)
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
				t.Fatalf("refresh.enc holds %q, want the rotated token", rt)
			}
		})
	}
}

// A failure that is settled leaves no doubt: the request was never written, or
// monoes.me answered a complete 4xx that is not invalid_grant, so the token was not
// consumed. The marker this attempt wrote is taken back and the token is presented
// again later, under the normal negative cache. The guard trusts the flag as the
// Refresher reports it: a 5xx is never settled (a gateway's 504 can come after a
// rotation), and B1b's transport is what keeps it so.
func TestASettledFailureTakesBackTheMarkerItWrote(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want account.Reason
	}{
		{"a DNS failure", &account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("lookup monoes.me: no such host")}, account.ReasonUnreachable},
		{"an HTTP 429", &account.TransientError{Reason: account.ReasonServerError, Settled: true, Err: errors.New("HTTP 429 Too Many Requests")}, account.ReasonServerError},
		{"a settled failure that another error wraps", errWrapping(&account.TransientError{Reason: account.ReasonUnreachable, Settled: true, Err: errors.New("dial")}), account.ReasonUnreachable},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.signIn(2*time.Hour, time.Hour)
				e.ref.set(func(r *fakeRefresher) { r.err = c.err })
				st, err := ep.call(e.g, context.Background())
				if err != nil || st.State != account.StateGrace || st.Reason != c.want {
					t.Fatalf("%s = %s/%q, %v, want grace/%q", ep.name, st.State, st.Reason, err, c.want)
				}
				if got := e.rawPending(); got != "" {
					t.Fatalf("session.json carries pending_since %s after a failure that left no doubt", got)
				}
				if sess := e.session(); sess.LastResult != string(c.want) || !sess.LastAttempt.Equal(e.f.Clock.Now()) {
					t.Fatalf("the attempt was not recorded: %s", describe(sess))
				}
				if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
					t.Fatalf("refresh.enc holds %q, want the token that was not consumed", rt)
				}
			})
		}
	}
}

// errWrapping is what a Refresher that adds context to the error it got returns.
func errWrapping(err error) error { return &wrapped{err} }

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "token endpoint: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }

// Every other failure leaves the marker: the request may have been processed, so
// the refresh token may be dead and nothing is known about the answer. Not only a
// *TransientError whose Settled is false: a Refresher that does not say, an error
// of another type, an answer with nothing in it, are all outcomes unknown.
func TestAnUnsettledFailureKeepsTheMarker(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeRefresher)
		want  account.Reason
	}{
		{"a lost answer", func(r *fakeRefresher) { r.err = lostAnswer(account.ReasonUnreachable) }, account.ReasonUnreachable},
		{"a lost answer that is a server error", func(r *fakeRefresher) { r.err = lostAnswer(account.ReasonServerError) }, account.ReasonServerError},
		{"a TransientError that does not say", func(r *fakeRefresher) {
			r.err = &account.TransientError{Reason: account.ReasonUnreachable, Err: errors.New("timeout")}
		}, account.ReasonUnreachable},
		{"an error of no known type", func(r *fakeRefresher) { r.err = errors.New("connection reset by peer") }, account.ReasonUnreachable},
		{"a deadline", func(r *fakeRefresher) { r.err = context.DeadlineExceeded }, account.ReasonUnreachable},
		{"an unsettled error that another error wraps", func(r *fakeRefresher) { r.err = errWrapping(lostAnswer(account.ReasonUnreachable)) }, account.ReasonUnreachable},
		{"a success with no tokens in it", func(r *fakeRefresher) { r.empty = true }, account.ReasonServerError},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.signIn(2*time.Hour, time.Hour)
				e.ref.set(c.setup)
				start := e.f.Clock.Now()
				st, err := ep.call(e.g, context.Background())
				if err != nil || st.State != account.StateGrace || st.Reason != c.want {
					t.Fatalf("%s = %s/%q, %v, want grace/%q", ep.name, st.State, st.Reason, err, c.want)
				}
				if got := e.pendingOn(); !got.Equal(start) {
					t.Fatalf("pending_since = %v, want the time of the attempt, %v: the token may have been rotated", got, start)
				}
				if sess := e.session(); sess.LastResult != string(c.want) || !sess.LastAttempt.Equal(start) {
					t.Fatalf("the attempt was not recorded: %s", describe(sess))
				}
				if rt, _ := e.store.LoadRefresh(); rt != "rt-1" {
					t.Fatalf("refresh.enc holds %q, want the token that was presented", rt)
				}
			})
		}
	}
}

// cancelAfterRefreshRead (guard_refresh_lostanswer_test.go) cancels the caller's
// context once the refresh token has been read, as a Ctrl-C does while a slow key
// store is answering. The caller has given up before anything was sent, so nothing
// is sent and nothing is written down: a marker left for a grant that never went
// out would have the next attempt drop a token that was never presented.
func TestACallerThatGivesUpWhileTheKeyStoreIsReadLeavesNoMarker(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour) // due
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			spy := &cancelAfterRefreshRead{countingStore: newCountingStore(account.OpenStore(e.dir, e.seal)), cancel: cancel}
			g := e.guardOver(spy, 0)
			st, err := ep.call(g, ctx)
			if !errors.Is(err, context.Canceled) || st.State != account.StateGrace || e.ref.calls.Load() != 0 {
				t.Fatalf("%s = %s/%q, %v with %d grants, want grace, context.Canceled and none", ep.name, st.State, st.Reason, err, e.ref.calls.Load())
			}
			if n := spy.calls("Save"); n != 0 {
				t.Fatalf("%d writes of the session by a caller that gave up before sending", n)
			}
			if got := e.rawPending(); got != "" {
				t.Fatalf("session.json carries pending_since %s although no grant was sent", got)
			}
			// A later attempt presents the same token, as if nothing had happened.
			later := e.newGuard(0)
			if st, err := ep.call(later, context.Background()); err != nil || st.State != account.StateOK || e.ref.calls.Load() != 1 {
				t.Fatalf("the later attempt = %s/%q, %v with %d grants, want ok after one", st.State, st.Reason, err, e.ref.calls.Load())
			}
			if rt, _ := e.store.LoadRefresh(); rt != "rt-2" {
				t.Fatalf("refresh.enc holds %q, want the token the later attempt rotated to", rt)
			}
		})
	}
}

// RULING A24(d): the answer arrived and its rotated refresh token could not be
// saved (the key store hiccups between the successful read and the Seal). The
// answer is then treated exactly as a lost one: the old token, which monoes.me has
// rotated away, stays on disk with the marker, and recordAttempt says
// keyring_unavailable. It is NOT deleted: the marker covers it, and a retry inside
// the window gets the same answer again and can store it.
func TestAnAnswerWhoseRefreshTokenCannotBeSavedKeepsTheOldTokenAndTheMarker(t *testing.T) {
	e := newEnv(t)
	e.signIn(56*time.Minute, time.Hour) // due, and ok for four more minutes
	start := e.f.Clock.Now()
	fs := &failingStore{Store: account.OpenStore(e.dir, e.seal), failSaveRefresh: true}
	g := e.guardWith(e.ref, fs)
	st, err := g.EnsureFresh(context.Background())
	if !errors.Is(err, account.ErrKeyringUnavailable) || st.State != account.StateOK || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d grants, want ok, the write error and one grant", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if got := fs.order(); !reflect.DeepEqual(got, []string{"Save", "SaveRefresh", "Save"}) {
		t.Fatalf("writes = %v, want the marker, the refresh token that failed and the record of the attempt: no DeleteRefresh", got)
	}
	if rt, err := e.store.LoadRefresh(); err != nil || rt != "rt-1" {
		t.Fatalf("refresh.enc holds %q (%v), want the old token: the marker covers it, deleting it would lose the way back", rt, err)
	}
	sess := e.session()
	if sess.LastResult != string(account.ReasonKeyringUnavailable) || !sess.PendingSince.Equal(start) || !sess.LastAttempt.Equal(start) {
		t.Fatalf("stored session = %s with pending %v, want keyring_unavailable at %v and the marker this attempt wrote", describe(sess), sess.PendingSince, start)
	}
}

// leavePending stores the marker that an earlier, unsettled attempt would have left
// (by this process or another one): the grant went out at `at` and monoes.me may
// have rotated the refresh token.
func (e *env) leavePending(at time.Time) {
	e.t.Helper()
	sess := e.session()
	sess.PendingSince = at
	e.save(sess)
}

// monoes.me's reuse window runs from the FIRST request it may have answered, and a
// retry inside it consumes nothing new, so the stamp of a retry that is lost again
// is the stamp it found, not a new one: a retry at +210 s that is lost again would
// otherwise look young at +450 s, and its token would be presented about 450 s after
// the rotation, outside the window, with every install revoked as the price.
func TestARetryThatIsLostAgainKeepsTheStampItFound(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"a lost answer", lostAnswer(account.ReasonUnreachable)},
		{"an error of no known type", errors.New("connection reset by peer")},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.signIn(2*time.Hour, time.Hour)
			first := e.f.Clock.Now().Add(-90 * time.Second)
			e.leavePending(first)
			raw := e.rawPending()
			e.ref.set(func(r *fakeRefresher) { r.err = c.err })
			if _, err := e.g.EnsureFresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.ref.calls.Load() != 1 {
				t.Fatalf("%d grants, want the retry", e.ref.calls.Load())
			}
			if got := e.rawPending(); got != raw {
				t.Fatalf("pending_since = %q after the retry, want the stamp of the first send, %q, byte for byte", got, raw)
			}
		})
	}
}

// A retry that fails settled says nothing about the send before it: that one may
// have been answered, and its answer is the one that was lost. Taking the marker back
// would let the token be presented at any later time, outside the window.
func TestASettledFailureOfARetryKeepsTheStampOfTheEarlierSend(t *testing.T) {
	e := newEnv(t)
	e.signIn(2*time.Hour, time.Hour)
	first := e.f.Clock.Now().Add(-90 * time.Second)
	e.leavePending(first)
	raw := e.rawPending()
	e.ref.set(func(r *fakeRefresher) { r.err = transient(account.ReasonUnreachable) }) // settled: the request never left this machine
	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateGrace || st.Reason != account.ReasonUnreachable || e.ref.calls.Load() != 1 {
		t.Fatalf("EnsureFresh = %s/%q, %v with %d grants, want grace/unreachable after the retry", st.State, st.Reason, err, e.ref.calls.Load())
	}
	if got := e.rawPending(); got != raw {
		t.Fatalf("pending_since = %q after a settled failure of the retry, want the stamp of the send that may have rotated the token, %q", got, raw)
	}
	if sess := e.session(); sess.LastResult != "unreachable" || !sess.LastAttempt.Equal(e.f.Clock.Now()) {
		t.Fatalf("the attempt was not recorded: %s", describe(sess))
	}
}
