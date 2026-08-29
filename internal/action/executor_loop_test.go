package action

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// capTestStorage is a minimal StorageInterface capturing writes for assertions.
type capTestStorage struct {
	stateUpdates   []string
	reachedIndexes []int
}

func (s *capTestStorage) UpdateActionState(id, state string) error {
	s.stateUpdates = append(s.stateUpdates, state)
	return nil
}

func (s *capTestStorage) UpdateActionReachedIndex(id string, index int) error {
	s.reachedIndexes = append(s.reachedIndexes, index)
	return nil
}

func (s *capTestStorage) SaveExtractedData(actionID string, items []map[string]interface{}) error {
	return nil
}

// newLoopTestExecutor builds an executor wired for loop-only tests: log steps
// only, no page, and an events channel the test drains.
func newLoopTestExecutor(t *testing.T, reachedIndex int) (*ActionExecutor, *capTestStorage, chan ExecutionEvent) {
	t.Helper()
	events := make(chan ExecutionEvent, 100)
	db := &capTestStorage{}
	ae := NewActionExecutor(context.Background(), nil, db, nil, events, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "test-action", ReachedIndex: reachedIndex}
	return ae, db, events
}

func loopTestItems(n int) []interface{} {
	items := make([]interface{}, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]interface{}{"url": "https://example.com/item"}
	}
	return items
}

func loopTestSteps() []StepDef {
	return []StepDef{{ID: "log_step", Type: "log", Value: "item {{item.url}}"}}
}

// drainEvents collects every pending event from the channel.
func drainEvents(t *testing.T, events chan ExecutionEvent) []ExecutionEvent {
	t.Helper()
	var collected []ExecutionEvent
	for {
		select {
		case evt := <-events:
			collected = append(collected, evt)
		default:
			return collected
		}
	}
}

// TestExecuteLoopCapEnforced verifies that a loop declaring maxItems stops
// after the capped number of items: maxItems 2 on a 5-item list processes
// exactly 2, records a cap note, persists the cap boundary, and emits a
// loop_cap_reached event.
func TestExecuteLoopCapEnforced(t *testing.T) {
	ae, db, events := newLoopTestExecutor(t, 0)
	ae.SetVariable("selectedListItems", loopTestItems(5))

	loop := LoopDef{
		ID:       "process_items",
		Iterator: "selectedListItems",
		IndexVar: "reachedIndex",
		Steps:    []string{"log_step"},
		MaxItems: "{{cap or 2}}",
	}
	if err := ae.executeLoop(context.Background(), loop, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}

	var indexes []int
	sawCapEvent := false
	for _, evt := range drainEvents(t, events) {
		if evt.Type == "loop_iteration" {
			indexes = append(indexes, evt.Index)
		}
		if evt.Type == "loop_cap_reached" {
			sawCapEvent = true
		}
	}
	if len(indexes) != 2 {
		t.Fatalf("expected cap of 2 iterations on a 5-item list, got %d (indexes %v)", len(indexes), indexes)
	}
	if !sawCapEvent {
		t.Fatal("expected a loop_cap_reached event to be emitted")
	}

	if len(db.reachedIndexes) != 1 || db.reachedIndexes[0] != 2 {
		t.Fatalf("expected reached index persisted once at cap boundary 2, got %v", db.reachedIndexes)
	}

	note, ok := ae.execCtx.GetData("loopCapReached")
	if !ok {
		t.Fatal("expected loopCapReached note stored in execution context data")
	}
	capNote, ok := note.(map[string]interface{})
	if !ok || capNote["loopId"] != "process_items" || capNote["cap"] != 2 {
		t.Fatalf("unexpected cap note: %#v", note)
	}
}

// countLoopIterations drains the events channel and returns the indexes of
// all loop_iteration events that were emitted.
func countLoopIterations(t *testing.T, events chan ExecutionEvent) []int {
	t.Helper()
	var indexes []int
	for _, evt := range drainEvents(t, events) {
		if evt.Type == "loop_iteration" {
			indexes = append(indexes, evt.Index)
		}
	}
	return indexes
}

