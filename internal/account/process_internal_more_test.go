package account

import (
	"testing"
	"time"
)

// With no guard installed the process is judged at the time Require and
// CurrentStatus hand over, against the enforcement date, to the nanosecond: the
// date itself is enforced, the instant before it is not, and while dormant
// nothing is, whatever the time. (Require and CurrentStatus hand over the real
// time, which a test cannot place on the nanosecond.)
func TestNoGuardStatusAtTheEnforcementDate(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		date     time.Time
		now      time.Time
		enforced bool
	}{
		{"dormant", time.Time{}, date.Add(24 * time.Hour), false},
		{"a nanosecond before the date", date, date.Add(-time.Nanosecond), false},
		{"at the date", date, date, true},
		{"a nanosecond after the date", date, date.Add(time.Nanosecond), true},
		{"a day after the date", date, date.Add(24 * time.Hour), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			SetEnforceFromForTest(t, c.date)
			st := noGuardStatus(c.now)
			if st.Enforced != c.enforced || st.Allowed() == c.enforced || !st.EnforceFrom.Equal(c.date) ||
				st.State != StateLocked || st.Reason != ReasonNotLoggedIn || st.V != 1 || st.User != nil {
				t.Fatalf("noGuardStatus(%v) with date %v = %+v, want locked/not_logged_in, enforced=%v", c.now, c.date, st, c.enforced)
			}
		})
	}
}
