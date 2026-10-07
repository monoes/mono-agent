package account

import (
	"context"
	"errors"
)

// AdoptResult is the outcome of trying to turn an older login's refresh token into
// the machine session (spec D23).
type AdoptResult struct {
	Adopted     bool      // the session was stored
	Status      Status    // its verdict, when Adopted
	Tokens      *TokenSet // what monoes.me issued, when it answered with tokens: the exchange spent the older refresh token
	Dead        bool      // monoes.me answered invalid_grant: the older refresh token is spent, revoked or expired
	Unconfirmed bool      // no verdict, and the older refresh token may be spent: the exchange went out and its answer was not a complete 4xx (A24: a 5xx, a reset, a body cut short or not a token set), or it was answered and this machine could not store the new refresh token (A24(d))
}

// Adopt tries to turn an older library login into the machine session (spec D23),
// all under the store lock, so that no other process can present the same refresh
// token meanwhile: monoes.me ends every refresh token of an account when a rotated
// one is presented again (plan A, spike S2). With the lock held it checks that
// nobody has signed in (signedIn: a session with no token that was not refused is
// only a clock-guard record, A23 and A25), asks older for the login's refresh token
// and user (ok is false when there is none), exchanges the token and stores the
// result, and calls done with the outcome before it releases the lock, so the
// caller updates the older login while nobody else can read it.
//
// Whatever monoes.me answers, the outcome is Adopted or not, never a refusal and
// never a stored "refused" state: a failure here only means the user signs in once
// more. The error is for local failures (the lock, the store, the key store). The
// result tells the caller what became of the older refresh token, because it must
// never be presented again once it is dead or may be: Dead when monoes.me answered
// invalid_grant, Unconfirmed when the exchange went out and its outcome is unknown
// (the answer was not a complete 4xx: a 5xx, a reset, a timeout, a body cut short or
// not a token set), so monoes.me may have spent the token (A24), or was answered and this machine could not store the new refresh token (A24(d): the error says what failed, and no tokens come back for the caller to keep elsewhere), Tokens when the exchange spent
// it and issued new ones that no session could be made of (the caller keeps the
// older login alive with them), and none of these when nothing was sent or monoes.me
// answered with a complete 4xx that is not invalid_grant (it processed nothing), so the
// token is as it was.
//
// Once the exchange is sent it is completed and stored even if ctx is cancelled (A20);
// until then a cancelled ctx stops the call, with the older refresh token untouched.
func (c *Client) Adopt(ctx context.Context, older func() (refreshToken string, user *User, ok bool), done func(AdoptResult)) (AdoptResult, error) {
	unlock, err := c.lock(ctx) // at most lockWaitTimeout: the older login is then untouched
	if err != nil {
		return AdoptResult{}, err
	}
	defer unlock()
	if sess, err := c.Store.Load(); err != nil || signedIn(sess) {
		return AdoptResult{}, err // somebody signed in meanwhile
	}
	refreshToken, u, ok := older()
	if !ok {
		return AdoptResult{}, nil
	}
	res, err := c.exchangeOlder(ctx, refreshToken, u)
	if done != nil {
		done(res)
	}
	return res, err
}

// signedIn says whether sess is somebody's login: it holds a token, or monoes.me refused it. A session
// with neither is only the clock-guard record that logout (A23) or the guard on a machine that never
// signed in (A25) leaves, and an adoption goes ahead over it.
func signedIn(sess *Session) bool {
	return sess != nil && (sess.AccessToken != "" || sess.State == stateRefused)
}

func (c *Client) exchangeOlder(ctx context.Context, refreshToken string, u *User) (AdoptResult, error) {
	// The exchange spends the older refresh token, and its answer holds the only copy of the one
	// that replaces it, so once it is sent it is completed whatever the caller does next (A20): it
	// runs on a context the caller's cancellation does not reach, bounded by the guard's own
	// deadline. A caller that gives up (Ctrl-C) waits for the grant, then for the key-store write of the new refresh token (A22). What follows asks the
	// network only for the user's name, which falls back to the older login's own when the
	// caller has given up, and commit takes no context.
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshCallTimeout)
	defer cancel()
	ts, err := NewRefresher(c.Host).Refresh(gctx, refreshToken)
	var refused *RefusedError
	var transient *TransientError
	switch {
	case errors.As(err, &refused):
		return AdoptResult{Dead: true}, nil
	case errors.As(err, &transient) && transient.Settled:
		return AdoptResult{}, nil // nothing was sent, or monoes.me answered with a complete 4xx: the older login is as it was
	case err != nil:
		// The request went out and its outcome is unknown (a 5xx, a reset, a timeout, a body cut short or
		// not a token set): monoes.me may have rotated the older refresh token and the new one is lost (A24).
		// Presenting the older one again after the 300-second reuse window would end every refresh token of
		// the account, so the caller drops it.
		return AdoptResult{Unconfirmed: true}, nil
	}
	res := AdoptResult{Tokens: ts}
	sess, err := c.session(ctx, ts, u)
	var ue *unusableError
	if errors.As(err, &ue) {
		return res, nil // an opaque token, say: the older login stays as it is, with the new tokens
	}
	if err != nil {
		return res, err
	}
	if err := c.commit(sess, ts.RefreshToken); err != nil {
		// Answered, but this machine could not store what it was answered with (the key store did not answer
		// in time, or a write failed): the older refresh token is spent and the new one is held in memory only
		// (A24(d)). The guard keeps a marker for such a case and recovers the answer; an adoption has none, so
		// it is an unknown outcome too: the caller drops its copy of the older token, no tokens are handed back
		// for it to keep elsewhere, and the error says what failed. A refresh token that was written before a
		// failed session write stays beside no session, where nothing reads it, until the next sign-in.
		return AdoptResult{Unconfirmed: true}, err
	}
	res.Adopted, res.Status = true, Evaluate(sess, c.Now())
	return res, nil
}
