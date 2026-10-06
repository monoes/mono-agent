package account_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

func TestEvaluateMatrix(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	cases := []struct {
		name        string
		iat         time.Duration // relative to now
		life        time.Duration
		hw          time.Duration // relative to now
		lastResult  string
		wantState   account.State
		wantReason  account.Reason
		wantAllowed bool
	}{
		{"fresh token", -10 * time.Minute, hour, 0, "ok", account.StateOK, "", true},
		{"a second before exp", -hour + time.Second, hour, 0, "", account.StateOK, "", true},
		{"at exp, nothing recorded: grace, unreachable", -hour, hour, 0, "", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after an unreachable attempt", -2 * hour, hour, 0, "unreachable", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after a server error", -2 * hour, hour, 0, "server_error", account.StateGrace, account.ReasonServerError, true},
		{"grace with the keyring unavailable", -2 * hour, hour, 0, "keyring_unavailable", account.StateGrace, account.ReasonKeyringUnavailable, true},
		{"grace after a last ok", -2 * hour, hour, 0, "ok", account.StateGrace, account.ReasonUnreachable, true},
		{"grace after an unknown key is server_error", -2 * hour, hour, 0, "key_unknown", account.StateGrace, account.ReasonServerError, true},
		{"a second before iat+24h", -24*hour + time.Second, hour, 0, "unreachable", account.StateGrace, account.ReasonUnreachable, true},
		{"exactly iat+24h", -24 * hour, hour, 0, "unreachable", account.StateLocked, account.ReasonExpired, false},
		{"long expired", -30 * hour, hour, 0, "unreachable", account.StateLocked, account.ReasonExpired, false},
		{"expired after an unknown key", -30 * hour, hour, 0, "key_unknown", account.StateLocked, account.ReasonKeyUnknown, false},
		// A refresh whose answer never arrived (A24): the refresh token is gone, so nothing repairs the login but a new sign-in.
		{"ok after an unconfirmed refresh, while the token lasts", -10 * time.Minute, hour, 0, "unconfirmed", account.StateOK, "", true},
		{"grace after an unconfirmed refresh", -2 * hour, hour, 0, "unconfirmed", account.StateGrace, account.ReasonUnconfirmed, true},
		{"a second before iat+24h after an unconfirmed refresh", -24*hour + time.Second, hour, 0, "unconfirmed", account.StateGrace, account.ReasonUnconfirmed, true},
		{"exactly iat+24h after an unconfirmed refresh", -24 * hour, hour, 0, "unconfirmed", account.StateLocked, account.ReasonUnconfirmed, false},
		{"long expired after an unconfirmed refresh", -30 * hour, hour, 0, "unconfirmed", account.StateLocked, account.ReasonUnconfirmed, false},
		{"rollback wins over an unconfirmed refresh", -30 * hour, hour, 2 * hour, "unconfirmed", account.StateLocked, account.ReasonClockRollback, false},
		{"a 24h token is ok to its exp", -24*hour + time.Second, 24 * hour, 0, "ok", account.StateOK, "", true},
		{"iat exactly 5m ahead", 5 * time.Minute, hour, 0, "ok", account.StateOK, "", true},
		{"iat a second over 5m ahead", 5*time.Minute + time.Second, hour, 0, "ok", account.StateLocked, account.ReasonClockSkew, false},
		{"hw exactly 5m ahead", -10 * time.Minute, hour, 5 * time.Minute, "ok", account.StateOK, "", true},
		{"hw a second over 5m ahead", -10 * time.Minute, hour, 5*time.Minute + time.Second, "ok", account.StateLocked, account.ReasonClockRollback, false},
		{"rollback wins over expiry", -30 * hour, hour, 2 * hour, "unreachable", account.StateLocked, account.ReasonClockRollback, false},
		{"rollback wins over skew", 20 * time.Minute, hour, 2 * hour, "ok", account.StateLocked, account.ReasonClockRollback, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &account.Session{
				V: 1, Host: account.HostURL, LastResult: c.lastResult,
				AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(c.iat), Lifetime: c.life}),
				HW:          now.Add(c.hw),
			}
			st := account.Evaluate(sess, now)
			if st.State != c.wantState || st.Reason != c.wantReason {
				t.Fatalf("Evaluate = %s/%q, want %s/%q", st.State, st.Reason, c.wantState, c.wantReason)
			}
			if !st.Enforced || st.Allowed() != c.wantAllowed {
				t.Fatalf("Enforced = %v, Allowed = %v, want enforced and allowed %v", st.Enforced, st.Allowed(), c.wantAllowed)
			}
			if !st.IssuedAt.Equal(now.Add(c.iat)) || !st.ValidUntil.Equal(now.Add(c.iat+c.life)) || !st.GraceUntil.Equal(now.Add(c.iat+24*hour)) {
				t.Fatalf("times = %v / %v / %v, want iat, iat+life, iat+24h", st.IssuedAt, st.ValidUntil, st.GraceUntil)
			}
			if st.V != 1 || st.User == nil || st.User.ID != "user-1" || st.Plan != "free" {
				t.Fatalf("status fields = %+v, want v 1, the sub of the token as user and plan free", st)
			}
		})
	}
}

