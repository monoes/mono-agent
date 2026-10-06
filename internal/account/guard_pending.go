package account

import "time"

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

// markPending writes the marker of a grant that is about to be sent, under the
// lock, and takes the session in. The stamp is written only when there is none: the
// reuse window runs from the first request that monoes.me may have answered, not
// from the last one, and a retry inside it gets the same answer and consumes
// nothing new, so a retry that is lost again keeps the stamp it found. wrote says
// whether this call wrote it. A marker that cannot be written is an error and
// nothing is taken in: a grant whose outcome cannot be recorded must not be sent.
func (g *Guard) markPending(cur *Session, now time.Time) (marked *Session, wrote bool, err error) {
	if !cur.PendingSince.IsZero() {
		return cur, false, nil
	}
	next := *cur
	next.PendingSince = now
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
