package account

import (
	"testing"
	"time"
)

// judge's comment promises an order: no session, refused, no token, a token that
// does not verify. Evaluate hands it a verify error or a receipt only for a
// session that is not refused and has a token, but the guard caches the
// verification of the stored token, and a cache that went stale (the token
// cleared or replaced since) can hand it a receipt next to a verify error, or a
// receipt for a session whose token was cleared. These rows give it those
// combinations, and the ones Evaluate never builds. A later change that also
// verifies the token of a refused session (to show its plan, say) must not unlock
// it. This file is in package account because judge is not exported, and so it
// cannot use accounttest, which imports account: the receipt and the error are
// built here.
func TestJudgeKeepsItsOrderForInputsEvaluateNeverBuilds(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	rcpt := &Receipt{Sub: "u-1", Plan: "free", IssuedAt: now.Add(-10 * time.Minute), ExpiresAt: now.Add(50 * time.Minute)}
	verr := &VerifyError{Reason: ReasonInvalid}
	keyUnknown := &VerifyError{Reason: ReasonKeyUnknown} // not invalid, so a pass cannot come from the no-receipt fallback
	cases := []struct {
		name string
		sess *Session
		rcpt *Receipt
		verr *VerifyError
		want Reason
	}{
		{"refused beats a verify error", &Session{V: 1, AccessToken: "x", State: stateRefused}, nil, verr, ReasonRefused},
		{"refused beats a receipt", &Session{V: 1, AccessToken: "x", State: stateRefused}, rcpt, nil, ReasonRefused},
		{"no token beats a verify error", &Session{V: 1}, nil, verr, ReasonNotLoggedIn},
		{"no token beats a receipt", &Session{V: 1}, rcpt, nil, ReasonNotLoggedIn},
		{"a verify error beats a receipt", &Session{V: 1, AccessToken: "x"}, rcpt, keyUnknown, ReasonKeyUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if st := judge(c.sess, c.rcpt, c.verr, now); st.State != StateLocked || st.Reason != c.want {
				t.Fatalf("judge = %s/%s, want locked/%s", st.State, st.Reason, c.want)
			}
		})
	}
}

// Status.Plan comes only from a verified token. A caller that hands judge no
// receipt (it forgot to verify) gets locked(invalid) and no plan, whatever plan
// the session stores.
func TestJudgeReportsNoPlanWithoutAReceipt(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	st := judge(&Session{V: 1, AccessToken: "x", Plan: "pro"}, nil, nil, now)
	if st.State != StateLocked || st.Reason != ReasonInvalid || st.Plan != "" {
		t.Fatalf("judge = %s/%s plan %q, want locked/invalid and no plan", st.State, st.Reason, st.Plan)
	}
}