// TestExecuteLoopCapFromParamString verifies CLI-style string params
// ("--param cap=2" arrives as string "2") are coerced to a working cap.
func TestExecuteLoopCapFromParamString(t *testing.T) {
	ae, db, events := newLoopTestExecutor(t, 0)
	ae.SetVariable("selectedListItems", loopTestItems(5))
	ae.SetVariable("cap", "2")

	loop := LoopDef{
		ID:       "process_items",
		Iterator: "selectedListItems",
		IndexVar: "reachedIndex",
		Steps:    []string{"log_step"},
		MaxItems: "{{cap or 99}}",
	}
	if err := ae.executeLoop(context.Background(), loop, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}

	if got := len(countLoopIterations(t, events)); got != 2 {
		t.Fatalf("expected 2 iterations with string param cap, got %d", got)
	}
	if len(db.reachedIndexes) != 1 || db.reachedIndexes[0] != 2 {
		t.Fatalf("expected reached index persisted at 2, got %v", db.reachedIndexes)
	}
}

// TestExecuteLoopUnlimitedWithoutMaxItems verifies a loop without maxItems
// (or with an unresolvable value of 0) processes the whole collection.
func TestExecuteLoopUnlimitedWithoutMaxItems(t *testing.T) {
	ae, _, events := newLoopTestExecutor(t, 0)
	ae.SetVariable("selectedListItems", loopTestItems(5))

	noCap := LoopDef{ID: "l", Iterator: "selectedListItems", IndexVar: "reachedIndex", Steps: []string{"log_step"}}
	if err := ae.executeLoop(context.Background(), noCap, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}
	if got := len(countLoopIterations(t, events)); got != 5 {
		t.Fatalf("expected 5 iterations without cap, got %d", got)
	}

	// Explicit zero must also mean unlimited, not "process nothing".
	zeroCap := noCap
	zeroCap.MaxItems = "{{maxContentCount or 0}}"
	ae2, _, events2 := newLoopTestExecutor(t, 0)
	ae2.SetVariable("selectedListItems", loopTestItems(3))
	if err := ae2.executeLoop(context.Background(), zeroCap, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}
	if got := len(countLoopIterations(t, events2)); got != 3 {
		t.Fatalf("expected 3 iterations with zero cap, got %d", got)
	}
}

// TestExecuteLoopResumesFromReachedIndex verifies a resumed loop starts at
// the persisted reached index instead of restarting from 0.
func TestExecuteLoopResumesFromReachedIndex(t *testing.T) {
	ae, db, events := newLoopTestExecutor(t, 3)
	ae.SetVariable("selectedListItems", loopTestItems(5))

	loop := LoopDef{ID: "l", Iterator: "selectedListItems", IndexVar: "reachedIndex", Steps: []string{"log_step"}}
	if err := ae.executeLoop(context.Background(), loop, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}

	indexes := countLoopIterations(t, events)
	if len(indexes) != 2 || indexes[0] != 3 || indexes[1] != 4 {
		t.Fatalf("expected resume at index 3 processing items 3,4 — got %v", indexes)
	}
	// Last item persists idx+1 = 5.
	if len(db.reachedIndexes) != 1 || db.reachedIndexes[0] != 5 {
		t.Fatalf("expected final reached index 5, got %v", db.reachedIndexes)
	}
}

// TestExecuteLoopCapCountsSessionItems verifies the cap bounds items
// processed in this session, so a resumed run still gets its full allowance.
func TestExecuteLoopCapCountsSessionItems(t *testing.T) {
	ae, _, events := newLoopTestExecutor(t, 3) // resume: 2 items remain
	ae.SetVariable("selectedListItems", loopTestItems(5))

	loop := LoopDef{
		ID:       "l",
		Iterator: "selectedListItems",
		IndexVar: "reachedIndex",
		Steps:    []string{"log_step"},
		MaxItems: "{{maxItems or 5}}",
	}
	if err := ae.executeLoop(context.Background(), loop, loopTestSteps()); err != nil {
		t.Fatalf("executeLoop returned error: %v", err)
	}
	// Session cap 5 with only 2 remaining → both run.
	if got := len(countLoopIterations(t, events)); got != 2 {
		t.Fatalf("expected 2 remaining iterations under a session cap of 5, got %d", got)
	}
}

