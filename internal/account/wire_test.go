package account_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// The strings below cross process boundaries: `account status --json`, the
// error JSON of every gated command, /health and the desktop app all carry them,
// and other programs compare against them. They are written out here, not built
// from the constants, so that renaming one is a change that has to be made on
// purpose, here, and not by a refactor (index §3.2).

func TestTheStateAndReasonStringsAreFrozen(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"StateOK", string(account.StateOK), "ok"},
		{"StateGrace", string(account.StateGrace), "grace"},
		{"StateLocked", string(account.StateLocked), "locked"},
		{"ReasonNone", string(account.ReasonNone), ""},
		{"ReasonNotLoggedIn", string(account.ReasonNotLoggedIn), "not_logged_in"},
		{"ReasonExpired", string(account.ReasonExpired), "expired"},
		{"ReasonRefused", string(account.ReasonRefused), "refused"},
		{"ReasonClockRollback", string(account.ReasonClockRollback), "clock_rollback"},
		{"ReasonClockSkew", string(account.ReasonClockSkew), "clock_skew"},
		{"ReasonKeyUnknown", string(account.ReasonKeyUnknown), "key_unknown"},
		{"ReasonInvalid", string(account.ReasonInvalid), "invalid"},
		{"ReasonUnreachable", string(account.ReasonUnreachable), "unreachable"},
		{"ReasonServerError", string(account.ReasonServerError), "server_error"},
		{"ReasonKeyringUnavailable", string(account.ReasonKeyringUnavailable), "keyring_unavailable"},
		{"ReasonUnconfirmed", string(account.ReasonUnconfirmed), "unconfirmed"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestTheStatusJSONIsFrozen(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		status account.Status
		want   string
	}{
		{"every field set", account.Status{
			V: 1, State: account.StateGrace, Reason: account.ReasonUnreachable,
			User: &account.User{ID: "u1", Email: "a@b.c", Username: "ab"}, Plan: "free",
			IssuedAt: at, ValidUntil: at.Add(time.Hour), GraceUntil: at.Add(24 * time.Hour), EnforceFrom: at.Add(-time.Hour), Enforced: true,
		}, `{"v":1,"state":"grace","reason":"unreachable","user":{"id":"u1","email":"a@b.c","username":"ab"},"plan":"free",` +
			`"issued_at":"2026-10-05T12:00:00Z","valid_until":"2026-10-05T13:00:00Z","grace_until":"2026-10-06T12:00:00Z",` +
			`"enforce_from":"2026-10-05T11:00:00Z","enforced":true}`},
		// What is left out when it is empty: the user, its optional fields, and the four times.
		{"nothing set", account.Status{}, `{"v":0,"state":"","reason":"","plan":"","enforced":false}`},
		{"a user with an ID only", account.Status{V: 1, State: account.StateOK, User: &account.User{ID: "u1"}, Plan: "free"},
			`{"v":1,"state":"ok","reason":"","user":{"id":"u1"},"plan":"free","enforced":false}`},
		{"a locked status", account.Status{V: 1, State: account.StateLocked, Reason: account.ReasonKeyringUnavailable, Enforced: true},
			`{"v":1,"state":"locked","reason":"keyring_unavailable","plan":"","enforced":true}`},
		// A refresh whose answer never arrived (A24): a grace reason first, a locked one after it.
		{"a grace after a refresh whose answer never arrived", account.Status{V: 1, State: account.StateGrace, Reason: account.ReasonUnconfirmed, Enforced: true},
			`{"v":1,"state":"grace","reason":"unconfirmed","plan":"","enforced":true}`},
		{"a locked status after it", account.Status{V: 1, State: account.StateLocked, Reason: account.ReasonUnconfirmed, Enforced: true},
			`{"v":1,"state":"locked","reason":"unconfirmed","plan":"","enforced":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.status)
			if err != nil || string(raw) != c.want {
				t.Fatalf("Status marshalled to\n%s (err %v), want\n%s", raw, err, c.want)
			}
		})
	}
}

// The text of a refusal is the first thing a person sees when the gate says no.
// The numbers in two of the lines come from GraceWindow and ClockSkew; they are
// written out here on purpose, so that a change to either constant (spike S6) has
// to show up as a changed message that someone reads. Every Reason is listed,
// those with no second line included.
func TestTheRefusalTextOfEveryReasonIsFrozen(t *testing.T) {
	first := "Log in to monoes.me first: monoagentcli account login"
	cases := []struct {
		reason account.Reason
		want   string
	}{
		{account.ReasonNone, first},
		{account.ReasonNotLoggedIn, first},
		{account.ReasonExpired, first + "\nThis login expired: monoes.me has not been reachable for 24 hours."},
		{account.ReasonRefused, first + "\nmonoes.me ended this login (the account was blocked or the login was revoked)."},
		{account.ReasonClockRollback, first + "\nThe system clock went back. Fix the clock, then sign in again."},
		{account.ReasonClockSkew, first + "\nThe system clock is more than 5 minutes behind monoes.me. Fix the clock."},
		{account.ReasonKeyUnknown, first + "\nThis build cannot verify the login. Update it: monoagentcli update"},
		{account.ReasonInvalid, first + "\nThe stored login is not valid. Sign in again."},
		{account.ReasonUnreachable, first},
		{account.ReasonServerError, first},
		{account.ReasonKeyringUnavailable, first},
	}
	for _, c := range cases {
		got := (&account.LoginRequiredError{Status: account.Status{State: account.StateLocked, Reason: c.reason}}).Error()
		if got != c.want {
			t.Errorf("reason %q: message\n%q\nwant\n%q", c.reason, got, c.want)
		}
	}
}
