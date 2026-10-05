package account

import "time"

// The enforcement date (spec D22). The zero time means the package is
// dormant: nothing locks, nothing warns and nothing contacts monoes.me
// implicitly. B5a is the one change that sets it, in the initializer below;
// everything else reads it through EnforceDate.
var enforceFrom = time.Time{}

// EnforceDate returns the enforcement date, or the zero time while dormant.
func EnforceDate() time.Time {
	globalsMu.RLock()
	defer globalsMu.RUnlock()
	return enforceFrom
}

// Enforced reports whether the gate is enforced: a date is set and
// max(now, hw) has reached it. Judging against the high-water mark hw as well
// means that setting the clock back does not postpone the date.
func Enforced(now, hw time.Time) bool {
	date := EnforceDate()
	if date.IsZero() {
		return false
	}
	if hw.After(now) {
		now = hw
	}
	return !now.Before(date)
}

// dormant reports whether no enforcement date is set.
func dormant() bool { return EnforceDate().IsZero() }
