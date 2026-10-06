package account

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// These tests are in package account because dueForRefresh, refreshIfDue, its
// outcome and the background mode are not exported: the refresher loop is their
// only caller, and no public call shows them. They cannot use accounttest, which
// imports account: rig signs its tokens with a throwaway key it makes itself.

func TestDueForRefreshDecidesByStateModeAndLastAttempt(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	session := func(lastAttempt time.Time) *Session {
		return &Session{V: 1, AccessToken: "token", LastAttempt: lastAttempt}
	}
	// The receipt of a token that lives an hour and was issued age ago.
	receipt := func(age time.Duration) *Receipt {
		iat := now.Add(-age)
		return &Receipt{Sub: "u-1", IssuedAt: iat, ExpiresAt: iat.Add(time.Hour)}
	}
	// The same for a token that lives eight minutes: its half-life comes after the CLI's margin.
	shortReceipt := func(age time.Duration) *Receipt {
		iat := now.Add(-age)
		return &Receipt{Sub: "u-1", IssuedAt: iat, ExpiresAt: iat.Add(8 * time.Minute)}
	}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	never := time.Time{}
	ok := Status{State: StateOK}
	locked := func(r Reason) Status { return Status{State: StateLocked, Reason: r} }

	// What can never be refreshed is not due in either mode, and does not crash.
	unrefreshable := []struct {
		name string
		sess *Session
		st   Status
	}{
		{"no session", nil, locked(ReasonNotLoggedIn)},
		{"a refused session, a token still in it", &Session{V: 1, AccessToken: "token", State: stateRefused}, locked(ReasonRefused)},
		{"no token, not refused", &Session{V: 1}, locked(ReasonNotLoggedIn)},
	}
	for _, mode := range []refreshMode{modeCLI, modeBackground} {
		for _, c := range unrefreshable {
			if dueForRefresh(c.sess, c.st, nil, now, mode) {
				t.Errorf("mode %d, %s: due, want never", mode, c.name)
			}
		}
	}

	cases := []struct {
		name string
		mode refreshMode
		st   Status
		rcpt *Receipt // nil for a token that does not verify
		last time.Time
		want bool
	}{
		// CLI: a session that is not ok is due, unless it was tried a minute ago or less.
		{"cli, in grace, never tried", modeCLI, Status{State: StateGrace}, receipt(2 * time.Hour), never, true},
		{"cli, expired, never tried", modeCLI, locked(ReasonExpired), receipt(30 * time.Hour), never, true},
		{"cli, a token this build cannot verify", modeCLI, locked(ReasonKeyUnknown), nil, never, true},
		{"cli, a clock that went back", modeCLI, locked(ReasonClockRollback), receipt(10 * time.Minute), never, true},
		{"cli, not ok, tried 30 seconds ago", modeCLI, Status{State: StateGrace}, receipt(2 * time.Hour), ago(30 * time.Second), false},
		{"cli, not ok, tried 59 seconds ago", modeCLI, Status{State: StateGrace}, receipt(2 * time.Hour), ago(59 * time.Second), false},
		{"cli, not ok, tried a minute ago", modeCLI, Status{State: StateGrace}, receipt(2 * time.Hour), ago(time.Minute), true},
		{"cli, not ok, last try dated in the future", modeCLI, Status{State: StateGrace}, receipt(2 * time.Hour), now.Add(time.Hour), true},
		// CLI: a session that is ok is due under five minutes left, and not before.
		{"cli, ok, 25 minutes left", modeCLI, ok, receipt(35 * time.Minute), never, false},
		{"cli, ok, 5 minutes left", modeCLI, ok, receipt(55 * time.Minute), never, false},
		{"cli, ok, 4 minutes 59 seconds left", modeCLI, ok, receipt(55*time.Minute + time.Second), never, true},
		{"cli, ok, under the margin, tried 30 seconds ago", modeCLI, ok, receipt(56 * time.Minute), ago(30 * time.Second), false},
		{"cli, ok, under the margin, tried a minute ago", modeCLI, ok, receipt(56 * time.Minute), ago(time.Minute), true},
		// Background: a session that is ok is due at half its lifetime, tried or not.
		{"background, ok, 20 minutes in", modeBackground, ok, receipt(20 * time.Minute), never, false},
		{"background, ok, 29 minutes 59 seconds in", modeBackground, ok, receipt(30*time.Minute - time.Second), never, false},
		{"background, ok, half the lifetime, to the second", modeBackground, ok, receipt(30 * time.Minute), never, true},
		{"background, ok, 31 minutes in", modeBackground, ok, receipt(31 * time.Minute), never, true},
		{"background, ok, 40 minutes in", modeBackground, ok, receipt(40 * time.Minute), never, true},
		{"background, ok, past half, tried 10 seconds ago", modeBackground, ok, receipt(31 * time.Minute), ago(10 * time.Second), true},
		// The Status and the receipt are read one after the other: a token that stopped verifying in between is not ok.
		{"cli, a Status of ok taken before the cache was swapped for a token that does not verify", modeCLI, ok, nil, never, true},
		{"background, the same", modeBackground, ok, nil, never, true},
		// The two rules are the margin and the half-life, each for its own mode, whatever the lifetime.
		{"cli, ok, 8-minute token, 2 minutes in: 6 minutes left", modeCLI, ok, shortReceipt(2 * time.Minute), never, false},
		{"cli, ok, 8-minute token, 3 minutes 30 seconds in: under the margin", modeCLI, ok, shortReceipt(3*time.Minute + 30*time.Second), never, true},
		{"background, ok, 8-minute token, 3 minutes 30 seconds in: under the CLI margin, before its half", modeBackground, ok, shortReceipt(3*time.Minute + 30*time.Second), never, false},
		{"background, ok, 8-minute token, half its life", modeBackground, ok, shortReceipt(4 * time.Minute), never, true},
		// Background: a session that is not ok is due, tried or not: the loop keeps its own backoff.
		{"background, not ok, never tried", modeBackground, Status{State: StateGrace}, receipt(2 * time.Hour), never, true},
		{"background, not ok, tried 10 seconds ago", modeBackground, Status{State: StateGrace}, receipt(2 * time.Hour), ago(10 * time.Second), true},
		{"background, a token this build cannot verify, tried 10 seconds ago", modeBackground, locked(ReasonKeyUnknown), nil, ago(10 * time.Second), true},
	}
	for _, c := range cases {
		if got := dueForRefresh(session(c.last), c.st, c.rcpt, now, c.mode); got != c.want {
			t.Errorf("%s: due = %t, want %t", c.name, got, c.want)
		}
	}

	// The negative cache counts from the last attempt, not from the mark.
	sess := session(ago(30 * time.Second))
	sess.HW = ago(2 * time.Hour)
	if dueForRefresh(sess, Status{State: StateGrace}, receipt(2*time.Hour), now, modeCLI) {
		t.Error("cli, a mark two hours old but an attempt 30 seconds ago: due, want held off by the attempt")
	}
}

