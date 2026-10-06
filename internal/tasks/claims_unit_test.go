package tasks

import (
	"math"
	"testing"
	"time"
)

// A lease the caller asked for is one of the two defaults or what it asked for, whatever number a
// caller made of it (a CLI that multiplied minutes into a Duration may have wrapped it round).
func TestClampLease(t *testing.T) {
	for _, c := range []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"none", 0, DefaultLease},
		{"a nanosecond below none", -1, DefaultLease},
		{"a negative lease", -time.Hour, DefaultLease},
		{"the most negative duration", time.Duration(math.MinInt64), DefaultLease},
		{"a nanosecond", 1, 1},
		{"a minute", time.Minute, time.Minute},
		{"the default", DefaultLease, DefaultLease},
		{"the longest lease", MaxLease, MaxLease},
		{"a nanosecond over the longest", MaxLease + 1, MaxLease},
		{"two days", 48 * time.Hour, MaxLease},
		{"the longest duration", time.Duration(math.MaxInt64), MaxLease},
	} {
		if got := clampLease(c.in); got != c.want {
			t.Errorf("%s: clampLease(%d) = %d, want %d", c.name, int64(c.in), int64(got), int64(c.want))
		}
	}
}

func TestTheClaimCapIsTwoThousandEventsAndTheCommentCapStaysAtFiveHundred(t *testing.T) {
	if MaxEventsToClaim != 2000 || MaxEventsPerTask != 500 {
		t.Errorf("a claim is refused from %d events and a comment from %d, the spec says 2000 and 500", MaxEventsToClaim, MaxEventsPerTask)
	}
}

func TestLaterKeepsTheLaterOfTwoTimes(t *testing.T) {
	a := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	b := a.Add(time.Second)
	if got := later(a, b); !got.Equal(b) {
		t.Errorf("later(a, b) = %v, want b", got)
	}
	if got := later(b, a); !got.Equal(b) {
		t.Errorf("later(b, a) = %v, want b: a renewal never shortens a lease", got)
	}
	if got := later(a, a); !got.Equal(a) {
		t.Errorf("later(a, a) = %v", got)
	}
}

// A time is stored to the second, so a lease ends on a whole second: the one after the end it was
// asked for. A stored lease is never shorter than the one asked for, and one that is asked for is
// never over already (a lease of a millisecond would otherwise end at the second it began in).
func TestALeaseEndsOnTheWholeSecondAfterTheEndItWasAskedFor(t *testing.T) {
	whole := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	half := whole.Add(500 * time.Millisecond)
	zone := time.FixedZone("plus0530", 5*3600+1800)
	for _, c := range []struct {
		name string
		now  time.Time
		d    time.Duration
		want time.Time
	}{
		{"half an hour from a whole second", whole, 30 * time.Minute, whole.Add(30 * time.Minute)},
		{"a nanosecond from a whole second", whole, 1, whole.Add(time.Second)},
		{"a second from a whole second", whole, time.Second, whole.Add(time.Second)},
		{"a second and a nanosecond from a whole second", whole, time.Second + 1, whole.Add(2 * time.Second)},
		{"half an hour from half a second", half, 30 * time.Minute, whole.Add(30*time.Minute + time.Second)},
		{"half a second from half a second", half, 500 * time.Millisecond, whole.Add(time.Second)},
		{"a millisecond from half a second", half, time.Millisecond, whole.Add(time.Second)},
		{"half an hour from half a second, in another zone", half.In(zone), 30 * time.Minute, whole.Add(30*time.Minute + time.Second)},
	} {
		got := leaseEnd(c.now, c.d)
		if !got.Equal(c.want) || got.Nanosecond() != 0 {
			t.Errorf("%s: leaseEnd = %v, want %v", c.name, got, c.want)
		}
	}
}
