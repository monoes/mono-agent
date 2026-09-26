package workflow

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/scheduler"
)

type nextRunScheduler struct {
	fakeScheduler
	next map[cron.EntryID]time.Time
}

func (s *nextRunScheduler) NextRun(id cron.EntryID) time.Time { return s.next[id] }

// ScheduledRuns reports the scheduler's own next fire time per schedule
// node, soonest first, and skips entries the scheduler has no time for.
func TestScheduledRunsReportsSchedulerTimes(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	sched := &nextRunScheduler{next: map[cron.EntryID]time.Time{1: base.Add(2 * time.Hour), 2: base.Add(time.Minute)}}
	tm := NewTriggerManager(nil, nil, sched, func(string, string, []Item) {}, zerolog.Nop())
	for _, n := range []struct{ wf, node string }{{"wf1", "a"}, {"wf2", "b"}, {"wf3", "c"}} {
		if err := tm.activateSchedule(n.wf, &WorkflowNode{ID: n.node, Config: map[string]interface{}{"cron": "0 0 * * * *"}}); err != nil {
			t.Fatal(err)
		}
	}
	runs := tm.ScheduledRuns()
	if len(runs) != 2 || runs[0].WorkflowID != "wf2" || runs[0].NodeID != "b" || !runs[0].Next.Equal(base.Add(time.Minute)) || runs[1].WorkflowID != "wf1" {
		t.Fatalf("runs = %+v", runs)
	}
}

// Without NextRun support there is nothing to publish.
func TestScheduledRunsWithoutNextRunner(t *testing.T) {
	tm := NewTriggerManager(nil, nil, &fakeScheduler{}, func(string, string, []Item) {}, zerolog.Nop())
	_ = tm.activateSchedule("wf1", &WorkflowNode{ID: "a", Config: map[string]interface{}{"cron": "0 0 * * * *"}})
	if runs := tm.ScheduledRuns(); len(runs) != 0 {
		t.Fatalf("runs = %+v", runs)
	}
}

// The real scheduler implements NextRunner.
func TestRealSchedulerReportsNextRun(t *testing.T) {
	s := scheduler.NewScheduler(zerolog.Nop())
	var _ NextRunner = s
	id, err := s.AddWorkflowJob("CRON_TZ=UTC 0 0 * * * *", func() {})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for s.NextRun(id).IsZero() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if next := s.NextRun(id); next.IsZero() || next.Minute() != 0 || next.Second() != 0 {
		t.Fatalf("NextRun = %v", next)
	}
}