// rig is one machine for the tests that call refreshIfDue: a throwaway signing
// key the package trusts, a clock, a session directory and a fake monoes.me that
// rotates the refresh token on every use, as the real one does.
type rig struct {
	t     *testing.T
	clock *stepClock
	dir   string
	seal  Sealer
	store Store
	priv  ed25519.PrivateKey
	srv   *rotatingServer
	g     *Guard
}

type rotatingServer struct {
	rig   *rig
	calls atomic.Int32
	valid string        // the one refresh token it accepts
	seq   int           // how many it has issued
	err   error         // answered instead of tokens
	bad   string        // the access token it answers with instead of a good one
	after func()        // runs inside the call, once it is counted
	life  time.Duration // of the access tokens it mints; an hour when zero

	// window, when set, is how long monoes.me repeats its answer to the token it has just
	// rotated away (300 s in plan A); zero refuses a token that was used, as before.
	window     time.Duration
	prev       string
	prevAnswer *TokenSet
	prevAt     time.Time
}

func (s *rotatingServer) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	s.calls.Add(1)
	if s.after != nil {
		s.after()
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.window > 0 && refreshToken == s.prev && s.rig.clock.Now().Sub(s.prevAt) <= s.window {
		return s.prevAnswer, nil // inside the reuse window: the same answer again
	}
	if refreshToken != s.valid {
		return nil, &RefusedError{Description: "refresh token already used"}
	}
	s.prev, s.prevAt = refreshToken, s.rig.clock.Now()
	s.seq++
	s.valid = fmt.Sprintf("rt-%d", s.seq+1)
	life := s.life
	if life == 0 {
		life = time.Hour
	}
	access := s.rig.token(s.rig.clock.Now(), life)
	if s.bad != "" {
		access = s.bad
	}
	s.prevAnswer = &TokenSet{AccessToken: access, RefreshToken: s.valid}
	return s.prevAnswer, nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, clock: newStepClock(), dir: filepath.Join(t.TempDir(), "account"), seal: NewMemorySealer(), priv: priv}
	SetTrustedKeysForTest(t, []Key{{KID: "rig-key", Public: pub}})
	SetEnforceFromForTest(t, r.clock.Now().Add(-24*time.Hour))
	r.store = OpenStore(r.dir, r.seal)
	r.srv = &rotatingServer{rig: r, valid: "rt-1"}
	r.g = NewGuard(GuardOptions{Store: r.store, Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(r.g.Close)
	return r
}

// token signs an access token issued at iat for life.
func (r *rig) token(iat time.Time, life time.Duration) string {
	r.t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "EdDSA", "kid": "rig-key", "typ": "JWT"})
	if err != nil {
		r.t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{"iss": Issuer, "aud": Audience, "azp": ClientID, "sub": "u-1", "iat": iat.Unix(), "exp": iat.Add(life).Unix()})
	if err != nil {
		r.t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	signed := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	return signed + "." + enc.EncodeToString(ed25519.Sign(r.priv, []byte(signed)))
}

