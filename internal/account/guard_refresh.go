package account

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// refreshMode says when a session is due for a refresh.
type refreshMode int

const (
	// modeCLI: under RefreshMargin left, or not ok at all, and not tried in the
	// last NegativeCache. What EnsureFresh and Refresh use, so an offline CLI
	// call pays the connect timeout at most once a minute.
	modeCLI refreshMode = iota
	// modeBackground: at half the token's lifetime, or not ok at all. The
	// refresher loop keeps its own backoff, so the stored attempt time does not
	// hold it off.
	modeBackground
)

// outcome is what one pass of refreshIfDue did.
type outcome int

const (
	outcomeSkipped   outcome = iota // nothing was due, or nothing could be tried
	outcomeRefreshed                // a new token was verified and stored
	outcomeFailed                   // an attempt failed in a way that keeps the grace
	outcomeRefused                  // monoes.me refused the refresh token
)

// EnsureFresh refreshes the session when it is due, and does nothing while the
// package is dormant (spec D22: no implicit call to monoes.me). It also keeps
// the high-water mark current. The returned Status is always usable; the error
// is advisory (the lock could not be taken in time, the context ended, a write
// failed) and a caller must not fail a command because of it.
//
// ctx ends the waits that come before anything is sent: the wait for the
// in-process slot and for the lock. A caller whose context has ended before the
// request is sent, even while the refresh token is being read, sends nothing and
// gets the context's error, unless the read failed (the key store failed or did
// not answer, or there is no refresh token it can open): that is recorded as
// keyring_unavailable and not reported as the context's error. Once the refresh request is sent it
// is not cancelled by ctx, since monoes.me rotates the refresh token as it
// answers and the answer must be stored: EnsureFresh then returns only after the
// answer is stored, which can take up to refreshCallTimeout (20 s) after ctx has
// ended, and then the write of the new refresh token to the key store, which the
// store gives up on after keyStoreTimeout (10 s). A process must not exit before
// it returns.
func (g *Guard) EnsureFresh(ctx context.Context) (Status, error) {
	if dormant() {
		return g.Status(), nil
	}
	st, _, err := g.refreshIfDue(ctx, modeCLI)
	return st, err
}

// Refresh is EnsureFresh that also runs while dormant: an explicit `account
// status` refreshes when due even before the enforcement date. It ends as
// EnsureFresh does: a request that was sent runs to its answer, whatever ctx
// does, and Refresh returns after the answer is stored.
func (g *Guard) Refresh(ctx context.Context) (Status, error) {
	st, _, err := g.refreshIfDue(ctx, modeCLI)
	return st, err
}

// dueForRefresh decides from the cached session whether a refresh should be
// tried now. A session that is not ok (grace, expired, a clock that went back,
// a token this build cannot verify) is always due: only a new token repairs it.
// So is one with a marker, in both modes and whatever the margin and the negative
// cache say (A24): a grant that may have been answered is retried at once while
// monoes.me still repeats its answer, and dropped after that, and the lock path
// decides which by the age of the marker.
func dueForRefresh(sess *Session, st Status, rcpt *Receipt, now time.Time, mode refreshMode) bool {
	if sess == nil || sess.State == stateRefused || sess.AccessToken == "" {
		return false
	}
	if !sess.PendingSince.IsZero() {
		return true
	}
	// Both callers read the Status and then the cache, one after the other, and a
	// poll in another goroutine can swap the cache between the two reads: a token
	// that stopped verifying in between leaves a session with a token and no
	// receipt beside a Status that still says ok. Without a receipt it is not ok.
	switch {
	case rcpt == nil || st.State != StateOK:
	case mode == modeCLI && rcpt.ExpiresAt.Sub(now) < RefreshMargin:
	case mode == modeBackground && !now.Before(rcpt.IssuedAt.Add(rcpt.ExpiresAt.Sub(rcpt.IssuedAt)/2)):
	default:
		return false
	}
	return mode == modeBackground || elapsed(now, sess.LastAttempt, NegativeCache)
}

// refreshIfDue is one pass of the refresh algorithm (spec §4.4): decide from the
// cache without touching the disk, and only when a refresh is due take the
// cross-process lock and read the session again.
func (g *Guard) refreshIfDue(ctx context.Context, mode refreshMode) (Status, outcome, error) {
	if err := ctx.Err(); err != nil {
		return g.Status(), outcomeSkipped, err // a caller that has given up starts nothing
	}
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-ctx.Done():
		return g.Status(), outcomeSkipped, ctx.Err()
	}
	st := g.Status()
	now := g.now()
	sess, rcpt := g.cached()
	if g.refresher == nil || !dueForRefresh(sess, st, rcpt, now, mode) {
		g.touchHW(now)
		return g.Status(), outcomeSkipped, nil
	}
	return g.refreshUnderLock(ctx, mode)
}

