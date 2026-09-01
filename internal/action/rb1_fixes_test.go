package action

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// fakeDB records StorageInterface calls for assertions.
type fakeDB struct {
	states       []string
	savedRows    int
	reachedIndex int
}

func (d *fakeDB) UpdateActionState(id, state string) error {
	d.states = append(d.states, state)
	return nil
}

func (d *fakeDB) UpdateActionReachedIndex(id string, index int) error {
	d.reachedIndex = index
	return nil
}

func (d *fakeDB) SaveExtractedData(actionID string, items []map[string]interface{}) error {
	d.savedRows += len(items)
	return nil
}

func (d *fakeDB) GetDailyActionCount(actionType string) (int, error) {
	return 0, nil
}

func (d *fakeDB) IncrementDailyActionCount(actionType string) (int, error) {
	return 1, nil
}

func (d *fakeDB) lastState() string {
	if len(d.states) == 0 {
		return ""
	}
	return d.states[len(d.states)-1]
}

// fakePage satisfies browser.PageInterface by embedding it; only the methods
// overridden per-test are functional, any other call panics via the nil
// embedded interface.
type fakePage struct {
	browser.PageInterface
	elementFn  func(selector string) (browser.ElementHandle, error)
	elementXFn func(xpath string) (browser.ElementHandle, error)
}

func (f *fakePage) Element(selector string, timeout time.Duration) (browser.ElementHandle, error) {
	if f.elementFn != nil {
		return f.elementFn(selector)
	}
	return nil, nil
}

func (f *fakePage) ElementX(xpath string, timeout time.Duration) (browser.ElementHandle, error) {
	if f.elementXFn != nil {
		return f.elementXFn(xpath)
	}
	return nil, nil
}

// stubBotAdapter exposes a single "hang" method backed by fn.
type stubBotAdapter struct {
	fn func(ctx context.Context, args ...interface{}) (interface{}, error)
}

func (s *stubBotAdapter) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	if name == "hang" {
		return s.fn, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Fix 1: evalSimpleCondition resolves variable RHS
// ---------------------------------------------------------------------------

// TestEvalSimpleConditionVariableRHS verifies the right-hand side of a simple
// condition is resolved as a variable when it is not a quoted/numeric/
// boolean literal. Previously "resultsCount < maxResultsCount" compared
// against toFloat("maxResultsCount") == 0, making pagination conditions
// constantly false.
func TestEvalSimpleConditionVariableRHS(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.SetVariable("resultsCount", 3)
	ae.SetVariable("maxResultsCount", 10)

	if !ae.evalSimpleCondition("resultsCount < maxResultsCount") {
		t.Fatal("expected 3 < maxResultsCount(10) to be true")
	}

	ae.SetVariable("resultsCount", 10)
	if ae.evalSimpleCondition("resultsCount < maxResultsCount") {
		t.Fatal("expected 10 < maxResultsCount(10) to be false")
	}
	ae.SetVariable("resultsCount", 11)
	if !ae.evalSimpleCondition("resultsCount > maxResultsCount") {
		t.Fatal("expected 11 > maxResultsCount(10) to be true")
	}
}

// TestEvalSimpleConditionLiteralRHS verifies literal right-hand sides still
// work after the resolution change: numeric, quoted, and boolean literals.
func TestEvalSimpleConditionLiteralRHS(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.SetVariable("resultsCount", 3)

	if !ae.evalSimpleCondition("resultsCount < 5") {
		t.Fatal("expected numeric literal RHS 3 < 5 to be true")
	}
	if ae.evalSimpleCondition("resultsCount > 5") {
		t.Fatal("expected numeric literal RHS 3 > 5 to be false")
	}

	ae.SetVariable("sourceType", "FOLLOWERS_FETCH")
	if !ae.evalSimpleCondition("sourceType == 'FOLLOWERS_FETCH'") {
		t.Fatal("expected quoted literal RHS equality to be true")
	}
	if ae.evalSimpleCondition("sourceType != 'FOLLOWERS_FETCH'") {
		t.Fatal("expected quoted literal RHS inequality to be false")
	}

	// Boolean literal RHS compared against a step result field.
	ae.execCtx.SetStepResult("find_message_button", &StepResult{Success: false})
	if !ae.evalSimpleCondition("find_message_button.success == false") {
		t.Fatal("expected boolean literal RHS equality to be true")
	}

	// Unknown variable RHS falls back to raw text (numeric 0) — preserved
	// pre-existing behavior.
	ae.SetVariable("resultsCount", 0)
	if ae.evalSimpleCondition("resultsCount < missingVar") {
		t.Fatal("expected unknown RHS variable to compare against 0, making 0 < 0 false")
	}
}

// ---------------------------------------------------------------------------
// Fix 2: stepSaveData must not double-append extracted items
// ---------------------------------------------------------------------------

// TestStepSaveDataFlushAllNoDuplicate verifies the flush-all path: extracting
// 3 items then running save_data persists 3 rows and leaves the extracted
// list at 3 entries (previously the copy was added back, yielding 6).
func TestStepSaveDataFlushAllNoDuplicate(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "save-test"}
	db := &fakeDB{}
	ae.db = db

	for i := 0; i < 3; i++ {
		ae.execCtx.AddExtractedItem(map[string]interface{}{"n": i})
	}

	result, err := ae.stepSaveData(context.Background(), StepDef{ID: "save", Type: "save_data"})
	if err != nil || !result.Success {
		t.Fatalf("save_data failed: err=%v result=%+v", err, result)
	}

	if db.savedRows != 3 {
		t.Fatalf("expected 3 persisted rows, got %d", db.savedRows)
	}
	if got := len(ae.execCtx.ExtractedItems); got != 3 {
		t.Fatalf("expected extracted list to stay at 3 items, got %d", got)
	}
}

