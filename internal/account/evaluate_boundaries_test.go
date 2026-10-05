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

// The tests below pin what evaluate_test.go leaves open, found by mutating
// session.go: the order of the checks against each other and against the stored
// last_result, which clock the verdict reads, what Status keeps of the stored
// session, the clock check of NewSession, and the keys of session.json. None of
// them prints a Session or a token.

// verdict is the state and the reason of a Status, as "state/reason".
func verdict(st account.Status) string { return string(st.State) + "/" + string(st.Reason) }

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
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

// A session with no usable token is locked for that reason whatever the clock
// guard and the last result say: they speak only about a token that verified.
func TestEarlyLocksOutrankTheClockGuardAndTheLastResult(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	valid := f.Token(accounttest.TokenOptions{})
	unpinned := f.Token(accounttest.TokenOptions{KID: "gone"})
	cases := []struct {
		name string
		sess account.Session
		want string
	}{
		{"refused, token kept", account.Session{AccessToken: valid, State: "refused"}, "locked/refused"},
		{"refused, token cleared", account.Session{State: "refused"}, "locked/refused"},
		{"no token", account.Session{}, "locked/not_logged_in"},
		{"garbage", account.Session{AccessToken: "not-a-jwt"}, "locked/invalid"},
		{"a key that is not pinned", account.Session{AccessToken: unpinned}, "locked/key_unknown"},
	}
	for _, c := range cases {
		for _, ahead := range []time.Duration{0, 2 * time.Hour} {
			for _, last := range []string{"", "key_unknown"} {
				t.Run(fmt.Sprintf("%s, hw %v ahead, last %q", c.name, ahead, last), func(t *testing.T) {
					sess := c.sess
					sess.V, sess.HW, sess.LastResult = 1, now.Add(ahead), last
					if got := verdict(account.Evaluate(&sess, now)); got != c.want {
						t.Fatalf("Evaluate = %s, want %s", got, c.want)
					}
				})
			}
		}
	}
}

// last_result only says why a token that ran out was not refreshed: it picks the
// reason of a grace and, once the grace is over, tells key_unknown from expired.
// For every other verdict it must not matter, whatever it holds.
func TestTheLastResultOnlyShapesTheReasonOfAGraceOrAnExpiredToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	// Every value last_result can hold, the other reasons (which a grace never
	// reports) and one nobody has made up yet.
	lasts := []string{"", "ok", "unreachable", "server_error", "keyring_unavailable", "key_unknown", "refused",
		"not_logged_in", "expired", "clock_rollback", "clock_skew", "invalid", "something new"}
	same := func(want string) func(string) string { return func(string) string { return want } }
	inGrace := func(last string) string {
		switch last {
		case "unreachable", "server_error", "keyring_unavailable":
			return "grace/" + last
		case "key_unknown":
			return "grace/server_error" // the key is only named once the grace is over
		}
		return "grace/unreachable"
	}
	pastGrace := func(last string) string {
		if last == "key_unknown" {
			return "locked/key_unknown"
		}
		return "locked/expired"
	}
	cases := []struct {
		name string
		iat  time.Duration
		hw   time.Duration
		want func(last string) string
	}{
		{"fresh", -10 * time.Minute, 0, same("ok/")},
		{"in grace", -2 * hour, 0, inGrace},
		{"past the grace", -30 * hour, 0, pastGrace},
		{"clock rolled back, fresh", -10 * time.Minute, 2 * hour, same("locked/clock_rollback")},
		{"clock rolled back, past the grace", -30 * hour, 2 * hour, same("locked/clock_rollback")},
		{"token from the future", 20 * time.Minute, 0, same("locked/clock_skew")},
	}
	for _, c := range cases {
		tok := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(c.iat), Lifetime: hour})
		for _, last := range lasts {
			t.Run(fmt.Sprintf("%s, last %q", c.name, last), func(t *testing.T) {
				sess := &account.Session{V: 1, AccessToken: tok, HW: now.Add(c.hw), LastResult: last}
				if got, want := verdict(account.Evaluate(sess, now)), c.want(last); got != want {
					t.Fatalf("Evaluate = %s, want %s", got, want)
				}
			})
		}
	}
}

