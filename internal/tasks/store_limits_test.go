package tasks

import (
	"errors"
	"testing"
	"time"
)

// 20 agent tasks created at one instant stop the agent until an hour has passed: a task exactly an
// hour old no longer counts, as a lease that ends exactly now has ended. The clock runs in three
// zones, because the stored text is UTC whatever zone the clock is in.
func TestHourlyLimitWindowIsOneHourInAnyZone(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("plus0530", 5*3600+1800), time.FixedZone("minus08", -8*3600)} {
		t.Run(zone.String(), func(t *testing.T) {
			s, db, c := newTestStore(t)
			base := c.t.In(zone)
			cur := base
			s.now = func() time.Time { return cur }
			for i := 0; i < 20; i++ {
				if _, _, err := s.Add(bg, "default", AddInput{Title: "a"}, bot("b")); err != nil {
					t.Fatalf("task %d: %v", i+1, err)
				}
			}
			var stored string
			if err := db.QueryRow(`SELECT created_at FROM tasks LIMIT 1`).Scan(&stored); err != nil || stored != "2026-10-05T12:00:00Z" {
				t.Fatalf("stored time %q (err %v): the text is the UTC time, not the zone's", stored, err)
			}
			for _, step := range []struct {
				after time.Duration
				limit bool
			}{
				{59*time.Minute + 59*time.Second, true},
				{time.Hour, false}, // created_at > since: a task exactly an hour old no longer counts
				{time.Hour + time.Second, false},
			} {
				cur = base.Add(step.after)
				_, _, err := s.Add(bg, "default", AddInput{Title: "a"}, bot("b"))
				if step.limit != errors.Is(err, ErrLimit) || (!step.limit && err != nil) {
					t.Errorf("%v after the twentieth: err %v, want limit %v", step.after, err, step.limit)
				}
			}
		})
	}
}
