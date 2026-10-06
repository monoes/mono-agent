package tasks

import (
	"errors"
	"math"
	"testing"
	"time"
)

// Whatever number of nanoseconds a caller passes, a claim holds the task for the default, for the
// lease it asked for or for the longest lease, and nothing wraps round.
func TestAClaimsLeaseIsClampedWhateverTheCallerAsked(t *testing.T) {
	for _, c := range []struct {
		name  string
		lease time.Duration
		want  time.Duration
	}{
		{"none", 0, DefaultLease},
		{"a negative lease", -time.Hour, DefaultLease},
		{"the most negative duration", time.Duration(math.MinInt64), DefaultLease},
		{"an hour", time.Hour, time.Hour},
		{"the longest lease", MaxLease, MaxLease},
		{"two days", 48 * time.Hour, MaxLease},
		{"the longest duration", time.Duration(math.MaxInt64), MaxLease},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, clk := newTestStore(t)
			byID, byNext := mustAdd(t, s, "default", "by id", true), mustAdd(t, s, "default", "by next", true)
			want := clk.t.Add(c.want)
			got, err := s.Claim(bg, "default", byID.ID, bot("one"), c.lease)
			if err != nil || got.Claim == nil || !got.Claim.Until.Equal(want) {
				t.Fatalf("claim: %+v, %v, want the lease to end at %v", got.Claim, err, want)
			}
			if _, until := opsClaim(t, db, byID.ID); until != want.Format(timeFmt) {
				t.Errorf("stored lease end %q, want %q", until, want.Format(timeFmt))
			}
			next, err := s.Next(bg, "default", bot("two"), true, c.lease)
			if err != nil || next == nil || next.ID != byNext.ID || next.Claim == nil || !next.Claim.Until.Equal(want) {
				t.Errorf("next --claim: %+v, %v, want the lease to end at %v", next, err, want)
			}
		})
	}
}

// A time is stored to the second: a lease of a millisecond that began half way through a second must
// not be over the moment it is written, which anybody could then take.
func TestAShortLeaseIsNeverOverAtBirth(t *testing.T) {
	s, _, clk := newTestStore(t)
	clk.t = clk.t.Add(500 * time.Millisecond)
	task := mustAdd(t, s, "default", "brief", true)
	got, err := s.Claim(bg, "default", task.ID, bot("one"), time.Millisecond)
	end := time.Date(2026, 10, 5, 12, 0, 1, 0, time.UTC)
	if err != nil || got.Claim == nil || !got.Claim.Until.Equal(end) || got.Claim.Stale {
		t.Fatalf("a lease of a millisecond: %+v, %v, want it to end at %v and not be stale", got.Claim, err, end)
	}
	if _, err := s.Claim(bg, "default", task.ID, bot("two"), 0); !errors.Is(err, ErrClaimed) {
		t.Errorf("a task nobody has had a lease on yet cannot be taken: %v", err)
	}
	if next, err := s.Next(bg, "default", bot("two"), true, 0); err != nil || next != nil {
		t.Errorf("next --claim: %+v, %v, want nothing to take", next, err)
	}
	clk.advance(500 * time.Millisecond) // the lease ends now
	got, err = s.Claim(bg, "default", task.ID, bot("two"), 0)
	if err != nil || got.Claim == nil || got.Claim.By != "two" || got.LastEvent == nil || got.LastEvent.Kind != "reclaimed" {
		t.Errorf("when the lease has ended: %+v, %v", got, err)
	}
}

// A lease is never shorter than the one asked for, to the second: half a second into a minute the
// lease of half an hour ends a second after half an hour from the minute's start.
func TestALeaseIsNeverShorterThanAskedForAndACommentRenewsToo(t *testing.T) {
	s, _, clk := newTestStore(t)
	clk.t = clk.t.Add(500 * time.Millisecond)
	task := mustAdd(t, s, "default", "long", true)
	got := claimsClaim(t, s, task.ID, "one", 0)
	if end := clk.t.Add(DefaultLease); got.Claim.Until.Before(end) || got.Claim.Until.Sub(end) >= time.Second {
		t.Errorf("a lease of half an hour from %v ends at %v, want the whole second at or after %v", clk.t, got.Claim.Until, end)
	}
	clk.advance(time.Minute)
	got, err := s.Comment(bg, "default", task.ID, "going on", bot("one"))
	if end := clk.t.Add(DefaultLease); err != nil || got.Claim.Until.Before(end) || got.Claim.Until.Sub(end) >= time.Second {
		t.Errorf("a comment renews to the whole second at or after %v: %+v, %v", end, got.Claim, err)
	}
}

