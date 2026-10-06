package account

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEnforced(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		date, now, hw time.Time
		want          bool
	}{
		{"dormant", time.Time{}, date.Add(time.Hour), time.Time{}, false},
		{"before the date", date, date.Add(-time.Second), time.Time{}, false},
		{"at the date", date, date, time.Time{}, true},
		{"after the date", date, date.Add(time.Hour), time.Time{}, true},
		{"clock set back but hw is past the date", date, date.Add(-48 * time.Hour), date.Add(time.Minute), true},
		{"hw behind now", date, date.Add(-time.Hour), date.Add(-2 * time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			SetEnforceFromForTest(t, c.date)
			if got := Enforced(c.now, c.hw); got != c.want {
				t.Fatalf("Enforced(%v, %v) with date %v = %v, want %v", c.now, c.hw, c.date, got, c.want)
			}
			if got := dormant(); got != c.date.IsZero() {
				t.Fatalf("dormant() = %v with date %v", got, c.date)
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		enforced bool
		state    State
		want     bool
	}{
		{false, StateLocked, true}, // nothing locks before the date
		{false, StateOK, true},
		{true, StateOK, true},
		{true, StateGrace, true},
		{true, StateLocked, false},
		{true, State(""), false}, // fail closed: once enforced, only ok and grace pass
		{false, StateGrace, true},
	}
	for _, c := range cases {
		if got := (Status{State: c.state, Enforced: c.enforced}).Allowed(); got != c.want {
			t.Errorf("Allowed(enforced=%v, %s) = %v, want %v", c.enforced, c.state, got, c.want)
		}
	}
}

func TestSeamsRestoreWhenTheTestEnds(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	dateBefore, keysBefore := EnforceDate(), len(TrustedKeys()) // whatever the release pins and dates, not zero
	t.Run("hooks", func(t *testing.T) {
		SetEnforceFromForTest(t, at)
		SetTrustedKeysForTest(t, []Key{{KID: "k1", Public: make(ed25519.PublicKey, ed25519.PublicKeySize)}})
		StrictForTest(t)
		if keys := TrustedKeys(); !EnforceDate().Equal(at) || len(keys) != 1 || keys[0].KID != "k1" {
			t.Fatalf("the hooks did not take effect: date %v, trusted keys %v", EnforceDate(), keys)
		}
		if !isStrict() {
			t.Fatal("StrictForTest did not set the strict flag")
		}
	})
	strictNow := isStrict()
	_, leaked := lookupKey("k1") // the length alone cannot tell a leaked key from a pinned one once a release pins exactly one
	if !EnforceDate().Equal(dateBefore) || len(TrustedKeys()) != keysBefore || leaked || strictNow {
		t.Fatalf("a hook leaked out of its test: date %v (was %v), keys %d (was %d), key k1 still trusted %v, strict %v", EnforceDate(), dateBefore, len(TrustedKeys()), keysBefore, leaked, strictNow)
	}
}

func TestSeamsPanicOutsideATestBinary(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "only be used from a test binary") {
			t.Fatalf("mustBeTestBinary(false) recovered %v, want the test-seam panic", r)
		}
	}()
	mustBeTestBinary(false, "SetEnforceFromForTest")
}

func TestTrustedKeysIsACopy(t *testing.T) {
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	SetTrustedKeysForTest(t, []Key{{KID: "k1", Public: pub}})
	got := TrustedKeys()
	got[0].KID = "changed"
	got[0].Public[0] = 9
	if again := TrustedKeys(); again[0].KID != "k1" || again[0].Public[0] != 0 {
		t.Fatalf("a caller changed the trusted set: %+v", again[0])
	}
	pub[0] = 7 // the caller changes its slice after the hook: the trusted set must not follow
	if TrustedKeys()[0].Public[0] != 0 {
		t.Fatal("SetTrustedKeysForTest kept the caller's slice instead of copying it")
	}
}

func TestPinKey(t *testing.T) {
	k := pinKey("kid-1", strings.Repeat("ab", ed25519.PublicKeySize))
	if k.KID != "kid-1" || len(k.Public) != ed25519.PublicKeySize || k.Public[0] != 0xab {
		t.Fatalf("pinKey = %+v", k)
	}
	for _, bad := range []string{"", "zz", strings.Repeat("ab", 31)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("pinKey(%q) did not panic", bad)
				}
			}()
			pinKey("bad", bad)
		}()
	}
}