// signIn stores the session a login would have: a token issued age ago for an
// hour, its mark and last attempt at its iat, and the refresh token the server
// accepts.
func (r *rig) signIn(age time.Duration) *Session {
	r.t.Helper()
	now := r.clock.Now()
	sess, err := NewSession(HostURL, r.token(now.Add(-age), time.Hour), &User{ID: "u-1"}, now)
	if err != nil {
		r.t.Fatal(err)
	}
	sess.LastAttempt = now.Add(-age)
	if err := r.store.Save(sess); err != nil {
		r.t.Fatal(err)
	}
	if err := r.store.SaveRefresh("rt-1"); err != nil {
		r.t.Fatal(err)
	}
	return sess
}

var outcomeNames = map[outcome]string{outcomeSkipped: "skipped", outcomeRefreshed: "refreshed", outcomeFailed: "failed", outcomeRefused: "refused"}

// refreshIfDue says what it did, for each mode: the refresher loop's decisions
// rest on it.
func TestRefreshIfDueSaysWhatItDid(t *testing.T) {
	cases := []struct {
		name       string
		mode       refreshMode
		age        time.Duration // of the token in the session, which lives an hour
		attemptAgo time.Duration // when the session says it last tried; zero: as old as the token
		serverErr  error
		badToken   string
		noRefresh  bool // refresh.enc is gone
		refused    bool // the session is marked refused
		want       outcome
		wantCalls  int32
	}{
		{name: "a healthy token, the CLI rule", mode: modeCLI, age: 10 * time.Minute, want: outcomeSkipped},
		{name: "under five minutes left", mode: modeCLI, age: 56 * time.Minute, want: outcomeRefreshed, wantCalls: 1},
		{name: "the server cannot be reached", mode: modeCLI, age: 2 * time.Hour, serverErr: &TransientError{Reason: ReasonUnreachable, Settled: true, Err: errors.New("no route")}, want: outcomeFailed, wantCalls: 1},
		{name: "invalid_grant", mode: modeCLI, age: 2 * time.Hour, serverErr: &RefusedError{Description: "revoked"}, want: outcomeRefused, wantCalls: 1},
		{name: "no refresh token on disk", mode: modeCLI, age: 2 * time.Hour, noRefresh: true, want: outcomeFailed},
		{name: "a token that does not verify", mode: modeCLI, age: 2 * time.Hour, badToken: "opaque-0123456789", want: outcomeFailed, wantCalls: 1},
		{name: "past the half-life, the background rule", mode: modeBackground, age: 31 * time.Minute, want: outcomeRefreshed, wantCalls: 1},
		{name: "before the half-life, the background rule", mode: modeBackground, age: 29 * time.Minute, want: outcomeSkipped},
		{name: "past the half-life, tried 10 seconds ago, the background rule", mode: modeBackground, age: 31 * time.Minute, attemptAgo: 10 * time.Second, want: outcomeRefreshed, wantCalls: 1},
		{name: "past the half-life, tried 10 seconds ago, the CLI rule", mode: modeCLI, age: 31 * time.Minute, attemptAgo: 10 * time.Second, want: outcomeSkipped},
		{name: "expired, tried 10 seconds ago, the CLI rule", mode: modeCLI, age: 2 * time.Hour, attemptAgo: 10 * time.Second, want: outcomeSkipped},
		{name: "expired, tried 10 seconds ago, the background rule", mode: modeBackground, age: 2 * time.Hour, attemptAgo: 10 * time.Second, want: outcomeRefreshed, wantCalls: 1},
		{name: "a refused session, the background rule", mode: modeBackground, age: 2 * time.Hour, refused: true, want: outcomeSkipped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			sess := r.signIn(c.age)
			if c.attemptAgo > 0 {
				sess.LastAttempt = r.clock.Now().Add(-c.attemptAgo)
				if err := r.store.Save(sess); err != nil {
					t.Fatal(err)
				}
			}
			if c.refused {
				if err := r.store.Save(&Session{V: 1, Host: HostURL, User: &User{ID: "u-1"}, State: stateRefused}); err != nil {
					t.Fatal(err)
				}
			}
			if c.noRefresh {
				if err := r.store.DeleteRefresh(); err != nil {
					t.Fatal(err)
				}
			}
			r.srv.err, r.srv.bad = c.serverErr, c.badToken
			_, got, err := r.g.refreshIfDue(context.Background(), c.mode)
			if err != nil {
				t.Fatalf("refreshIfDue: %v", err)
			}
			if got != c.want || r.srv.calls.Load() != c.wantCalls {
				t.Fatalf("refreshIfDue = %s with %d network refreshes, want %s with %d", outcomeNames[got], r.srv.calls.Load(), outcomeNames[c.want], c.wantCalls)
			}
		})
	}
}

