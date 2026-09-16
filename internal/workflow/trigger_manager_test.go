package workflow

import (
	"context"
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

type fakeTriggerSource struct {
	fire    func([]Item)
	stopped int
}

func (f *fakeTriggerSource) Activate(w *Workflow, n *WorkflowNode, fire func([]Item)) (func(), error) {
	f.fire = fire
	return func() { f.stopped++ }, nil
}

// Source-backed trigger types (trigger.org) activate once, fire through
// triggerFn, and stop on deactivation; DeactivateAll clears the registry so
// a re-activation registers again.
func TestTriggerManagerSources(t *testing.T) {
	var fired []string
	tm := NewTriggerManager(nil, nil, &fakeScheduler{}, func(wf, node string, items []Item) {
		fired = append(fired, wf+"/"+node)
	}, zerolog.Nop())
	src := &fakeTriggerSource{}
	tm.RegisterProvider("trigger.org", src)
	w := &Workflow{ID: "wf", Nodes: []WorkflowNode{{ID: "t", Type: "trigger.org", Config: map[string]interface{}{}}}}

	ctx := context.Background()
	if err := tm.ActivateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := tm.ActivateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	src.fire([]Item{{JSON: map[string]interface{}{"x": 1}}})
	if len(fired) != 1 || fired[0] != "wf/t" {
		t.Fatalf("fired = %v", fired)
	}
	tm.DeactivateWorkflow("wf")
	if src.stopped != 1 {
		t.Fatalf("stopped = %d", src.stopped)
	}
	if err := tm.ActivateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	tm.DeactivateAll()
	if src.stopped != 2 || len(tm.active) != 0 {
		t.Fatalf("DeactivateAll: stopped=%d active=%v", src.stopped, tm.active)
	}
	if err := tm.ActivateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	if len(tm.active["wf"]) != 1 {
		t.Fatal("re-activation after DeactivateAll was skipped")
	}
}