// ok, grace and expired are judged by now, not by the high-water mark: a stale
// or missing hw must not keep a token alive, and one inside the allowance must
// not cut it short.
func TestTheClockNotTheHighWaterMarkDecidesOKGraceAndExpiry(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	cases := []struct {
		name string
		iat  time.Duration
		hw   time.Duration
		noHW bool
		want string
	}{
		{"a stale hw does not keep an expired token ok", -2 * hour, -3 * hour, false, "grace/unreachable"},
		{"no hw does not keep an expired token ok", -2 * hour, 0, true, "grace/unreachable"},
		{"a stale hw does not extend the grace", -30 * hour, -3 * hour, false, "locked/expired"},
		{"no hw does not extend the grace", -30 * hour, 0, true, "locked/expired"},
		{"no hw, a fresh token", -10 * time.Minute, 0, true, "ok/"},
		{"an hw inside the allowance does not expire a token early", -58 * time.Minute, 4 * time.Minute, false, "ok/"},
		{"an hw inside the allowance does not end the grace early", -24*hour + 2*time.Minute, 4 * time.Minute, false, "grace/unreachable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &account.Session{V: 1, AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(c.iat), Lifetime: hour})}
			if !c.noHW {
				sess.HW = now.Add(c.hw)
			}
			if got := verdict(account.Evaluate(sess, now)); got != c.want {
				t.Fatalf("Evaluate = %s, want %s", got, c.want)
			}
		})
	}
}

// Status keeps the whole stored user, a copy of it, even when the token names
// another sub. The plan is the token's, in every state that has a receipt: the
// token is signed, the stored plan is not.
func TestEvaluateKeepsTheStoredUserAndReportsThePlanOfTheToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	const hour = time.Hour
	stored := account.User{ID: "stored-id", Email: "a@b.c", Username: "abc"}
	cases := []struct {
		name string
		iat  time.Duration
		hw   time.Duration
		last string
		want string
	}{
		{"ok", -10 * time.Minute, 0, "", "ok/"},
		{"grace", -2 * hour, 0, "", "grace/unreachable"},
		{"expired", -30 * hour, 0, "", "locked/expired"},
		{"key_unknown after the grace", -30 * hour, 0, "key_unknown", "locked/key_unknown"},
		{"clock rolled back", -10 * time.Minute, 2 * hour, "", "locked/clock_rollback"},
		{"token from the future", 20 * time.Minute, 0, "", "locked/clock_skew"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			user := stored
			sess := &account.Session{
				V: 1, User: &user, Plan: "stale", HW: now.Add(c.hw), LastResult: c.last,
				AccessToken: f.Token(accounttest.TokenOptions{IssuedAt: now.Add(c.iat), Sub: "token-sub", Plan: "pro"}),
			}
			st := account.Evaluate(sess, now)
			if got := verdict(st); got != c.want {
				t.Fatalf("Evaluate = %s, want %s", got, c.want)
			}
			if st.User == nil || *st.User != stored || st.User == sess.User {
				t.Fatalf("user = %+v, want a copy of the stored %+v, not one rebuilt from the token's sub", st.User, stored)
			}
			if st.Plan != "pro" {
				t.Fatalf("plan = %q, want the token's pro, not the stored stale one", st.Plan)
			}
		})
	}
}

// Only a verified token says what the plan is. The stored plan is unverified,
// user-writable JSON, so a session without a verified token reports none, even
// while the gate is dormant: a locked status is then still Allowed(), and a
// consumer that read the plan from it would be trusting that file.
func TestEvaluateReportsNoPlanWithoutAVerifiedToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	valid := f.Token(accounttest.TokenOptions{Plan: "pro"})
	unpinned := f.Token(accounttest.TokenOptions{KID: "gone", Plan: "pro"})
	cases := []struct {
		name string
		sess account.Session
		want string
	}{
		{"refused, token kept", account.Session{AccessToken: valid, State: "refused"}, "locked/refused"},
		{"refused, token cleared", account.Session{State: "refused"}, "locked/refused"},
		{"no token", account.Session{}, "locked/not_logged_in"},
		{"garbage", account.Session{AccessToken: "not-a-jwt"}, "locked/invalid"},
		{"a key that is not pinned", account.Session{AccessToken: unpinned}, "locked/key_unknown"},
	}
	for _, c := range cases {
		for _, dormant := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, dormant %v", c.name, dormant), func(t *testing.T) {
				if dormant {
					account.SetEnforceFromForTest(t, time.Time{})
				}
				sess := c.sess
				sess.V, sess.HW, sess.Plan = 1, now, "pro"
				st := account.Evaluate(&sess, now)
				if got := verdict(st); got != c.want || st.Allowed() != dormant {
					t.Fatalf("Evaluate = %s, allowed %v, want %s, allowed %v", got, st.Allowed(), c.want, dormant)
				}
				if st.Plan != "" {
					t.Fatalf("plan = %q, want none: the stored plan is not verified", st.Plan)
				}
			})
		}
	}
}

// Session.State is "" or "refused" (spec §4.6). This pins what Evaluate does
// with any other value, so that changing it is a decision: it does not lock by
// itself, the verdict follows the token.
func TestOnlyTheRefusedStateLocksBeforeTheToken(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	sess := &account.Session{V: 1, State: "blocked", AccessToken: f.Token(accounttest.TokenOptions{}), HW: now}
	if got := verdict(account.Evaluate(sess, now)); got != "ok/" {
		t.Fatalf("a stored state other than refused: Evaluate = %s, want ok/", got)
	}
}