// The 24 hours cap what is stored (R11): a lease is rounded up to a whole second so that a short one is
// never over at birth, but it never ends later than MaxLease after the whole second the claim is stamped
// with, which is the start that is stored. On a clock with a fraction of a second (every real one)
// rounding the longest lease up would make it a second longer than the cap. The clock stands half way
// through a second here, and each lease is taken by id and by next --claim.
func TestARoundedUpLeaseNeverEndsLaterThanTheCapAllows(t *testing.T) {
	for _, c := range []struct {
		name  string
		lease time.Duration
		end   string // where the lease is stored as ending, for a claim at 12:00:00.5 on 2026-10-05
	}{
		{"the longest lease", MaxLease, "2026-10-06T12:00:00Z"},
		{"two days", 48 * time.Hour, "2026-10-06T12:00:00Z"},
		{"the longest duration", time.Duration(math.MaxInt64), "2026-10-06T12:00:00Z"},
		{"a second short of the longest", MaxLease - time.Second, "2026-10-06T12:00:00Z"},
		{"two seconds short of the longest", MaxLease - 2*time.Second, "2026-10-06T11:59:59Z"},
		{"none", 0, "2026-10-05T12:30:01Z"},
		{"a negative lease", -time.Hour, "2026-10-05T12:30:01Z"},
		{"the most negative duration", time.Duration(math.MinInt64), "2026-10-05T12:30:01Z"},
		{"a nanosecond", 1, "2026-10-05T12:00:01Z"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, clk := newTestStore(t)
			clk.t = clk.t.Add(500 * time.Millisecond)
			byID, byNext := mustAdd(t, s, "default", "by id", true), mustAdd(t, s, "default", "by next", true)
			got, err := s.Claim(bg, "default", byID.ID, bot("one"), c.lease)
			if err != nil || got.Claim == nil {
				t.Fatalf("claim: %+v, %v", got, err)
			}
			next, err := s.Next(bg, "default", bot("two"), true, c.lease)
			if err != nil || next == nil || next.ID != byNext.ID || next.Claim == nil {
				t.Fatalf("next --claim: %+v, %v", next, err)
			}
			for what, task := range map[string]Task{"claim": got, "next --claim": *next} {
				if _, until := opsClaim(t, db, task.ID); until != c.end {
					t.Errorf("%s: the lease is stored as ending %q, want %q", what, until, c.end)
				}
				if end := task.Claim.Until.UTC().Format(timeFmt); end != c.end || task.Claim.Stale {
					t.Errorf("%s: the task says the lease ends %q and is stale %v, want %q and a lease that is not over", what, end, task.Claim.Stale, c.end)
				}
			}
			// asked for, a lease is never over at birth, whatever it was asked for: nobody takes the task
			if _, err := s.Claim(bg, "default", byID.ID, bot("three"), 0); !errors.Is(err, ErrClaimed) {
				t.Errorf("a claim of the task right after: %v, want ErrClaimed", err)
			}
			if cn, err := s.Counts(bg, "default"); err != nil || cn.Stale != 0 {
				t.Errorf("stale claims %d (err %v), want 0", cn.Stale, err)
			}
		})
	}
}

