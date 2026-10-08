package account

import (
	"testing"
	"time"
)

// Every time the guard keeps or compares is wall-clock time (see NewGuard). A
// time from time.Now carries a monotonic reading, and when both operands of
// Before, After or Sub carry one, those compare the readings alone. The
// monotonic clock does not follow a system clock that is set back, so a guard
// that kept such times would not see a rollback made while a long-lived process
// (a daemon, httpapi, mcp) runs; and the high-water mark that the refresh code
// stores from the guard's clock would persist the rolled-back time over the
// mark. A time with no reading is == to its Round(0); a time with one is not.
// This file is in package account because it reads the guard's own clock.
func TestTheGuardClockCarriesNoMonotonicReading(t *testing.T) {
	newGuard := func(now func() time.Time) *Guard {
		return NewGuard(GuardOptions{Store: OpenStore(t.TempDir(), NewMemorySealer()), Now: now})
	}

	// A supplied clock: the guard must hand back what the clock returned, with
	// the reading stripped. got is what the clock returned on the last call.
	supplied := []struct {
		name string
		read func() time.Time
	}{
		{"time.Now", time.Now},
		{"a clock an hour ahead", func() time.Time { return time.Now().Add(time.Hour) }},
	}
	for _, c := range supplied {
		t.Run(c.name, func(t *testing.T) {
			var got time.Time
			g := newGuard(func() time.Time { got = c.read(); return got })
			read := g.now()
			if got.Round(0) == got {
				t.Fatalf("the clock returned %v, which has no monotonic reading: this test cannot tell a guard that strips it from one that does not", got)
			}
			if want := got.Round(0); read != want {
				t.Fatalf("g.now() = %v, want the clock's reading without its monotonic part (%v)", read, want)
			}
		})
	}

	// No clock supplied: the default is time.Now, stripped the same way.
	t.Run("the default clock", func(t *testing.T) {
		g := newGuard(nil)
		before := time.Now().Round(0)
		read := g.now()
		if read != read.Round(0) {
			t.Fatalf("g.now() = %v, which still has a monotonic reading", read)
		}
		// A minute either way is far outside any scheduling delay, and still
		// tells time.Now from a zero time or another fixed one.
		if d := read.Sub(before); d < -time.Minute || d > time.Minute {
			t.Fatalf("g.now() = %v, %v away from time.Now: the default clock is not time.Now", read, d)
		}
	})
}