// refreshUnderLock is the part of a refresh that holds the cross-process lock:
// it reads the session again, and if it is still due it reads the refresh token,
// sends the grant and stores what came back. ctx matters until the request is
// sent (the lock wait, the two checks below); the request itself runs on a
// context of its own, so that a request monoes.me may have answered is never
// abandoned and its answer is always stored.
func (g *Guard) refreshUnderLock(ctx context.Context, mode refreshMode) (Status, outcome, error) {
	lctx, cancel := context.WithTimeout(ctx, lockWaitTimeout)
	unlock, err := g.store.Lock(lctx)
	cancel()
	if err != nil {
		return g.Status(), outcomeSkipped, fmt.Errorf("account: taking the session lock: %w", err)
	}
	defer unlock()

	// Another process may have refreshed, signed in or been refused while this
	// one waited: read the session again and decide again.
	sess, err := g.store.Load()
	if err != nil {
		return g.Status(), outcomeSkipped, err
	}
	g.adopt(sess)
	st := g.Status()
	now := g.now()
	sess, rcpt := g.cached()
	if !dueForRefresh(sess, st, rcpt, now, mode) {
		return st, outcomeSkipped, nil
	}

	if err := ctx.Err(); err != nil {
		return st, outcomeSkipped, err // the context ended while this waited for the lock
	}
	refreshToken, err := g.store.LoadRefresh()
	if err != nil || refreshToken == "" {
		switch {
		case err == nil && !sess.PendingSince.IsZero():
			// No token beside a marker: a drop that stopped after it took the token out (a
			// crash, a record that could not be saved). It is finished here, with the
			// record it would have written, and nothing is sent (A24).
			return g.recordAttempt(withoutPending(sess), now, string(ReasonUnconfirmed))
		case err == nil && sess.LastResult == string(ReasonUnconfirmed):
			// A drop took the refresh token and recorded why (A24). There is nothing to
			// present, and a key store problem recorded over that reason would have the
			// grace say the wrong thing, and its end say expired instead of unconfirmed.
			return st, outcomeSkipped, nil
		}
		// Unreadable: the key store is unavailable or does not answer in time, or
		// the file is gone or does not open. Not a decision about the account, so
		// the grace applies.
		return g.recordAttempt(sess, now, string(ReasonKeyringUnavailable))
	}
	if pendingExpired(sess, now) {
		// The grant that left the marker is out of reach of monoes.me's reuse window, or
		// the clock went back and its age cannot be told: the token is not presented.
		return g.dropUnconfirmed(sess, now)
	}
	if err := ctx.Err(); err != nil {
		// The last place where giving up sends nothing: reading the refresh token
		// can take a while (a key store that prompts), and from the call on the
		// context is no longer the caller's. It stays before the marker below: a
		// marker left for a grant that was never sent would have the next attempt
		// drop a token that was never presented.
		return st, outcomeSkipped, err
	}
	// Write down that a grant is about to go out (A24), so that a process that dies
	// or an answer that is lost leaves the evidence beside the token that may now
	// be dead. A grant whose outcome cannot be recorded is not sent.
	sess, stamped, err := g.markPending(sess, now)
	if err != nil {
		return g.Status(), outcomeFailed, err
	}
	// The grant gets a context of its own, not the caller's: Ctrl-C, SIGTERM, a
	// deadline or the guard closing must not abandon a request monoes.me may
	// already have answered. It rotates the refresh token as it answers, so the
	// one on disk is dead from then on, and presenting it after the reuse window
	// is taken for theft: monoes.me ends every refresh token of the account. A
	// request abandoned mid-call leaves exactly that token on disk. So a request
	// that was sent runs to its answer, or to refreshCallTimeout, and what it got
	// is stored whatever the caller does. WithoutCancel also drops the caller's
	// deadline: the timeout below is the only one.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshCallTimeout)
	ts, err := g.refresher.Refresh(cctx, refreshToken)
	cancel()
	if isTypedNil(err) {
		// A Refresher that returns its error variable unconditionally means "no error"
		// by a nil one. Read it so: dropping the token set that comes with it would keep
		// the retired refresh token, and the next call would present it.
		err = nil
	}

	var refused *RefusedError
	var transient *TransientError
	result := ReasonUnreachable // D27: every failure that is not invalid_grant is "unreachable" unless it says server_error
	switch {
	case errors.As(err, &refused) && refused != nil:
		return g.applyRefusal(sess, now, refused)
	case err == nil && ts != nil:
		// Even a token set with no usable access token: by answering, the server has
		// rotated the refresh token, so applyTokens must keep the new one. An access
		// token that does not verify, an empty one included, is a server error there.
		return g.applyTokens(sess, now, refreshToken, ts)
	case err == nil:
		result = ReasonServerError // an answer with nothing in it is the server's fault
	case errors.As(err, &transient) && transient != nil:
		if transient.Reason == ReasonServerError {
			result = ReasonServerError
		}
		if transient.Settled && stamped {
			// Nothing was consumed: the request never left this machine, or monoes.me
			// answered with an HTTP status. The marker this attempt wrote says nothing
			// then and is taken back. A marker that an earlier attempt left stays as it
			// is: that grant may have been answered, and this one says nothing about it.
			sess = withoutPending(sess)
		}
	}
	// Any other failure keeps the marker: the request may have been processed, so
	// the refresh token may be dead and the answer is not held.
	return g.recordAttempt(sess, now, string(result))
}

