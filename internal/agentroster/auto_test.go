package agentroster

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAuto is an AutoScheduler over a real settings table with a fake
// clock, idle check, picker, validator and lock. No model calls.
type fakeAuto struct {
	s       *AutoScheduler
	now     time.Time
	busy    atomic.Bool
	locked  bool
	picks   int
	locks   int
	runs    []AutoPlan
	mu      sync.Mutex
	nothing bool
}

func newFakeAuto(t *testing.T, db *sql.DB, start time.Time) *fakeAuto {
	f := &fakeAuto{now: start}
	f.s = &AutoScheduler{
		DB:   db,
		Now:  func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now },
		Busy: func(context.Context) (bool, string) { return f.busy.Load(), "chat turn" },
		Pick: func(_ context.Context, maxModels int) (*AutoPlan, error) {
			f.picks++
			if f.nothing {
				return nil, nil
			}
			p := &AutoPlan{Runtime: "codex", EstCostUSD: 0.002}
			for i := 0; i < maxModels; i++ {
				p.Targets = append(p.Targets, Target{Runtime: "codex", Model: "m" + string(rune('a'+i))})
			}
			return p, nil
		},
		Validate: func(_ context.Context, p AutoPlan) (AutoRunResult, error) {
			f.runs = append(f.runs, p)
			return AutoRunResult{Summary: Summary{Planned: len(p.Targets), OK: len(p.Targets)}, SpentUSD: 0.001}, nil
		},
		Lock:          func() (func(), bool) { f.locks++; return func() {}, !f.locked },
		WatchInterval: time.Millisecond,
	}
	return f
}

func (f *fakeAuto) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func enableAuto(t *testing.T, db *sql.DB, c AutoConfig) {
	t.Helper()
	c.Enabled = true
	if err := SaveAutoConfig(context.Background(), db, c); err != nil {
		t.Fatal(err)
	}
}

// localNoon keeps the tests clear of a local-day boundary.
func localNoon() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.Local)
}

func TestAutoConfigDefaultsOff(t *testing.T) {
	db := openDB(t)
	c, err := LoadAutoConfig(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || c.MaxRuntimesPerDay != 1 || c.MaxModelsPerRun != 3 || c.QuietPeriod != 15*time.Minute {
		t.Fatalf("default config = %+v, want off, 1/day, 3 models, 15m", c)
	}
	n := AutoConfig{MaxRuntimesPerDay: 100, MaxModelsPerRun: 100, QuietPeriod: time.Second}.Normalize()
	if n.MaxRuntimesPerDay != MaxAutoRuntimesPerDay || n.MaxModelsPerRun != MaxAutoModelsPerRun || n.QuietPeriod != MinAutoQuietPeriod {
		t.Fatalf("clamped = %+v", n)
	}
}

func TestAutoOffRunsNothing(t *testing.T) {
	db := openDB(t)
	f := newFakeAuto(t, db, localNoon())
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if got := f.s.Step(ctx); got != AutoCheckOff {
			t.Fatalf("step %d = %q, want off", i, got)
		}
		f.advance(time.Hour)
	}
	if len(f.runs) != 0 {
		t.Fatalf("ran %d times while off", len(f.runs))
	}
}

func TestAutoNeverOnStartupThenOneRuntimeWhenIdle(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{})
	f := newFakeAuto(t, db, localNoon())
	ctx := context.Background()

	if got := f.s.Step(ctx); got != AutoCheckQuiet {
		t.Fatalf("startup step = %q, want %q", got, AutoCheckQuiet)
	}
	f.advance(14 * time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckQuiet {
		t.Fatalf("before quiet period = %q", got)
	}
	f.advance(time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("after quiet period = %q, want ran", got)
	}
	if len(f.runs) != 1 || f.runs[0].Runtime != "codex" || len(f.runs[0].Targets) != DefaultAutoModelsPerRun {
		t.Fatalf("runs = %+v, want one codex run of %d models", f.runs, DefaultAutoModelsPerRun)
	}
	st, _ := LoadAutoState(ctx, db, f.now)
	if st.RuntimesToday != 1 || st.SpentTodayUSD != 0.001 || st.LastRuntime != "codex" || st.LastSummary == nil || st.LastSummary.OK != 3 {
		t.Fatalf("state = %+v", st)
	}
}