// NewSession checks the token with the same allowance as the guard: a token
// issued up to 5m ahead is a session (its hw is that iat, which the guard
// tolerates), one a second further is not.
func TestNewSessionAcceptsTheSkewAllowanceAndNoMore(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	edge := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(5 * time.Minute)})
	sess, err := account.NewSession("h", edge, nil, now)
	if err != nil {
		t.Fatalf("a token issued exactly 5m ahead must become a session: %v", err)
	}
	if got := verdict(account.Evaluate(sess, now)); got != "ok/" {
		t.Fatalf("the session just made, at the time it was made: Evaluate = %s, want ok/", got)
	}
	over := f.Token(accounttest.TokenOptions{IssuedAt: now.Add(5*time.Minute + time.Second)})
	if sess, err := account.NewSession("h", over, nil, now); sess != nil || reasonOf(err) != account.ReasonClockSkew {
		t.Fatalf("a token issued 5m and a second ahead: a session %v, reason %q, want no session and clock_skew", sess != nil, reasonOf(err))
	}
}

func TestNewSessionIsNilWhenTheTokenDoesNotVerify(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	cases := []struct {
		name  string
		token string
		want  account.Reason
	}{
		{"garbage", "not-a-jwt", account.ReasonInvalid},
		{"a key that is not pinned", f.Token(accounttest.TokenOptions{KID: "gone"}), account.ReasonKeyUnknown},
		{"from the future", f.Token(accounttest.TokenOptions{IssuedAt: now.Add(time.Hour)}), account.ReasonClockSkew},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if sess, err := account.NewSession("h", c.token, nil, now); sess != nil || reasonOf(err) != c.want {
				t.Fatalf("NewSession: a session %v, reason %q, want no session and %q", sess != nil, reasonOf(err), c.want)
			}
		})
	}
}

// NewSession reads the time it is given, not the wall clock.
func TestNewSessionUsesTheClockItIsGiven(t *testing.T) {
	f := accounttest.New(t)
	far := time.Date(2040, time.January, 2, 3, 4, 5, 0, time.UTC) // nowhere near the wall clock
	sess, err := account.NewSession("h", f.Token(accounttest.TokenOptions{IssuedAt: far}), nil, far)
	if err != nil {
		t.Fatalf("a token issued at the given time is not from the future: %v", err)
	}
	if !sess.HW.Equal(far) || !sess.LastAttempt.Equal(far) {
		t.Fatalf("hw = %v, last attempt = %v, want both %v", sess.HW, sess.LastAttempt, far)
	}
}

// The keys of session.json are what an installed build reads back after an
// update, and what carries hw and a refusal from one process to the next: a
// renamed key drops them silently.
func TestSessionJSON(t *testing.T) {
	// The file of spec §4.6 with every key set.
	const stored = `{"v":1,"host":"https://monoes.me","access_token":"t","user":{"id":"u-1","email":"a@b.c","username":"abc"},` +
		`"plan":"pro","hw":"2026-10-05T12:00:00Z","last_attempt":"2026-10-05T11:59:00Z","last_result":"server_error","state":"refused","reason":"revoked"}`
	var sess account.Session
	if err := json.Unmarshal([]byte(stored), &sess); err != nil {
		t.Fatal(err)
	}
	hw := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if sess.V != 1 || sess.Host != "https://monoes.me" || sess.AccessToken != "t" || sess.User == nil || *sess.User != (account.User{ID: "u-1", Email: "a@b.c", Username: "abc"}) ||
		sess.Plan != "pro" || !sess.HW.Equal(hw) || !sess.LastAttempt.Equal(hw.Add(-time.Minute)) || sess.LastResult != "server_error" || sess.State != "refused" || sess.Reason != "revoked" {
		t.Fatalf("a session.json written as the spec lays it out did not read back: v=%d host=%q user=%v plan=%q hw=%v attempt=%v last=%q state=%q reason=%q",
			sess.V, sess.Host, sess.User, sess.Plan, sess.HW, sess.LastAttempt, sess.LastResult, sess.State, sess.Reason)
	}
	want := []string{"access_token", "host", "hw", "last_attempt", "last_result", "plan", "reason", "state", "user", "v"}
	if got := jsonKeys(t, sess); !reflect.DeepEqual(got, want) {
		t.Fatalf("session.json keys = %v, want %v", got, want)
	}
	// Everything optional drops out when empty.
	if got, want := jsonKeys(t, account.Session{}), []string{"access_token", "host", "v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty session.json keys = %v, want %v", got, want)
	}
}
