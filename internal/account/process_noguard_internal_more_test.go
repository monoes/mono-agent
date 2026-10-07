package account

import (
	"errors"
	"testing"
	"time"
)

// requireNoGuard is what Require does when no guard is installed. Its first
// parameter is testing.Testing(), which is true in every test binary, so what a
// release binary does (isTest false, and the strict flag, which only a test sets,
// off) can be tried only through that parameter. The matrix is every kind of
// process at every moment of the rollout: dormant (D22: nothing locks), before the
// date (nothing locks yet), at the date and after it (the process is judged as not
// logged in, so it is refused). The one exception is a test binary that has not
// asked for strictness: it is let through at every moment (D24).
func TestRequireNoGuardMatrix(t *testing.T) {
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	moments := []struct {
		name        string
		enforceFrom time.Time
		now         time.Time
	}{
		{"dormant", time.Time{}, date.Add(24 * time.Hour)},
		{"before the date", date, date.Add(-time.Nanosecond)},
		{"at the date", date, date},
		{"after the date", date, date.Add(24 * time.Hour)},
	}
	processes := []struct {
		name             string
		isTest, isStrict bool
		refuses          [4]bool // at each moment, in the order above
	}{
		{"a release binary", false, false, [4]bool{false, false, true, true}},
		{"a release binary with the strict flag set", false, true, [4]bool{false, false, true, true}},
		{"a test binary that has not asked for strictness", true, false, [4]bool{false, false, false, false}},
		{"a strict test", true, true, [4]bool{false, false, true, true}},
	}
	for _, p := range processes {
		for i, m := range moments {
			t.Run(p.name+", "+m.name, func(t *testing.T) {
				SetEnforceFromForTest(t, m.enforceFrom)
				err := requireNoGuard(p.isTest, p.isStrict, m.now)
				if !p.refuses[i] {
					if err != nil {
						t.Fatalf("requireNoGuard(isTest=%v, isStrict=%v) %s = %v, want nil", p.isTest, p.isStrict, m.name, err)
					}
					return
				}
				var lr *LoginRequiredError
				if !errors.As(err, &lr) {
					t.Fatalf("requireNoGuard(isTest=%v, isStrict=%v) %s = %v, want a *LoginRequiredError", p.isTest, p.isStrict, m.name, err)
				}
				if st := lr.Status; st.V != 1 || st.State != StateLocked || st.Reason != ReasonNotLoggedIn || !st.Enforced || !st.EnforceFrom.Equal(m.enforceFrom) {
					t.Fatalf("the refusal carries %+v, want locked/not_logged_in, enforced since %v", st, m.enforceFrom)
				}
			})
		}
	}
}