func TestAutoDailyCapHoldsAcrossRestart(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{MaxRuntimesPerDay: 2, QuietPeriod: 10 * time.Minute})
	start := localNoon()
	f := newFakeAuto(t, db, start)
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(10 * time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("first = %q", got)
	}
	// One runtime at a time: the next waits another quiet period.
	f.advance(5 * time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckQuiet {
		t.Fatalf("right after a run = %q", got)
	}
	f.advance(5 * time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("second = %q", got)
	}
	f.advance(time.Hour)
	if got := f.s.Step(ctx); got != AutoCheckCap {
		t.Fatalf("third = %q, want cap", got)
	}
	st, _ := LoadAutoState(ctx, db, f.now)
	if want := nextDay(f.now).Add(10 * time.Minute); !st.NextEligibleAt.Equal(want) {
		t.Fatalf("next eligible = %v, want %v", st.NextEligibleAt, want)
	}

	// A restarted daemon reads the persisted count: still capped today.
	g := newFakeAuto(t, db, f.now)
	g.advance(time.Hour)
	if got := g.s.Step(ctx); got != AutoCheckQuiet {
		t.Fatalf("restart step = %q, want quiet (never at startup)", got)
	}
	g.advance(20 * time.Minute)
	if got := g.s.Step(ctx); got != AutoCheckCap {
		t.Fatalf("restart = %q, want cap", got)
	}
	// Tomorrow the count starts over.
	g.advance(24 * time.Hour)
	if got := g.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("next day = %q, want ran", got)
	}
	if n := len(f.runs) + len(g.runs); n != 3 {
		t.Fatalf("runs = %d, want 3", n)
	}
}

func TestAutoBusyWaitsForQuietPeriod(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: 10 * time.Minute})
	f := newFakeAuto(t, db, localNoon())
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(9 * time.Minute)
	f.busy.Store(true)
	if got := f.s.Step(ctx); !strings.HasPrefix(got, AutoCheckBusy) {
		t.Fatalf("busy step = %q", got)
	}
	f.advance(time.Hour) // busy for an hour
	f.s.Step(ctx)
	f.busy.Store(false)
	f.advance(9 * time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckQuiet {
		t.Fatalf("9m after busy = %q, want quiet", got)
	}
	if len(f.runs) != 0 {
		t.Fatal("ran while busy or before the quiet period")
	}
	f.advance(time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("after quiet = %q", got)
	}
}