// A pass that could not start is skipped, with the reason as an advisory error.
func TestRefreshIfDueSkipsWhatItCannotStart(t *testing.T) {
	skipped := func(t *testing.T, r *rig, ctx context.Context, mode refreshMode, wantErr error, wantCalls int32) {
		t.Helper()
		st, got, err := r.g.refreshIfDue(ctx, mode)
		if got != outcomeSkipped || !errors.Is(err, wantErr) || r.srv.calls.Load() != wantCalls {
			t.Fatalf("refreshIfDue = %s, %v with %d network refreshes, want skipped, %v and %d", outcomeNames[got], err, r.srv.calls.Load(), wantErr, wantCalls)
		}
		if st.State == "" {
			t.Fatal("the Status that comes with a skipped pass is empty")
		}
	}
	t.Run("the lock is held by another process", func(t *testing.T) {
		r := newRig(t)
		r.signIn(56 * time.Minute)
		unlock, err := OpenStore(r.dir, r.seal).Lock(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		skipped(t, r, ctx, modeCLI, context.DeadlineExceeded, 0)
	})
	t.Run("the caller gave up before it began", func(t *testing.T) {
		r := newRig(t)
		r.signIn(2 * time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		skipped(t, r, ctx, modeCLI, context.Canceled, 0)
	})
}

// A caller that gives up during the call does not end the pass differently from
// how it would have ended: the call is not cancelled by it, so a failure is the
// call's failure, recorded as any other, and an answer is an answer.
func TestARefreshPassWhoseCallerGivesUpDuringTheCallEndsAsItWouldHave(t *testing.T) {
	t.Run("a failure is recorded like any other", func(t *testing.T) {
		r := newRig(t)
		r.signIn(2 * time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.srv.after, r.srv.err = cancel, errors.New("interrupted")
		_, got, err := r.g.refreshIfDue(ctx, modeCLI)
		if got != outcomeFailed || err != nil || r.srv.calls.Load() != 1 {
			t.Fatalf("refreshIfDue = %s, %v with %d network refreshes, want failed, no error and one", outcomeNames[got], err, r.srv.calls.Load())
		}
		sess, err := r.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if sess.LastResult != string(ReasonUnreachable) || !sess.LastAttempt.Equal(r.clock.Now()) {
			t.Fatalf("the failure was recorded as %q at %v, want %q at %v", sess.LastResult, sess.LastAttempt, ReasonUnreachable, r.clock.Now())
		}
	})
	t.Run("an answer that arrives as the caller gives up is kept", func(t *testing.T) {
		r := newRig(t)
		r.signIn(2 * time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.srv.after = cancel
		if _, got, err := r.g.refreshIfDue(ctx, modeCLI); got != outcomeRefreshed || err != nil {
			t.Fatalf("refreshIfDue = %s, %v, want refreshed", outcomeNames[got], err)
		}
		if rt, _ := r.store.LoadRefresh(); rt != "rt-2" {
			t.Fatal("the rotated refresh token was lost")
		}
	})
}
