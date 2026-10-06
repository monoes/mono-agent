package account

import (
	"context"
	"time"
)

// A refresh grant whose answer is lost (A24). monoes.me rotates the refresh token
// as it answers, and takes a token that was rotated away and is presented again
// after its reuse window of 300 s for theft: it then ends every refresh token of
// the account, every install's. A grant whose answer never arrives (a connection
// that dies after the request was written, a timeout, a kill, a laptop that sleeps
// mid-call) leaves exactly such a token on disk. So the guard writes down, under
// the session lock and before it sends a grant, that one is about to go out
// (Session.PendingSince), and it never presents a token whose consumption it cannot
// rule out outside the window: inside pendingRetryWindow the retry is immediate and
// monoes.me answers it again with the same answer, after it the token is dropped
// and this machine signs in again. One install pays, never all of them.
//
// The age of a marker is read on this machine's clock, so a clock set back can make
// it look younger than it is. A clock that reads before the marker, or before the
// last attempt the session records (always written from this clock), has gone back,
// and the token is dropped then too. What the drop cannot see, an accepted
// residual: on a machine where no refresher runs (a CLI-only machine), a clock
// stepped back by about a minute or more after the last recorded evidence, so that
// it then reads inside the stamp's window although more than 300 s have passed;
// closing that needs a boot-time or monotonic reference in the marker (out of
// scope). A running refresher sees a step back of more than clockBackTolerance at
// its next pass: it compares each reading of the clock with the previous one,
// raises the last attempt to the earlier reading (keepLastAttempt) and passes at
// once, which drops the token; its high-water write also keeps the evidence on disk
// at most a minute old for the commands of other processes. What it leaves is a
// command within one poll of the step, before that pass, and a step smaller than the
// tolerance, both inside the 60 s between pendingRetryWindow and monoes.me's window.
//
// The age is judged on the clock read after the refresh token, just before the send;
// a process suspended inside the Refresher after that, before the request is written
// (discovery, dial, TLS), is not covered: closing that needs the deadline of the send
// handed to the transport (a later hardening of B1b's Refresher).
//
// Nor can the marker tell a refusal whose record could not be saved from a lost
// answer: the refresh token stays for the next process to learn the refusal again
// (A21), but one that comes after the window drops it instead, and this machine
// shows grace and then locked(unconfirmed) where it would have shown
// locked(refused). That is accepted: nothing is presented and both end locked.

// markPending writes the marker of a grant that is about to be sent, under the
// lock, and takes the session in. The stamp is written only when there is none: the
// reuse window runs from the first request that monoes.me may have answered, not
// from the last one, and a retry inside it gets the same answer and consumes
// nothing new, so a retry that is lost again keeps the stamp it found. A fresh stamp
// starts the evidence of a clock that went back: a last attempt recorded after it,
// while nothing was in doubt, is moved back to the stamp. wrote says whether this
// call wrote it. A marker that cannot be written is an error and nothing is taken
// in: a grant whose outcome cannot be recorded must not be sent.
func (g *Guard) markPending(cur *Session, now time.Time) (marked *Session, wrote bool, err error) {
	if !cur.PendingSince.IsZero() {
		return cur, false, nil
	}
	next := *cur
	next.PendingSince = now
	if next.LastAttempt.After(now) {
		// The clock went back before this grant, while nothing was in doubt: an attempt
		// recorded ahead of the stamp would read as a clock gone back since it
		// (pendingExpired) and drop the first retry. The evidence starts at the stamp.
		next.LastAttempt = now
	}
	if err := g.store.Save(&next); err != nil {
		return nil, false, err
	}
	g.adopt(&next)
	return &next, true, nil
}

// withoutPending is cur with no marker: the session to record when the marker no
// longer says anything, because the outcome of the grant is known.
func withoutPending(cur *Session) *Session {
	next := *cur
	next.PendingSince = time.Time{}
	return &next
}

// pendingExpired reports whether the grant that left cur's marker is out of reach
// of monoes.me's reuse window: the marker is older than pendingRetryWindow, or it
// or the session's last attempt lies after now, which means that the clock went
// back since they were written and the marker's age cannot be told. Exactly
// pendingRetryWindow is still a retry, and so is a pass at the very instant of the
// last attempt. now must be read after the session under the lock, since another
// process may have recorded an attempt while this one waited for it, and after the
// refresh token, just before the send, since that read may take as long as a
// suspended process sleeps.
func pendingExpired(cur *Session, now time.Time) bool {
	if cur.PendingSince.IsZero() {
		return false
	}
	age := now.Sub(cur.PendingSince)
	return age < 0 || age > pendingRetryWindow || now.Before(cur.LastAttempt)
}

// dropUnconfirmed gives up the refresh token that the marker is about: monoes.me may
// have rotated it, its answer never arrived, and presenting it now would be taken for
// theft and end every refresh token of the account. Nothing is sent and nothing is
// revoked: this machine signs in again, and the other installs are untouched. The
// session is recorded as unconfirmed (a grace reason, then locked) and keeps its
// access token, which is good until it expires.
//
// The dead token goes FIRST (os.Remove needs no key store), whether or not the record
// of why can be saved: a process that stops between the two steps then leaves a missing
// token and the old marker, a drop that the next pass finishes, and never a token that
// nothing marks. If the token cannot be removed the marker stays, so that the next pass
// drops it again and never presents it.
func (g *Guard) dropUnconfirmed(cur *Session, now time.Time) (Status, outcome, error) {
	removed := g.store.DeleteRefresh()
	next := cur
	if removed == nil {
		next = withoutPending(cur)
	}
	st, oc, err := g.recordAttempt(next, now, string(ReasonUnconfirmed))
	if err == nil {
		err = removed
	}
	return st, oc, err
}

// keepLastAttempt raises the last attempt of a session that still has a marker to at,
// a reading of the clock that the refresher took before the clock went back (A24):
// pendingExpired then finds the clock before it and drops the token. It takes the lock,
// reads the session again, only ever raises, and never reports a failure.
func (g *Guard) keepLastAttempt(at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), hwLockWait)
	defer cancel()
	unlock, err := g.store.Lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	fresh, err := g.store.Load()
	if err != nil || fresh == nil || fresh.PendingSince.IsZero() || !at.After(fresh.LastAttempt) {
		return
	}
	next := *fresh
	next.LastAttempt = at
	if g.store.Save(&next) == nil {
		g.adopt(&next)
	}
}

// pendingStamp is the marker of the cached session, the zero time when there is none.
func (g *Guard) pendingStamp() time.Time {
	if sess, _ := g.cached(); sess != nil {
		return sess.PendingSince
	}
	return time.Time{}
}