// TestStepSaveDataDataSourceNoDuplicate verifies the dataSource path: items
// already tracked by their producing step (extract_multiple /
// call_bot_method) are not re-added, while untracked items are added once.
func TestStepSaveDataDataSourceNoDuplicate(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "save-test-2"}
	db := &fakeDB{}
	ae.db = db

	// Simulate extract_multiple: sets the variable AND tracks the items.
	tracked := []map[string]interface{}{
		{"text": "a"},
		{"text": "b"},
	}
	ae.SetVariable("extracted", tracked)
	for _, item := range tracked {
		ae.execCtx.AddExtractedItem(item)
	}

	if _, err := ae.stepSaveData(context.Background(), StepDef{ID: "save", Type: "save_data", DataSource: "extracted"}); err != nil {
		t.Fatalf("save_data failed: %v", err)
	}
	if got := len(ae.execCtx.ExtractedItems); got != 2 {
		t.Fatalf("expected extracted list to stay at 2 items, got %d", got)
	}
	if db.savedRows != 2 {
		t.Fatalf("expected 2 persisted rows, got %d", db.savedRows)
	}

	// A dataSource holding an untracked item: added exactly once.
	fresh := []map[string]interface{}{{"text": "c"}}
	ae.SetVariable("fresh", fresh)
	if _, err := ae.stepSaveData(context.Background(), StepDef{ID: "save2", Type: "save_data", DataSource: "fresh"}); err != nil {
		t.Fatalf("save_data (fresh) failed: %v", err)
	}
	if got := len(ae.execCtx.ExtractedItems); got != 3 {
		t.Fatalf("expected extracted list to grow to 3 items, got %d", got)
	}
	if db.savedRows != 3 {
		t.Fatalf("expected 3 total persisted rows, got %d", db.savedRows)
	}
}

// ---------------------------------------------------------------------------
// Fix 3: cancelled / timed-out runs persist FAILED, not COMPLETED
// ---------------------------------------------------------------------------

