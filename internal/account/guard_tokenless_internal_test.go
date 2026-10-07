package account

import (
	"testing"
	"time"
)

// A session with no token is what a logout leaves behind to keep its mark, and it
// has nothing to refresh with. dueForRefresh must never select it, whatever the
// Status and the receipt that the caller read beside it say: both callers read
// them one after the other and a poll can swap the cache in between, so a
// session with no token can meet a Status of ok and a receipt about to expire.
func TestATokenlessSessionIsNeverDueWhateverTheStatusAndTheReceiptSay(t *testing.T) {
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	sess := &Session{V: 1, Host: HostURL, HW: now, LastAttempt: now.Add(-time.Hour)} // no token, last tried an hour ago
	expiring := &Receipt{Sub: "u-1", IssuedAt: now.Add(-57 * time.Minute), ExpiresAt: now.Add(3 * time.Minute)}
	expired := &Receipt{Sub: "u-1", IssuedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour)}
	cases := []struct {
		name string
		st   Status
		rcpt *Receipt
	}{
		{"ok, a receipt about to expire", Status{State: StateOK}, expiring},
		{"grace, an expired receipt", Status{State: StateGrace}, expired},
		{"locked as expired, an expired receipt", Status{State: StateLocked, Reason: ReasonExpired}, expired},
		{"locked as not logged in, no receipt", Status{State: StateLocked, Reason: ReasonNotLoggedIn}, nil},
		{"ok, no receipt", Status{State: StateOK}, nil},
	}
	withToken := *sess
	withToken.AccessToken = "token"
	for _, mode := range []refreshMode{modeCLI, modeBackground} {
		for _, c := range cases {
			if dueForRefresh(sess, c.st, c.rcpt, now, mode) {
				t.Errorf("mode %d, %s: a session with no token is due, want never", mode, c.name)
			}
			// Control: the same inputs with a token in the session are due, so that
			// the table above would show a guard that selected the session.
			if !dueForRefresh(&withToken, c.st, c.rcpt, now, mode) {
				t.Errorf("mode %d, %s: the same session with a token is not due: this case cannot tell", mode, c.name)
			}
		}
	}
}