// pending_since is the guard's own bookkeeping of a refresh that is in flight (A24):
// it never changes a verdict by itself, whatever the clock says about it.
func TestAPendingMarkNeverChangesAVerdict(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	cases := []struct {
		name string
		sess account.Session
	}{
		{"a fresh token", account.Session{AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Minute)}), HW: now, LastResult: "ok"}},
		{"a grace", account.Session{AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-2 * hour)}), HW: now, LastResult: "unreachable"}},
		{"past the grace", account.Session{AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-30 * hour)}), HW: now, LastResult: "unreachable"}},
		{"a clock that went back", account.Session{AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Minute)}), HW: now.Add(2 * hour)}},
		{"a refused session", account.Session{State: "refused", LastResult: "refused"}},
		{"no token", account.Session{HW: now}},
	}
	for _, c := range cases {
		for _, at := range []time.Duration{-30 * time.Minute, -time.Second, 0, time.Hour} { // inside the window, old, now and in the future
			t.Run(fmt.Sprintf("%s, marked %v from now", c.name, at), func(t *testing.T) {
				plain, marked := c.sess, c.sess
				plain.V, marked.V = 1, 1
				marked.PendingSince = now.Add(at)
				if got, want := account.Evaluate(&marked, now), account.Evaluate(&plain, now); !reflect.DeepEqual(got, want) {
					t.Fatalf("Evaluate with a pending mark = %+v, want what it says without one: %+v", got, want)
				}
			})
		}
	}
}

func TestEvaluateWithoutAUsableToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	valid := f.Token(accounttest.TokenOptions{})
	notAJWT := "not-a-jwt"
	user := &account.User{ID: "u-9", Email: "a@b.c"}
	cases := []struct {
		name       string
		sess       *account.Session
		wantReason account.Reason
	}{
		{"no session", nil, account.ReasonNotLoggedIn},
		{"an empty token", &account.Session{V: 1, User: user}, account.ReasonNotLoggedIn},
		{"garbage", &account.Session{V: 1, AccessToken: notAJWT, User: user}, account.ReasonInvalid},
		{"a key that is not pinned", &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{KID: "gone"}), User: user}, account.ReasonKeyUnknown},
		{"refused, whatever the token", &account.Session{V: 1, AccessToken: valid, User: user, State: "refused", Reason: "revoked"}, account.ReasonRefused},
		{"refused with the token cleared", &account.Session{V: 1, User: user, State: "refused"}, account.ReasonRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := account.Evaluate(c.sess, now)
			if st.State != account.StateLocked || st.Reason != c.wantReason || st.Allowed() {
				t.Fatalf("Evaluate = %s/%q allowed=%v, want locked/%q", st.State, st.Reason, st.Allowed(), c.wantReason)
			}
			if c.sess != nil && (st.User == nil || st.User.ID != "u-9") {
				t.Fatalf("the user must be kept for the message: %+v", st.User)
			}
			if !st.IssuedAt.IsZero() || st.Plan != "" {
				t.Fatalf("a locked session without a verified token reports no times or plan: %+v", st)
			}
		})
	}
}