// TestExecuteMarksFailedOnContextCancel runs Execute with a pre-cancelled
// context, once with a step outside loops and once with the step inside a
// loop. Both must persist FAILED and return the context error, never
// COMPLETED.
func TestExecuteMarksFailedOnContextCancel(t *testing.T) {
	waitStep := StepDef{ID: "w", Type: "wait", Duration: 60}

	cases := []struct {
		name string
		def  *ActionDef
	}{
		{
			name: "initial steps",
			def:  &ActionDef{Steps: []StepDef{waitStep}},
		},
		{
			name: "loop",
			def: &ActionDef{
				Steps: []StepDef{waitStep},
				Loops: []LoopDef{{ID: "L", Iterator: "items", IndexVar: "i", Steps: []string{"w"}}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader := GetLoader()
			loader.cache.Store("rb1test/cancelme", tc.def)
			defer loader.Invalidate("rb1test", "cancelme")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			db := &fakeDB{}
			ae := NewActionExecutor(ctx, nil, db, nil, nil, nil, zerolog.Nop())
			action := &StorageAction{
				ID:             "cancel-test",
				TargetPlatform: "RB1Test",
				Type:           "CancelMe",
				Params:         map[string]interface{}{"items": []interface{}{1, 2}},
			}

			_, err := ae.Execute(action)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled, got %v", err)
			}
			if db.lastState() != "FAILED" {
				t.Fatalf("expected final state FAILED, got %q (states: %v)", db.lastState(), db.states)
			}
			for _, s := range db.states {
				if s == "COMPLETED" {
					t.Fatalf("cancelled run must never be persisted COMPLETED, states: %v", db.states)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fix 4: call_bot_method honors step.Timeout
// ---------------------------------------------------------------------------

// TestStepCallBotMethodAppliesTimeout verifies the bot method call is bounded
// by the step's timeout: a method that blocks until ctx.Done returns a
// deadline error roughly at the configured timeout instead of hanging.
func TestStepCallBotMethodAppliesTimeout(t *testing.T) {
	adapter := &stubBotAdapter{
		fn: func(ctx context.Context, args ...interface{}) (interface{}, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, adapter, zerolog.Nop())

	start := time.Now()
	result, err := ae.stepCallBotMethod(context.Background(), StepDef{
		ID:         "call",
		Type:       "call_bot_method",
		MethodName: "hang",
		Timeout:    0.2, // 200ms
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Success {
		t.Fatal("expected step to fail with deadline error")
	}
	if !errors.Is(result.Error, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded in step error, got %v", result.Error)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("method was not bounded by the step timeout, took %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Fix 5: resolveElement threads the lookup cause into step errors
// ---------------------------------------------------------------------------

// TestResolveElementErrorIncludesCause verifies that when element resolution
// fails, the step error message contains the underlying find error.
func TestResolveElementErrorIncludesCause(t *testing.T) {
	cause := errors.New("selector timed out after 10s")
	page := &fakePage{elementFn: func(string) (browser.ElementHandle, error) {
		return nil, cause
	}}
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())

	result, err := ae.stepClick(context.Background(), StepDef{ID: "c1", Type: "click", Selector: "#btn"})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Success {
		t.Fatal("expected click to fail")
	}
	if !strings.Contains(result.Error.Error(), "no element to click") {
		t.Fatalf("expected 'no element to click' in error, got: %v", result.Error)
	}
	if !strings.Contains(result.Error.Error(), "selector timed out after 10s") {
		t.Fatalf("expected resolution cause in error, got: %v", result.Error)
	}
	if !errors.Is(result.Error, cause) {
		t.Fatalf("expected cause to be wrapped (errors.Is), got: %v", result.Error)
	}
}

// TestFindElementKeepsFirstSelectorError verifies find_element reports the
// first meaningful selector error rather than the last one overwriting it.
func TestFindElementKeepsFirstSelectorError(t *testing.T) {
	calls := 0
	page := &fakePage{elementXFn: func(string) (browser.ElementHandle, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("first-boom")
		}
		return nil, errors.New("second-boom")
	}}
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())

	result, err := ae.stepFindElement(context.Background(), StepDef{
		ID:           "f1",
		Type:         "find_element",
		XPath:        "//first",
		Alternatives: []string{"//second"},
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Success {
		t.Fatal("expected find_element to fail")
	}
	if !strings.Contains(result.Error.Error(), "first-boom") {
		t.Fatalf("expected first selector error to be kept, got: %v", result.Error)
	}
	if strings.Contains(result.Error.Error(), "second-boom") {
		t.Fatalf("expected later errors not to replace the first, got: %v", result.Error)
	}
}

// ---------------------------------------------------------------------------
// Fix 6: condition recursion cap resets across outer-loop iterations
// ---------------------------------------------------------------------------

// TestConditionRecursionResetsAcrossLoopIterations builds a self-referencing
// condition (then: [counter, check]) inside a 2-iteration loop. Each
// iteration must get its own recursion budget (100 branch executions), so
// the counter reaches 200; with a single shared counter it would stall at
// 100 after the first iteration.
func TestConditionRecursionResetsAcrossLoopIterations(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "recursion-test"}
	ae.actionDef = &ActionDef{
		Steps: []StepDef{
			{ID: "counter", Type: "update_progress", Increment: "markerCount"},
			{ID: "check", Type: "condition", Condition: "go", Then: []string{"counter", "check"}},
		},
		Loops: []LoopDef{
			{ID: "L", Iterator: "items", IndexVar: "li", Steps: []string{"check"}},
		},
	}
	ae.SetVariable("go", true)
	ae.SetVariable("items", []interface{}{"a", "b"})

	if err := ae.executeLoop(context.Background(), ae.actionDef.Loops[0], ae.actionDef.Steps); err != nil {
		t.Fatalf("executeLoop failed: %v", err)
	}

	count, _ := ae.execCtx.GetVariable("markerCount")
	if got := count.(int); got != 200 {
		t.Fatalf("expected 100 branch executions per iteration (200 total), got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Fix 7: reached index tracked per loop ID
// ---------------------------------------------------------------------------

// TestExecuteLoopReachedIndexPerLoop verifies per-loop reached-index tracking:
// re-running the same loop resumes from its own progress instead of
// restarting, while a different loop's first run still seeds from the
// action-level reached index (legacy resume behavior).
func TestExecuteLoopReachedIndexPerLoop(t *testing.T) {
	ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "loop-test", ReachedIndex: 0}
	steps := []StepDef{{ID: "count", Type: "update_progress", Increment: "processed"}}
	ae.SetVariable("items", []interface{}{"a", "b", "c", "d"})
	loopA := LoopDef{ID: "A", Iterator: "items", IndexVar: "i", Steps: []string{"count"}}

	// First run: all 4 items processed.
	if err := ae.executeLoop(context.Background(), loopA, steps); err != nil {
		t.Fatalf("executeLoop A (1st) failed: %v", err)
	}
	if v, _ := ae.execCtx.GetVariable("processed"); v.(int) != 4 {
		t.Fatalf("expected 4 processed on first run, got %v", v)
	}

	// Re-run of the same loop: resumes from its own reached index (4), no
	// reprocessing. Previously it restarted from action.ReachedIndex.
	if err := ae.executeLoop(context.Background(), loopA, steps); err != nil {
		t.Fatalf("executeLoop A (2nd) failed: %v", err)
	}
	if v, _ := ae.execCtx.GetVariable("processed"); v.(int) != 4 {
		t.Fatalf("expected no reprocessing on re-run (still 4), got %v", v)
	}

	// A different loop's first run seeds from the action-level reached index.
	loopB := LoopDef{ID: "B", Iterator: "items", IndexVar: "j", Steps: []string{"count"}}
	ae2 := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
	ae2.action = &StorageAction{ID: "loop-test-2", ReachedIndex: 2}
	ae2.SetVariable("items", []interface{}{"a", "b", "c", "d"})
	if err := ae2.executeLoop(context.Background(), loopB, steps); err != nil {
		t.Fatalf("executeLoop B failed: %v", err)
	}
	if v, _ := ae2.execCtx.GetVariable("processed"); v.(int) != 2 {
		t.Fatalf("expected loop B to resume at action reached index 2 (2 items), got %v", v)
	}
}