func TestAutoSkipsWhileAnotherValidationRuns(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	f := newFakeAuto(t, db, localNoon())
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(time.Minute)
	f.locked = true
	if got := f.s.Step(ctx); got != AutoCheckLocked {
		t.Fatalf("locked = %q", got)
	}
	if len(f.runs) != 0 {
		t.Fatal("ran while locked")
	}
	st, _ := LoadAutoState(ctx, db, f.now)
	if st.RuntimesToday != 0 {
		t.Fatalf("a skipped run counted against the cap: %+v", st)
	}
	f.locked = false
	f.nothing = true
	f.advance(time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckNothing {
		t.Fatalf("nothing stale = %q", got)
	}
}

// Shutdown cancels a run in progress and Run returns once it has stopped.
func TestAutoRunStopsOnShutdown(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	f := newFakeAuto(t, db, localNoon())
	// Each clock read moves an hour on, so the quiet period passes at the
	// first tick.
	f.s.Now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); f.now = f.now.Add(time.Hour); return f.now }
	f.s.Interval = 5 * time.Millisecond
	started := make(chan struct{})
	var sawCancel atomic.Bool
	f.s.Validate = func(ctx context.Context, p AutoPlan) (AutoRunResult, error) {
		close(started)
		<-ctx.Done()
		sawCancel.Store(true)
		return AutoRunResult{Summary: Summary{Planned: len(p.Targets), Cancelled: len(p.Targets)}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.s.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
	if !sawCancel.Load() {
		t.Fatal("the run in progress was not cancelled")
	}
	st, _ := LoadAutoState(context.Background(), db, f.now)
	if st.LastSummary == nil || st.LastSummary.Cancelled != DefaultAutoModelsPerRun {
		t.Fatalf("cancelled run not recorded: %+v", st)
	}
}

func TestPickStale(t *testing.T) {
	now := time.Now()
	old := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	e := func(model, state string, at time.Time) Entry {
		return Entry{Result: Result{Model: model, ValidatedAt: at, Status: StatusOK}, State: state}
	}
	roster := []RuntimeRoster{
		{Runtime: "claude", Installed: true, Version: "2.0", Models: []Entry{
			e("opus", StateStale, old(8)), e("sonnet", StateReady, old(1)),
		}},
		{Runtime: "codex", Installed: true, Version: "1.5", Models: []Entry{
			e("a", StateStale, old(9)), e("b", StateStale, old(20)), e("c", StateStale, old(10)),
			e("d", StateStale, old(11)), e("f", StateFailed, old(30)), e("u", StateUntested, time.Time{}),
		}},
		{Runtime: "gone", Installed: false, Models: []Entry{e("x", StateStale, old(40))}},
	}
	previous := []Result{
		{Runtime: "codex", Model: "b", HasCost: true, CostUSD: 0.001},
		{Runtime: "codex", Model: "d", HasCost: true, CostUSD: 0.002},
	}
	p := PickStale(roster, previous, 3)
	if p == nil || p.Runtime != "codex" {
		t.Fatalf("plan = %+v, want codex (oldest stale, installed)", p)
	}
	var models []string
	for _, tg := range p.Targets {
		models = append(models, tg.Model)
		if tg.RuntimeVersion != "1.5" || tg.Runtime != "codex" {
			t.Fatalf("target = %+v", tg)
		}
	}
	if strings.Join(models, ",") != "b,d,c" {
		t.Fatalf("models = %v, want the 3 oldest stale b,d,c", models)
	}
	if p.EstCostUSD != 0.003 || p.UnknownCost != 1 {
		t.Fatalf("estimate = %v + %d unknown", p.EstCostUSD, p.UnknownCost)
	}
	if PickStale([]RuntimeRoster{{Runtime: "claude", Installed: true, Models: []Entry{e("s", StateReady, now)}}}, nil, 3) != nil {
		t.Fatal("picked with nothing stale")
	}
}

// A state that can't be read stops the run: the cap must not fail open,
// and the stored state is not overwritten.
func TestAutoUnreadableStateFailsClosed(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	if err := setSetting(context.Background(), db, autoStateKey, "{broken"); err != nil {
		t.Fatal(err)
	}
	f := newFakeAuto(t, db, localNoon())
	ctx := context.Background()
	f.s.Step(ctx)
	for i := 0; i < 3; i++ {
		f.advance(time.Hour)
		if got := f.s.Step(ctx); got != AutoCheckFailed {
			t.Fatalf("step = %q, want failed", got)
		}
	}
	if len(f.runs) != 0 || f.picks != 0 {
		t.Fatalf("ran (%d) or planned (%d) without a readable state", len(f.runs), f.picks)
	}
	if raw, _, _ := getSetting(ctx, db, autoStateKey); raw != "{broken" {
		t.Fatalf("state overwritten: %q", raw)
	}
}

// A run in progress stops when the app gets busy or the setting is turned
// off; the run still counts, and the next one waits a quiet period.
func TestAutoRunCancelledMidRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause func(f *fakeAuto, db *sql.DB)
		want  string
	}{
		{"busy", func(f *fakeAuto, _ *sql.DB) { f.busy.Store(true) }, AutoCheckCancelled + ": the app got busy (chat turn)"},
		{"off", func(_ *fakeAuto, db *sql.DB) {
			_ = SaveAutoConfig(context.Background(), db, AutoConfig{Enabled: false, QuietPeriod: time.Minute})
		}, AutoCheckCancelled + ": turned off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
			f := newFakeAuto(t, db, localNoon())
			f.s.Validate = func(ctx context.Context, p AutoPlan) (AutoRunResult, error) {
				tc.cause(f, db) // between the first and second model
				<-ctx.Done()
				return AutoRunResult{Summary: Summary{Planned: len(p.Targets), OK: 1, Cancelled: len(p.Targets) - 1}}, nil
			}
			ctx := context.Background()
			f.s.Step(ctx)
			f.advance(time.Minute)
			if got := f.s.Step(ctx); got != tc.want {
				t.Fatalf("step = %q, want %q", got, tc.want)
			}
			st, _ := LoadAutoState(ctx, db, f.now)
			if st.RuntimesToday != 1 || st.LastSummary == nil || st.LastSummary.Cancelled != 2 || st.LastCheck != tc.want {
				t.Fatalf("state = %+v", st)
			}
		})
	}
}

// Nothing stale: no scan or lock every minute; planning waits an hour.
func TestAutoNothingStaleBacksOff(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	f := newFakeAuto(t, db, localNoon())
	f.nothing = true
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(time.Minute)
	for i := 0; i < 59; i++ {
		if got := f.s.Step(ctx); got != AutoCheckNothing {
			t.Fatalf("step %d = %q", i, got)
		}
		f.advance(time.Minute)
	}
	if f.picks != 1 || f.locks != 0 {
		t.Fatalf("picks = %d, locks = %d; want one plan and no lock within the hour", f.picks, f.locks)
	}
	f.advance(time.Minute)
	f.s.Step(ctx)
	if f.picks != 2 {
		t.Fatalf("picks after the backoff = %d, want 2", f.picks)
	}
}

func TestDailyCeiling(t *testing.T) {
	c := AutoConfig{MaxRuntimesPerDay: 2, MaxModelsPerRun: 3}
	if _, _, known := DailyCeiling(c, []Result{{HasCost: false}}); known {
		t.Fatal("known without any cost")
	}
	perDay, priciest, known := DailyCeiling(c, []Result{{HasCost: true, CostUSD: 0.01}, {HasCost: true, CostUSD: 0.05}})
	if !known || priciest != 0.05 || perDay < 0.2999 || perDay > 0.3001 {
		t.Fatalf("ceiling = %v/day, priciest %v, known %v", perDay, priciest, known)
	}
}