// A lease that ends exactly now has ended (spec 4.2): the other agent's claim and next --claim take the
// task at that second, and not a second before. Each of them states the comparison on its own.
func TestALeaseThatEndsExactlyNowHasEnded(t *testing.T) {
	takers := []struct {
		name string
		take func(s *Store, id int64) (*Task, error)
	}{
		{"claim by id", func(s *Store, id int64) (*Task, error) {
			got, err := s.Claim(bg, "default", id, bot("two"), 0)
			if err != nil {
				return nil, err
			}
			return &got, nil
		}},
		{"next --claim", func(s *Store, id int64) (*Task, error) { return s.Next(bg, "default", bot("two"), true, 0) }},
	}
	for _, k := range takers {
		for _, after := range []time.Duration{0, time.Second, time.Hour} {
			t.Run(k.name+", "+after.String()+" after the end", func(t *testing.T) {
				s, _, clk := newTestStore(t)
				task := mustAdd(t, s, "default", "abandoned", true)
				claimsClaim(t, s, task.ID, "one", 0)
				clk.advance(DefaultLease - time.Second)
				got, err := k.take(s, task.ID)
				if k.name == "claim by id" && !errors.Is(err, ErrClaimed) {
					t.Fatalf("a second before the end: %+v, %v, want ErrClaimed", got, err)
				}
				if k.name == "next --claim" && (err != nil || got != nil) {
					t.Fatalf("a second before the end: %+v, %v, want nothing to take", got, err)
				}
				clk.advance(time.Second + after)
				got, err = k.take(s, task.ID)
				if err != nil || got == nil || got.ID != task.ID || got.Claim == nil || got.Claim.By != "two" || got.Claim.Stale {
					t.Fatalf("%v after the end: %+v, %v, want the task taken by two", after, got, err)
				}
				if got.LastEvent == nil || got.LastEvent.Kind != "reclaimed" || got.LastEvent.Actor != "two" {
					t.Errorf("last event: %+v", got.LastEvent)
				}
			})
		}
	}
}

// The stored text is UTC whatever zone the clock runs in, for the lease, the event and the row.
func TestTheTimesOfAClaimAreStoredInUTCWhateverZoneTheClockRunsIn(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("plus0530", 5*3600+1800), time.FixedZone("minus08", -8*3600)} {
		t.Run(zone.String(), func(t *testing.T) {
			s, db, clk := newTestStore(t)
			s.now = func() time.Time { return clk.t.In(zone) }
			task := mustAdd(t, s, "default", "zoned", true)
			got := claimsClaim(t, s, task.ID, "one", 2*time.Hour)
			if !got.Claim.Until.Equal(clk.t.Add(2 * time.Hour)) {
				t.Errorf("the lease ends at %v, want %v", got.Claim.Until, clk.t.Add(2*time.Hour))
			}
			if _, until := opsClaim(t, db, task.ID); until != "2026-10-05T14:00:00Z" {
				t.Errorf("stored lease end %q, want the UTC text 2026-10-05T14:00:00Z", until)
			}
			if at := claimsText(t, db, `SELECT at FROM task_events WHERE task_id = ? AND kind = 'claimed'`, task.ID); at != "2026-10-05T12:00:00Z" {
				t.Errorf("claimed event at %q", at)
			}
			clk.advance(10 * time.Minute)
			if _, err := s.Comment(bg, "default", task.ID, "going on", bot("one")); err != nil {
				t.Fatal(err)
			}
			if at, upd := claimsText(t, db, `SELECT at FROM task_events WHERE task_id = ? AND kind = 'comment'`, task.ID),
				claimsText(t, db, `SELECT updated_at FROM tasks WHERE id = ?`, task.ID); at != "2026-10-05T12:10:00Z" || upd != at {
				t.Errorf("comment at %q, row updated at %q, want both 2026-10-05T12:10:00Z", at, upd)
			}
			if _, until := opsClaim(t, db, task.ID); until != "2026-10-05T14:00:00Z" {
				t.Errorf("a comment shortened the lease to %q", until)
			}
			if _, err := s.Finish(bg, "default", task.ID, Outcome{Result: "done"}, bot("one")); err != nil {
				t.Fatal(err)
			}
			if upd := claimsText(t, db, `SELECT updated_at FROM tasks WHERE id = ?`, task.ID); upd != "2026-10-05T12:10:00Z" {
				t.Errorf("finished at %q", upd)
			}
		})
	}
}