// isTypedNil reports whether err is an error that holds a nil pointer, as
// `var terr *TransientError; return ts, terr` makes of a success: a non-nil error
// interface with nothing in it. Only pointers count, and a nil error is not one
// (its reflect kind is Invalid).
func isTypedNil(err error) bool {
	v := reflect.ValueOf(err)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// applyTokens stores a successful refresh. The server rotates the refresh
// token on use, so the old one is dead, and presenting a rotated-away token
// again is taken for theft: monoes.me then revokes every refresh token of the
// account, which locks every install of it. Hence the order and the cleanup.
// The new refresh token is saved before the session, even when the access token
// that came with it is not usable, so a crash in between leaves a working pair.
// If it cannot be saved, the answer is treated as a lost one (A24): the old token,
// which monoes.me has rotated away, stays on disk with the marker that says so, and
// the attempt is recorded as a key store failure. Within the retry window the next
// attempt presents it again and monoes.me repeats its answer, which can be stored
// then; after it the token is dropped without being presented. Deleting it here
// would only be the same drop without the retry, and a delete that fails would leave
// a dead token with nothing to say so.
func (g *Guard) applyTokens(cur *Session, now time.Time, oldRefresh string, ts *TokenSet) (Status, outcome, error) {
	next, verr := NewSession(cur.Host, ts.AccessToken, cur.User, now)
	if ts.RefreshToken != "" && ts.RefreshToken != oldRefresh {
		if err := g.store.SaveRefresh(ts.RefreshToken); err != nil {
			st, oc, _ := g.recordAttempt(cur, now, string(ReasonKeyringUnavailable))
			return st, oc, err
		}
	}
	if verr != nil {
		if ts.RefreshToken != "" {
			// An answer that says which refresh token to hold (the new one is saved, or
			// the old one is confirmed) is a definitive one, whatever its access token is
			// worth: nothing is in doubt any more. An answer with neither token tells
			// nothing about the one that was presented, as one with no token set does.
			cur = withoutPending(cur)
		}
		// A token this build cannot verify is never stored: a bad monoes.me
		// deploy (an opaque token, a wrong audience) must not log every online
		// install out. A kid it does not know is remembered, so that when the
		// grace ends the verdict says key_unknown (run update), not expired.
		result := string(ReasonServerError)
		var ve *VerifyError
		if errors.As(verr, &ve) && ve.Reason == ReasonKeyUnknown {
			result = string(ReasonKeyUnknown)
		}
		return g.recordAttempt(cur, now, result)
	}
	err := g.store.Save(next)
	g.adopt(next)
	return g.Status(), outcomeRefreshed, err
}

// applyRefusal records that monoes.me answered invalid_grant: the session is
// marked refused (keeping the user for the message) until a new sign-in.
func (g *Guard) applyRefusal(cur *Session, now time.Time, r *RefusedError) (Status, outcome, error) {
	next := *cur
	next.PendingSince = time.Time{} // the answer is in: monoes.me refused the token, nothing is in doubt
	if now.After(next.HW) {
		next.HW = now // a refused session keeps the enforcement evidence: a clock set back must not un-enforce the date
	}
	next.State = stateRefused
	next.Reason = r.Description
	if len(next.Reason) > 200 {
		next.Reason = next.Reason[:200]
	}
	next.AccessToken = ""
	next.LastAttempt, next.LastResult = now, string(ReasonRefused)
	// The marker first, then the refresh token: a crash in between leaves a
	// marker beside a dead token (still locked), never a live-looking session. The
	// token goes only once the marker is saved. If the marker cannot be written
	// (a full disk, a read-only session.json) the disk still holds the old session,
	// and without a token the other processes would read it as a key store problem
	// and keep the grace for up to 24 hours, so the token stays: the next process
	// presents it, is refused again (nothing is left to revoke) and writes the
	// marker.
	err := g.store.Save(&next)
	if err == nil {
		err = g.store.DeleteRefresh()
	}
	g.adopt(&next)
	return g.Status(), outcomeRefused, err
}

// recordAttempt stores the result of a failed attempt: the negative cache, and
// the reason a session in grace shows.
func (g *Guard) recordAttempt(cur *Session, now time.Time, result string) (Status, outcome, error) {
	next := *cur
	next.LastAttempt, next.LastResult = now, result
	bumpHW(&next, now)
	err := g.store.Save(&next)
	g.adopt(&next)
	return g.Status(), outcomeFailed, err
}

// bumpHW raises the high-water mark to now once it is a minute stale. It never
// lowers it: only a verified token resets it (NewSession).
func bumpHW(s *Session, now time.Time) {
	if now.After(s.HW) && now.Sub(s.HW) >= hwInterval {
		s.HW = now
	}
}

// touchHW persists the high-water mark (spec §4.5): only when the package is not
// dormant, the stored mark is a minute stale, and this guard has not tried in the
// last minute. A refused session has no mark worth writing. A machine that has no
// session at all keeps the record too, from the enforcement date on (A25): the date
// is judged on max(now, hw) and hw lives only in session.json, so without a file a
// clock set back to before the date would un-enforce the gate for a machine that was
// refused, or never signed in. It never waits long for the lock and never reports a
// failure: a missed write is made up at the next call.
func (g *Guard) touchHW(now time.Time) {
	if dormant() {
		return
	}
	sess, _ := g.cached()
	switch {
	case sess == nil:
		if !Enforced(now, time.Time{}) { // a date is set and the clock has reached it
			return
		}
	case sess.State == stateRefused || !now.After(sess.HW) || now.Sub(sess.HW) < hwInterval:
		return
	}
	g.mu.Lock()
	try := elapsed(now, g.lastHWAttempt, hwInterval)
	if try {
		g.lastHWAttempt = now
	}
	g.mu.Unlock()
	if !try {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hwLockWait)
	defer cancel()
	unlock, err := g.store.Lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	fresh, err := g.store.Load()
	if err != nil {
		return
	}
	if fresh == nil {
		if sess == nil { // not a session that vanished under a cached one: that file is not this guard's to bring back
			g.keepRecord(now)
		}
		return
	}
	if fresh.State == stateRefused {
		// Another process marked the session refused. Take it in now: a marker
		// that left session.json's modification time as it was (a coarse file
		// timestamp) would otherwise stay unseen by a long-running guard.
		g.adopt(fresh)
		return
	}
	next := *fresh
	bumpHW(&next, now)
	if next.HW.Equal(fresh.HW) {
		// A peer has raised the mark already: nothing to write, only to take in.
		g.adoptUnlessOlder(fresh)
		return
	}
	if g.store.Save(&next) != nil {
		// The mark was not written. The session read under the lock is as new as the
		// file, and another process may have written it within one modification-time
		// tick, so take it in, unless the guard is ahead of the file.
		g.adoptUnlessOlder(fresh)
		return
	}
	g.adopt(&next)
}

// keepRecord saves the session with no token that a machine which never signed in
// leaves (A25): a host and a mark, the record that a logout leaves, and nothing else.
// The caller holds the lock and has found no session. A failure is not reported.
func (g *Guard) keepRecord(now time.Time) {
	record := &Session{V: sessionVersion, Host: HostURL, HW: now}
	if g.store.Save(record) != nil {
		return
	}
	g.adopt(record)
}

// adoptUnlessOlder takes fresh, a session just read under the file lock, in as the
// cached one, unless the guard is ahead of the file. It is ahead when a write of its
// own failed (applyTokens' save of a refreshed session, say) and only the cache
// holds that session: the older file must not put it back, or the guard would go
// back to the old token, find it due and refresh again at every call. LastAttempt
// orders the writes that change a session (NewSession, recordAttempt and
// applyRefusal set it); the high-water writes leave it alone, so a file that only
// has a newer mark is as new as the session it holds.
func (g *Guard) adoptUnlessOlder(fresh *Session) {
	if cur, _ := g.cached(); cur == nil || !fresh.LastAttempt.Before(cur.LastAttempt) {
		g.adopt(fresh)
	}
}
