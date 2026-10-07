package account

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// A time from time.Now carries a monotonic clock reading, and when both operands
// of Before, After or Sub carry one, those compare the monotonic readings alone.
// The monotonic clock does not follow a system clock that is set back, so a
// comparison of two such times cannot see the rollback the verdict exists to
// catch. Production gives judge exactly that: the guard keeps hw = time.Now() in
// memory and passes time.Now() as now. The receipt times (time.Unix) and the
// enforcement date (a constant) carry no reading, so they are compared by the wall
// clock: without a strip, the checks of one verdict would use two clocks.
//
// time offers no way to build a time.Now() whose wall reading was set back and
// whose monotonic reading went on, so wallBack edits the wall field of a copy
// through unsafe. It checks the layout first, and the test fails when the layout
// is not the one it edits: CI runs without -v, so a skip would retire the test
// unseen. This file is in package account because judge is not exported, and so
// it cannot use accounttest, which imports account: the one token it needs is
// signed here with a throwaway key.

// rawTime mirrors time.Time: wall is the hasMonotonic bit (63), 33 bits of seconds
// (62 to 30) and 30 bits of nanoseconds; ext is the monotonic reading.
type rawTime struct {
	wall uint64
	ext  int64
	loc  *time.Location
}

// wallBack returns now with its wall reading moved back by d, a whole number of
// seconds, and its monotonic reading kept: the value time.Now returns after the
// system clock was set back by d. It fails the test when time.Time does not have
// the layout rawTime assumes: a Go release that changed it must not retire the
// only test that kills the removal of judge's strip without anyone seeing.
func wallBack(t *testing.T, now time.Time, d time.Duration) time.Time {
	t.Helper()
	if d <= 0 || d%time.Second != 0 {
		t.Fatalf("wallBack(%v): d must be a positive whole number of seconds", d)
	}
	const hasReading = " m=" // Time.String ends with the monotonic reading when there is one
	layoutChanged := func(why string) {
		t.Fatal("the layout of time.Time changed (" + why + "): wallBack edits it through unsafe and must be updated for this Go version; until then the test of the wall-clock verdict does not run")
	}
	if unsafe.Sizeof(now) != unsafe.Sizeof(rawTime{}) {
		layoutChanged("a different size")
	}
	if !strings.Contains(now.String(), hasReading) {
		layoutChanged("time.Now carries no monotonic reading")
	}
	secs := int64(d / time.Second)
	shifted := now
	(*rawTime)(unsafe.Pointer(&shifted)).wall -= uint64(secs) << 30
	if now.Unix()-shifted.Unix() != secs || shifted.Nanosecond() != now.Nanosecond() || shifted.Sub(now) != 0 || !strings.Contains(shifted.String(), hasReading) {
		layoutChanged("the shift did not move only the wall seconds")
	}
	return shifted
}

// rolledBack returns what a long-running process holds after the system clock was
// set back by d: hw, a time.Now() it kept in memory, now, a time.Now() read after
// the rollback, and wall, the time hw showed, without a monotonic reading, which
// is what receipts and the enforcement date are made from.
func rolledBack(t *testing.T, d time.Duration) (hw, now, wall time.Time) {
	t.Helper()
	hw = time.Now()
	now = wallBack(t, time.Now(), d)
	return hw, now, hw.Round(0)
}

// mintToken signs an access token issued at iat for an hour, and makes the package
// trust its key for the rest of the test.
func mintToken(t *testing.T, iat time.Time) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "monotonic-test"
	SetTrustedKeysForTest(t, []Key{{KID: kid, Public: pub}})
	header, err := json.Marshal(map[string]any{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{"iss": Issuer, "aud": Audience, "azp": ClientID, "sub": "u-1", "iat": iat.Unix(), "exp": iat.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	signed := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	return signed + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(signed)))
}

