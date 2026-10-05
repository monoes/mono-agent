package account

import "time"

// Session is the stored session, the JSON of session.json (spec §4.6). It
// holds a token that expires within the hour and no secret that needs the
// keychain: the refresh token lives in refresh.enc.
type Session struct {
	V           int       `json:"v"`
	Host        string    `json:"host"`
	AccessToken string    `json:"access_token"`
	User        *User     `json:"user,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	HW          time.Time `json:"hw,omitzero"`
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastResult  string    `json:"last_result,omitempty"` // "ok", "unreachable", "server_error", "keyring_unavailable", "key_unknown" or "refused"
	State       string    `json:"state,omitempty"`       // "" or "refused"
	Reason      string    `json:"reason,omitempty"`
}

const (
	sessionVersion = 1
	resultOK       = "ok"
	stateRefused   = "refused"
)

// NewSession builds the session to store for a freshly issued access token. It
// verifies the token at now and starts the clock guard at the token's iat:
// server time is authoritative, which is also the way out of a rolled-back
// clock (spec §4.5). A nil user is taken from the token's sub.
func NewSession(host, accessToken string, user *User, now time.Time) (*Session, error) {
	r, err := Verify(accessToken, now)
	if err != nil {
		return nil, err
	}
	if user == nil {
		user = &User{ID: r.Sub}
	}
	return &Session{
		V: sessionVersion, Host: host, AccessToken: accessToken, User: user, Plan: r.Plan,
		HW: r.IssuedAt, LastAttempt: now, LastResult: resultOK,
	}, nil
}

// Evaluate is the pure verdict of a stored session at a time: no I/O. It
// verifies the token, applies the clock guard and the grace rule, and fills
// Enforced and EnforceFrom from rollout.go. A nil session is
// locked(not_logged_in); a session marked refused is locked(refused).
func Evaluate(sess *Session, now time.Time) Status {
	var rcpt *Receipt
	var verr *VerifyError
	if sess != nil && sess.State != stateRefused && sess.AccessToken != "" {
		rcpt, verr = verifyToken(sess.AccessToken)
	}
	return judge(sess, rcpt, verr, now)
}

// judge is Evaluate after the token has been verified. verifyToken does not
// read the clock, so a guard can cache its result and call judge on every
// Status without checking a signature each time. The checks run in this order:
// no session, refused, no token, a token that does not verify, a clock that
// went back, a token from the future, then ok, grace and expired.
func judge(sess *Session, rcpt *Receipt, verr *VerifyError, now time.Time) Status {
	// Compare wall-clock time only. A time from time.Now carries a monotonic
	// reading, and when both operands do, Before, After and Sub compare those
	// alone; the monotonic clock does not follow a system clock that is set back,
	// so a wall-clock rollback would go unseen. Round(0) strips the reading.
	now = now.Round(0)
	st := Status{V: 1, EnforceFrom: EnforceDate()}
	var hw time.Time
	if sess != nil {
		hw = sess.HW
	}
	st.Enforced = Enforced(now, hw)
	locked := func(r Reason) Status {
		st.State, st.Reason = StateLocked, r
		return st
	}
	if sess == nil {
		return locked(ReasonNotLoggedIn)
	}
	if sess.User != nil {
		u := *sess.User
		st.User = &u
	}
	switch {
	case sess.State == stateRefused:
		return locked(ReasonRefused)
	case sess.AccessToken == "":
		return locked(ReasonNotLoggedIn)
	case verr != nil:
		return locked(verr.Reason)
	case rcpt == nil:
		return locked(ReasonInvalid) // a caller that forgot to verify must not crash the gate
	}
	st.IssuedAt, st.ValidUntil, st.GraceUntil = rcpt.IssuedAt, rcpt.ExpiresAt, rcpt.IssuedAt.Add(GraceWindow)
	st.Plan = rcpt.Plan // only a verified token says what the plan is: the stored one is unverified, user-writable JSON
	if st.User == nil {
		st.User = &User{ID: rcpt.Sub}
	}
	switch {
	case now.Before(sess.HW.Add(-ClockSkew)):
		return locked(ReasonClockRollback)
	case rcpt.IssuedAt.After(now.Add(ClockSkew)):
		return locked(ReasonClockSkew)
	case now.Before(rcpt.ExpiresAt):
		st.State = StateOK
		return st
	case now.Before(st.GraceUntil):
		st.State, st.Reason = StateGrace, graceReason(sess.LastResult)
		return st
	case sess.LastResult == string(ReasonKeyUnknown):
		// The last refresh returned a token this build cannot verify: the
		// server rotated its key (spec §4.7). Say so, not just "expired".
		return locked(ReasonKeyUnknown)
	}
	return locked(ReasonExpired)
}

// graceReason says why a session in grace was not refreshed: the stored result
// of the last attempt, unreachable when none is recorded. A key this build
// does not know is reported as server_error until the grace ends.
func graceReason(lastResult string) Reason {
	switch r := Reason(lastResult); r {
	case ReasonUnreachable, ReasonServerError, ReasonKeyringUnavailable:
		return r
	case ReasonKeyUnknown:
		return ReasonServerError
	}
	return ReasonUnreachable
}

// elapsed reports whether d has passed between t and now. A zero t, or a t
// after now (the clock went back since it was stored), counts as elapsed: a
// stored timestamp must never be able to hold a refresh off, or a rolled-back
// clock could not be repaired by the refresh that resets it.
func elapsed(now, t time.Time, d time.Duration) bool {
	return t.IsZero() || t.After(now) || now.Sub(t) >= d
}
