package tasks

import (
	"errors"
	"testing"
	"time"
)

// These tests pin rules that the tests of store_test.go leave open (found by
// mutation). Every number below is a literal on purpose: a test that reads
// AgentTasksPerHour or MaxOpenTasks from the code under test cannot see the
// constant change.

func TestLimitsAreTheDocumentedNumbers(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"agent tasks per hour", AgentTasksPerHour, 20},
		{"open tasks per profile", MaxOpenTasks, 2000},
		{"source title runes", MaxSourceTitleRunes, 200},
		{"app name runes", MaxAppRunes, 100},
		{"actor name length", MaxNameLen, 64},
		{"client id length", MaxClientIDLen, 64},
		{"url bytes", MaxURLBytes, 2048},
		{"title runes", MaxTitleRunes, 200},
		{"notes bytes", MaxNotesBytes, 64 * 1024},
		{"position gap", positionGap, 1024},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d, the spec says %d", c.name, c.got, c.want)
		}
	}
}

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

func TestOnlyAgentTasksUseUpTheHourlyLimit(t *testing.T) {
	s, db, _ := newTestStore(t)
	for i := 0; i < 20; i++ {
		if _, _, err := s.Add(bg, "default", AddInput{Title: "a"}, bot("b")); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		in    AddInput
		actor Actor
	}{
		{"operator in the app", AddInput{Title: "t", SourceKind: SourceApp}, human},
		{"chrome capture", AddInput{Title: "t", SourceKind: SourceChrome}, Actor{Kind: Capture, Name: SourceChrome}},
		{"os capture", AddInput{Title: "t", SourceKind: SourceOS}, Actor{Kind: Capture, Name: SourceOS}},
	} {
		if _, _, err := s.Add(bg, "default", c.in, c.actor); err != nil {
			t.Errorf("%s while the agents are at their limit: %v", c.name, err)
		}
	}
	// Archiving an agent's task does not give the agent its quota back: the limit counts creations.
	if _, err := db.Exec(`UPDATE tasks SET status = 'archived' WHERE source_kind = 'agent'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "a"}, bot("b")); !errors.Is(err, ErrLimit) {
		t.Errorf("after archiving the agent's tasks: %v, want ErrLimit", err)
	}
}

// 1999 open cards leave room for one, 2000 for none, whatever the columns the cards sit in;
// archived cards never count.
func TestOpenTaskLimitIsExactAndCountsEveryColumn(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	columns := []string{"inbox", "ready", "in_progress", "review", "done"}
	for i := 0; i < 1999; i++ {
		if _, err := tx.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES ('default', 'seed', ?, ?, ?, ?)`, columns[i%5], i, rowTime, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 300; i++ {
		if _, err := tx.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES ('default', 'old', 'archived', ?, ?, ?)`, i, rowTime, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "the 2000th"}, human); err != nil {
		t.Fatalf("with 1999 open cards: %v", err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "the 2001st"}, human); !errors.Is(err, ErrLimit) {
		t.Fatalf("with 2000 open cards: %v, want ErrLimit", err)
	}
	if _, _, err := s.Add(bg, other, AddInput{Title: "elsewhere"}, human); err != nil {
		t.Errorf("another profile: %v", err)
	}
}
