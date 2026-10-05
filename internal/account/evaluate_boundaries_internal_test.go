package account

import (
	"testing"
	"time"
)

// judge's comment promises an order: no session, refused, no token, a token that
// does not verify. Evaluate, and the guard that verifies the same way, hand it a
// verify error or a receipt only for a session that is not refused and has a
// token, so these rows give it the combinations they never build. A later change
// that also verifies the token of a refused session (to show its plan, say) must
// not unlock it. This file is in package account because judge is not exported,
// and so it cannot use accounttest, which imports account: the receipt and the
// error are built here.
func TestJudgeKeepsItsOrderForInputsEvaluateNeverBuilds(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	rcpt := &Receipt{Sub: "u-1", Plan: "free", IssuedAt: now.Add(-10 * time.Minute), ExpiresAt: now.Add(50 * time.Minute)}
	verr := &VerifyError{Reason: ReasonInvalid}
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if st := judge(c.sess, c.rcpt, c.verr, now); st.State != StateLocked || st.Reason != c.want {
				t.Fatalf("judge = %s/%s, want locked/%s", st.State, st.Reason, c.want)
			}
		})
	}
}
