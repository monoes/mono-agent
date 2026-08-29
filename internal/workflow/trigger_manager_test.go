package workflow

import (
	"testing"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// fakeScheduler captures the specs passed to AddWorkflowJob (and the entries
// removed via RemoveJob) without registering real cron jobs.
type fakeScheduler struct {
	specs   []string
	removed []cron.EntryID
}

func (f *fakeScheduler) AddWorkflowJob(spec string, fn func()) (cron.EntryID, error) {
	f.specs = append(f.specs, spec)
	return cron.EntryID(len(f.specs)), nil
}

func (f *fakeScheduler) RemoveJob(id cron.EntryID) {
	f.removed = append(f.removed, id)
}

// TestActivateScheduleAppliesTimezone is a regression test: the "timezone" config
// field on trigger.schedule nodes was previously read nowhere, so a schedule
// configured for a specific IANA timezone silently ran in the server's local
// time zone instead.
func TestActivateScheduleAppliesTimezone(t *testing.T) {
	sched := &fakeScheduler{}
	tm := NewTriggerManager(nil, nil, sched, func(string, string, []Item) {}, zerolog.Nop())

	node := &WorkflowNode{
		ID: "n1",
		Config: map[string]interface{}{
			"cron":     "0 0 9 * * *",
			"timezone": "America/New_York",
		},
	}

	if err := tm.activateSchedule("wf1", node); err != nil {
		t.Fatalf("activateSchedule: %v", err)
	}

	want := "CRON_TZ=America/New_York 0 0 9 * * *"
	if got := sched.specs[len(sched.specs)-1]; got != want {
		t.Errorf("spec = %q, want %q", got, want)
	}
}

// TestActivateScheduleAlwaysCarriesTimezone verifies every schedule spec carries
// an explicit CRON_TZ prefix — including the default/UTC case. The underlying
// cron instance evaluates unprefixed specs in time.Local, so omitting CRON_TZ
// for UTC made default-UTC schedules fire at local time on non-UTC hosts.
func TestActivateScheduleAlwaysCarriesTimezone(t *testing.T) {
	cases := []struct {
		name     string
		timezone interface{}
		want     string
	}{
		{"no timezone field (default)", nil, "CRON_TZ=UTC 0 0 9 * * *"},
		{"empty timezone", "", "CRON_TZ=UTC 0 0 9 * * *"},
		{"explicit UTC", "UTC", "CRON_TZ=UTC 0 0 9 * * *"},
	}
	for _, tc := range cases {
		sched := &fakeScheduler{}
		tm := NewTriggerManager(nil, nil, sched, func(string, string, []Item) {}, zerolog.Nop())

		cfg := map[string]interface{}{"cron": "0 0 9 * * *"}
		if tc.timezone != nil {
			cfg["timezone"] = tc.timezone
		}
		node := &WorkflowNode{ID: "n2", Config: cfg}

		if err := tm.activateSchedule("wf1", node); err != nil {
			t.Fatalf("%s: activateSchedule: %v", tc.name, err)
		}
		if got := sched.specs[len(sched.specs)-1]; got != tc.want {
			t.Errorf("%s: spec = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestDeactivateWorkflowExactKeyOwnership is a regression test for the prefix
// collision: keys were workflowID+"_"+nodeID and Deactivation prefix-matched,
// so deactivating workflow "a" also tore down workflow "a_b"'s triggers.
func TestDeactivateWorkflowExactKeyOwnership(t *testing.T) {
	sched := &fakeScheduler{}
	tm := NewTriggerManager(nil, nil, sched, func(string, string, []Item) {}, zerolog.Nop())

	nodeCfg := func() map[string]interface{} {
		return map[string]interface{}{"cron": "0 0 9 * * *", "timezone": "UTC"}
	}
	// Workflow "a_b" registered first so its key sorts before "a"'s.
	if err := tm.activateSchedule("a_b", &WorkflowNode{ID: "n1", Config: nodeCfg()}); err != nil {
		t.Fatalf("activateSchedule(a_b): %v", err)
	}
	if err := tm.activateSchedule("a", &WorkflowNode{ID: "n1", Config: nodeCfg()}); err != nil {
		t.Fatalf("activateSchedule(a): %v", err)
	}
	if len(sched.specs) != 2 {
		t.Fatalf("registered %d triggers, want 2", len(sched.specs))
	}

	tm.DeactivateWorkflow("a")

	if len(sched.removed) != 1 {
		t.Fatalf("DeactivateWorkflow(a) removed %d entries, want exactly 1", len(sched.removed))
	}

	// "a_b"'s trigger must still be active: re-activating it is a no-op
	// (idempotent skip), while re-activating "a"'s registers a new entry.
	before := len(sched.specs)
	if err := tm.activateSchedule("a_b", &WorkflowNode{ID: "n1", Config: nodeCfg()}); err != nil {
		t.Fatalf("re-activateSchedule(a_b): %v", err)
	}
	if len(sched.specs) != before {
		t.Errorf("a_b trigger was torn down by DeactivateWorkflow(a); re-activation added a new entry")
	}
	if err := tm.activateSchedule("a", &WorkflowNode{ID: "n1", Config: nodeCfg()}); err != nil {
		t.Fatalf("re-activateSchedule(a): %v", err)
	}
	if len(sched.specs) != before+1 {
		t.Errorf("re-activating a after deactivation should register a new entry")
	}
}