func TestEvaluateDormantAndEnforcementDate(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	expired := &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-30 * time.Hour)})}

	account.SetEnforceFromForTest(t, time.Time{})
	st := account.Evaluate(expired, now)
	if st.State != account.StateLocked || st.Reason != account.ReasonExpired || st.Enforced || !st.Allowed() || !st.EnforceFrom.IsZero() {
		t.Fatalf("dormant: %+v; the state is still computed, but nothing is enforced", st)
	}
	if st := account.Evaluate(nil, now); st.Reason != account.ReasonNotLoggedIn || !st.Allowed() {
		t.Fatalf("dormant with no session: %+v", st)
	}

	date := now.Add(48 * time.Hour)
	account.SetEnforceFromForTest(t, date)
	if st := account.Evaluate(expired, now); st.Enforced || !st.Allowed() || !st.EnforceFrom.Equal(date) {
		t.Fatalf("before the date: %+v", st)
	}
	if st := account.Evaluate(expired, date); !st.Enforced || st.Allowed() {
		t.Fatalf("at the date: %+v", st)
	}
	// Setting the clock back does not postpone the date: hw has seen it.
	rolledBack := *expired
	rolledBack.HW = date.Add(time.Minute)
	if st := account.Evaluate(&rolledBack, now); !st.Enforced {
		t.Fatalf("with hw past the date, enforcement must hold even for an earlier now: %+v", st)
	}
}

func TestEvaluateDoesNotShareTheSessionUser(t *testing.T) {
	f := accounttest.New(t)
	sess := &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{}), User: &account.User{ID: "u-1", Email: "x@y.z"}}
	st := account.Evaluate(sess, f.Clock.Now())
	st.User.Email = "changed"
	if sess.User.Email != "x@y.z" {
		t.Fatal("Status.User must be a copy: a caller changed the stored session")
	}
}

func TestStatusJSONShape(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	st := account.Evaluate(&account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{Plan: "pro"}), User: &account.User{ID: "u-1", Email: "a@b.c"}, HW: now}, now)
	keys := func(st account.Status) []string {
		raw, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"enforce_from", "enforced", "grace_until", "issued_at", "plan", "reason", "state", "user", "v", "valid_until"}
	if got := keys(st); !reflect.DeepEqual(got, want) {
		t.Fatalf("signed-in JSON keys = %v, want %v", got, want)
	}
	// Locked with no session: the optional fields drop out, the always-present ones stay.
	if got, want := keys(account.Evaluate(nil, now)), []string{"enforce_from", "enforced", "plan", "reason", "state", "v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("no-session JSON keys = %v, want %v", got, want)
	}
}

func TestNewSession(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	iat := now.Add(-10 * time.Minute)
	issued := f.Token(accounttest.TokenOptions{IssuedAt: iat, Sub: "u-3", Plan: "pro"})
	sess, err := account.NewSession("https://monoes.me", issued, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if sess.V != 1 || sess.Host != "https://monoes.me" || sess.AccessToken != issued || sess.User == nil || sess.User.ID != "u-3" ||
		sess.Plan != "pro" || !sess.HW.Equal(iat) || !sess.LastAttempt.Equal(now) || sess.LastResult != "ok" || sess.State != "" {
		t.Fatalf("session fields: v=%d host=%q user=%v plan=%q hw=%v attempt=%v last=%q state=%q; hw must be the iat of the token (server time is authoritative)",
			sess.V, sess.Host, sess.User, sess.Plan, sess.HW, sess.LastAttempt, sess.LastResult, sess.State)
	}
	user := &account.User{ID: "u-3", Email: "a@b.c"}
	if sess, _ := account.NewSession("h", issued, user, now); sess.User != user {
		t.Fatal("a given user must be kept")
	}
	notAJWT := "not-a-jwt"
	if _, err := account.NewSession("h", notAJWT, nil, now); reasonOf(err) != account.ReasonInvalid {
		t.Fatalf("a token that does not verify must not become a session: %v", err)
	}
	ahead := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(time.Hour)})
	if _, err := account.NewSession("h", ahead, nil, now); reasonOf(err) != account.ReasonClockSkew {
		t.Fatalf("a token from the future must not become a session: %v", err)
	}
}