// Planning again under the lock: models a manual validation just checked
// are not tested twice, and an empty second plan runs nothing.
func TestAutoRepicksUnderLock(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	f := newFakeAuto(t, db, localNoon())
	repicks := 0
	f.s.Repick = func(context.Context, int) (*AutoPlan, error) {
		repicks++
		if repicks == 1 {
			return nil, nil // a manual validate re-checked everything meanwhile
		}
		return &AutoPlan{Runtime: "codex", Targets: []Target{{Runtime: "codex", Model: "mb"}}}, nil
	}
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckNothing {
		t.Fatalf("empty repick = %q, want nothing stale", got)
	}
	st, _ := LoadAutoState(ctx, db, f.now)
	if len(f.runs) != 0 || st.RuntimesToday != 0 {
		t.Fatalf("ran %d / counted %d after an empty repick", len(f.runs), st.RuntimesToday)
	}
	f.advance(time.Minute)
	if got := f.s.Step(ctx); got != AutoCheckRan {
		t.Fatalf("second = %q", got)
	}
	if len(f.runs) != 1 || len(f.runs[0].Targets) != 1 || f.runs[0].Targets[0].Model != "mb" {
		t.Fatalf("ran %+v, want the repicked plan", f.runs)
	}
}

// Calls cancelled mid-flight still count: their cost, or as unknown.
func TestAutoRunResultCountsCancelledCalls(t *testing.T) {
	var r AutoRunResult
	for _, l := range []Line{
		{Type: "validate.started"},
		{Type: "validate.result", Result: &Result{Status: StatusOK, HasCost: true, CostUSD: 0.01}},
		{Type: "validate.result", Result: &Result{Status: StatusCancelled, HasCost: true, CostUSD: 0.02}},
		{Type: "validate.result", Result: &Result{Status: StatusCancelled}},
		{Type: "validate.done", Summary: &Summary{}},
	} {
		r.Add(l)
	}
	if r.SpentUSD < 0.0299 || r.SpentUSD > 0.0301 || r.UnknownCost != 1 {
		t.Fatalf("spent %v, unknown %d; want 0.03 and 1", r.SpentUSD, r.UnknownCost)
	}
}

// Only a corrupt state is reset. A read error that may be transient leaves
// the stored count alone, so today's cap still holds.
func TestResetCorruptAutoStateOnlyWhenCorrupt(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	now := localNoon()
	if err := SaveAutoState(ctx, db, AutoState{Day: now.Format("2006-01-02"), RuntimesToday: 1}); err != nil {
		t.Fatal(err)
	}
	if reset, err := ResetCorruptAutoState(ctx, db, now); reset || err != nil {
		t.Fatalf("readable state: reset=%v err=%v", reset, err)
	}

	// Unreadable for another reason: the table is missing for a moment.
	if _, err := db.Exec(`ALTER TABLE settings RENAME TO settings_away`); err != nil {
		t.Fatal(err)
	}
	reset, err := ResetCorruptAutoState(ctx, db, now)
	if reset || err == nil || errors.Is(err, ErrAutoStateCorrupt) {
		t.Fatalf("transient error: reset=%v err=%v; want no reset and the error", reset, err)
	}
	if _, err := db.Exec(`ALTER TABLE settings_away RENAME TO settings`); err != nil {
		t.Fatal(err)
	}
	if st, err := LoadAutoState(ctx, db, now); err != nil || st.RuntimesToday != 1 {
		t.Fatalf("count lost after a transient error: %+v, %v", st, err)
	}

	if err := setSetting(ctx, db, autoStateKey, "{broken"); err != nil {
		t.Fatal(err)
	}
	if reset, err := ResetCorruptAutoState(ctx, db, now); !reset || err != nil {
		t.Fatalf("corrupt state: reset=%v err=%v; want reset", reset, err)
	}
	if _, err := LoadAutoState(ctx, db, now); err != nil {
		t.Fatalf("still unreadable after reset: %v", err)
	}
}

// A watcher cancel while planning again under the lock is "cancelled".
func TestAutoCancelDuringRepick(t *testing.T) {
	db := openDB(t)
	enableAuto(t, db, AutoConfig{QuietPeriod: time.Minute})
	f := newFakeAuto(t, db, localNoon())
	f.s.Repick = func(ctx context.Context, _ int) (*AutoPlan, error) {
		f.busy.Store(true)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx := context.Background()
	f.s.Step(ctx)
	f.advance(time.Minute)
	if got, want := f.s.Step(ctx), AutoCheckCancelled+": the app got busy (chat turn)"; got != want {
		t.Fatalf("step = %q, want %q", got, want)
	}
	if len(f.runs) != 0 {
		t.Fatal("ran after a cancelled repick")
	}
}