func TestTheVerdictComparesWallClockTimeOnly(t *testing.T) {
	wallBack(t, time.Now(), time.Second) // fails the whole test when the layout differs
	const hour = time.Hour

	// The token expired 29 hours ago by the true time; the clock, set back 29h50m,
	// puts "now" 50 minutes into its hour.
	t.Run("judge: an expired token, the clock set back into its hour", func(t *testing.T) {
		hw, now, wall := rolledBack(t, 29*hour+50*time.Minute)
		rcpt := &Receipt{Sub: "u-1", Plan: "free", IssuedAt: wall.Add(-30 * hour), ExpiresAt: wall.Add(-29 * hour)}
		st := judge(&Session{V: 1, AccessToken: "x", HW: hw}, rcpt, nil, now)
		if st.State != StateLocked || st.Reason != ReasonClockRollback {
			t.Fatalf("judge = %s/%s, want locked/clock_rollback", st.State, st.Reason)
		}
	})

	t.Run("Evaluate: the same", func(t *testing.T) {
		hw, now, wall := rolledBack(t, 29*hour+50*time.Minute)
		SetEnforceFromForTest(t, wall.Add(-100*24*hour))
		st := Evaluate(&Session{V: 1, AccessToken: mintToken(t, wall.Add(-30*hour)), HW: hw}, now)
		if st.State != StateLocked || st.Reason != ReasonClockRollback || st.Allowed() {
			t.Fatalf("Evaluate = %s/%s, allowed %v, want locked/clock_rollback and not allowed", st.State, st.Reason, st.Allowed())
		}
	})

	// hw is past the enforcement date and the clock was set back to before it:
	// max(now, hw) is what the date is judged against, so it is enforced.
	t.Run("judge: the enforcement date is not postponed", func(t *testing.T) {
		hw, now, wall := rolledBack(t, 3*hour)
		SetEnforceFromForTest(t, wall.Add(-hour))
		rcpt := &Receipt{Sub: "u-1", Plan: "free", IssuedAt: wall.Add(-10 * time.Minute), ExpiresAt: wall.Add(50 * time.Minute)}
		st := judge(&Session{V: 1, AccessToken: "x", HW: hw}, rcpt, nil, now)
		if !st.Enforced || st.Allowed() {
			t.Fatalf("judge = %s/%s, enforced %v, allowed %v, want enforced and not allowed", st.State, st.Reason, st.Enforced, st.Allowed())
		}
	})

	t.Run("Enforced: called directly", func(t *testing.T) {
		hw, now, wall := rolledBack(t, 3*hour)
		SetEnforceFromForTest(t, wall.Add(-hour))
		if !Enforced(now, hw) {
			t.Fatal("Enforced(now, hw) = false, want true: hw is past the date and the set-back now must not postpone it")
		}
	})

	// The same readings with no clock set back: the helper must not turn every
	// verdict into a rollback or every date into an enforced one.
	t.Run("control: a clock that was not set back", func(t *testing.T) {
		hw := time.Now()
		now := time.Now()
		wall := hw.Round(0)
		SetEnforceFromForTest(t, wall.Add(hour))
		rcpt := &Receipt{Sub: "u-1", Plan: "free", IssuedAt: wall.Add(-10 * time.Minute), ExpiresAt: wall.Add(50 * time.Minute)}
		if st := judge(&Session{V: 1, AccessToken: "x", HW: hw}, rcpt, nil, now); st.State != StateOK || st.Enforced {
			t.Fatalf("judge = %s/%s, enforced %v, want ok and not enforced", st.State, st.Reason, st.Enforced)
		}
		if st := Evaluate(&Session{V: 1, AccessToken: mintToken(t, wall.Add(-10*time.Minute)), HW: hw}, now); st.State != StateOK {
			t.Fatalf("Evaluate = %s/%s, want ok", st.State, st.Reason)
		}
		if Enforced(now, hw) {
			t.Fatal("Enforced(now, hw) = true with the date an hour ahead and no clock set back")
		}
	})
}