// TestExecuteMissingRequiredInputFails verifies that Execute() rejects an
// action before running any step (and before marking it RUNNING) when a
// declared required input is absent.
func TestExecuteMissingRequiredInputFails(t *testing.T) {
	db := &capTestStorage{}
	events := make(chan ExecutionEvent, 100)
	ae := NewActionExecutor(context.Background(), nil, db, nil, events, nil, zerolog.Nop())

	act := &StorageAction{
		ID:             "req-1",
		Type:           "send_dms",
		TargetPlatform: "instagram",
		// No ContentMessage (→ messageText missing) and no selectedListItems.
	}
	_, err := ae.Execute(act)
	if err == nil {
		t.Fatal("expected error for missing required inputs, got nil")
	}
	if !strings.Contains(err.Error(), "missing required input") {
		t.Fatalf("expected 'missing required input' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "messageText") {
		t.Fatalf("expected error to name 'messageText', got: %v", err)
	}
	if len(db.stateUpdates) != 0 {
		t.Fatalf("expected no state updates before validation passes, got %v", db.stateUpdates)
	}
	if got := len(countLoopIterations(t, events)); got != 0 {
		t.Fatalf("expected no loop iterations, got %d", got)
	}
}

// TestValidateRequiredInputsPassesWhenPresent verifies validation succeeds
// once every declared required input is seeded — including auto_reply_dms's
// date/interval inputs, which come from the storage action's own fields.
func TestValidateRequiredInputsPassesWhenPresent(t *testing.T) {
	ae, _, _ := newLoopTestExecutor(t, 0)

	sendDms, err := GetLoader().Load("instagram", "send_dms")
	if err != nil {
		t.Fatalf("loading send_dms def: %v", err)
	}
	act := &StorageAction{ContentMessage: "hello there"}
	act.Params = map[string]interface{}{}
	ae.seedVariables(act)
	ae.SetVariable("selectedListItems", loopTestItems(2))
	if err := ae.validateRequiredInputs(sendDms); err != nil {
		t.Fatalf("expected validation to pass with all inputs present, got: %v", err)
	}

	autoReply, err := GetLoader().Load("instagram", "auto_reply_dms")
	if err != nil {
		t.Fatalf("loading auto_reply_dms def: %v", err)
	}
	ae2, _, _ := newLoopTestExecutor(t, 0)
	act2 := &StorageAction{
		ContentMessage:    "hi",
		StartDate:         "2026-01-01",
		EndDate:           "2026-12-31",
		ExecutionInterval: 30,
	}
	ae2.seedVariables(act2)
	if err := ae2.validateRequiredInputs(autoReply); err != nil {
		t.Fatalf("expected auto_reply_dms validation to pass with seeded dates, got: %v", err)
	}
}

// TestAllEmbeddedActionJSONsLoad verifies every shipped action JSON parses
// and that every loop references only step IDs that actually exist — the
// safety net for JSON edits (cap wiring, added wait steps).
func TestAllEmbeddedActionJSONsLoad(t *testing.T) {
	loader := GetLoader()
	available, err := loader.ListAvailable()
	if err != nil {
		t.Fatalf("listing actions: %v", err)
	}
	if len(available) == 0 {
		t.Fatal("no embedded action definitions found")
	}
	for _, key := range available {
		parts := strings.SplitN(key, "/", 2)
		def, err := loader.Load(parts[0], parts[1])
		if err != nil {
			t.Errorf("%s: failed to load: %v", key, err)
			continue
		}
		stepIDs := make(map[string]bool, len(def.Steps))
		for _, s := range def.Steps {
			stepIDs[s.ID] = true
		}
		for _, loop := range def.Loops {
			if loop.MaxItems != "" && strings.TrimSpace(loop.MaxItems) == "" {
				t.Errorf("%s: loop %s has blank maxItems", key, loop.ID)
			}
			for _, sid := range loop.Steps {
				if !stepIDs[sid] {
					t.Errorf("%s: loop %s references unknown step %q", key, loop.ID, sid)
				}
			}
		}
	}
}