func TestLoginRequiredError(t *testing.T) {
	first := "Log in to monoes.me first: monoagentcli account login"
	if LoginRequiredMessage != first {
		t.Fatalf("LoginRequiredMessage = %q", LoginRequiredMessage)
	}
	for _, c := range []struct {
		reason   Reason
		hasExtra bool
	}{
		{ReasonNotLoggedIn, false},
		{ReasonExpired, true},
		{ReasonRefused, true},
		{ReasonClockRollback, true},
		{ReasonClockSkew, true},
		{ReasonKeyUnknown, true},
		{ReasonInvalid, true},
	} {
		msg := (&LoginRequiredError{Status: Status{State: StateLocked, Reason: c.reason}}).Error()
		line, rest, hasRest := strings.Cut(msg, "\n")
		if line != first {
			t.Errorf("%s: first line = %q, want %q", c.reason, line, first)
		}
		if hasRest != c.hasExtra || (hasRest && rest == "") {
			t.Errorf("%s: extra line present = %v, want %v (message %q)", c.reason, hasRest, c.hasExtra, msg)
		}
	}
	wrapped := fmt.Errorf("gate: %w", &LoginRequiredError{})
	if !IsLoginRequired(wrapped) || IsLoginRequired(errors.New("other")) || IsLoginRequired(nil) {
		t.Fatal("IsLoginRequired must find a wrapped *LoginRequiredError and nothing else")
	}
}

// The CLI's JSON error wrappers find these fields with errors.As on an
// interface (cmd/monoagentcli/automation.go: jsonErrorFields), so a layer-2
// refusal that comes out of any command carries the document of index §3.4 item 2.
func TestLoginRequiredErrorJSONFields(t *testing.T) {
	cases := []struct {
		name   string
		status Status
		want   string
	}{
		{"locked, not logged in", Status{State: StateLocked, Reason: ReasonNotLoggedIn},
			`{"account":{"reason":"not_logged_in","state":"locked"},"code":"auth_or_connection","login_required":true}`},
		{"locked, refused", Status{State: StateLocked, Reason: ReasonRefused},
			`{"account":{"reason":"refused","state":"locked"},"code":"auth_or_connection","login_required":true}`},
		{"grace", Status{State: StateGrace, Reason: ReasonUnreachable},
			`{"account":{"reason":"unreachable","state":"grace"},"code":"auth_or_connection","login_required":true}`},
	}
	type fieldser = interface{ JSONErrorFields() map[string]any }
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("gate: %w", &LoginRequiredError{Status: c.status})
			var fields fieldser
			if !errors.As(wrapped, &fields) {
				t.Fatal("errors.As does not find JSONErrorFields on a wrapped *LoginRequiredError")
			}
			raw, err := json.Marshal(fields.JSONErrorFields())
			if err != nil || string(raw) != c.want {
				t.Fatalf("JSONErrorFields marshalled to %s (err %v), want %s", raw, err, c.want)
			}
		})
	}
}

func TestRefreshErrors(t *testing.T) {
	var refused *RefusedError
	if !errors.As(fmt.Errorf("w: %w", &RefusedError{Description: "revoked"}), &refused) || refused.Description != "revoked" {
		t.Fatal("RefusedError must survive wrapping")
	}
	if got := (&RefusedError{}).Error(); !strings.Contains(got, "invalid_grant") {
		t.Fatalf("RefusedError message %q does not name invalid_grant", got)
	}
	cause := errors.New("dial tcp: i/o timeout")
	te := &TransientError{Reason: ReasonUnreachable, Err: cause}
	if !errors.Is(te, cause) || !strings.Contains(te.Error(), "unreachable") {
		t.Fatalf("TransientError = %q, want it to wrap its cause and name the reason", te)
	}
	if got := (&TransientError{Reason: ReasonServerError}).Error(); !strings.Contains(got, "server_error") {
		t.Fatalf("TransientError without a cause = %q", got)
	}
}
